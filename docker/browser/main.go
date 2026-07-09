// browser — Jarvis's cloud browser bridge.
//
// A small HTTP server that drives a real headless Chromium via CDP
// (chromedp) and exposes an agent-robust verb set to Core. The point of
// this service is "true agent browsing without jank" — so the contract is
// the observe → act → extract loop, NOT raw CSS-selector clicking:
//
//   - observe   the agent's eyes. Tags every visible interactive element
//     with a stable data-jarvis-idx and returns the numbered
//     list + the page's readable text. The agent picks an index.
//   - act       click / type / select / press / scroll by that index, with
//     auto-wait for network-idle + one auto-retry on DOM change.
//     This is where ~80% of jank dies.
//   - extract   Readability-style markdown of the current page so scraping
//     into a report is reliable, not regex-on-HTML.
//
// Design notes (mirrors docker/workspace, the sibling cloud bridge):
//
//   - No LLM inside. Cognition lives in Core; this is pure substrate the
//     agent assembles (Rule #1 in CLAUDE.md: building blocks, not agents).
//
//   - Reachable only on Railway's private network at
//     browser.railway.internal:$PORT. No public ingress. Bearer auth via
//     BROWSER_BRIDGE_TOKEN so even on a leaky private net only Core drives.
//
//   - Each session is its own Chromium context with persistent cookies for
//     the life of the task, so multi-step flows (search → results → detail)
//     keep state. Capped at BROWSER_MAX_SESSIONS (default 2) — Chromium is
//     ~300MB RSS awake, and Railway App Sleeping scales the box to zero.
//
//   - A CDP screencast runs per session and is streamed to Core over SSE
//     (GET /session/{id}/screencast). Core relays frames to Studio so the
//     boss watches the agent drive in real time. Frames only emit on visual
//     change, so an idle page costs nothing.
//
//   - Cold start: first request after App Sleeping wakes the box and
//     launches Chromium (~5-15s). Core retries the first call.
package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
	"github.com/chromedp/chromedp/kb"
)

const (
	defaultMaxSessions = 2
	idleTimeout        = 30 * time.Minute // janitor closes sessions idle longer than this
	settleTimeout      = 8 * time.Second  // hard cap on auto-wait after nav/act
	netQuietWindow     = 500 * time.Millisecond
	actionTimeout      = 45 * time.Second // hard cap on any single CDP action
	viewportW          = 1280
	viewportH          = 800
	screencastQuality  = 70
	textLimit          = 24 << 10 // 24 KB cap on readable text returned to the agent
)

// infoLog goes to stdout so Railway tags it severity:info, not error.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

var (
	bearerToken string
	maxSessions int
	chromePath  string
	mgr         *Manager
)

func main() {
	// Self-healthcheck mode for the Docker HEALTHCHECK — avoids baking curl
	// into the runtime image. `browser-bridge -healthcheck` hits /health and
	// exits 0 on 200, 1 otherwise.
	if len(os.Args) > 1 && os.Args[1] == "-healthcheck" {
		resp, err := http.Get("http://localhost:" + envDefault("PORT", "8080") + "/health")
		if err != nil || resp.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		os.Exit(0)
	}

	bearerToken = strings.TrimSpace(os.Getenv("BROWSER_BRIDGE_TOKEN"))
	if bearerToken == "" {
		log.Println("WARNING: BROWSER_BRIDGE_TOKEN unset — bridge will refuse all calls")
	}
	maxSessions = envInt("BROWSER_MAX_SESSIONS", defaultMaxSessions)
	chromePath = strings.TrimSpace(os.Getenv("CHROME_PATH")) // empty = chromedp auto-detect

	mgr = NewManager()
	go mgr.janitor()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/session", auth(handleSessionCreate)) // POST
	mux.HandleFunc("/sessions", auth(handleSessionList))  // GET — core reconciles zombies against this
	mux.HandleFunc("/session/", auth(routeSession))       // POST/GET /session/{id}/...

	addr := ":" + envDefault("PORT", "8080")
	infoLog.Printf("browser bridge: listening on %s (max_sessions=%d)", addr, maxSessions)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("browser bridge: %v", err)
	}
}

