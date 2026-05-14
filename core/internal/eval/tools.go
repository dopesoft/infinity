package eval

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// RegisterTools wires the two agent-facing verification tools:
//
//	eval_record    — record the outcome of an assembled capability
//	eval_scorecard — read a capability's success rate + regression trend
//
// Lives in package eval (which imports tools) so the tools register
// without a tools → eval cycle — same pattern as skills / extensions.
func RegisterTools(reg *tools.Registry, store *Store) {
	if reg == nil || store == nil {
		return
	}
	reg.Register(&evalRecordTool{store: store})
	reg.Register(&evalScorecardTool{store: store})
}

// ── eval_record ─────────────────────────────────────────────────────────────

type evalRecordTool struct{ store *Store }

func (t *evalRecordTool) Name() string { return "eval_record" }
func (t *evalRecordTool) Description() string {
	return "Record the outcome of an assembled capability — a skill, workflow, " +
		"tool, or extension. This is how you (and the boss) learn whether what " +
		"you built actually works, and catch one that's regressing. Pass an " +
		"`outcome` (success|failure|partial), an optional 0-100 `score` for " +
		"non-binary results, and `notes` on what happened. The workflow engine " +
		"auto-records every workflow run — use this for skills, tools, and your " +
		"own judgment calls after you rely on something."
}
func (t *evalRecordTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject_kind": map[string]any{"type": "string", "enum": []string{"skill", "workflow", "tool", "extension"}, "description": "What kind of thing you're grading."},
			"subject_name": map[string]any{"type": "string", "description": "Its name (the skill name, workflow name, tool name, …)."},
			"outcome":      map[string]any{"type": "string", "enum": []string{"success", "failure", "partial"}},
			"score":        map[string]any{"type": "integer", "description": "Optional 0-100 quality score for non-binary outcomes."},
			"notes":        map[string]any{"type": "string", "description": "What happened / why — the qualitative signal."},
			"run_id":       map[string]any{"type": "string", "description": "Optional pointer to the concrete run this outcome came from."},
		},
		"required": []string{"subject_kind", "subject_name", "outcome"},
	}
}
func (t *evalRecordTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	e := &Eval{
		SubjectKind: evalStr(in, "subject_kind"),
		SubjectName: evalStr(in, "subject_name"),
		Outcome:     Outcome(evalStr(in, "outcome")),
		Notes:       evalStr(in, "notes"),
		RunID:       evalStr(in, "run_id"),
		Source:      "agent",
	}
	if v, ok := in["score"].(float64); ok {
		s := int(v)
		e.Score = &s
	}
	if err := t.store.Record(ctx, e); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "subject": e.SubjectKind + ":" + e.SubjectName, "outcome": string(e.Outcome),
	})
	return string(out), nil
}

// ── eval_scorecard ──────────────────────────────────────────────────────────

type evalScorecardTool struct{ store *Store }

func (t *evalScorecardTool) Name() string { return "eval_scorecard" }
func (t *evalScorecardTool) Description() string {
	return "Get the scorecard for an assembled capability — success rate, " +
		"recent-vs-historical trend, average score, and whether it's regressing. " +
		"Check this before relying on a skill or workflow, or to spot a " +
		"capability that's degrading and needs attention."
}
func (t *evalScorecardTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"subject_kind": map[string]any{"type": "string", "enum": []string{"skill", "workflow", "tool", "extension"}},
			"subject_name": map[string]any{"type": "string"},
			"window":       map[string]any{"type": "integer", "description": "How many recent outcomes to roll up. Default 50."},
		},
		"required": []string{"subject_kind", "subject_name"},
	}
}
func (t *evalScorecardTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	kind := evalStr(in, "subject_kind")
	name := evalStr(in, "subject_name")
	if kind == "" || name == "" {
		return "", errors.New("eval_scorecard: subject_kind and subject_name are required")
	}
	window := 0
	if v, ok := in["window"].(float64); ok {
		window = int(v)
	}
	card, err := t.store.Scorecard(ctx, kind, name, window)
	if err != nil {
		return "", err
	}
	if card.Total == 0 {
		out, _ := json.Marshal(map[string]any{
			"subject": kind + ":" + name,
			"message": fmt.Sprintf("No outcomes recorded yet for %s %q.", kind, name),
		})
		return string(out), nil
	}
	b, _ := json.MarshalIndent(card, "", "  ")
	return string(b), nil
}

func evalStr(in map[string]any, key string) string {
	if v, ok := in[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}
