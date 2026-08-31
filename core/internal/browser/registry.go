package browser

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
)

// infoLog → stdout so Railway tags these severity:info, not error.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// FrameSink receives a screencast frame for a chat session. serve.go wires
// this to the WS session broadcaster so frames reach the live Studio tab;
// when unset, frames are dropped and browsing still works headless.
type FrameSink func(chatSessionID string, f Frame)

// idleReap closes a core-side session whose tools haven't touched it in
// this long. Slightly longer than the sidecar's own 30m reap so the sidecar
// is the primary GC and we just close the mem_runs row afterwards.
const idleReap = 35 * time.Minute

// Registry tracks live browser sessions, ties each to a mem_runs row, and
// runs a per-session relay goroutine that streams the sidecar's screencast
// to Studio. The relay lifetime equals the browser session — NOT a single
// tool call — so the boss watches continuously across observe/act/extract.
type Registry struct {
	backend Backend
	tracker *runs.Tracker

	mu           sync.Mutex
	sessions     map[string]*entry // browser session id → entry
	latestByChat map[string]string // chat session id → most-recent browser session id
	sink         FrameSink
	// controlNotify broadcasts controller changes (agent↔human) so Studio
	// can render "you're driving / hand back". Wired from serve.go.
	controlNotify func(chatID, browserID, controller, reason string)
	// helpNotify pings the boss when the agent requests a takeover
	// (captcha, login wall, 2FA). Wired from serve.go to Web Push.
	helpNotify func(chatID, browserID, reason string)
}

// Controller values for a session. Exactly one actor drives at a time:
// the agent's write verbs (act/navigate) yield while a human is driving,
// and manual input implicitly claims control (see Input).
const (
	ControllerAgent = "agent"
	ControllerHuman = "human"
)

type entry struct {
	browserID string
	chatID    string
	handle    *runs.Handle
	cancel    context.CancelFunc // stops the relay goroutine
	url       string
	lastUsed  time.Time
	done      bool
	// controller is who is driving: ControllerAgent (default) or
	// ControllerHuman during a takeover. Guarded by Registry.mu.
	controller string
	// recoveries counts how many times this session is the REPLACEMENT for one
	// that died under a verb, carried forward across each auto-recovery.
	// Deliberately not reset by a successful verb: a browser that dies once is
	// a hiccup, one that dies repeatedly is a fault, and the whole point is to
	// stop grinding through replacements while reporting progress. Opening a
	// browser deliberately (browser_open) starts a fresh chain at 0.
	recoveries int
}

func NewRegistry(backend Backend, tracker *runs.Tracker) *Registry {
	r := &Registry{
		backend:      backend,
		tracker:      tracker,
		sessions:     make(map[string]*entry),
		latestByChat: make(map[string]string),
	}
	if backend != nil {
		go r.janitor()
	}
	return r
}

// Enabled reports whether the browser backend is configured.
func (r *Registry) Enabled() bool { return r != nil && r.backend != nil }

// SetSink wires the frame broadcaster. Called once from serve.go after the
// HTTP server (which owns the WS session map) is constructed.
func (r *Registry) SetSink(sink FrameSink) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
}

func (r *Registry) emit(chatID string, f Frame) {
	r.mu.Lock()
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink(chatID, f)
	}
}

// sessionLister is the optional backend capability behind zombie reconcile.
// The chromedp sidecar client implements it; camofox doesn't (its engine owns
// its own session lifecycle), and every reconcile path degrades to a no-op.
type sessionLister interface {
	ListSessions(ctx context.Context) ([]RemoteSession, error)
}