// ── auth + routing ─────────────────────────────────────────────────────────

func auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearerToken == "" {
			http.Error(w, "bridge bearer token not configured", http.StatusServiceUnavailable)
			return
		}
		if strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ") != bearerToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// routeSession dispatches /session/{id}/{verb}.
func routeSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/session/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		writeJSON(w, http.StatusBadRequest, errBody("missing session id"))
		return
	}
	id := parts[0]
	verb := ""
	if len(parts) == 2 {
		verb = parts[1]
	}
	s := mgr.Get(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, errBody("session not found or expired"))
		return
	}
	switch verb {
	case "navigate":
		handleNavigate(w, r, s)
	case "observe":
		handleObserve(w, r, s)
	case "act":
		handleAct(w, r, s)
	case "input":
		handleInput(w, r, s)
	case "extract":
		handleExtract(w, r, s)
	case "screenshot":
		handleScreenshot(w, r, s)
	case "screencast":
		handleScreencast(w, r, s)
	case "close":
		handleClose(w, r, s)
	default:
		writeJSON(w, http.StatusNotFound, errBody("unknown verb: "+verb))
	}
}

// ── manager ──────────────────────────────────────────────────────────────

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

func (m *Manager) Get(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s != nil {
		s.touch()
	}
	return s
}

func (m *Manager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) put(s *Session) {
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
}

// sessionDTO is one row of GET /sessions — just enough for Core to name and
// age a session it doesn't recognise.
type sessionDTO struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	LastUsed  string `json:"last_used"` // RFC3339
}

func (m *Manager) list() []sessionDTO {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]sessionDTO, 0, len(m.sessions))
	for id, s := range m.sessions {
		out = append(out, sessionDTO{
			SessionID: id,
			URL:       s.url(),
			LastUsed:  s.lastUsed().UTC().Format(time.RFC3339),
		})
	}
	return out
}

func (m *Manager) remove(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s != nil {
		s.shutdown()
	}
}

// janitor reaps sessions idle longer than idleTimeout so a forgotten tab
// doesn't pin a Chromium process forever on a scale-to-zero box.
func (m *Manager) janitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		var stale []string
		m.mu.Lock()
		for id, s := range m.sessions {
			if time.Since(s.lastUsed()) > idleTimeout {
				stale = append(stale, id)
			}
		}
		m.mu.Unlock()
		for _, id := range stale {
			infoLog.Printf("browser bridge: reaping idle session %s", id)
			m.remove(id)
		}
	}
}

// ── session ────────────────────────────────────────────────────────────────

type Session struct {
	id          string
	allocCtx    context.Context
	allocCancel context.CancelFunc
	ctx         context.Context
	cancel      context.CancelFunc

	runMu sync.Mutex // serializes CDP actions — CDP is sequential per target

	lastUsedNano atomic.Int64
	inflight     atomic.Int64 // in-flight network requests, for network-idle wait
	currentURL   atomic.Value // string

	scMu      sync.Mutex
	subs      map[chan string]struct{}
	lastFrame string
}

func (s *Session) touch()              { s.lastUsedNano.Store(time.Now().UnixNano()) }
func (s *Session) lastUsed() time.Time { return time.Unix(0, s.lastUsedNano.Load()) }

func (s *Session) url() string {
	if v, ok := s.currentURL.Load().(string); ok {
		return v
	}
	return ""
}

