// Crosscheck tool — mandate_verify. The agent calls this before closing a
// high-stakes Mandate; a DIFFERENT LLM vendor audits the result against the
// criteria. The vendor pick + the call + folding the verdict back onto the
// mandate are mechanics in the crosscheck package; this tool is the thin
// agent-facing trigger. The close gate (mandate.Store.Close) enforces that a
// high-stakes mandate has a passing verdict — so this isn't optional theatre.
package tools

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/dopesoft/infinity/core/internal/crosscheck"
	"github.com/dopesoft/infinity/core/internal/mandate"
)

// RegisterCrosscheckTools wires mandate_verify. No-op when deps are nil.
func RegisterCrosscheckTools(r *Registry, verifier *crosscheck.Verifier, store *mandate.Store) {
	if r == nil || verifier == nil || store == nil {
		return
	}
	r.Register(&mandateVerifyTool{verifier: verifier, store: store})
}

type mandateVerifyTool struct {
	verifier *crosscheck.Verifier
	store    *mandate.Store
}

func (t *mandateVerifyTool) Name() string { return "mandate_verify" }
func (t *mandateVerifyTool) Description() string {
	return "Verify the current Mandate: a fresh, independent, deliberately-skeptical " +
		"pass — on the SAME model the boss is using (no other vendor, no extra cost) — " +
		"audits your result against each acceptance criterion and the evidence you " +
		"recorded. Required before you can close a high-stakes mandate: you don't get " +
		"to wave your own work through on the things that matter. Pass an optional " +
		"`result` summarizing what you produced; the criteria + evidence come from the " +
		"mandate. Returns the verdict (overall pass/fail, per-criterion). A rejected " +
		"criterion is flipped to fail, so if it fails, go fix what was flagged."
}
func (t *mandateVerifyTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"result":     map[string]any{"type": "string", "description": "Optional summary of what you produced, for the auditor's context."},
			"mandate_id": map[string]any{"type": "string", "description": "Optional; defaults to this session's open mandate."},
		},
	}
}
func (t *mandateVerifyTool) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "mandate_id")
	if id == "" {
		m, err := t.store.GetOpenForSession(ctx, SessionIDFromContext(ctx))
		if err != nil {
			return "", err
		}
		if m == nil {
			return "", errors.New("no open mandate for this session — pass mandate_id or open one first")
		}
		id = m.ID
	}
	verdict, err := t.verifier.Verify(ctx, id, strString(in, "result"))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok":         true,
		"mandate_id": id,
		"overall":    verdict.Overall,
		"passed":     verdict.Passed(),
		"confidence": verdict.Confidence,
		"auditor":    verdict.Auditor,
		"notes":      verdict.Notes,
		"criteria":   verdict.Criteria,
	})
	return string(out), nil
}
