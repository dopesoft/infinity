package browser

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
		return "", errors.New("no open browser session — call browser_open first")
	}
	return id, nil
}

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
	id, err := t.Reg.resolveSession(ctx, input)
	if err != nil {
		return "", err
	}
	res, err := t.Reg.backend.Navigate(ctx, id, url)
	if err != nil {
		return "", err
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
	id, err := t.Reg.resolveSession(ctx, input)
	if err != nil {
		return "", err
	}
	res, err := t.Reg.backend.Observe(ctx, id)
	if err != nil {
		return "", err
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
	id, err := t.Reg.resolveSession(ctx, input)
	if err != nil {
		return "", err
	}
	res, err := t.Reg.backend.Extract(ctx, id, "markdown")
	if err != nil {
		return "", err
	}
	t.Reg.UpdateURL(id, res.URL)
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
		return "", err
	}
	if err := t.Reg.Close(ctx, id); err != nil {
		return "", err
	}
	return "Browser session closed.", nil
}

// AllTools returns the six browser tools bound to the registry, for
// registration in serve.go.
func (r *Registry) AllTools() []tools.Tool {
	return []tools.Tool{
		&OpenTool{Reg: r},
		&NavigateTool{Reg: r},
		&ObserveTool{Reg: r},
		&ActTool{Reg: r},
		&ExtractTool{Reg: r},
		&CloseTool{Reg: r},
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
