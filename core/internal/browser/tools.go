package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// The six-verb agent surface. The whole contract is the observe → act →
// extract loop: open a page, observe to get numbered elements + text, act on
// an element BY INDEX, extract clean content when you're ready to scrape.
// Acting by index (resolved against a live data-jarvis-idx attribute) plus
// the sidecar's network-idle auto-wait is what keeps this from being janky.

const sessionIDDesc = "Browser session id from browser_open. Optional — defaults to the most recent session in this chat."

// resolveSession returns the browser session id to act on, or an error the
// agent can act on (open a session first).
func (r *Registry) resolveSession(ctx context.Context, input map[string]any) (string, error) {
	chatID := tools.SessionIDFromContext(ctx)
	explicit, _ := input["session_id"].(string)
	id, ok := r.Resolve(chatID, strings.TrimSpace(explicit))
	if !ok {
		return "", errors.New("no open browser session — call browser_open (or browser_navigate with a url) first")
	}
	return id, nil
}

// resolveOrOpen is resolveSession with auto-recovery: if there's no live
// session for this chat, it opens a fresh one instead of erroring. This is a
// Rule #1b mechanic — "you need a browser session before you can look at a
// page" is something the CODE guarantees, not something the model has to
// remember to do with browser_open first. The reference failure: the agent
// called browser_extract before browser_open, got "no open browser session",
// and punted to telling the boss to navigate himself. Now the verb just opens
// the session it needs and carries on.
func (r *Registry) resolveOrOpen(ctx context.Context, input map[string]any) (string, error) {
	chatID := tools.SessionIDFromContext(ctx)
	explicit, _ := input["session_id"].(string)
	if id, ok := r.Resolve(chatID, strings.TrimSpace(explicit)); ok {
		return id, nil
	}
	info, err := r.Open(ctx, chatID, "")
	if err != nil {
		return "", err
	}
	return info.SessionID, nil
}

// recoverSession is the shared dead-session mechanic. When a verb fails
// because the session is gone, evict it (BOTH halves — the sidecar keeps
// holding the slot against its cap otherwise) and open a replacement, at
// atURL when the caller has a target or at the session's last known URL when
// it doesn't. Returns the fresh session so the caller can carry on.
//
// Rule #1b: "the session died, open another" is a mechanic, so it lives in
// code. It must not depend on the model remembering to call browser_open
// again — the reference failure is exactly that, an agent bouncing off
// "context canceled" and punting the task back to the boss.
//
// Returns the ORIGINAL error untouched when the session is not actually dead,
// so a real page-level failure is never laundered into a session restart.
func (r *Registry) recoverSession(ctx context.Context, browserID, atURL string, cause error) (*SessionInfo, error) {
	if strings.TrimSpace(atURL) == "" {
		if last := r.URL(browserID); last != "about:blank" {
			atURL = last
		}
	}
	priorRecoveries := r.Recoveries(browserID)
	if !r.EvictIfDead(ctx, browserID, cause) {
		return nil, cause
	}
	// Recovering forever is how a broken browser masquerades as a working one:
	// every verb quietly reopens, the turn keeps reporting progress, and the
	// underlying fault never surfaces. Two replacements is a hiccup; a third
	// is a fault, and a fault must be loud enough to reach the backlog rather
	// than be absorbed. The original cause is wrapped so the failure names the
	// real problem instead of the symptom we happened to stop on.
	if priorRecoveries >= maxAutoRecoveries {
		return nil, fmt.Errorf(
			"the browser session has died and been reopened %d times in a row, so something is wrong with the browser itself rather than with this page: %w",
			priorRecoveries, cause)
	}
	chatID := tools.SessionIDFromContext(ctx)
	info, err := r.Open(ctx, chatID, strings.TrimSpace(atURL))
	if err != nil {
		return nil, fmt.Errorf("the browser session had closed unexpectedly and opening a replacement failed: %w", err)
	}
	r.SetRecoveries(info.SessionID, priorRecoveries+1)
	r.UpdateURL(info.SessionID, info.URL)
	return info, nil
}

// maxAutoRecoveries caps how many times in a row a dying session may be
// silently replaced before the failure is surfaced instead.
const maxAutoRecoveries = 2

// ── browser_open ─────────────────────────────────────────────────────────

type OpenTool struct{ Reg *Registry }

