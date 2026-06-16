package browser

// CamofoxBackend drives the Camoufox anti-detect browser server
// (redf0x1/camofox-browser) over its REST API. Camoufox is a Firefox fork that
// spoofs device fingerprints at the C++ engine level (no JS injection), so it
// passes Cloudflare / DataDome / reCAPTCHA where the plain chromedp sidecar gets
// blocked. We map the browser_* verb contract onto the server's OpenClaw +
// core routes; the engine choice is invisible to the tools and the agent.
//
// Wire contract (verified against camofox-browser src as of this commit):
//
//	create   POST   /tabs                      {userId, sessionKey, url?}   -> {tabId, url}
//	navigate POST   /navigate                  {userId, targetId, url}      -> {ok, url}
//	observe  GET    /snapshot?userId&targetId                                -> {url, snapshot(aria yaml w/ [eN] refs), refsCount}
//	act      POST   /act                        {userId, targetId, kind, ref:"e"+idx, ...}
//	extract  POST   /tabs/:id/evaluate          {userId, expression}         -> {ok, result}
//	shot     GET    /tabs/:id/screenshot?userId                              -> image/png bytes
//	close    DELETE /tabs/:id                   {userId}
//	health   GET    /health
//
// Refs are sequential e1..eN in document order, so our integer element index
// maps to a ref by "e"+index and back by trimming the "e". Refs reset on every
// navigation, which is why the observe→act loop must observe before each act —
// the seeded skill already enforces that.
//
// Auth: bearer CAMOFOX_API_KEY on every request; when the server sits behind a
// Cloudflare Access tunnel (the home-Mac residential instance) the CF-Access
// service-token headers ride along too, mirroring the claude_code bridge.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// camofoxFramePoll is the screenshot cadence for the live Preview pane. Camoufox
// has no CDP screencast, so we poll. ~1.2s keeps the watch-pane responsive
// without hammering a residential connection or the Firefox process.
const camofoxFramePoll = 1200 * time.Millisecond

// defaultCamofoxUser is the canonical-profile owner when CAMOFOX_USER_ID is
// unset. One stable user = one stable residential fingerprint for the box.
const defaultCamofoxUser = "jarvis"

type CamofoxBackend struct {
	baseURL  string
	apiKey   string
	userID   string
	cfID     string // Cloudflare Access service-token client id (empty = no CF Access)
	cfSecret string
	http     *http.Client
}

// NewCamofoxBackend returns a backend for the Camoufox server at baseURL, or nil
// when baseURL is empty so callers can treat nil as "this side not configured".
// cfClientID/cfClientSecret are set only for the Cloudflare-Access-fronted Mac
// instance; pass "" for the Railway private-network Cloud instance.
func NewCamofoxBackend(baseURL, apiKey, userID, cfClientID, cfClientSecret string) *CamofoxBackend {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	if strings.TrimSpace(userID) == "" {
		userID = defaultCamofoxUser
	}
	return &CamofoxBackend{
		baseURL:  baseURL,
		apiKey:   strings.TrimSpace(apiKey),
		userID:   strings.TrimSpace(userID),
		cfID:     strings.TrimSpace(cfClientID),
		cfSecret: strings.TrimSpace(cfClientSecret),
		http:     &http.Client{Timeout: 90 * time.Second},
	}
}

func (c *CamofoxBackend) setHeaders(req *http.Request) {
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if c.cfID != "" && c.cfSecret != "" {
		req.Header.Set("CF-Access-Client-Id", c.cfID)
		req.Header.Set("CF-Access-Client-Secret", c.cfSecret)
	}
}

// doJSON issues a JSON request and decodes a JSON response into out (out may be
// nil). Non-2xx is turned into an error carrying the server's {error} message.
func (c *CamofoxBackend) doJSON(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error != "" {
			return fmt.Errorf("camofox: %s", e.Error)
		}
		return fmt.Errorf("camofox: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(data, out)
	}
	return nil
}

func (c *CamofoxBackend) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("camofox health: HTTP %d", resp.StatusCode)
	}
	return nil
}