// Open creates a browser session, books a mem_runs row, and starts the
// screencast relay. chatID is the Studio chat session the frames route to.
//
// If the sidecar refuses because its concurrent-session cap is full, that is
// almost never the boss actually running that many browsers — it is registry
// divergence: sessions the sidecar holds that core no longer tracks (a core
// redeploy wipes this in-memory registry; a dropped relay used to forget
// without closing). Those zombies are unusable by definition — nothing can
// name them — so reconcile closes them and the create retries once. The agent
// never sees the deadlock. (2026-07-09: without this, the agent spent a turn
// bouncing off "max 2 concurrent sessions" while browser_close insisted there
// was nothing to close, then wandered off toward scraper sites.)
func (r *Registry) Open(ctx context.Context, chatID, url string) (*SessionInfo, error) {
	info, err := r.createSession(ctx, url)
	if err != nil {
		return nil, err
	}

	label := "browser session"
	if info.URL != "" && info.URL != "about:blank" {
		label = "browser: " + info.URL
	}
	handle := r.tracker.Begin(ctx, runs.KindBrowserSession, info.SessionID, label, runs.SourceAgent)

	// Relay context is independent of the tool-call ctx so the screencast
	// keeps streaming across every later observe/act/extract call. It ends
	// only on Close, idle-reap, or core shutdown.
	relayCtx, cancel := context.WithCancel(context.Background())
	e := &entry{
		browserID: info.SessionID,
		chatID:    chatID,
		handle:    handle,
		cancel:    cancel,
		url:       info.URL,
		lastUsed:  time.Now(),
	}
	r.mu.Lock()
	r.sessions[info.SessionID] = e
	if chatID != "" {
		r.latestByChat[chatID] = info.SessionID
	}
	r.mu.Unlock()

	go r.relay(relayCtx, e)
	infoLog.Printf("browser: opened session %s for chat %s", info.SessionID, chatID)
	return info, nil
}

// createSession creates a sidecar session, recovering from the two ways the
// sidecar can hand back something unusable:
//
//   - AT CAPACITY, because core and the sidecar diverged — sessions the
//     sidecar holds that core can no longer name. reconcileZombies closes
//     those and the create retries.
//   - DEAD ON ARRIVAL, because the create-time navigate came back reporting a
//     dead browser context. The id looks live, so core would track it, hand it
//     to the agent, and every verb would answer "context canceled" while the
//     slot stayed occupied until the sidecar's 30m idle reap. Two of those and
//     the agent is walled off behind "max 2 concurrent browser sessions" while
//     browser_close insists there is nothing open.
//
// Each is retried exactly once, then surfaced loudly. Never hand back a dead
// id: a session that cannot be driven is a failure, not a session with a note.
func (r *Registry) createSession(ctx context.Context, url string) (*SessionInfo, error) {
	info, err := r.backend.CreateSession(ctx, url)
	if err != nil && strings.Contains(err.Error(), "concurrent browser sessions") {
		if n := r.reconcileZombies(ctx); n > 0 {
			infoLog.Printf("browser: closed %d zombie session(s) at capacity, retrying open", n)
			info, err = r.backend.CreateSession(ctx, url)
		}
	}
	if err != nil {
		return nil, err
	}
	if !r.deadOnArrival(info) {
		return info, nil
	}
	log.Printf("browser: session %s dead on arrival (%s) — reclaiming the slot and retrying", info.SessionID, info.Error)
	r.discard(ctx, info.SessionID)
	info, err = r.backend.CreateSession(ctx, url)
	if err != nil {
		return nil, err
	}
	if r.deadOnArrival(info) {
		r.discard(ctx, info.SessionID)
		return nil, fmt.Errorf("browser sidecar could not start a usable session: %s", info.Error)
	}
	return info, nil
}

// deadOnArrival reports whether a freshly created session is already unusable.
func (r *Registry) deadOnArrival(info *SessionInfo) bool {
	return info != nil && info.SessionID != "" && isDeadSessionText(info.Error)
}

// discard closes a sidecar session core never started tracking, so there is no
// relay or mem_runs row to tear down — the only point is giving the slot back.
// Detached from the caller's ctx: reclaiming capacity must still happen when
// the turn that triggered it is being cancelled.
func (r *Registry) discard(ctx context.Context, browserID string) {
	if browserID == "" {
		return
	}
	cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	_ = r.backend.Close(cctx, browserID)
}

// deadSessionSignals are the sidecar error texts that mean a session id is
// terminal: chromedp's browser/target context is gone, or the sidecar has
// already forgotten the id. No verb will ever succeed on it again, so the only
// correct response is to evict it from both registries instead of continuing
// to hand it to the agent.
var deadSessionSignals = []string{
	"context canceled",
	"context cancelled",
	"session not found",
	"session died",
	"session closed",
	"target closed",
	"websocket: close",
}

func isDeadSessionText(s string) bool {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return false
	}
	for _, sig := range deadSessionSignals {
		if strings.Contains(s, sig) {
			return true
		}
	}
	return false
}

