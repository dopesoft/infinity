// Curiosity tools - let the agent see and resolve the questions that
// appear in the dashboard's "Questions" card.
//
// These are NOT generic surface items (mem_surface_items). They live in
// mem_curiosity_questions and surface to Studio via the dashboard API's
// Approval loader. Without these tools the agent would call surface_list
// for "questions" - which it now does correctly - get 0 results, and
// have to tell the boss "I can't find anything to dismiss" while the UI
// is showing 5 cards. This file plugs that gap with question_list +
// question_decide.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterCuriosityTools wires question_list + question_decide. No-op
// when pool is nil so chat-only deployments don't break.
func RegisterCuriosityTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&questionList{pool: pool})
	r.Register(&questionDecide{pool: pool})
}

// ── question_list ─────────────────────────────────────────────────────────

type questionList struct{ pool *pgxpool.Pool }

func (t *questionList) Name() string   { return "question_list" }
func (t *questionList) ReadOnly() bool { return true }
func (t *questionList) Description() string {
	return "List the curiosity questions Jarvis has raised for the boss - the " +
		"items that render in the dashboard's 'Questions' card. Use this BEFORE " +
		"question_decide; ids are not shown in the UI. Filter by status " +
		"(default 'open'). Returns id, question, rationale, importance, kind, " +
		"created_at."
}
func (t *questionList) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"status": map[string]any{"type": "string", "enum": []string{"open", "asked", "answered", "dismissed", "approved", "all"}, "default": "open"},
			"limit":  map[string]any{"type": "integer", "default": 100},
		},
	}
}
func (t *questionList) Execute(ctx context.Context, in map[string]any) (string, error) {
	status := strDefault(in, "status", "open")
	limit := 100
	if v, ok := numFloat(in["limit"]); ok && v > 0 {
		limit = int(v)
	}
	if limit > 500 {
		limit = 500
	}
	// NOTE: the actual column on mem_curiosity_questions is `source_kind`
	// (gap | contradiction | low_confidence | uncovered_mention |
	// high_surprise - see migration 011). Alias it to `kind` in the
	// result so the JSON shape the agent reads stays stable.
	q := `SELECT id::text, question, COALESCE(rationale,''),
	             COALESCE(importance,0)::text, COALESCE(source_kind,'') AS kind,
	             to_char(created_at,'YYYY-MM-DD"T"HH24:MI:SSOF')
	        FROM mem_curiosity_questions`
	args := []any{}
	if status != "all" {
		q += ` WHERE status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY importance DESC NULLS LAST, created_at DESC LIMIT ` + fmt.Sprintf("%d", limit)
	return queryRowsAsJSON(ctx, t.pool, q, args,
		[]string{"id", "question", "rationale", "importance", "kind", "created_at"})
}

// ── question_decide ───────────────────────────────────────────────────────

type questionDecide struct{ pool *pgxpool.Pool }

func (t *questionDecide) Name() string { return "question_decide" }
func (t *questionDecide) Description() string {
	return "Resolve a curiosity question by id. decision='dismissed' removes it " +
		"from the dashboard (boss decided not to act). 'answered' marks it " +
		"handled with an optional answer string. 'approved' records that the " +
		"boss said go and the agent should act on it. Mirrors the same endpoint " +
		"the dashboard's dismiss button hits - UI updates live."
}
func (t *questionDecide) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string"},
			"decision": map[string]any{"type": "string", "enum": []string{"answered", "dismissed", "approved"}},
			"answer":   map[string]any{"type": "string", "description": "Optional - only used when decision='answered'."},
		},
		"required": []string{"id", "decision"},
	}
}
func (t *questionDecide) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", errors.New("id required")
	}
	decision := strString(in, "decision")
	switch decision {
	case "answered", "dismissed", "approved":
	default:
		return "", fmt.Errorf("decision must be answered|dismissed|approved, got %q", decision)
	}
	answer := strings.TrimSpace(strString(in, "answer"))
	ct, err := t.pool.Exec(ctx, `
		UPDATE mem_curiosity_questions
		   SET status      = $2,
		       answer      = CASE WHEN $3 <> '' THEN $3 ELSE answer END,
		       resolved_at = NOW()
		 WHERE id = $1::uuid
	`, id, decision, answer)
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":       ct.RowsAffected() > 0,
		"id":       id,
		"decision": decision,
		"updated":  ct.RowsAffected(),
	})
	return string(out), nil
}
