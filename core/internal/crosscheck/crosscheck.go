// Package crosscheck is the cross-vendor VERIFICATION pass for a Mandate.
//
// On the work that matters (high-stakes mandates), the agent doesn't get to
// grade its own homework: a DIFFERENT LLM vendor than the runtime brain audits
// the result against the mandate's binary criteria and the evidence the agent
// claimed for each. The verdict is folded back onto the criteria (a criterion
// the auditor rejects flips to fail) and stamped on the mandate; a passing
// overall verdict clears the high-stakes done-gate.
//
// The vendor selection + the call + the verdict persistence are MECHANICS (this
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
	Overall      string             `json:"overall"` // pass | fail
	Confidence   float64            `json:"confidence"`
	Notes        string             `json:"notes"`
	Criteria     []CriterionVerdict `json:"criteria"`
	Auditor      string             `json:"auditor"`       // the vendor that audited
	SingleVendor bool               `json:"single_vendor"` // true when no alternate vendor was available
}

// Passed reports an overall pass.
func (v Verdict) Passed() bool { return strings.EqualFold(v.Overall, "pass") }

// Verifier runs cross-vendor audits.
type Verifier struct {
	reg       *llm.Registry
	activeFn  func() string // returns the active brain's provider id
	store     *mandate.Store
}

// NewVerifier builds a Verifier. activeFn returns the currently-selected
// provider id (e.g. from settings) so we can pick a DIFFERENT vendor to audit.
func NewVerifier(reg *llm.Registry, store *mandate.Store, activeFn func() string) *Verifier {
	return &Verifier{reg: reg, store: store, activeFn: activeFn}
}

// familyOf collapses provider ids to a vendor family so openai and openai_oauth
// count as the same vendor (auditing OpenAI-with-OpenAI isn't a cross-check).
func familyOf(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	switch {
	case strings.HasPrefix(name, "openai"):
		return "openai"
	case strings.HasPrefix(name, "anthropic"), strings.HasPrefix(name, "claude"):
		return "anthropic"
	case strings.HasPrefix(name, "google"), strings.HasPrefix(name, "gemini"):
		return "google"
	}
	return name
}

// pickAuditor chooses an alternate provider whose family differs from the
// active brain. Returns (provider, name, singleVendor). singleVendor is true
// when only the active vendor's family is available — we still audit (with an
// adversarial persona) but flag that it wasn't truly cross-vendor.
func (v *Verifier) pickAuditor() (llm.Provider, string, bool) {
	if v == nil || v.reg == nil {
		return nil, "", false
	}
	active := ""
	if v.activeFn != nil {
		active = v.activeFn()
	}
	activeFamily := familyOf(active)
	avail := v.reg.Available()
	// First choice: a different family.
	for _, name := range avail {
		if familyOf(name) != activeFamily {
			if p, ok := v.reg.Get(name); ok {
				return p, name, false
			}
		}
	}
	// Fallback: same family (or the active provider) with an adversarial persona.
	if active != "" {
		if p, ok := v.reg.Get(active); ok {
			return p, active, true
		}
	}
	for _, name := range avail {
		if p, ok := v.reg.Get(name); ok {
			return p, name, true
		}
	}
	return nil, "", false
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
	provider, auditor, single := v.pickAuditor()
	if provider == nil {
		return Verdict{}, errors.New("no LLM provider available to crosscheck with")
	}

	var verdict Verdict
	handle := runs.BeginGlobal(ctx, runs.Kind("crosscheck"), mandateID,
		fmt.Sprintf("Crosscheck: %s", trim(m.Title, 60)), runs.SourceAgent)

	runErr := func() error {
		system := verifySystem
		if single {
			system = verifySystemSingleVendor
		}
		prompt := buildPrompt(m, resultNarrative)
		raw, cerr := llm.Complete(ctx, provider, "", system, prompt)
		if cerr != nil {
			return cerr
		}
		verdict, cerr = parseVerdict(raw)
		if cerr != nil {
			return cerr
		}
		verdict.Auditor = auditor
		verdict.SingleVendor = single

		// Fold per-criterion results back onto the mandate: an auditor REJECTION
		// flips that criterion to fail (so the done-gate holds). We don't auto-
		// promote pending→pass on the auditor's word; the agent owns claiming pass.
		for _, cv := range verdict.Criteria {
			if !cv.Pass {
				_ = v.store.CheckCriterion(ctx, mandateID, cv.ID, mandate.CritFail,
					"crosscheck ("+auditor+"): "+cv.Note)
			}
		}
		_ = v.store.SetCrosscheck(ctx, mandateID, verdictToMap(verdict), verdict.Passed())
		return nil
	}()

	summary := verdictSummary(verdict, single, runErr)
	if handle != nil {
		handle.SetMeta(ctx, map[string]any{
			"auditor":       auditor,
			"single_vendor": single,
			"overall":       verdict.Overall,
			"confidence":    verdict.Confidence,
			"notes":         verdict.Notes,
		})
		handle.Finish(ctx, runErr, summary)
	}
	if runErr != nil {
		return Verdict{}, runErr
	}
	return verdict, nil
}

func verdictSummary(v Verdict, single bool, err error) string {
	if err != nil {
		return "Crosscheck could not complete: " + err.Error()
	}
	tag := "cross-vendor"
	if single {
		tag = "single-vendor (no alternate brain available)"
	}
	if v.Passed() {
		return fmt.Sprintf("Passed crosscheck (%s by %s, confidence %.0f%%).", tag, v.Auditor, v.Confidence*100)
	}
	return fmt.Sprintf("Crosscheck FAILED (%s by %s): %s", tag, v.Auditor, trim(v.Notes, 200))
}

func verdictToMap(v Verdict) map[string]any {
	b, _ := json.Marshal(v)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

const verifySystem = `You are an independent auditor. You did NOT do this work; a different AI did, and you are checking it.

You'll be given a task's DEFINITION OF DONE — a list of binary acceptance criteria — and, for each, the evidence the worker claimed. Your job: for each criterion, decide whether the evidence ACTUALLY proves it. Be strict and skeptical. "I did the step" is not proof the step worked. Missing or hand-wavy evidence is a fail.

Return ONLY a JSON object, no prose, no code fences:
{
  "overall": "pass" | "fail",
  "confidence": 0.0,
  "notes": "1-3 sentences on what's solid and what isn't",
  "criteria": [ { "id": "c1", "pass": true, "note": "why" } ]
}

overall is "pass" ONLY if every criterion genuinely passes. If any criterion's evidence doesn't hold, overall is "fail". confidence is 0..1.`

const verifySystemSingleVendor = verifySystem + `

(Note: you are the same vendor family as the worker — there was no alternate model available. Compensate by being EXTRA adversarial: actively try to find a reason each criterion has NOT been met.)`

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