// EvictIfDead closes both halves of a session when err says the id is
// terminal, and reports whether it did. This is the single chokepoint for
// "the browser went away": the sidecar half must close (it holds the slot
// against maxSessions otherwise) and the core half must be forgotten (so
// Resolve stops handing the agent an id that can only fail).
//
// callerCtx guards the ambiguous case. "context canceled" is also what we see
// when OUR OWN request context died — the turn ended, the tool was cancelled —
// and that says nothing about the browser. Only evict while the caller's
// context is still live.
func (r *Registry) EvictIfDead(callerCtx context.Context, browserID string, err error) bool {
	if err == nil || browserID == "" {
		return false
	}
	if callerCtx != nil && callerCtx.Err() != nil {
		return false
	}
	if !isDeadSessionText(err.Error()) {
		return false
	}
	log.Printf("browser: evicting dead session %s: %v", browserID, err)
	r.closeAndFinish(browserID, err)
	return true
}

// relay streams frames from the sidecar to the chat session until ctx ends
// or the sidecar stream closes.
//
// Teardown here MUST be symmetric: if this side forgets the session (finish),
// the sidecar side must be closed too. The old shape forgot without closing —
// core's registry emptied while the sidecar kept the session against its
// concurrency cap for the full idle timeout, which is how the agent ended up
// walled off from a browser it couldn't close ("max 2 concurrent" vs "no open
// browser session to close", 30 minutes apart from any fix).
// screencastReattempts is how many times the relay will re-subscribe to a
// session the sidecar still reports as alive before giving up on the view.
const screencastReattempts = 3

func (r *Registry) relay(ctx context.Context, e *entry) {
	var lastErr error
	for attempt := 0; ; attempt++ {
		stream, err := r.backend.SubscribeScreencast(ctx, e.browserID)
		if err != nil {
			// Never subscribed: the entry would otherwise sit here untouched
			// with its mem_runs row 'running' until the idle janitor. Same
			// rule as the stream-end path — forget AND close, together.
			log.Printf("browser: screencast subscribe failed for %s: %v", e.browserID, err)
			r.closeAndFinish(e.browserID, err)
			return
		}
		for f := range stream.Frames {
			// The engine's own url wins. Core's tracked url only moves when a
			// tool result comes back, so stamping it here made the address bar
			// lag the picture it sits above, and diverge outright once more
			// than one session was in play. A frame that carries its url is
			// the truth about what is on screen at that instant; e.url is the
			// fallback for engines that send none (Camoufox).
			if f.URL != "" {
				r.UpdateURL(e.browserID, f.URL)
			} else {
				r.mu.Lock()
				f.URL = e.url
				r.mu.Unlock()
			}
			f.BrowserID = e.browserID
			r.emit(e.chatID, f)
		}
		if err := stream.Err(); err != nil {
			lastErr = err
		}

		// Our own teardown (Close, idle reap, core shutting down). Nothing to
		// report and nothing to reattach to.
		if ctx.Err() != nil {
			return
		}

		// The stream ended but the session may be perfectly fine. The relay is
		// the VIEWING layer, and until now its death killed live WORK: one SSE
		// drop tore down a browser the agent was mid-task in. The anti-zombie
		// invariant below is still right — core must never forget a session
		// without closing it — but it must not be reached by way of a
		// screencast hiccup.
		//
		// So: ask the sidecar whether the session is still there. If it is,
		// reattach and carry on. Only tear down when the session is genuinely
		// gone, or when the view will not come back.
		if attempt < screencastReattempts && r.sessionAliveRemotely(ctx, e.browserID) {
			log.Printf("browser: screencast for %s ended (%v); session is still alive, reattaching (attempt %d/%d)",
				e.browserID, lastErr, attempt+1, screencastReattempts)
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt+1) * time.Second):
			}
			continue
		}

		// Session is gone, or the view kept dropping. Close the sidecar half
		// so it can't linger as an unnameable zombie holding the cap, and
		// finish with WHY. Passing nil here is what recorded six dead sessions
		// as ok on 2026-08-30; an abnormal end must read as an error so it
		// reaches the boss's backlog like any other honest failure.
		if lastErr == nil && attempt > 0 {
			lastErr = fmt.Errorf("browser screencast dropped %d times and the session did not come back", attempt+1)
		}
		r.closeAndFinish(e.browserID, lastErr)
		return
	}
}