// newSession launches a fresh headless Chromium context and wires the
// network-idle counter + the screencast listener.
func newSession(id string) (*Session, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
		chromedp.Headless,
		chromedp.DisableGPU,
		chromedp.WindowSize(viewportW, viewportH),
		chromedp.Flag("no-sandbox", true),
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-setuid-sandbox", true),
		chromedp.Flag("hide-scrollbars", true),
		chromedp.Flag("mute-audio", true),
		chromedp.UserAgent("Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"),
	)
	if chromePath != "" {
		opts = append(opts, chromedp.ExecPath(chromePath))
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	ctx, cancel := chromedp.NewContext(allocCtx)

	s := &Session{
		id:          id,
		allocCtx:    allocCtx,
		allocCancel: allocCancel,
		ctx:         ctx,
		cancel:      cancel,
		subs:        make(map[chan string]struct{}),
	}
	s.touch()
	s.currentURL.Store("about:blank")

	// Single listener handles both the network-idle counter and screencast
	// frames. Runs on chromedp's event goroutine — keep it cheap and never
	// block; defer the frame ack to its own goroutine.
	chromedp.ListenTarget(ctx, func(ev interface{}) {
		switch e := ev.(type) {
		case *network.EventRequestWillBeSent:
			s.inflight.Add(1)
		case *network.EventLoadingFinished:
			s.inflight.Add(-1)
		case *network.EventLoadingFailed:
			s.inflight.Add(-1)
		case *page.EventFrameNavigated:
			if e.Frame != nil && e.Frame.ParentID == "" {
				s.currentURL.Store(e.Frame.URL)
			}
		case *page.EventScreencastFrame:
			data := e.Data
			sid := e.SessionID
			go func() {
				// Ack via the low-level executor so we don't fight a
				// chromedp.Run action holding the target.
				_ = page.ScreencastFrameAck(sid).Do(cdp.WithExecutor(s.ctx, chromedp.FromContext(s.ctx).Target))
				s.publishFrame("data:image/jpeg;base64," + data)
			}()
		}
	})

	// Boot the browser, enable domains, start the screencast.
	bootCtx, bootCancel := context.WithTimeout(ctx, 60*time.Second)
	defer bootCancel()
	err := chromedp.Run(bootCtx,
		network.Enable(),
		page.Enable(),
		page.StartScreencast().
			WithFormat(page.ScreencastFormatJpeg).
			WithQuality(screencastQuality).
			WithMaxWidth(viewportW).
			WithMaxHeight(viewportH).
			WithEveryNthFrame(1),
	)
	if err != nil {
		cancel()
		allocCancel()
		return nil, fmt.Errorf("boot chromium: %w", err)
	}

	// Warm up: run one lightweight action on ctx (not bootCtx) to anchor the
	// chromedp target against s.ctx before defer bootCancel() fires.  Without
	// this warm-up, the first external navigate on a no-URL session races with
	// the bootCtx teardown and returns "context canceled".
	warmCtx, warmCancel := context.WithTimeout(ctx, 5*time.Second)
	var warmTitle string
	_ = chromedp.Run(warmCtx, chromedp.Title(&warmTitle))
	warmCancel()

	return s, nil
}

func (s *Session) shutdown() {
	s.scMu.Lock()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.scMu.Unlock()
	if s.cancel != nil {
		s.cancel()
	}
	if s.allocCancel != nil {
		s.allocCancel()
	}
}

// settle waits for the page to stop changing: readyState complete + network
// quiet for netQuietWindow, capped at settleTimeout. This is the anti-jank
// heart — every navigate/act calls it before returning so the agent never
// observes a half-loaded page.
func (s *Session) settle() {
	deadline := time.Now().Add(settleTimeout)

	// Brief grace so a click that kicks off a request is counted before we
	// declare the network quiet.
	time.Sleep(150 * time.Millisecond)

	var quietSince time.Time
	for time.Now().Before(deadline) {
		if s.inflight.Load() <= 0 {
			if quietSince.IsZero() {
				quietSince = time.Now()
			} else if time.Since(quietSince) >= netQuietWindow {
				break
			}
		} else {
			quietSince = time.Time{}
		}
		time.Sleep(80 * time.Millisecond)
	}

	// Final readyState check (best-effort, short).
	rsCtx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	var rs string
	_ = chromedp.Run(rsCtx, chromedp.Evaluate(`document.readyState`, &rs))
}