func (t *OpenTool) Name() string { return "browser_open" }
func (t *OpenTool) Description() string {
	return "Open a cloud browser session and optionally navigate to a URL. Returns a session id and the live page is streamed to the Preview pane (column 3) so the boss can watch. Follow with browser_observe to see what's on the page."
}
func (t *OpenTool) ReadOnly() bool { return true }
func (t *OpenTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{"type": "string", "description": "URL to open immediately (optional). e.g. https://google.com"},
		},
	}
}
func (t *OpenTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	chatID := tools.SessionIDFromContext(ctx)
	url, _ := input["url"].(string)
	info, err := t.Reg.Open(ctx, chatID, strings.TrimSpace(url))
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Browser session opened: %s\n", info.SessionID)
	if info.URL != "" && info.URL != "about:blank" {
		fmt.Fprintf(&b, "URL: %s\n", info.URL)
		if info.Title != "" {
			fmt.Fprintf(&b, "Title: %s\n", info.Title)
		}
		t.Reg.UpdateURL(info.SessionID, info.URL)
		b.WriteString("\nCall browser_observe to see the page's interactive elements and text.")
	} else {
		b.WriteString("\nNothing loaded yet. Call browser_navigate with a URL.")
	}
	if info.Error != "" {
		fmt.Fprintf(&b, "\nNote: %s", info.Error)
	}
	return b.String(), nil
}

// ── browser_navigate ───────────────────────────────────────────────────────

type NavigateTool struct{ Reg *Registry }

func (t *NavigateTool) Name() string { return "browser_navigate" }
func (t *NavigateTool) Description() string {
	return "Navigate the browser to a URL and wait for the page to settle (network idle). Returns the final URL + title. Follow with browser_observe."
}
func (t *NavigateTool) ReadOnly() bool { return true }
func (t *NavigateTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": sessionIDDesc},
			"url":        map[string]any{"type": "string", "description": "URL to navigate to."},
		},
		"required": []string{"url"},
	}
}
func (t *NavigateTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	url, _ := input["url"].(string)
	if strings.TrimSpace(url) == "" {
		return "", errors.New("url is required")
	}
	chatID := tools.SessionIDFromContext(ctx)
	explicit, _ := input["session_id"].(string)
	id, ok := t.Reg.Resolve(chatID, strings.TrimSpace(explicit))
	if !ok {
		// No session yet — open one straight onto the target URL instead of
		// erroring. "Navigate the browser to X" is a complete instruction even
		// if the agent skipped browser_open; the mechanic lives here, not in the
		// model's memory of "call browser_open first" (Rule #1b).
		info, err := t.Reg.Open(ctx, chatID, url)
		if err != nil {
			return "", err
		}
		t.Reg.UpdateURL(info.SessionID, info.URL)
		out := fmt.Sprintf("Opened a browser and navigated to %s\nTitle: %s\n\nThe live page is now in the boss's Preview pane. Call browser_observe to see what's on it.", info.URL, info.Title)
		if info.Error != "" {
			out += "\nNote: " + info.Error
		}
		return out, nil
	}
	if err := t.Reg.yieldToHuman(id); err != nil {
		return "", err
	}
	res, err := t.Reg.backend.Navigate(ctx, id, url)
	if err != nil {
		// The session died under us: evict it and open a fresh one straight
		// onto the target URL. recoverSession returns the original error
		// unchanged when the session was fine and the navigate simply failed.
		info, rerr := t.Reg.recoverSession(ctx, id, url, err)
		if rerr != nil {
			return "", rerr
		}
		out := fmt.Sprintf("The previous browser session had closed unexpectedly. Opened a fresh session and navigated to %s\nTitle: %s\n\nCall browser_observe to see the page.", info.URL, info.Title)
		if info.Error != "" {
			out += "\nNote: " + info.Error
		}
		return out, nil
	}
	t.Reg.UpdateURL(id, res.URL)
	out := fmt.Sprintf("Navigated to %s\nTitle: %s\n\nCall browser_observe to see what's on the page.", res.URL, res.Title)
	if res.Error != "" {
		out += "\nNote: " + res.Error
	}
	return out, nil
}

// ── browser_observe ──────────────────────────────────────────────────────

type ObserveTool struct{ Reg *Registry }

func (t *ObserveTool) Name() string { return "browser_observe" }
func (t *ObserveTool) Description() string {
	return "Look at the current page: returns every visible interactive element numbered by index, plus the page's readable text. Pick an index, then use browser_act to click/type on it. This is your eyes — observe before you act."
}
func (t *ObserveTool) ReadOnly() bool { return true }
func (t *ObserveTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": sessionIDDesc},
		},
	}
}
func (t *ObserveTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, err := t.Reg.resolveOrOpen(ctx, input)
	if err != nil {
		return "", err
	}
	res, err := t.Reg.backend.Observe(ctx, id)
	if err != nil {
		info, rerr := t.Reg.recoverSession(ctx, id, "", err)
		if rerr != nil {
			return "", rerr
		}
		if res, err = t.Reg.backend.Observe(ctx, info.SessionID); err != nil {
			return "", err
		}
		t.Reg.UpdateURL(info.SessionID, res.URL)
		return "(the browser session had closed unexpectedly, so I reopened it before looking)\n\n" + formatObserve(res), nil
	}
	t.Reg.UpdateURL(id, res.URL)
	return formatObserve(res), nil
}

