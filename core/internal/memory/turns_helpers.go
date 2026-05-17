package memory

import (
	"encoding/json"
	"sort"
	"strings"
	"time"
)

// pgtimeWrap is a minimal time.Time wrapper that emits RFC3339 strings for
// JSON serialization. The traces API surfaces every timestamp as a string
// so the client doesn't have to deal with pg's TIMESTAMPTZ encoding.
type pgtimeWrap struct {
	t time.Time
}

func (p pgtimeWrap) iso() string {
	if p.t.IsZero() {
		return ""
	}
	return p.t.UTC().Format(time.RFC3339Nano)
}

// decodePayload parses an observation payload JSON string into a map. Empty
// / invalid payloads return nil so the trace API surfaces no payload at
// all rather than an awkward {} the client has to special-case.
func decodePayload(s string) map[string]any {
	s = strings.TrimSpace(s)
	if s == "" || s == "{}" || s == "null" {
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// obsKind maps a hook name to the timeline "kind" the /logs UI groups
// events by. Returns the lower-case hook name when no specific bucket
// applies - the UI just renders it as a generic observation.
func obsKind(hookName string) string {
	switch hookName {
	case "UserPromptSubmit":
		return "user"
	case "TaskCompleted":
		return "assistant"
	case "PreToolUse":
		return "tool_call"
	case "PostToolUse":
		return "tool_result"
	case "PostToolUseFailure":
		return "tool_error"
	case "ToolGated":
		return "gate"
	case "SessionStart":
		return "session_start"
	case "SessionEnd":
		return "session_end"
	default:
		return strings.ToLower(hookName)
	}
}

// hydrateFromPayload promotes a few known payload keys (name, input, output,
// tool_call_id) into TraceEvent's top-level fields so the client doesn't
// have to dig into the generic payload map for the common rendering path.
func hydrateFromPayload(e *TraceEvent) {
	if e == nil || e.Payload == nil {
		return
	}
	if v, ok := e.Payload["name"].(string); ok {
		e.ToolName = v
	}
	if v, ok := e.Payload["tool_call_id"].(string); ok {
		e.ToolCallID = v
	}
	if v, ok := e.Payload["output"].(string); ok {
		e.Output = v
	}
	if v, ok := e.Payload["reason"].(string); ok {
		e.Reason = v
	}
	if v, ok := e.Payload["input"]; ok {
		b, _ := json.Marshal(v)
		e.Input = string(b)
	}
}

// sortByTimestamp orders events by their RFC3339Nano string. String compare
// is correct for RFC3339 within the same offset and observations always
// carry UTC timestamps so this is safe.
func sortByTimestamp(events []TraceEvent) {
	sort.SliceStable(events, func(i, j int) bool {
		return events[i].Timestamp < events[j].Timestamp
	})
}