// run executes CDP actions under the session lock with a hard timeout.
func (s *Session) run(actions ...chromedp.Action) error {
	s.runMu.Lock()
	defer s.runMu.Unlock()
	ctx, cancel := context.WithTimeout(s.ctx, actionTimeout)
	defer cancel()
	return chromedp.Run(ctx, actions...)
}

func (s *Session) publishFrame(dataURL string) {
	s.scMu.Lock()
	s.lastFrame = dataURL
	for ch := range s.subs {
		select {
		case ch <- dataURL:
		default: // slow consumer — drop the frame rather than block Chromium
		}
	}
	s.scMu.Unlock()
}

func (s *Session) subscribe() (chan string, func()) {
	ch := make(chan string, 4)
	s.scMu.Lock()
	s.subs[ch] = struct{}{}
	last := s.lastFrame
	s.scMu.Unlock()
	if last != "" {
		ch <- last // paint immediately for a new viewer
	}
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.scMu.Lock()
			delete(s.subs, ch)
			close(ch)
			s.scMu.Unlock()
		})
	}
}

// ── handlers ─────────────────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"service":  "browser-bridge",
		"sessions": mgr.count(),
		"max":      maxSessions,
	})
}

// handleSessionList names every live session so Core can reconcile its own
// registry against ours. The two registries WILL diverge — a relay stream
// drops, or Core redeploys and forgets everything while we keep holding
// sessions against maxSessions. Without this endpoint that divergence is a
// deadlock: Core can't close what it can't name, and creates bounce off the
// cap for the full idleTimeout (2026-07-09: the agent burned a whole turn
// walled off from its own browser). GET only.
func handleSessionList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errBody("GET only"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": mgr.list()})
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service":     "browser-bridge",
		"git_sha":     strings.TrimSpace(os.Getenv("RAILWAY_GIT_COMMIT_SHA")),
		"deployed_at": strings.TrimSpace(os.Getenv("RAILWAY_DEPLOYMENT_CREATED_AT")),
	})
}

type createRequest struct {
	URL string `json:"url,omitempty"`
}

type sessionResponse struct {
	SessionID string `json:"session_id"`
	URL       string `json:"url"`
	Title     string `json:"title,omitempty"`
}

func handleSessionCreate(w http.ResponseWriter, r *http.Request) {
	if mgr.count() >= maxSessions {
		writeJSON(w, http.StatusServiceUnavailable, errBody(
			fmt.Sprintf("max %d concurrent browser sessions reached — close one first", maxSessions)))
		return
	}
	var req createRequest
	_ = readJSON(r, &req) // body optional

	id := newID()
	s, err := newSession(id)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err.Error()))
		return
	}
	mgr.put(s)
	infoLog.Printf("browser bridge: opened session %s", id)

	resp := sessionResponse{SessionID: id, URL: s.url()}
	if strings.TrimSpace(req.URL) != "" {
		title, err := s.navigate(req.URL)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{
				"session_id": id, "url": s.url(), "error": err.Error(),
			})
			return
		}
		resp.URL = s.url()
		resp.Title = title
	}
	writeJSON(w, http.StatusOK, resp)
}

type navigateRequest struct {
	URL string `json:"url"`
}

func (s *Session) navigate(url string) (string, error) {
	url = normalizeURL(url)
	var title string
	if err := s.run(chromedp.Navigate(url)); err != nil {
		return "", err
	}
	s.settle()
	_ = s.run(chromedp.Title(&title))
	if cur := currentLocation(s); cur != "" {
		s.currentURL.Store(cur)
	}
	return title, nil
}

func handleNavigate(w http.ResponseWriter, r *http.Request, s *Session) {
	var req navigateRequest
	if err := readJSON(r, &req); err != nil || strings.TrimSpace(req.URL) == "" {
		writeJSON(w, http.StatusBadRequest, errBody("url required"))
		return
	}
	title, err := s.navigate(req.URL)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"url": s.url(), "title": title})
}

