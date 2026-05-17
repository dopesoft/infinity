package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
)

// LoadTools is the model-callable tool that mutates the current session's
// ActiveSet - moves dormant catalog entries into the live tool surface so
// their schemas reach the next LLM call. Pair with tool_search (find) +
// load_tools (commit) so the model takes a deliberate two-step.
//
// Optional ttl_turns auto-unloads after N turns - keeps an exploratory
// load from squatting forever once the relevant work is done.
type LoadTools struct {
	Registry *Registry
}

func (l *LoadTools) Name() string { return "load_tools" }
func (l *LoadTools) Description() string {
	return "Add tools to the agent's active tool surface so their schemas are visible on the next turn. " +
		"Use after tool_search to commit candidates you actually want. Optional ttl_turns auto-unloads after N turns."
}
func (l *LoadTools) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"names": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Exact tool names to load (e.g. ['composio__GMAIL_SEND_EMAIL']).",
			},
			"ttl_turns": map[string]any{
				"type":        "integer",
				"description": "Optional auto-unload after this many agent turns. 0 or omitted = permanent until unload_tools.",
				"default":     0,
			},
		},
		"required": []string{"names"},
	}
}

func (l *LoadTools) Execute(ctx context.Context, input map[string]any) (string, error) {
	active := ActiveSetFromContext(ctx)
	if active == nil {
		return "", errors.New("load_tools requires a session-scoped active set (must be invoked from the agent loop)")
	}
	raw, ok := input["names"].([]any)
	if !ok {
		return `{"error":"names must be an array of tool name strings"}`, nil
	}
	names := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			if s = strings.TrimSpace(s); s != "" {
				names = append(names, s)
			}
		}
	}
	if len(names) == 0 {
		return `{"error":"no tool names provided"}`, nil
	}

	ttl := 0
	if v, ok := input["ttl_turns"].(float64); ok && v > 0 {
		ttl = int(v)
	}

	// Filter to names that actually exist - silently dropping ghosts so
	// the model gets a clean error report instead of a poisoned set.
	valid := make([]string, 0, len(names))
	missing := make([]string, 0)
	for _, n := range names {
		if _, ok := l.Registry.Get(n); ok {
			valid = append(valid, n)
		} else {
			missing = append(missing, n)
		}
	}
	active.Load(valid, ttl)

	body := map[string]any{
		"loaded":      valid,
		"ttl_turns":   ttl,
		"active_size": len(active.Names()),
	}
	if len(missing) > 0 {
		body["missing"] = missing
		body["hint"] = "Unknown tool names - check Connectors page or tool_search again."
	}
	b, _ := json.Marshal(body)
	return string(b), nil
}

// UnloadTools removes named tools from the active set. Pinned tools
// (the discipline core: delegate, tool_search, load_tools, compact, …)
// are silently ignored - the model can't accidentally lose its
// foundation.
type UnloadTools struct{}

func (u *UnloadTools) Name() string { return "unload_tools" }
func (u *UnloadTools) Description() string {
	return "Remove tools from the active tool surface to shrink the per-turn schema cost. Pinned core tools (delegate, tool_search, load_tools, compact) cannot be unloaded."
}
func (u *UnloadTools) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"names": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "Tool names to remove from the active set.",
			},
		},
		"required": []string{"names"},
	}
}

func (u *UnloadTools) Execute(ctx context.Context, input map[string]any) (string, error) {
	active := ActiveSetFromContext(ctx)
	if active == nil {
		return "", errors.New("unload_tools requires a session-scoped active set")
	}
	raw, ok := input["names"].([]any)
	if !ok {
		return `{"error":"names must be an array of tool name strings"}`, nil
	}
	names := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			names = append(names, strings.TrimSpace(s))
		}
	}
	active.Unload(names)
	body := map[string]any{
		"unloaded":    names,
		"active_size": len(active.Names()),
	}
	b, _ := json.Marshal(body)
	return string(b), nil
}