// ── direct verb access ────────────────────────────────────────────────────
//
// These let a trusted in-process caller drive a session WITHOUT going through
// the tool registry, which matters for the payment fill boundary: the card is
// typed by Go, so the value never becomes a tool input, never reaches the
// model's context, and never lands in an observation payload. The verb tools
// above remain the only path the agent itself has.

// ObserveDirect returns the current page's elements and text.
func (r *Registry) ObserveDirect(ctx context.Context, browserID string) (*ObserveResult, error) {
	if !r.isLive(browserID) {
		return nil, fmt.Errorf("browser session not found or already closed")
	}
	return r.backend.Observe(ctx, browserID)
}

// ActDirect performs one action. Callers driving a payment must treat a
// charge-bearing action as single-shot: see purchase.BrowserExecutor.
func (r *Registry) ActDirect(ctx context.Context, browserID string, req ActRequest) (*ActResult, error) {
	if !r.isLive(browserID) {
		return nil, fmt.Errorf("browser session not found or already closed")
	}
	return r.backend.Act(ctx, browserID, req)
}

// ExtractDirect returns the page as markdown.
func (r *Registry) ExtractDirect(ctx context.Context, browserID string) (*ExtractResult, error) {
	if !r.isLive(browserID) {
		return nil, fmt.Errorf("browser session not found or already closed")
	}
	return r.backend.Extract(ctx, browserID, "markdown")
}

// ScreenshotDirect captures the page. Used for the receipt on a confirmed
// purchase; payment fields are masked by the engine before capture.
func (r *Registry) ScreenshotDirect(ctx context.Context, browserID string) (*ShotResult, error) {
	if !r.isLive(browserID) {
		return nil, fmt.Errorf("browser session not found or already closed")
	}
	return r.backend.Screenshot(ctx, browserID)
}

// IsLive reports whether core still tracks a drivable session.
func (r *Registry) IsLive(browserID string) bool { return r.isLive(browserID) }

// Recoveries reports how many auto-recoveries deep this session is.
func (r *Registry) Recoveries(browserID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[browserID]; ok {
		return e.recoveries
	}
	return 0
}

// SetRecoveries records how many auto-recoveries deep a session is, so the
// count survives the replacement of the session it is counting.
func (r *Registry) SetRecoveries(browserID string, n int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[browserID]; ok {
		e.recoveries = n
	}
}

