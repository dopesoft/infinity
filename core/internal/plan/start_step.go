package plan

import "context"

// StepStart is the outcome of EnsureStepStarted: which step of the session's
// plan is now in flight, and whether this call is what put it there.
type StepStart struct {
	Plan *Plan `json:"plan"`
	Step *Step `json:"step"`
	// Started is true only when this call flipped the step to in_progress.
	// False means the step was already running and was left untouched.
	Started bool `json:"started"`
}

// StepStarter is the one-method seam the agent loop consumes, so the loop
// depends on the capability rather than on *Store.
type StepStarter interface {
	EnsureStepStarted(ctx context.Context, sessionID string) (*StepStart, error)
}

// EnsureStepStarted guarantees that a foreground session's approved plan has
// exactly one step in flight before consequential work begins.
//
// The mechanic lives here, in code, rather than in prose the model has to
// remember (Rule #1b): the 2026-07-02 failure was gpt-5.x drafting a plan and
// stopping without ever marking a step in_progress, which left the plan
// 'active' with nothing running and no signal that work had begun.
//
// It is idempotent by construction. A step already in_progress is returned
// unchanged (Started false), so calling this every turn can never produce a
// second concurrent step. Otherwise the first actionable step in plan order is
// marked in_progress through MarkStep, so it picks up the same started_at
// stamp, the same proposal chokepoint, and the same recompute as any normal
// plan progression. Returns nil when there is nothing to start: no session, no
// active plan, an unapproved proposal, a plan awaiting the boss at a
// checkpoint, or every step already settled.
func (s *Store) EnsureStepStarted(ctx context.Context, sessionID string) (*StepStart, error) {
	if s == nil || s.pool == nil || sessionID == "" {
		return nil, nil
	}
	// GetActiveBySession is already blind to proposals; startableStep re-checks
	// so the boundary holds for any caller that hands us a plan another way.
	p, err := s.GetActiveBySession(ctx, sessionID)
	if err != nil || p == nil {
		return nil, err
	}

	target, needsStart := startableStep(p)
	if target == nil {
		return nil, nil
	}
	if !needsStart {
		return &StepStart{Plan: p, Step: target}, nil
	}

	updated, err := s.MarkStep(ctx, target.ID, StepInProgress, "")
	if err != nil {
		return nil, err
	}
	return &StepStart{Plan: updated, Step: findStep(updated, target.ID), Started: true}, nil
}

// startableStep resolves which step of p should be in flight, and whether it
// still needs starting. Pure, so the whole policy is testable without a
// database — the same shape as abandonedSteps.
//
// Order matters: an in_progress step anywhere in the plan wins outright, which
// is what makes "exactly one in flight" true rather than merely intended.
func startableStep(p *Plan) (step *Step, needsStart bool) {
	if !planMayStart(p) {
		return nil, false
	}
	for i := range p.Steps {
		if p.Steps[i].Status == StepInProgress {
			return &p.Steps[i], false
		}
	}
	for i := range p.Steps {
		st := &p.Steps[i]
		// A pending checkpoint is the boss's decision, not ours. Starting it —
		// or reaching past it to a later step — would execute work he was asked
		// to approve first, so the plan stops here (same boundary abandonedSteps
		// draws for the terminal settle).
		if st.IsCheckpoint && st.Status == StepPending {
			return nil, false
		}
		if isActionableStep(st.Status) {
			return st, true
		}
	}
	return nil, false
}

// isActionableStep reports whether a step still has work to do.
//
// This deliberately does NOT reuse isTerminalStep: that predicate answers a
// different question ("is this step settled for bookkeeping purposes") and
// counts 'failed' as terminal, because the settle paths must never revive a
// step they already closed out. For resumption, a failed step IS actionable —
// a step that failed and a step blocked by verification are precisely the
// stalls the plan is meant to be picked back up from. 'done' and 'skipped' are
// the only genuinely finished states.
func isActionableStep(status string) bool {
	switch status {
	case StepPending, StepFailed, StepBlocked:
		return true
	default:
		return false
	}
}

// planMayStart reports whether a plan's steps may be started at all.
//
// 'proposed' is the consent boundary and never starts. Terminal plans have
// nothing to run. Note this deliberately does NOT use Plan.Approved(): a
// checklist plan written by todo_write via SyncChecklist inserts status
// 'active' with approved_at NULL, so Approved() would refuse a plan the boss is
// actively working through. The executable set is the same one
// GetActiveBySession serves.
func planMayStart(p *Plan) bool {
	if p == nil || p.Status == PlanProposed {
		return false
	}
	return p.Status == PlanActive || p.Status == PlanPaused
}

// findStep returns the step with id from the refreshed plan, or nil.
func findStep(p *Plan, id string) *Step {
	if p == nil {
		return nil
	}
	for i := range p.Steps {
		if p.Steps[i].ID == id {
			return &p.Steps[i]
		}
	}
	return nil
}
