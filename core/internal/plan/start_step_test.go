package plan

import "testing"

// These tests pin the "exactly one step in flight" contract. Both failure modes
// are silent:
//
//   - Start nothing and the 2026-07-02 report failure recurs: a plan sits
//     'active' with every step pending, no run booked, and the board shows work
//     that was never begun.
//   - Start too much (a second concurrent step, a proposal, a step behind a
//     checkpoint) and the plan stops describing what is actually happening, or
//     executes work the boss was asked to approve first.

// TestStartableStep_PendingAutoStarts is the core case: an approved plan whose
// first step is pending must nominate that step for starting.
func TestStartableStep_PendingAutoStarts(t *testing.T) {
	p := &Plan{Status: PlanActive, Steps: []Step{
		{ID: "s0", Idx: 0, Status: StepDone},
		{ID: "s1", Idx: 1, Status: StepPending},
		{ID: "s2", Idx: 2, Status: StepPending},
	}}
	got, needsStart := startableStep(p)
	if got == nil || got.ID != "s1" {
		t.Fatalf("want first pending step s1, got %v", stepID(got))
	}
	if !needsStart {
		t.Fatal("a pending step must be reported as needing a start")
	}
}

// TestStartableStep_ExistingInProgressUnchanged: the step already running is
// returned as-is and needsStart is false, so calling this every turn is
// idempotent and never re-stamps started_at or books a second run.
func TestStartableStep_ExistingInProgressUnchanged(t *testing.T) {
	p := &Plan{Status: PlanActive, Steps: []Step{
		{ID: "s0", Idx: 0, Status: StepDone},
		{ID: "s1", Idx: 1, Status: StepInProgress},
		{ID: "s2", Idx: 2, Status: StepPending},
	}}
	got, needsStart := startableStep(p)
	if got == nil || got.ID != "s1" {
		t.Fatalf("want the in-flight step s1, got %v", stepID(got))
	}
	if needsStart {
		t.Fatal("an already-running step must not be started again")
	}
}

// TestStartableStep_ExactlyOneInProgress is the invariant itself: with a step
// in flight, a later pending step must NOT be nominated. Without this the loop
// would start a second step on every turn and the plan would claim several
// things were happening at once.
func TestStartableStep_ExactlyOneInProgress(t *testing.T) {
	p := &Plan{Status: PlanActive, Steps: []Step{
		{ID: "s0", Idx: 0, Status: StepInProgress},
		{ID: "s1", Idx: 1, Status: StepPending},
		{ID: "s2", Idx: 2, Status: StepPending},
	}}
	got, needsStart := startableStep(p)
	if needsStart {
		t.Fatalf("must not start a second step while s0 is in flight (picked %v)", stepID(got))
	}
	if got == nil || got.ID != "s0" {
		t.Fatalf("want the single in-flight step s0, got %v", stepID(got))
	}
}

// TestStartableStep_InProgressWinsFromAnyPosition: the in-flight scan covers the
// whole plan, not just the head. A pending step EARLIER than a running one (a
// step reopened after a replan) must not preempt it.
func TestStartableStep_InProgressWinsFromAnyPosition(t *testing.T) {
	p := &Plan{Status: PlanActive, Steps: []Step{
		{ID: "s0", Idx: 0, Status: StepPending},
		{ID: "s1", Idx: 1, Status: StepInProgress},
	}}
	got, needsStart := startableStep(p)
	if needsStart || got == nil || got.ID != "s1" {
		t.Fatalf("in-flight step must win regardless of position, got %v needsStart=%v", stepID(got), needsStart)
	}
}

// TestStartableStep_NoActivePlan: nothing to start when there is no plan. The
// method-level equivalent (a session with no plan) returns a nil result.
func TestStartableStep_NoActivePlan(t *testing.T) {
	got, needsStart := startableStep(nil)
	if got != nil || needsStart {
		t.Fatalf("nil plan must start nothing, got %v needsStart=%v", stepID(got), needsStart)
	}
}

// TestStartableStep_UnapprovedProposalUntouched is the consent boundary. A
// proposal is laid out for the boss and has not been approved; auto-starting its
// first step would execute work he was explicitly asked to green-light.
func TestStartableStep_UnapprovedProposalUntouched(t *testing.T) {
	p := &Plan{Status: PlanProposed, Steps: []Step{
		{ID: "s0", Idx: 0, Status: StepPending},
	}}
	got, needsStart := startableStep(p)
	if got != nil || needsStart {
		t.Fatalf("a proposal must never start, got %v needsStart=%v", stepID(got), needsStart)
	}
}