// Element is one interactable node returned by observe.
type Element struct {
	Idx         int    `json:"idx"`
	Tag         string `json:"tag"`
	Role        string `json:"role,omitempty"`
	Text        string `json:"text,omitempty"`
	Type        string `json:"type,omitempty"`
	Name        string `json:"name,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Href        string `json:"href,omitempty"`
	Value       string `json:"value,omitempty"`
}

type observeResult struct {
	Title    string    `json:"title"`
	URL      string    `json:"url"`
	Text     string    `json:"text"`
	Elements []Element `json:"elements"`
}

func handleObserve(w http.ResponseWriter, r *http.Request, s *Session) {
	res, err := s.observe()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Session) observe() (*observeResult, error) {
	var res observeResult
	if err := s.run(chromedp.Evaluate(observeJS, &res)); err != nil {
		return nil, err
	}
	if len(res.Text) > textLimit {
		res.Text = res.Text[:textLimit] + "\n…[truncated]"
	}
	if res.URL != "" {
		s.currentURL.Store(res.URL)
	}
	return &res, nil
}

type actRequest struct {
	Index  int    `json:"index"`
	Action string `json:"action"` // click | type | select | press | scroll | clear
	Value  string `json:"value,omitempty"`
}

func handleAct(w http.ResponseWriter, r *http.Request, s *Session) {
	var req actRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid body"))
		return
	}
	if err := s.act(req); err != nil {
		// One auto-retry: the DOM may have shifted since the last observe.
		// Re-tag and try once more before surfacing the failure.
		if _, oerr := s.observe(); oerr == nil {
			if err2 := s.act(req); err2 == nil {
				s.settle()
				var title string
				_ = s.run(chromedp.Title(&title))
				writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": s.url(), "title": title, "retried": true})
				return
			}
		}
		writeJSON(w, http.StatusUnprocessableEntity, errBody(err.Error()))
		return
	}
	s.settle()
	var title string
	_ = s.run(chromedp.Title(&title))
	if cur := currentLocation(s); cur != "" {
		s.currentURL.Store(cur)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": s.url(), "title": title})
}

func (s *Session) act(req actRequest) error {
	sel := fmt.Sprintf(`[data-jarvis-idx="%d"]`, req.Index)
	switch strings.ToLower(req.Action) {
	case "click":
		return s.run(chromedp.Click(sel, chromedp.ByQuery, chromedp.NodeVisible))
	case "type":
		return s.run(
			chromedp.Focus(sel, chromedp.ByQuery),
			chromedp.Clear(sel, chromedp.ByQuery),
			chromedp.SendKeys(sel, req.Value, chromedp.ByQuery),
		)
	case "clear":
		return s.run(chromedp.Clear(sel, chromedp.ByQuery))
	case "select":
		// Set <select>.value and dispatch change so frameworks notice.
		js := fmt.Sprintf(
			`(()=>{const el=document.querySelector(%q);if(!el)return false;el.value=%q;el.dispatchEvent(new Event('input',{bubbles:true}));el.dispatchEvent(new Event('change',{bubbles:true}));return true;})()`,
			sel, req.Value)
		var ok bool
		if err := s.run(chromedp.Evaluate(js, &ok)); err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("element %d not found for select", req.Index)
		}
		return nil
	case "press":
		key := keyFor(req.Value)
		return s.run(
			chromedp.Focus(sel, chromedp.ByQuery),
			chromedp.SendKeys(sel, key, chromedp.ByQuery),
		)
	case "scroll":
		// Scroll the element into view; if no element, page-scroll down.
		js := fmt.Sprintf(
			`(()=>{const el=document.querySelector(%q);if(el){el.scrollIntoView({behavior:'instant',block:'center'});return true;}window.scrollBy(0,window.innerHeight*0.8);return true;})()`,
			sel)
		var ok bool
		return s.run(chromedp.Evaluate(js, &ok))
	default:
		return fmt.Errorf("unknown action %q (use click|type|select|press|scroll|clear)", req.Action)
	}
}

// inputRequest is a raw, coordinate-based interaction forwarded from Studio
// when the boss takes over the live browser by hand (clicking the screencast,
// typing, scrolling). This is the human-takeover path - distinct from /act,
// which is the agent's index-based verb set. Coordinates are in the
// screencast's own pixel space (the CDP frame is capped at viewportW×viewportH
// and emitted at native size, so Studio maps a click on the <img> straight to
// these coords with no DPI math).
type inputRequest struct {
	Type   string  `json:"type"`             // click | move | scroll | text | key | resize
	X      float64 `json:"x,omitempty"`      // viewport px
	Y      float64 `json:"y,omitempty"`      // viewport px
	Button string  `json:"button,omitempty"` // left (default) | right | middle
	DeltaX float64 `json:"delta_x,omitempty"`
	DeltaY float64 `json:"delta_y,omitempty"`
	Text   string  `json:"text,omitempty"`   // for type=text: literal characters to insert
	Key    string  `json:"key,omitempty"`    // for type=key: Enter|Tab|Backspace|ArrowDown|…
	Width  int64   `json:"width,omitempty"`  // for type=resize: match the remote viewport to the Studio pane
	Height int64   `json:"height,omitempty"` // so the page reflows + the screencast fills with no letterbox
}

func handleInput(w http.ResponseWriter, r *http.Request, s *Session) {
	var req inputRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, errBody("invalid body"))
		return
	}
	if err := s.input(req); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, errBody(err.Error()))
		return
	}
	// No settle() here: human interaction is high-frequency (every keystroke,
	// every scroll tick) and the screencast already streams the result. A
	// navigation triggered by a click still updates currentURL via the frame
	// listener, so the toolbar stays accurate.
	if cur := currentLocation(s); cur != "" {
		s.currentURL.Store(cur)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "url": s.url()})
}

// input dispatches one raw human interaction through the CDP Input domain.
func (s *Session) input(req inputRequest) error {
	switch strings.ToLower(strings.TrimSpace(req.Type)) {
	case "click":
		btn := input.Left
		switch strings.ToLower(req.Button) {
		case "right":
			btn = input.Right
		case "middle":
			btn = input.Middle
		}
		return s.run(
			input.DispatchMouseEvent(input.MousePressed, req.X, req.Y).WithButton(btn).WithClickCount(1),
			input.DispatchMouseEvent(input.MouseReleased, req.X, req.Y).WithButton(btn).WithClickCount(1),
		)
	case "move":
		return s.run(input.DispatchMouseEvent(input.MouseMoved, req.X, req.Y))
	case "scroll":
		return s.run(input.DispatchMouseEvent(input.MouseWheel, req.X, req.Y).
			WithDeltaX(req.DeltaX).WithDeltaY(req.DeltaY))
	case "text":
		if req.Text == "" {
			return nil
		}
		// InsertText types into whatever is focused (the field the boss just
		// clicked), exactly like a paste/IME commit - robust across frameworks.
		return s.run(input.InsertText(req.Text))
	case "key":
		return s.run(chromedp.KeyEvent(keyFor(req.Key)))
	case "resize":
		// Match the remote page's viewport to the Studio pane so it reflows to
		// the boss's window size and the screencast fills edge-to-edge instead
		// of letterboxing a fixed 1280×800 frame. Clamp to sane bounds (and the
		// screencast's own max) so a stray value can't blow up Chromium.
		w := clampDim(req.Width, 320, viewportW)
		h := clampDim(req.Height, 240, viewportH)
		return s.run(chromedp.EmulateViewport(w, h))
	default:
		return fmt.Errorf("unknown input type %q (use click|move|scroll|text|key|resize)", req.Type)
	}
}

// clampDim bounds a requested viewport dimension to [lo, hi].
func clampDim(v, lo, hi int64) int64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

type extractRequest struct {
	Format string `json:"format,omitempty"` // markdown (default) | text
}

func handleExtract(w http.ResponseWriter, r *http.Request, s *Session) {
	var req extractRequest
	_ = readJSON(r, &req)
	var res struct {
		Title   string `json:"title"`
		URL     string `json:"url"`
		Content string `json:"content"`
	}
	if err := s.run(chromedp.Evaluate(extractJS, &res)); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err.Error()))
		return
	}
	if len(res.Content) > textLimit*2 {
		res.Content = res.Content[:textLimit*2] + "\n…[truncated]"
	}
	writeJSON(w, http.StatusOK, res)
}

func handleScreenshot(w http.ResponseWriter, r *http.Request, s *Session) {
	var buf []byte
	if err := s.run(chromedp.CaptureScreenshot(&buf)); err != nil {
		writeJSON(w, http.StatusInternalServerError, errBody(err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"url":      s.url(),
		"data_url": "data:image/png;base64," + b64(buf),
	})
}

// handleScreencast streams the session's live frames as SSE. Core consumes
// this and relays each frame to Studio over the chat websocket.
func handleScreencast(w http.ResponseWriter, r *http.Request, s *Session) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, errBody("streaming unsupported"))
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsub := s.subscribe()
	defer unsub()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	seq := 0
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case frame, open := <-ch:
			if !open {
				return
			}
			seq++
			payload, _ := json.Marshal(map[string]any{"seq": seq, "frame": frame})
			fmt.Fprintf(w, "event: frame\ndata: %s\n\n", payload)
			flusher.Flush()
		}
	}
}

func handleClose(w http.ResponseWriter, r *http.Request, s *Session) {
	mgr.remove(s.id)
	infoLog.Printf("browser bridge: closed session %s", s.id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "closed": s.id})
}

// ── injected JS ────────────────────────────────────────────────────────────

// observeJS tags every visible, interactable element with a stable
// data-jarvis-idx and returns the numbered list + the page's readable text.
// The agent acts by index; act() resolves [data-jarvis-idx="N"] in the live
// DOM, so the handle survives re-render as long as the node persists.
const observeJS = `
(() => {
  const SEL = 'a[href], button, input, select, textarea, [role=button], [role=link], [role=checkbox], [role=radio], [role=tab], [role=menuitem], [role=option], [role=switch], [contenteditable=true], [onclick], summary, label';
  const isVisible = (el) => {
    const st = window.getComputedStyle(el);
    if (st.display === 'none' || st.visibility === 'hidden' || parseFloat(st.opacity) === 0) return false;
    const r = el.getBoundingClientRect();
    if (r.width < 2 || r.height < 2) return false;
    if (r.bottom < 0 || r.right < 0 || r.top > (window.innerHeight + 2000)) return false;
    return true;
  };
  const norm = (s) => (s || '').replace(/\s+/g, ' ').trim().slice(0, 200);
  const nodes = Array.from(document.querySelectorAll(SEL)).filter(isVisible);
  const seen = new Set();
  const elements = [];
  let idx = 0;
  for (const el of nodes) {
    if (seen.has(el)) continue;
    seen.add(el);
    el.setAttribute('data-jarvis-idx', String(idx));
    const tag = el.tagName.toLowerCase();
    const label = norm(el.getAttribute('aria-label') || el.innerText || el.value || el.getAttribute('placeholder') || el.getAttribute('title') || el.getAttribute('alt') || el.name || '');
    elements.push({
      idx,
      tag,
      role: el.getAttribute('role') || '',
      text: label,
      type: el.getAttribute('type') || '',
      name: el.getAttribute('name') || '',
      placeholder: el.getAttribute('placeholder') || '',
      href: tag === 'a' ? (el.getAttribute('href') || '') : '',
      value: (tag === 'input' || tag === 'textarea' || tag === 'select') ? norm(el.value) : '',
    });
    idx++;
    if (idx >= 250) break;
  }
  // Readable text: clone, strip noise, take innerText of the densest content.
  const clone = document.body.cloneNode(true);
  clone.querySelectorAll('script,style,noscript,svg,iframe,nav,footer,header,aside,[aria-hidden=true]').forEach(n => n.remove());
  let text = (clone.innerText || '').replace(/\n{3,}/g, '\n\n').trim();
  return { title: document.title, url: location.href, text, elements };
})()
`

// extractJS returns a Readability-flavoured markdown of the main content of
// the page — good enough for scraping listings/articles into a report
// without dragging in a full Readability dependency.
const extractJS = `
(() => {
  const pick = () => {
    const cands = ['main','article','[role=main]','#content','#main','.content','.main'];
    for (const c of cands) { const el = document.querySelector(c); if (el && el.innerText && el.innerText.length > 200) return el; }
    return document.body;
  };
  const root = pick().cloneNode(true);
  root.querySelectorAll('script,style,noscript,svg,iframe,nav,footer,header,aside,form,button,[aria-hidden=true]').forEach(n => n.remove());
  const out = [];
  const walk = (node) => {
    for (const child of node.childNodes) {
      if (child.nodeType === 3) { const t = child.textContent.replace(/\s+/g,' ').trim(); if (t) out.push(t); continue; }
      if (child.nodeType !== 1) continue;
      const tag = child.tagName.toLowerCase();
      if (/^h[1-6]$/.test(tag)) { const lvl = +tag[1]; const t = child.innerText.trim(); if (t) out.push('\n' + '#'.repeat(lvl) + ' ' + t + '\n'); continue; }
      if (tag === 'li') { const t = child.innerText.replace(/\s+/g,' ').trim(); if (t) out.push('- ' + t); continue; }
      if (tag === 'a') { const t = child.innerText.replace(/\s+/g,' ').trim(); const h = child.getAttribute('href') || ''; if (t) out.push(h ? '[' + t + '](' + h + ')' : t); continue; }
      if (tag === 'p' || tag === 'div' || tag === 'section' || tag === 'tr') { walk(child); out.push('\n'); continue; }
      walk(child);
    }
  };
  walk(root);
  const content = out.join(' ').replace(/[ \t]{2,}/g,' ').replace(/\n{3,}/g,'\n\n').trim();
  return { title: document.title, url: location.href, content };
})()
`

// ── helpers ──────────────────────────────────────────────────────────────

// currentLocation reads window.location.href best-effort (cheap, short).
func currentLocation(s *Session) string {
	ctx, cancel := context.WithTimeout(s.ctx, 2*time.Second)
	defer cancel()
	var loc string
	if err := chromedp.Run(ctx, chromedp.Evaluate(`location.href`, &loc)); err != nil {
		return ""
	}
	return loc
}

func normalizeURL(u string) string {
	u = strings.TrimSpace(u)
	if u == "" {
		return u
	}
	if !strings.Contains(u, "://") && !strings.HasPrefix(u, "about:") {
		return "https://" + u
	}
	return u
}

func keyFor(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "enter", "return", "":
		return kb.Enter
	case "tab":
		return kb.Tab
	case "escape", "esc":
		return kb.Escape
	case "backspace":
		return kb.Backspace
	case "delete", "del":
		return kb.Delete
	case "arrowdown", "down":
		return kb.ArrowDown
	case "arrowup", "up":
		return kb.ArrowUp
	default:
		return name
	}
}

func newID() string {
	return fmt.Sprintf("br_%d", time.Now().UnixNano())
}

func b64(b []byte) string {
	return base64.StdEncoding.EncodeToString(b)
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	return json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(dst)
}

func errBody(msg string) map[string]string { return map[string]string{"error": msg} }

func envDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' {
				return fallback
			}
			n = n*10 + int(c-'0')
		}
		if n > 0 {
			return n
		}
	}
	return fallback
}
