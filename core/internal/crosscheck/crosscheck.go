// Package crosscheck is the VERIFICATION pass for a Mandate.
//
// On the work that matters (high-stakes mandates), the agent doesn't get to
// close on its own say-so: a fresh, deliberately-skeptical pass — on the boss's
// OWN active model (gpt-5.x / whatever is selected), with an independent
// "you did not do this work, try to refute it" persona — audits the result
// against the mandate's binary criteria and the evidence claimed for each. The
// verdict is folded back onto the criteria (a rejected criterion flips to fail)
// and stamped on the mandate; a passing overall verdict clears the high-stakes
// done-gate.
//
// This uses the SAME model the boss chose — it is not a different vendor. The
// independence comes from a clean context + an adversarial persona, not from
// swapping brains. The call + the verdict persistence are MECHANICS (this
// package). Whether a mandate is high-stakes — i.e. whether this even runs — is
// JUDGMENT, in the frame-the-mandate skill.
package crosscheck

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/mandate"
	"github.com/dopesoft/infinity/core/internal/runs"
)

// CriterionVerdict is the auditor's call on one criterion.
type CriterionVerdict struct {
	ID   string `json:"id"`
	Pass bool   `json:"pass"`
	Note string `json:"note"`
}

// Verdict is the full audit outcome.
type Verdict struct {
	Overall    string             `json:"overall"` // pass | fail
	Confidence float64            `json:"confidence"`
	Notes      string             `json:"notes"`
	Criteria   []CriterionVerdict `json:"criteria"`
	Auditor    string             `json:"auditor"` // the model that audited (the boss's active model)
}

// Passed reports an overall pass.
func (v Verdict) Passed() bool { return strings.EqualFold(v.Overall, "pass") }

// Verifier runs verification audits on the boss's OWN active model.
type Verifier struct {
	reg      *llm.Registry
	activeFn func() (provider string, model string) // the currently-selected brain
	store    *mandate.Store
}

// NewVerifier builds a Verifier. activeFn returns the boss's currently-selected
// (provider, model) — e.g. ("openai_oauth", "gpt-5.4") from settings — so the
// audit runs on the SAME brain the boss is already paying for (his ChatGPT
// subscription), never a different vendor and never an extra-billed API.
func NewVerifier(reg *llm.Registry, store *mandate.Store, activeFn func() (string, string)) *Verifier {
	return &Verifier{reg: reg, store: store, activeFn: activeFn}
}

// pickAuditor returns the boss's active provider + model. The independence of
// the audit comes from a clean context + an adversarial persona, NOT from
// swapping vendors — so it always rides the selected brain and bills the same
// way the chat does. Falls back to the registry default only if the active
// provider can't be resolved.
func (v *Verifier) pickAuditor() (llm.Provider, string, string) {
	if v == nil || v.reg == nil {
		return nil, "", ""
	}
	name, model := "", ""
	if v.activeFn != nil {
		name, model = v.activeFn()
	}
	if name != "" {
		if p, ok := v.reg.Get(name); ok {
			return p, name, model
		}
	}
	// No usable active selection: use whatever single provider is registered,
	// on its default model. (Shouldn't happen in normal operation.)
	for _, n := range v.reg.Available() {
		if p, ok := v.reg.Get(n); ok {
			return p, n, ""
		}
	}
	return nil, "", ""
}

