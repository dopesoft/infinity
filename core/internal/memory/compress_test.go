package memory

import "testing"

// TestIsLowValueObservation guards the plumbing filter: the batch compressor
// pulls every uncompressed observation regardless of hook, so without this
// gate PreToolUse markers and tool-loader traces were memorialized as episodic
// "facts" (e.g. "Tool loader initialized with 29 active tools"), polluting
// retrieval. These cases encode what must (not) be compressed.
func TestIsLowValueObservation(t *testing.T) {
	drop := []struct{ hook, raw string }{
		{"PreToolUse", "composio__GMAIL_FETCH_EMAILS"},
		{"PostToolUse", `load_tools: {"active_size":29,"loaded":["composio__GMAIL_FETCH_EMAILS"],"ttl_turns":2}`},
		{"PostToolUse", `tool_search: {"matches":[]}`},
		{"PostToolUse", `system_map: {"tools":42}`},
		{"PostToolUse", `surface_list: {"count":0,"items":[]}`},
	}
	for _, c := range drop {
		if !isLowValueObservation(c.hook, c.raw) {
			t.Errorf("expected low-value (drop): hook=%s raw=%q", c.hook, c.raw)
		}
	}
	keep := []struct{ hook, raw string }{
		{"TaskCompleted", "Drafted the Q3 board deck and shared it with Kai."},
		{"PostToolUseFailure", "error: composio GMAIL_FETCH_EMAILS 401 invalid api key"},
		{"PostToolUse", `composio__GMAIL_FETCH_EMAILS: {"emails":[{"subject":"Invoice due"}]}`},
		{"UserPromptSubmit", "triage my inbox and surface anything from investors"},
	}
	for _, c := range keep {
		if isLowValueObservation(c.hook, c.raw) {
			t.Errorf("expected keep (compress): hook=%s raw=%q", c.hook, c.raw)
		}
	}
}
