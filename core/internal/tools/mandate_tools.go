// Mandate tools — the agent-facing surface for a per-task DEFINITION OF DONE.
//
// mandate_open / mandate_check / mandate_close let a recipe pin what "done"
// means as binary criteria and then prove each one. The MECHANICS live in
// mandate.Store: criterion ids, the "can't close until all pass" gate, and the
// "high_stakes needs a passing crosscheck" gate are all enforced there, so the
// model can't skip them by forgetting (Rule #1b). The JUDGMENT — when to open
// one, how to decompose, when it's high-stakes — lives in the seeded
// `frame-the-mandate` skill.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/mandate"
	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterMandateTools wires mandate_open / _check / _close / _abandon against a
// shared store (the same instance the ambient announcer is set on). No-op when
// the store is nil so chat-only deployments don't break registration.
// pool is optional: with it, mandate_open can bind a mandate to a plan step by
// its 1-based number as well as its uuid, resolved the same way every other
// plan tool resolves a step ref. Without it, only an explicit step id works.
func RegisterMandateTools(r *Registry, store *mandate.Store, pool *pgxpool.Pool) {
	if r == nil || store == nil {
		return
	}
	var plans *plan.Store
	if pool != nil {
		plans = plan.NewStore(pool)
	}
	r.Register(&mandateOpenTool{store: store, plans: plans})
	r.Register(&mandateCheckTool{store: store})
	r.Register(&mandateCloseTool{store: store})
	r.Register(&mandateAbandonTool{store: store})
}

// resolveMandateID returns the explicit id, or the session's open mandate when
// the agent omitted one (convenience, mirrors plan_tools resolving by session).
func resolveMandateID(ctx context.Context, store *mandate.Store, explicit string) (string, error) {
	if strings.TrimSpace(explicit) != "" {
		return strings.TrimSpace(explicit), nil
	}
	m, err := store.GetOpenForSession(ctx, SessionIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	if m == nil {
		return "", errors.New("no open mandate for this session — pass mandate_id or open one first")
	}
	return m.ID, nil
}

// ── mandate_open ──────────────────────────────────────────────────────────────

type mandateOpenTool struct {
	store *mandate.Store
	// plans resolves a positional step ref ("2") to a step id, and names the
	// plan the step belongs to. Nil without a pool.
	plans *plan.Store
}

func (t *mandateOpenTool) Name() string { return "mandate_open" }
func (t *mandateOpenTool) Description() string {
	return "Open a Mandate: a definition of done for the current task. Pass a " +
		"`title`, a one-line `summary`, and `criteria` — a list of BINARY, testable " +
		"acceptance conditions (each true/false, verifiable with evidence). You can't " +
		"close the mandate until every criterion is checked off as passed, so write " +
		"criteria you can actually prove ('go build exits 0', 'shared with kai@…', " +
		"'figures match the model'), never vague ones ('looks good'). Set " +
		"`high_stakes` true when being wrong is costly or hard to undo (money moves, " +
		"something is sent/published/deleted, the boss acts on it without re-checking) " +
		"— that forces a second-model crosscheck before close. Returns the mandate id " +
		"and the criteria with their ids."
}
func (t *mandateOpenTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":   map[string]any{"type": "string", "description": "Short name for the task. Required."},
			"summary": map[string]any{"type": "string", "description": "One line on what's being delivered."},
			"criteria": map[string]any{
				"type":        "array",
				"description": "The binary acceptance criteria. Each item is a string (the testable condition) OR an object {text, id?}. Aim for the things that actually fail: the output exists, it's correct, it's in the right place, it's verified.",
				"items":       map[string]any{"type": "string"},
			},
			"high_stakes": map[string]any{"type": "boolean", "description": "True when being wrong is expensive/irreversible. Requires mandate_verify (cross-model audit) before close. Default false."},
			"importance":  map[string]any{"type": "integer", "description": "0-100 optional ranking for the dashboard."},
			"step_id": map[string]any{
				"type": "string",
				"description": "Optional: bind this mandate to a plan step (its id, or its 1-based number in the current plan). " +
					"That step then CANNOT be marked done until every criterion here passes — the definition of done stops being " +
					"a separate note and becomes the gate on the thing the boss is watching. Use it whenever the mandate is the " +
					"definition of done for one step of the plan you're working.",
			},
		},
		"required": []string{"title", "criteria"},
	}
}
func (t *mandateOpenTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	m := &mandate.Mandate{
		Title:      strString(in, "title"),
		Summary:    strString(in, "summary"),
		HighStakes: boolVal(in, "high_stakes"),
		SessionID:  SessionIDFromContext(ctx),
		Source:     "agent",
	}
	if v, ok := in["importance"].(float64); ok {
		imp := int(v)
		m.Importance = &imp
	}
	m.Criteria = parseCriteria(in["criteria"])
	if len(m.Criteria) == 0 {
		return "", errors.New("a mandate needs at least one binary criterion — what does done look like?")
	}
	id, err := t.store.Open(ctx, m)
	if err != nil {
		return "", err
	}

	// Bind it to the plan step it defines done for, if one was named. The step
	// then inherits this mandate's gate: plan_update can't mark it done until
	// every criterion here passes.
	gated := ""
	if ref := strings.TrimSpace(strString(in, "step_id")); ref != "" && t.plans != nil {
		stepID, rerr := resolveStepRef(ctx, t.plans, ref)
		if rerr != nil {
			// The mandate exists and is useful; only the coupling failed. Say
			// which, rather than failing the whole call or claiming a gate
			// that isn't there.
			gated = "could not bind it to a plan step: " + rerr.Error()
		} else {
			planID := ""
			if st, gerr := t.plans.GetStep(ctx, stepID); gerr == nil && st != nil {
				planID = st.PlanID
			}
			if lerr := t.store.LinkPlanStep(ctx, id, planID, stepID); lerr != nil {
				gated = "could not bind it to that plan step: " + lerr.Error()
			} else {
				gated = "That plan step is now gated on this mandate — it can't be marked done until every criterion passes."
			}
		}
	}

	msg := fmt.Sprintf("Mandate opened with %d criteria. Check each off with mandate_check as you satisfy it; mandate_close when all pass.", len(m.Criteria))
	if gated != "" {
		msg += " " + gated
	}
	out, _ := json.Marshal(map[string]any{
		"ok":          true,
		"mandate_id":  id,
		"criteria":    m.Criteria,
		"high_stakes": m.HighStakes,
		"message":     msg,
	})
	return string(out), nil
}

