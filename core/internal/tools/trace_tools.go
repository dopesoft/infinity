// Agent-callable tools for the LangSmith-style /logs feature.
//
// The boss asked for this so that when the agent's `self-improve-from-finding`
// skill is diagnosing a regression, it can read the actual per-turn
// execution log from `mem_observations` + `mem_predictions` + `mem_turns`
// instead of working from a summary. Three tools:
//
//   - traces_recent - list the last N turns (optionally filtered by session
//     or status). Each entry is a one-line summary the agent can scan.
//   - trace_inspect - pull one turn's full event timeline. Returns the
//     same shape /api/traces/{id} does (turn + events array).
//   - traces_search - fuzzy match over user_text + assistant_text + summary
//     + session name. Useful when the agent only remembers what the boss
//     was talking about, not the turn id.
//
// None of the three is pinned or default-loaded - the agent finds them via
// tool_search → load_tools the same way it discovers everything else.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterTraceTools wires traces_recent / trace_inspect / traces_search
// into the registry. No-op when pool is nil.
func RegisterTraceTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	store := memory.NewTurnStore(pool)
	r.Register(&tracesRecentTool{store: store})
	r.Register(&traceInspectTool{store: store})
	r.Register(&tracesSearchTool{store: store})
}

// ---- traces_recent ---------------------------------------------------------

type tracesRecentTool struct {
	store *memory.TurnStore
}

func (t *tracesRecentTool) Name() string { return "traces_recent" }
func (t *tracesRecentTool) Description() string {
	return "List the most recent agent turns from the LangSmith-style trace log. Returns id, status, user prompt, summary, tool-call count, latency. Use to find a turn id to drill into with trace_inspect."
}
func (t *tracesRecentTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"session_id": map[string]any{"type": "string", "description": "Optional - restrict to one session id."},
			"status":     map[string]any{"type": "string", "enum": []string{"in_flight", "ok", "empty", "errored", "interrupted"}, "description": "Optional - filter by outcome status."},
			"limit":      map[string]any{"type": "integer", "default": 20, "minimum": 1, "maximum": 100},
		},
	}
}
func (t *tracesRecentTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	if t.store == nil {
		return "", errors.New("trace store unavailable")
	}
	sessionID, _ := input["session_id"].(string)
	status, _ := input["status"].(string)
	limit := 20
	if v, ok := input["limit"].(float64); ok {
		limit = int(v)
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := t.store.List(ctx, sessionID, status, limit)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(rows)
	return string(out), nil
}

// ---- trace_inspect ---------------------------------------------------------

type traceInspectTool struct {
	store *memory.TurnStore
}

func (t *traceInspectTool) Name() string { return "trace_inspect" }
func (t *traceInspectTool) Description() string {
	return "Fetch the full per-turn timeline for one turn id: the turn row plus every observation, prediction, and trust contract tied to it. Use this BEFORE proposing a fix for a misbehaving tool or empty turn - it shows the real execution log, not your summary of it."
}
func (t *traceInspectTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"turn_id": map[string]any{"type": "string", "description": "UUID of the turn (from traces_recent / traces_search)."},
		},
		"required": []string{"turn_id"},
	}
}
func (t *traceInspectTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	if t.store == nil {
		return "", errors.New("trace store unavailable")
	}
	turnID, _ := input["turn_id"].(string)
	if strings.TrimSpace(turnID) == "" {
		return "", errors.New("turn_id is required")
	}
	row, err := t.store.Get(ctx, turnID)
	if err != nil {
		return "", err
	}
	events, err := t.store.Events(ctx, turnID)
	if err != nil {
		return "", err
	}
	if events == nil {
		events = []memory.TraceEvent{}
	}
	out, _ := json.Marshal(map[string]any{
		"turn":   row,
		"events": events,
	})
	return string(out), nil
}

// ---- traces_search ---------------------------------------------------------

type tracesSearchTool struct {
	store *memory.TurnStore
}

func (t *tracesSearchTool) Name() string { return "traces_search" }
func (t *tracesSearchTool) Description() string {
	return "Fuzzy-search recent turns by user prompt, assistant reply, summary, or session name. Returns matching turn rows sorted newest-first. Use when you remember what the boss was talking about but not the turn id."
}
func (t *tracesSearchTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Free-text query - matched ILIKE against user_text, assistant_text, summary, and session name."},
			"limit": map[string]any{"type": "integer", "default": 10, "minimum": 1, "maximum": 50},
		},
		"required": []string{"query"},
	}
}
func (t *tracesSearchTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	if t.store == nil {
		return "", errors.New("trace store unavailable")
	}
	q, _ := input["query"].(string)
	if strings.TrimSpace(q) == "" {
		return "", errors.New("query is required")
	}
	limit := 10
	if v, ok := input["limit"].(float64); ok {
		limit = int(v)
	}
	rows, err := t.store.Search(ctx, q, limit)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(rows)
	return string(out), nil
}
