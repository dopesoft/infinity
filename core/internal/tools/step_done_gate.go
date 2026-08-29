package tools

import "context"

// The plan-step DONE-GATE seam.
//
// A plan step going green is the single most load-bearing claim the agent
// makes: it is what the boss reads on the dashboard and in the dock above the
// composer, it is what the plan's progress bar counts, and it is what makes a
// plan finish. Until now the only thing standing between "the model decided
// this is done" and that green tick was `verify_required`, which most steps
// never set. So "done" was, for most steps, a vibe.
//
// Meanwhile mem_mandates already held a real contract for exactly this —
// binary criteria, evidence per criterion, a close gate enforced in Go — and
// the two substrates had no connection. This seam is the connection, kept as
// an interface so the plan tools never learn what a mandate is: serve.go hands
// them a gate, and any future notion of "done" (a crosscheck, an eval, a
// deploy check) plugs into the same one method.
//
// Wiring lives at the ONE chokepoint every path reaches — plan_update — rather
// than at each caller, so no route to `done` can be added later that quietly
// bypasses it (CLAUDE.md Rule #1c).

// StepDoneGate decides whether a plan step is allowed to be marked done.
// Returning nil means yes; an error is shown to the model verbatim and must
// therefore explain what is missing and how to satisfy it.
type StepDoneGate interface {
	CheckStepDone(ctx context.Context, stepID string) error
}

// AttachStepDoneGate installs the gate on plan_update. Optional: without it a
// step's only gate is verify_required, exactly as before.
//
// Wiring (serve.go, next to RegisterMandateTools):
//
//	tools.AttachStepDoneGate(registry, mandateStore)
func AttachStepDoneGate(r *Registry, g StepDoneGate) bool {
	if r == nil || g == nil {
		return false
	}
	t, ok := r.Get("plan_update")
	if !ok {
		return false
	}
	pu, ok := t.(*planUpdate)
	if !ok {
		return false
	}
	pu.doneGate = g
	return true
}