// sessionAliveRemotely asks the sidecar whether it still holds this session.
// Backends that cannot list report false, which keeps the old conservative
// teardown behaviour for them rather than inventing an optimistic answer.
func (r *Registry) sessionAliveRemotely(ctx context.Context, browserID string) bool {
	lister, ok := r.backend.(sessionLister)
	if !ok {
		return false
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	remote, err := lister.ListSessions(lctx)
	if err != nil {
		return false
	}
	for _, s := range remote {
		if s.SessionID == browserID {
			return true
		}
	}
	return false
}

// closeAndFinish tears down both halves of a session — sidecar first
// (best-effort; it may already be gone), then the core bookkeeping. The one
// invariant: no path may finish without also closing.
func (r *Registry) closeAndFinish(browserID string, runErr error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_ = r.backend.Close(ctx, browserID)
	r.finish(ctx, browserID, runErr)
}

// reconcileZombies closes every sidecar session core doesn't track. Returns
// how many were closed. Requires the backend to support listing; otherwise 0.
func (r *Registry) reconcileZombies(ctx context.Context) int {
	lister, ok := r.backend.(sessionLister)
	if !ok {
		return 0
	}
	remote, err := lister.ListSessions(ctx)
	if err != nil {
		log.Printf("browser: zombie reconcile list failed: %v", err)
		return 0
	}
	r.mu.Lock()
	var zombies []string
	for _, s := range remote {
		if _, tracked := r.sessions[s.SessionID]; !tracked {
			zombies = append(zombies, s.SessionID)
		}
	}
	r.mu.Unlock()
	closed := 0
	for _, id := range zombies {
		if err := r.backend.Close(ctx, id); err == nil {
			closed++
			infoLog.Printf("browser: reconciled zombie session %s", id)
		}
	}
	return closed
}

// Resolve returns the browser session id a tool should act on: the explicit
// id if it names a live session, otherwise the most-recent session for the
// chat. The bool is false when neither resolves (agent must Open first).
func (r *Registry) Resolve(chatID, explicit string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if explicit != "" {
		if e, ok := r.sessions[explicit]; ok {
			e.lastUsed = time.Now()
			return explicit, true
		}
		return "", false
	}
	if id, ok := r.latestByChat[chatID]; ok {
		if e, ok := r.sessions[id]; ok {
			e.lastUsed = time.Now()
			return id, true
		}
	}
	return "", false
}

// UpdateURL records the session's current URL (from a nav/act result) so
// the live tab toolbar and mem_runs label stay accurate.
func (r *Registry) UpdateURL(browserID, url string) {
	if url == "" {
		return
	}
	r.mu.Lock()
	if e, ok := r.sessions[browserID]; ok {
		e.url = url
		e.lastUsed = time.Now()
	}
	r.mu.Unlock()
}

// URL returns the last known URL for a session (used by the frame event).
func (r *Registry) URL(browserID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[browserID]; ok {
		return e.url
	}
	return ""
}

// Navigate points a live session at a new URL (Studio's editable URL bar in
// the live-browser toolbar). Updates the tracked URL so the toolbar + mem_runs
// label stay accurate. Errors if the session isn't live.
func (r *Registry) Navigate(ctx context.Context, browserID, url string) error {
	if !r.isLive(browserID) {
		return fmt.Errorf("browser session not found or already closed")
	}
	res, err := r.backend.Navigate(ctx, browserID, url)
	if err != nil {
		return err
	}
	if res != nil && res.URL != "" {
		r.UpdateURL(browserID, res.URL)
	} else {
		r.UpdateURL(browserID, url)
	}
	return nil
}

// Input forwards one raw human interaction (click/type/scroll) to a live
// session - the boss's manual takeover of the screencast. An event that
// expresses INTENT TO DRIVE implicitly claims control (controller=human) so the
// agent's write verbs yield instead of clobbering the boss mid-captcha; he
// hands back explicitly via the Studio button (SetController) or by going idle.
//
// Not every event is intent. Scrolling, moving the pointer and resizing the
// pane are how you WATCH the agent work, and treating them as a takeover made
// the browser unusable on 2026-08-30: reading the page stole control, the
// agent's next verb was refused with "the boss is driving", it called
// browser_request_takeover and blocked for up to five minutes waiting to be
// handed a browser the boss never meant to take. Worse, it self-perpetuated —
// he scrolled to see why it had stalled, and the scroll re-took control.
//
// So the rule is: touching the page takes it, looking at it does not.
func (r *Registry) Input(ctx context.Context, browserID string, ev InputEvent) error {
	if !r.isLive(browserID) {
		return fmt.Errorf("browser session not found or already closed")
	}
	if claimsControl(ev.Type) {
		r.claimHumanControl(browserID)
	}
	return r.backend.Input(ctx, browserID, ev)
}

// claimsControl reports whether a raw input event means "I am driving now".
//
//	click / text / key  — acting on the page. Intent.
//	scroll / move       — looking at the page. Not intent.
//	resize              — sizing the viewer, not the page. Not intent.
//
// Unknown types default to claiming, because a new event we forgot to classify
// is far more likely to be an interaction than a way of watching, and the cost
// of being wrong that way is a takeover the boss can hand straight back rather
// than a click landing on top of him.
func claimsControl(evType string) bool {
	switch strings.ToLower(strings.TrimSpace(evType)) {
	case "scroll", "move", "resize":
		return false
	default:
		return true
	}
}

// SetControlNotify wires the controller-change broadcaster (WS event so
// Studio renders the takeover state live). Called once from serve.go.
func (r *Registry) SetControlNotify(fn func(chatID, browserID, controller, reason string)) {
	r.mu.Lock()
	r.controlNotify = fn
	r.mu.Unlock()
}

// SetHelpNotify wires the "agent needs a human" pinger (Web Push). Called
// once from serve.go.
func (r *Registry) SetHelpNotify(fn func(chatID, browserID, reason string)) {
	r.mu.Lock()
	r.helpNotify = fn
	r.mu.Unlock()
}

// Controller reports who is driving a session. Unknown sessions read as
// agent-controlled (the zero value) so read verbs never block on stale ids.
func (r *Registry) Controller(browserID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e := r.sessions[browserID]; e != nil && e.controller != "" {
		return e.controller
	}
	return ControllerAgent
}

// SetController flips who is driving and broadcasts the change. reason is a
// short human phrase shown in Studio ("captcha", "manual input", "handed
// back").
func (r *Registry) SetController(browserID, controller, reason string) error {
	if controller != ControllerAgent && controller != ControllerHuman {
		return fmt.Errorf("controller must be %q or %q", ControllerAgent, ControllerHuman)
	}
	r.mu.Lock()
	e := r.sessions[browserID]
	var notify func(chatID, browserID, controller, reason string)
	var chatID string
	if e != nil && !e.done && e.controller != controller {
		e.controller = controller
		e.lastUsed = time.Now()
		notify = r.controlNotify
		chatID = e.chatID
	}
	r.mu.Unlock()
	if e == nil {
		return fmt.Errorf("browser session not found or already closed")
	}
	if notify != nil {
		notify(chatID, browserID, controller, reason)
	}
	return nil
}

// claimHumanControl is Input's implicit takeover: first manual event flips
// the session to human control (no-op if already human).
func (r *Registry) claimHumanControl(browserID string) {
	r.mu.Lock()
	e := r.sessions[browserID]
	var notify func(chatID, browserID, controller, reason string)
	var chatID string
	if e != nil && !e.done && e.controller != ControllerHuman {
		e.controller = ControllerHuman
		notify = r.controlNotify
		chatID = e.chatID
	}
	r.mu.Unlock()
	if notify != nil {
		notify(chatID, browserID, ControllerHuman, "manual input")
	}
}

// RequestTakeover is the agent-initiated half: flip to human control and
// ping the boss's phone with why. The paired AwaitAgentControl blocks the
// calling tool until he hands back (or times out).
func (r *Registry) RequestTakeover(browserID, reason string) error {
	if err := r.SetController(browserID, ControllerHuman, reason); err != nil {
		return err
	}
	r.mu.Lock()
	help := r.helpNotify
	var chatID string
	if e := r.sessions[browserID]; e != nil {
		chatID = e.chatID
	}
	r.mu.Unlock()
	if help != nil {
		help(chatID, browserID, reason)
	}
	return nil
}

// AwaitAgentControl blocks until the session is agent-controlled again, the
// timeout lapses, or ctx cancels. On timeout it reclaims control for the
// agent so a no-show never strands the session in human mode. Returns true
// if the human actually handed back, false on timeout/cancel.
func (r *Registry) AwaitAgentControl(ctx context.Context, browserID string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	for {
		if r.Controller(browserID) == ControllerAgent {
			return true
		}
		if time.Now().After(deadline) {
			_ = r.SetController(browserID, ControllerAgent, "takeover timed out")
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
		}
	}
}

// isLive reports whether a browser session id maps to a live entry and bumps
// its lastUsed so manual interaction defers the idle reaper.
func (r *Registry) isLive(browserID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[browserID]
	if ok && !e.done {
		e.lastUsed = time.Now()
		return true
	}
	return false
}

// Close tears down a session: stops the sidecar browser, ends the relay,
// closes the mem_runs row.
func (r *Registry) Close(ctx context.Context, browserID string) error {
	err := r.backend.Close(ctx, browserID)
	r.finish(ctx, browserID, err)
	return err
}

// finish closes the relay + mem_runs row exactly once for a session.
func (r *Registry) finish(ctx context.Context, browserID string, runErr error) {
	r.mu.Lock()
	e, ok := r.sessions[browserID]
	if !ok || e.done {
		r.mu.Unlock()
		return
	}
	e.done = true
	delete(r.sessions, browserID)
	if r.latestByChat[e.chatID] == browserID {
		delete(r.latestByChat, e.chatID)
	}
	r.mu.Unlock()

	if e.cancel != nil {
		e.cancel()
	}
	e.handle.Finish(ctx, runErr, "")
	infoLog.Printf("browser: closed session %s", browserID)
}

// janitor reaps core-side sessions idle past idleReap so a forgotten
// session's mem_runs row doesn't stay 'running' forever.
func (r *Registry) janitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		var stale []string
		now := time.Now()
		r.mu.Lock()
		for id, e := range r.sessions {
			if now.Sub(e.lastUsed) > idleReap {
				stale = append(stale, id)
			}
		}
		r.mu.Unlock()
		for _, id := range stale {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = r.Close(ctx, id)
			cancel()
		}
	}
}