func formatObserve(res *ObserveResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\nTitle: %s\n\n", res.URL, res.Title)
	if len(res.Elements) == 0 {
		b.WriteString("No interactive elements found.\n")
	} else {
		b.WriteString("Interactive elements (use browser_act with the index):\n")
		for _, el := range res.Elements {
			b.WriteString(formatElement(el))
			b.WriteByte('\n')
		}
	}
	if strings.TrimSpace(res.Text) != "" {
		b.WriteString("\nPage text:\n")
		b.WriteString(res.Text)
	}
	return b.String()
}

func formatElement(el Element) string {
	label := el.Text
	if label == "" {
		label = el.Placeholder
	}
	line := fmt.Sprintf("[%d] %s", el.Idx, el.Tag)
	if el.Type != "" && el.Type != el.Tag {
		line += "(" + el.Type + ")"
	}
	if label != "" {
		line += fmt.Sprintf(" %q", label)
	}
	var extra []string
	if el.Placeholder != "" && el.Placeholder != label {
		extra = append(extra, "placeholder: "+el.Placeholder)
	}
	if el.Value != "" {
		extra = append(extra, "value: "+el.Value)
	}
	if el.Href != "" {
		href := el.Href
		if len(href) > 60 {
			href = href[:60] + "…"
		}
		extra = append(extra, "→ "+href)
	}
	if len(extra) > 0 {
		line += " (" + strings.Join(extra, ", ") + ")"
	}
	return line
}

// ── browser_act ────────────────────────────────────────────────────────────

type ActTool struct{ Reg *Registry }

func (t *ActTool) Name() string { return "browser_act" }
func (t *ActTool) Description() string {
	return "Act on an element from the last browser_observe, by index. action is one of: click, type, select, press, scroll, clear. For type/select/press pass value (the text to type, the option to select, or the key like Enter). Always pass label — a short description of what you're acting on (e.g. \"Buy now button\") — so risky transactions can be confirmed. The page auto-waits to settle after the action."
}
func (t *ActTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": sessionIDDesc},
			"index":      map[string]any{"type": "integer", "description": "Element index from the last browser_observe."},
			"action":     map[string]any{"type": "string", "enum": []string{"click", "type", "select", "press", "scroll", "clear"}},
			"value":      map[string]any{"type": "string", "description": "Text to type, option to select, or key to press (e.g. Enter). Omit for click/scroll/clear."},
			"label":      map[string]any{"type": "string", "description": "Short human description of the element/action, e.g. \"Search button\" or \"Place order\". Used for safety confirmation on transactional actions."},
		},
		"required": []string{"index", "action"},
	}
}
func (t *ActTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, err := t.Reg.resolveSession(ctx, input)
	if err != nil {
		return "", err
	}
	if err := t.Reg.yieldToHuman(id); err != nil {
		return "", err
	}
	idx, ok := asInt(input["index"])
	if !ok {
		return "", errors.New("index is required (integer from browser_observe)")
	}
	action, _ := input["action"].(string)
	if strings.TrimSpace(action) == "" {
		return "", errors.New("action is required (click|type|select|press|scroll|clear)")
	}
	value, _ := input["value"].(string)
	res, err := t.Reg.backend.Act(ctx, id, ActRequest{Index: idx, Action: action, Value: value})
	if err != nil {
		// Evict a dead session so it stops occupying a sidecar slot, but do
		// NOT silently retry on a fresh one the way the read verbs do: a
		// replacement browser is a different page, so the element indexes from
		// the last observe mean nothing and a blind re-click could act on the
		// wrong thing.
		if t.Reg.EvictIfDead(ctx, id, err) {
			return "", errors.New("the browser session closed before that action could run, so I cleaned it up. Call browser_navigate to get back to the page and browser_observe again before acting — the element indexes from the last observe are stale now")
		}
		return "", err
	}
	if !res.OK && res.Error != "" {
		return "", errors.New(res.Error)
	}
	t.Reg.UpdateURL(id, res.URL)
	out := fmt.Sprintf("Done: %s on element %d. Now at %s", action, idx, res.URL)
	if res.Title != "" {
		out += " (" + res.Title + ")"
	}
	if res.Retried {
		out += "\n(the page had changed, so I re-observed and retried)"
	}
	out += "\n\nCall browser_observe to see the updated page."
	return out, nil
}

// ── browser_extract ──────────────────────────────────────────────────────

type ExtractTool struct{ Reg *Registry }