// Verify audits a mandate. resultNarrative is the agent's optional summary of
// what it produced; the criteria + their claimed evidence are pulled from the
// mandate. Books a mem_runs row ('crosscheck') carrying the verdict so it shows
// in /logs and the Mandate modal. Persists the verdict onto the mandate.
func (v *Verifier) Verify(ctx context.Context, mandateID, resultNarrative string) (Verdict, error) {
	if v == nil || v.store == nil {
		return Verdict{}, errors.New("crosscheck verifier not configured")
	}
	m, err := v.store.Get(ctx, mandateID)
	if err != nil {
		return Verdict{}, err
	}
	provider, auditorName, auditorModel := v.pickAuditor()
	if provider == nil {
		return Verdict{}, errors.New("no LLM provider available to verify with")
	}
	// Label the audit by the model when known (e.g. "gpt-5.4"), else the
	// provider id — this is the boss's own selected brain either way.
	auditorLabel := auditorModel
	if auditorLabel == "" {
		auditorLabel = auditorName
	}

	var verdict Verdict
	handle := runs.BeginGlobal(ctx, runs.Kind("crosscheck"), mandateID,
		fmt.Sprintf("Verify: %s", trim(m.Title, 60)), runs.SourceAgent)

	runErr := func() error {
		prompt := buildPrompt(m, resultNarrative)
		// Empty-string model falls back to the provider default; pass the boss's
		// selected model explicitly so the audit uses the EXACT brain he's on
		// (and bills the same — subscription, not a separate API).
		raw, cerr := llm.Complete(ctx, provider, auditorModel, verifySystem, prompt)
		if cerr != nil {
			return cerr
		}
		verdict, cerr = parseVerdict(raw)
		if cerr != nil {
			return cerr
		}
		verdict.Auditor = auditorLabel

		// Fold per-criterion results back onto the mandate: a REJECTION flips
		// that criterion to fail (so the done-gate holds). We don't auto-promote
		// pending→pass on the audit's word; the agent owns claiming pass.
		for _, cv := range verdict.Criteria {
			if !cv.Pass {
				_ = v.store.CheckCriterion(ctx, mandateID, cv.ID, mandate.CritFail,
					"verify: "+cv.Note)
			}
		}
		_ = v.store.SetCrosscheck(ctx, mandateID, verdictToMap(verdict), verdict.Passed())
		return nil
	}()

	summary := verdictSummary(verdict, runErr)
	if handle != nil {
		handle.SetMeta(ctx, map[string]any{
			"auditor":    auditorLabel,
			"overall":    verdict.Overall,
			"confidence": verdict.Confidence,
			"notes":      verdict.Notes,
		})
		handle.Finish(ctx, runErr, summary)
	}
	if runErr != nil {
		return Verdict{}, runErr
	}
	return verdict, nil
}

func verdictSummary(v Verdict, err error) string {
	if err != nil {
		return "Verification could not complete: " + err.Error()
	}
	if v.Passed() {
		return fmt.Sprintf("Passed verification (by %s, confidence %.0f%%).", v.Auditor, v.Confidence*100)
	}
	return fmt.Sprintf("Verification FAILED (by %s): %s", v.Auditor, trim(v.Notes, 200))
}

func verdictToMap(v Verdict) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

const verifySystem = `You are an independent auditor with a fresh, skeptical eye. Treat this as work someone ELSE did that you must check — do not assume it's correct because it looks plausible.

You'll be given a task's DEFINITION OF DONE — a list of binary acceptance criteria — and, for each, the evidence the worker claimed. For each criterion, decide whether the evidence ACTUALLY proves it. Be strict and adversarial: actively try to find a reason each criterion has NOT been met. "I did the step" is not proof the step worked. Missing or hand-wavy evidence is a fail.

Return ONLY a JSON object, no prose, no code fences:
{
  "overall": "pass" | "fail",
  "confidence": 0.0,
  "notes": "1-3 sentences on what's solid and what isn't",
  "criteria": [ { "id": "c1", "pass": true, "note": "why" } ]
}

overall is "pass" ONLY if every criterion genuinely passes. If any criterion's evidence doesn't hold, overall is "fail". confidence is 0..1.`

func buildPrompt(m *mandate.Mandate, resultNarrative string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "TASK: %s\n", m.Title)
	if strings.TrimSpace(m.Summary) != "" {
		fmt.Fprintf(&b, "SUMMARY: %s\n", m.Summary)
	}
	if strings.TrimSpace(resultNarrative) != "" {
		fmt.Fprintf(&b, "\nWORKER'S RESULT:\n%s\n", resultNarrative)
	}
	b.WriteString("\nACCEPTANCE CRITERIA (with the worker's claimed evidence):\n")
	for _, c := range m.Criteria {
		ev := strings.TrimSpace(c.Evidence)
		if ev == "" {
			ev = "(no evidence provided)"
		}
		fmt.Fprintf(&b, "- [%s] %s\n    claimed status: %s\n    evidence: %s\n", c.ID, c.Text, c.Status, ev)
	}
	b.WriteString("\nAudit each criterion now.")
	return b.String()
}

func parseVerdict(raw string) (Verdict, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	// Tolerate leading/trailing prose by slicing to the outermost braces.
	if i := strings.IndexByte(raw, '{'); i > 0 {
		raw = raw[i:]
	}
	if j := strings.LastIndexByte(raw, '}'); j >= 0 && j < len(raw)-1 {
		raw = raw[:j+1]
	}
	if raw == "" {
		return Verdict{}, errors.New("empty verdict")
	}
	var v Verdict
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return Verdict{}, fmt.Errorf("parse verdict: %w", err)
	}
	v.Overall = strings.ToLower(strings.TrimSpace(v.Overall))
	if v.Overall != "pass" && v.Overall != "fail" {
		// Default to fail on an ambiguous overall — fail-closed for verification.
		v.Overall = "fail"
	}
	if v.Confidence < 0 {
		v.Confidence = 0
	} else if v.Confidence > 1 {
		v.Confidence = 1
	}
	return v, nil
}

func trim(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
