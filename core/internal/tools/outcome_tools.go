// outcome_tools.go - decision/commitment tracking, activating mem_outcomes.
//
// The heartbeat already CHECKS mem_outcomes every tick for overdue decisions
// ("did the thing the boss decided actually resolve by its follow-up date?"),
// but nothing ever WROTE the table - a dead loop the audit caught. These two
// generic tools close it: the agent records a decision with a follow-up date,
// and resolves it when done. Overdue-and-unresolved decisions then surface
// through the existing heartbeat check. Pure substrate, Rule #1: the judgment
// of WHAT to track lives with the agent (guided by soul.md), the table + tools
// are generic.
package tools

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterOutcomeTools wires outcome_track + outcome_resolve. No-op without a pool.
func RegisterOutcomeTools(r *Registry, pool *pgxpool.Pool) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&outcomeTrack{pool: pool})
	r.Register(&outcomeResolve{pool: pool})
}

// ── outcome_track ──────────────────────────────────────────────────────────

type outcomeTrack struct{ pool *pgxpool.Pool }

func (t *outcomeTrack) Name() string { return "outcome_track" }
func (t *outcomeTrack) Description() string {
	return "Record a decision or commitment that has a follow-up: what was decided and when to check it landed. Use when the boss decides something with a deadline ('ship X by Friday', 'follow up with the client next week') so you can hold yourself accountable - overdue, unresolved outcomes resurface automatically. Resolve it with outcome_resolve when done."
}
func (t *outcomeTrack) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"decision":         map[string]any{"type": "string", "description": "What was decided / committed to, in one line."},
			"follow_up_in_days": map[string]any{"type": "number", "description": "How many days from now to check it resolved (e.g. 1, 3, 7). Use this OR follow_up_at."},
			"follow_up_at":     map[string]any{"type": "string", "description": "Optional explicit RFC3339 timestamp for the follow-up, instead of follow_up_in_days."},
		},
		"required": []string{"decision"},
	}
}
func (t *outcomeTrack) Execute(ctx context.Context, in map[string]any) (string, error) {
	decision := strString(in, "decision")
	if decision == "" {
		return "", fmt.Errorf("decision is required")
	}
	followUp := time.Now().Add(72 * time.Hour) // sensible default: 3 days
	if v, ok := in["follow_up_in_days"].(float64); ok && v > 0 {
		followUp = time.Now().Add(time.Duration(v*24) * time.Hour)
	} else if s := strString(in, "follow_up_at"); s != "" {
		if parsed, err := time.Parse(time.RFC3339, s); err == nil {
			followUp = parsed
		}
	}
	var id string
	err := t.pool.QueryRow(ctx, `
		INSERT INTO mem_outcomes (decision_text, decided_at, follow_up_at, status)
		VALUES ($1, NOW(), $2, 'open')
		RETURNING id::text
	`, decision, followUp).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("outcome_track: %w", err)
	}
	return fmt.Sprintf(`{"ok":true,"id":"%s","follow_up_at":"%s"}`, id, followUp.UTC().Format(time.RFC3339)), nil
}

// ── outcome_resolve ────────────────────────────────────────────────────────

type outcomeResolve struct{ pool *pgxpool.Pool }

func (t *outcomeResolve) Name() string { return "outcome_resolve" }
func (t *outcomeResolve) Description() string {
	return "Mark a tracked decision (from outcome_track) as resolved, with a one-line result. Use when a commitment you were tracking actually landed, so it stops resurfacing as overdue."
}
func (t *outcomeResolve) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":         map[string]any{"type": "string", "description": "The outcome id returned by outcome_track."},
			"resolution": map[string]any{"type": "string", "description": "One line on how it resolved."},
		},
		"required": []string{"id"},
	}
}
func (t *outcomeResolve) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	if id == "" {
		return "", fmt.Errorf("id is required")
	}
	tag, err := t.pool.Exec(ctx, `
		UPDATE mem_outcomes
		   SET status = 'resolved', resolution_text = $2
		 WHERE id = $1::uuid AND status = 'open'
	`, id, strString(in, "resolution"))
	if err != nil {
		return "", fmt.Errorf("outcome_resolve: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return `{"ok":false,"reason":"no open outcome with that id"}`, nil
	}
	return `{"ok":true}`, nil
}