func (c *CamofoxBackend) CreateSession(ctx context.Context, target string) (*SessionInfo, error) {
	// sessionKey is required by POST /tabs; the first tab establishes the
	// canonical (proxy/geo/fingerprint) profile for this user, later tabs reuse
	// it. One shared session group is fine for our 1–2 concurrent browse tasks.
	body := map[string]any{"userId": c.userID, "sessionKey": "default"}
	if strings.TrimSpace(target) != "" {
		body["url"] = target
	}
	var out struct {
		TabID string `json:"tabId"`
		URL   string `json:"url"`
		Title string `json:"title"`
		Error string `json:"error"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/tabs", body, &out); err != nil {
		return nil, err
	}
	if out.TabID == "" {
		return nil, errors.New("camofox: tab create returned no tabId")
	}
	return &SessionInfo{SessionID: out.TabID, URL: out.URL, Title: out.Title, Error: out.Error}, nil
}

func (c *CamofoxBackend) Navigate(ctx context.Context, sessionID, target string) (*NavResult, error) {
	body := map[string]any{"userId": c.userID, "targetId": sessionID, "url": target}
	var out struct {
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/navigate", body, &out); err != nil {
		return nil, err
	}
	return &NavResult{URL: out.URL, Error: out.Error}, nil
}

func (c *CamofoxBackend) Observe(ctx context.Context, sessionID string) (*ObserveResult, error) {
	path := "/snapshot?userId=" + url.QueryEscape(c.userID) + "&targetId=" + url.QueryEscape(sessionID)
	var out struct {
		URL       string `json:"url"`
		Snapshot  string `json:"snapshot"`
		RefsCount int    `json:"refsCount"`
	}
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &out); err != nil {
		return nil, err
	}
	elements := parseAriaRefs(out.Snapshot)
	// Text carries the full annotated accessibility tree: it shows page
	// structure AND each interactive node's [eN] ref inline, which is exactly
	// how Camoufox/Playwright agents are meant to read a page.
	return &ObserveResult{URL: out.URL, Text: out.Snapshot, Elements: elements}, nil
}

// ariaRefLine matches an annotated aria-snapshot line, e.g.
//
//   - button "Sign in" [e7]
//   - textbox "Search" [e12]: ...
//
// capturing role, optional quoted name, and the eN ref id.
var ariaRefLine = regexp.MustCompile(`^\s*-\s+(\w+)(?:\s+"([^"]*)")?[^\[]*\[(e\d+)\]`)

func parseAriaRefs(snapshot string) []Element {
	var els []Element
	for _, line := range strings.Split(snapshot, "\n") {
		m := ariaRefLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		idx, err := strconv.Atoi(strings.TrimPrefix(m[3], "e"))
		if err != nil {
			continue
		}
		els = append(els, Element{Idx: idx, Tag: m[1], Role: m[1], Text: m[2]})
	}
	return els
}

func (c *CamofoxBackend) Act(ctx context.Context, sessionID string, req ActRequest) (*ActResult, error) {
	ref := "e" + strconv.Itoa(req.Index)

	// Camoufox /act has no "clear" kind: focus the field, select-all, delete.
	if req.Action == "clear" {
		if _, err := c.act(ctx, sessionID, map[string]any{"kind": "click", "ref": ref}); err != nil {
			return nil, err
		}
		if _, err := c.act(ctx, sessionID, map[string]any{"kind": "press", "key": "Control+a"}); err != nil {
			return nil, err
		}
		return c.act(ctx, sessionID, map[string]any{"kind": "press", "key": "Delete"})
	}

	fields := map[string]any{"kind": req.Action, "ref": ref}
	switch req.Action {
	case "type":
		fields["text"] = req.Value
	case "select":
		fields["value"] = req.Value
	case "press":
		// press acts on the focused element, not a ref.
		delete(fields, "ref")
		fields["key"] = req.Value
	case "scroll":
		// with a ref, /act scrolls the element into view.
	case "click":
		// ref-only.
	}
	return c.act(ctx, sessionID, fields)
}

func (c *CamofoxBackend) act(ctx context.Context, sessionID string, fields map[string]any) (*ActResult, error) {
	fields["userId"] = c.userID
	fields["targetId"] = sessionID
	var out struct {
		OK    bool   `json:"ok"`
		URL   string `json:"url"`
		Error string `json:"error"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/act", fields, &out); err != nil {
		return nil, err
	}
	return &ActResult{OK: out.OK, URL: out.URL, Error: out.Error}, nil
}

// Input is the manual human-takeover path. The Camoufox REST engine has no
// raw CDP input domain, but it DOES expose /tabs/:id/evaluate, so we drive the
// boss's click/type/scroll/key as in-page DOM events at the given coordinates.
// This is best-effort (synthetic events; trusted-event-only sites like some
// OAuth password fields may resist) - the high-fidelity path is the chromedp
// sidecar, and device-login itself goes through the CanvasAuthCard (the real
// page in the boss's own browser), so this covers ordinary in-app browsing.
// resize is a no-op: Camoufox has no viewport-override endpoint, and the page
// already renders at the server's fixed window.
func (c *CamofoxBackend) Input(ctx context.Context, sessionID string, ev InputEvent) error {
	expr := camofoxInputJS(ev)
	if expr == "" {
		return nil // move/resize or empty - nothing to do on this engine
	}
	body := map[string]any{"userId": c.userID, "expression": expr}
	var out struct {
		OK bool `json:"ok"`
	}
	return c.doJSON(ctx, http.MethodPost, "/tabs/"+url.PathEscape(sessionID)+"/evaluate", body, &out)
}

// camofoxInputJS renders one human interaction as a self-contained JS
// expression. Coordinates use elementFromPoint so a click lands on whatever the
// boss sees under the cursor; text/keys act on the focused element.
func camofoxInputJS(ev InputEvent) string {
	switch strings.ToLower(strings.TrimSpace(ev.Type)) {
	case "click":
		return fmt.Sprintf(`(function(){var e=document.elementFromPoint(%d,%d);`+
			`if(!e)return false;try{e.focus&&e.focus();}catch(_){}`+
			`e.dispatchEvent(new MouseEvent('mousedown',{bubbles:true,clientX:%d,clientY:%d}));`+
			`e.dispatchEvent(new MouseEvent('mouseup',{bubbles:true,clientX:%d,clientY:%d}));`+
			`e.click&&e.click();return true;})()`,
			int(ev.X), int(ev.Y), int(ev.X), int(ev.Y), int(ev.X), int(ev.Y))
	case "scroll":
		return fmt.Sprintf(`(function(){window.scrollBy(%d,%d);return true;})()`, int(ev.DeltaX), int(ev.DeltaY))
	case "text":
		return fmt.Sprintf(`(function(){var t=document.activeElement;if(!t)return false;`+
			`if('value' in t){t.value=(t.value||'')+%s;t.dispatchEvent(new Event('input',{bubbles:true}));}`+
			`else if(t.isContentEditable){t.textContent=(t.textContent||'')+%s;}return true;})()`,
			jsString(ev.Text), jsString(ev.Text))
	case "key":
		return camofoxKeyJS(ev.Key)
	default:
		return "" // move | resize | unknown → no-op on this engine
	}
}

// camofoxKeyJS handles the named keys that matter for forms: Enter submits,
// Backspace deletes, others fire a key event on the focused element.
func camofoxKeyJS(key string) string {
	k := strings.TrimSpace(key)
	switch k {
	case "Backspace":
		return `(function(){var t=document.activeElement;if(!t||!('value' in t))return false;` +
			`t.value=(t.value||'').slice(0,-1);t.dispatchEvent(new Event('input',{bubbles:true}));return true;})()`
	case "Enter":
		return `(function(){var t=document.activeElement;if(!t)return false;` +
			`['keydown','keyup'].forEach(function(tp){t.dispatchEvent(new KeyboardEvent(tp,{key:'Enter',keyCode:13,which:13,bubbles:true}));});` +
			`if(t.form&&t.form.requestSubmit){t.form.requestSubmit();}return true;})()`
	default:
		return fmt.Sprintf(`(function(){var t=document.activeElement;if(!t)return false;`+
			`['keydown','keyup'].forEach(function(tp){t.dispatchEvent(new KeyboardEvent(tp,{key:%s,bubbles:true}));});return true;})()`,
			jsString(k))
	}
}

// jsString safely encodes a Go string as a JS string literal (JSON is a valid
// subset of JS for this).
func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func (c *CamofoxBackend) Extract(ctx context.Context, sessionID, _ string) (*ExtractResult, error) {
	// No readability endpoint; evaluate innerText in-page. Capped so a huge DOM
	// can't blow the response. The seeded skill uses extractStructured via
	// browser_act when it needs schema'd scraping instead of prose.
	const expr = `(function(){var t=(document.body&&document.body.innerText)||'';` +
		`return {title:document.title,url:location.href,text:t.slice(0,200000)};})()`
	body := map[string]any{"userId": c.userID, "expression": expr}
	var out struct {
		OK     bool `json:"ok"`
		Result struct {
			Title string `json:"title"`
			URL   string `json:"url"`
			Text  string `json:"text"`
		} `json:"result"`
	}
	if err := c.doJSON(ctx, http.MethodPost, "/tabs/"+url.PathEscape(sessionID)+"/evaluate", body, &out); err != nil {
		return nil, err
	}
	return &ExtractResult{Title: out.Result.Title, URL: out.Result.URL, Content: out.Result.Text}, nil
}

func (c *CamofoxBackend) Screenshot(ctx context.Context, sessionID string) (*ShotResult, error) {
	path := "/tabs/" + url.PathEscape(sessionID) + "/screenshot?userId=" + url.QueryEscape(c.userID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		var e struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &e)
		if e.Error != "" {
			return nil, fmt.Errorf("camofox screenshot: %s", e.Error)
		}
		return nil, fmt.Errorf("camofox screenshot: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	return &ShotResult{DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)}, nil
}

func (c *CamofoxBackend) Close(ctx context.Context, sessionID string) error {
	// DELETE /tabs/:id reads userId from the JSON body.
	return c.doJSON(ctx, http.MethodDelete, "/tabs/"+url.PathEscape(sessionID), map[string]any{"userId": c.userID}, nil)
}

// SubscribeScreencast emulates a screencast by polling the screenshot endpoint
// so Studio's Preview pane works identically to the chromedp backend. The
// goroutine stops when ctx is cancelled (the registry's relay lifetime).
func (c *CamofoxBackend) SubscribeScreencast(ctx context.Context, sessionID string) (<-chan Frame, error) {
	out := make(chan Frame, 4)
	go func() {
		defer close(out)
		t := time.NewTicker(camofoxFramePoll)
		defer t.Stop()
		seq := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				shotCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
				shot, err := c.Screenshot(shotCtx, sessionID)
				cancel()
				if err != nil {
					// Page may be mid-navigation or the tab just closed; skip
					// this frame, the next tick supersedes it.
					continue
				}
				seq++
				select {
				case out <- Frame{Seq: seq, Frame: shot.DataURL}:
				case <-ctx.Done():
					return
				default:
					// Relay can't keep up; drop — newest frame wins.
				}
			}
		}
	}()
	return out, nil
}