// boolVal reads a bool from a tool-input map, tolerating the string "true".
func boolVal(in map[string]any, k string) bool {
	switch v := in[k].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// parseCriteria accepts either ["text", …] or [{text, id?}, …].
func parseCriteria(raw any) []mandate.Criterion {
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]mandate.Criterion, 0, len(arr))
	for i, item := range arr {
		switch v := item.(type) {
		case string:
			if strings.TrimSpace(v) == "" {
				continue
			}
			out = append(out, mandate.Criterion{ID: fmt.Sprintf("c%d", i+1), Text: v, Status: mandate.CritPending})
		case map[string]any:
			text := strString(v, "text")
			if strings.TrimSpace(text) == "" {
				continue
			}
			id := strString(v, "id")
			if id == "" {
				id = fmt.Sprintf("c%d", i+1)
			}
			out = append(out, mandate.Criterion{ID: id, Text: text, Status: mandate.CritPending})
		}
	}
	return out
}

// ── mandate_check ─────────────────────────────────────────────────────────────

type mandateCheckTool struct{ store *mandate.Store }

func (t *mandateCheckTool) Name() string { return "mandate_check" }
func (t *mandateCheckTool) Description() string {
	return "Mark one criterion of the current Mandate pass/fail, with the concrete " +
		"evidence that proves it (the command output, the URL, the figure). Pass the " +
		"criterion's `id` (from mandate_open) or its exact text. `mandate_id` is " +
		"optional — defaults to this session's open mandate. Only mark pass when it's " +
		"actually true; the close gate trusts these checks."
}
func (t *mandateCheckTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"criterion":  map[string]any{"type": "string", "description": "The criterion id (e.g. 'c2') or its exact text."},
			"status":     map[string]any{"type": "string", "enum": []string{"pass", "fail", "pending"}, "description": "Default 'pass'."},
			"evidence":   map[string]any{"type": "string", "description": "How it was verified — concrete proof, not 'done'."},
			"mandate_id": map[string]any{"type": "string", "description": "Optional; defaults to this session's open mandate."},
		},
		"required": []string{"criterion"},
	}
}
func (t *mandateCheckTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, err := resolveMandateID(ctx, t.store, strString(in, "mandate_id"))
	if err != nil {
		return "", err
	}
	status := strString(in, "status")
	if status == "" {
		status = mandate.CritPass
	}
	if err := t.store.CheckCriterion(ctx, id, strString(in, "criterion"), status, strString(in, "evidence")); err != nil {
		return "", err
	}
	m, _ := t.store.Get(ctx, id)
	remaining := 0
	if m != nil {
		remaining = len(m.FailingCriteria())
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "mandate_id": id, "criteria_remaining": remaining})
	return string(out), nil
}

// ── mandate_close ─────────────────────────────────────────────────────────────

type mandateCloseTool struct{ store *mandate.Store }

func (t *mandateCloseTool) Name() string { return "mandate_close" }
func (t *mandateCloseTool) Description() string {
	return "Close the current Mandate as done. This is GATED: it refuses unless " +
		"every criterion is checked off as passed, and (for a high-stakes mandate) " +
		"unless a passing mandate_verify has run. If it refuses, a criterion isn't " +
		"actually satisfied — go finish it, don't work around the gate. `mandate_id` " +
		"optional; defaults to this session's open mandate."
}
func (t *mandateCloseTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mandate_id": map[string]any{"type": "string", "description": "Optional; defaults to this session's open mandate."},
		},
	}
}
func (t *mandateCloseTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, err := resolveMandateID(ctx, t.store, strString(in, "mandate_id"))
	if err != nil {
		return "", err
	}
	m, err := t.store.Close(ctx, id)
	if err != nil {
		// The gate refusal IS the message — surface it as a tool error so the
		// model reads it and goes back to finish the work.
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":         true,
		"mandate_id": m.ID,
		"status":     m.Status,
		"message":    fmt.Sprintf("Mandate %q closed — all %d criteria verified.", m.Title, len(m.Criteria)),
	})
	return string(out), nil
}

// ── mandate_abandon ───────────────────────────────────────────────────────────

type mandateAbandonTool struct{ store *mandate.Store }

func (t *mandateAbandonTool) Name() string { return "mandate_abandon" }
func (t *mandateAbandonTool) Description() string {
	return "Drop the current Mandate when the boss changes direction or the task no " +
		"longer applies. Use this instead of force-closing a mandate whose criteria " +
		"will never be met. `mandate_id` optional; defaults to the session's open one."
}
func (t *mandateAbandonTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"mandate_id": map[string]any{"type": "string", "description": "Optional; defaults to this session's open mandate."},
		},
	}
}
func (t *mandateAbandonTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id, err := resolveMandateID(ctx, t.store, strString(in, "mandate_id"))
	if err != nil {
		return "", err
	}
	if err := t.store.Abandon(ctx, id); err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "mandate_id": id, "status": "abandoned"})
	return string(out), nil
}