// TestStartableStep_TerminalPlanStartsNothing: a completed / failed / cancelled
// plan has no work left, and reviving a step would resurrect a closed card.
func TestStartableStep_TerminalPlanStartsNothing(t *testing.T) {
	for _, status := range []string{PlanCompleted, PlanFailed, PlanCancelled} {
		p := &Plan{Status: status, Steps: []Step{{ID: "s0", Status: StepPending}}}
		if got, needsStart := startableStep(p); got != nil || needsStart {
			t.Fatalf("%s plan must start nothing, got %v needsStart=%v", status, stepID(got), needsStart)
		}
	}
}

// TestStartableStep_StopsAtPendingCheckpoint: a pending checkpoint means
// "awaiting you". We neither start it nor reach past it to a later step.
func TestStartableStep_StopsAtPendingCheckpoint(t *testing.T) {
	p := &Plan{Status: PlanPaused, Steps: []Step{
		{ID: "s0", Status: StepDone},
		{ID: "s1", Status: StepPending, IsCheckpoint: true},
		{ID: "s2", Status: StepPending},
	}}
	got, needsStart := startableStep(p)
	if got != nil || needsStart {
		t.Fatalf("must stop at the checkpoint awaiting the boss, got %v needsStart=%v", stepID(got), needsStart)
	}
}

// TestStartableStep_ResumesFailedAndBlocked: a paused plan whose step failed or
// was blocked by verification is resumable — that is exactly the stall the
// continuation backstop exists to break, so those statuses are actionable.
func TestStartableStep_ResumesFailedAndBlocked(t *testing.T) {
	for _, status := range []string{StepFailed, StepBlocked} {
		p := &Plan{Status: PlanPaused, Steps: []Step{
			{ID: "s0", Status: StepDone},
			{ID: "s1", Status: status},
			{ID: "s2", Status: StepPending},
		}}
		got, needsStart := startableStep(p)
		if got == nil || got.ID != "s1" || !needsStart {
			t.Fatalf("%s step must be resumable, got %v needsStart=%v", status, stepID(got), needsStart)
		}
	}
}

// TestStartableStep_AllSettled: every step finished means nothing to start,
// even though the plan row may not have recomputed to 'completed' yet. Only
// 'done' and 'skipped' count as finished here — a failed step is resumable, per
// the test above, which is why this fixture holds neither.
func TestStartableStep_AllSettled(t *testing.T) {
	p := &Plan{Status: PlanActive, Steps: []Step{
		{ID: "s0", Status: StepDone},
		{ID: "s1", Status: StepSkipped},
		{ID: "s2", Status: StepDone},
	}}
	if got, needsStart := startableStep(p); got != nil || needsStart {
		t.Fatalf("a settled plan must start nothing, got %v needsStart=%v", stepID(got), needsStart)
	}
}

// TestStartableStep_ChecklistPlanWithoutApprovedAt: todo_write's SyncChecklist
// inserts an 'active' plan with approved_at NULL, so Plan.Approved() is false
// for it. Gating on Approved() would silently refuse to start every checklist
// the boss is working through — this pins that we gate on status instead.
func TestStartableStep_ChecklistPlanWithoutApprovedAt(t *testing.T) {
	p := &Plan{Status: PlanActive, Steps: []Step{{ID: "s0", Status: StepPending}}}
	if p.Approved() {
		t.Fatal("fixture should model the approved_at-NULL checklist plan")
	}
	got, needsStart := startableStep(p)
	if got == nil || got.ID != "s0" || !needsStart {
		t.Fatalf("a checklist plan must still start, got %v needsStart=%v", stepID(got), needsStart)
	}
}

// TestEnsureStepStarted_NoStore: the nil-pool path (chat-only deployments, and
// the "session has no plan" case) must be a silent no-op, never an error the
// agent loop has to special-case.
func TestEnsureStepStarted_NoStore(t *testing.T) {
	got, err := NewStore(nil).EnsureStepStarted(t.Context(), "550e8400-e29b-41d4-a716-446655440000")
	if err != nil {
		t.Fatalf("nil store must not error, got %v", err)
	}
	if got != nil {
		t.Fatalf("nil store must start nothing, got %+v", got)
	}
}

// TestEnsureStepStarted_NoSession: no session id means no foreground plan to
// drive.
func TestEnsureStepStarted_NoSession(t *testing.T) {
	got, err := NewStore(nil).EnsureStepStarted(t.Context(), "")
	if err != nil || got != nil {
		t.Fatalf("empty session must be a no-op, got %+v err=%v", got, err)
	}
}

// TestStoreSatisfiesStepStarter keeps the seam the agent loop will consume
// honest: if the signature drifts, this stops compiling.
func TestStoreSatisfiesStepStarter(t *testing.T) {
	var _ StepStarter = NewStore(nil)
}

func stepID(s *Step) string {
	if s == nil {
		return "<nil>"
	}
	return s.ID
}