func (t *ExtractTool) Name() string { return "browser_extract" }
func (t *ExtractTool) Description() string {
	return "Extract the current page's main content as clean markdown — the reliable way to scrape listings, articles, or search results into a report. Use this instead of guessing from the raw page text."
}
func (t *ExtractTool) ReadOnly() bool { return true }
func (t *ExtractTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": sessionIDDesc},
		},
	}
}
func (t *ExtractTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, err := t.Reg.resolveOrOpen(ctx, input)
	if err != nil {
		return "", err
	}
	res, err := t.Reg.backend.Extract(ctx, id, "markdown")
	if err != nil {
		info, rerr := t.Reg.recoverSession(ctx, id, "", err)
		if rerr != nil {
			return "", rerr
		}
		if res, err = t.Reg.backend.Extract(ctx, info.SessionID, "markdown"); err != nil {
			return "", err
		}
		t.Reg.UpdateURL(info.SessionID, res.URL)
	} else {
		t.Reg.UpdateURL(id, res.URL)
	}
	return fmt.Sprintf("# %s\nSource: %s\n\n%s", res.Title, res.URL, res.Content), nil
}

// ── browser_close ──────────────────────────────────────────────────────────

type CloseTool struct{ Reg *Registry }

func (t *CloseTool) Name() string { return "browser_close" }
func (t *CloseTool) Description() string {
	return "Close a browser session and free the cloud browser. Do this when the task is done."
}
func (t *CloseTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": sessionIDDesc},
		},
	}
}
func (t *CloseTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, err := t.Reg.resolveSession(ctx, input)
	if err != nil {
		// Nothing open is not a failure to hand back — closing is idempotent.
		return "No open browser session to close.", nil
	}
	if err := t.Reg.Close(ctx, id); err != nil {
		return "", err
	}
	return "Browser session closed.", nil
}

// yieldToHuman guards the write verbs (act/navigate): while the boss is
// driving a takeover, the agent's mutations refuse instead of clobbering
// him mid-captcha. Read verbs (observe/extract/screenshot) stay allowed.
func (r *Registry) yieldToHuman(browserID string) error {
	if r.Controller(browserID) == ControllerHuman {
		return errors.New("the boss is driving this browser right now (takeover in progress) — do not act on the page. Call browser_request_takeover to wait for him to hand control back, or continue other work")
	}
	return nil
}

// ── browser_request_takeover ───────────────────────────────────────────────

// requestTakeoverWait is how long the agent parks waiting for the boss to
// clear the blocker before reclaiming control. Long enough to grab a phone
// off a desk, short enough that a no-show doesn't strand the turn.
const requestTakeoverWait = 5 * time.Minute

type RequestTakeoverTool struct{ Reg *Registry }

func (t *RequestTakeoverTool) Name() string { return "browser_request_takeover" }
func (t *RequestTakeoverTool) Description() string {
	return "Hand the live browser to the boss and wait. Use when you hit something " +
		"only a human can clear: a CAPTCHA, a login/2FA prompt, a consent wall. " +
		"His phone gets pinged, the Preview pane becomes his to click and type in, " +
		"and this call blocks until he hands control back (or ~5 minutes pass). " +
		"When it returns after a handback, ALWAYS browser_observe before acting — " +
		"the page will have changed. Also call this to wait if he took over on his own."
}
func (t *RequestTakeoverTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": sessionIDDesc},
			"reason":     map[string]any{"type": "string", "description": "One short line the boss sees on his phone: what you need him to do (e.g. \"CAPTCHA on united.com checkout\")."},
		},
		"required": []string{"reason"},
	}
}
func (t *RequestTakeoverTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, err := t.Reg.resolveSession(ctx, input)
	if err != nil {
		return "", err
	}
	reason := strings.TrimSpace(strAny(input["reason"]))
	if reason == "" {
		return "", errors.New("reason is required — the boss needs to know what to do")
	}
	// If he's already driving (implicit takeover via manual input), don't
	// re-ping — just wait for the handback.
	if t.Reg.Controller(id) != ControllerHuman {
		if err := t.Reg.RequestTakeover(id, reason); err != nil {
			return "", err
		}
	}
	if t.Reg.AwaitAgentControl(ctx, id, requestTakeoverWait) {
		return "The boss handed control back. The page state has likely changed — call browser_observe before your next action.", nil
	}
	return "The boss didn't respond within the wait window; control has returned to you but the blocker (" + reason + ") is likely still there. Don't spin on it — park this task, notify him what's blocked, and move on.", nil
}

func strAny(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// AllTools returns the browser tools bound to the registry, for
// registration in serve.go.
func (r *Registry) AllTools() []tools.Tool {
	return []tools.Tool{
		&OpenTool{Reg: r},
		&NavigateTool{Reg: r},
		&ObserveTool{Reg: r},
		&ActTool{Reg: r},
		&ExtractTool{Reg: r},
		&CloseTool{Reg: r},
		&RequestTakeoverTool{Reg: r},
	}
}

func asInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	}
	return 0, false
}
