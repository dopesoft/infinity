package plan

import "testing"

// These tests pin the BOUNDARY of the terminal settle (CloseSession /
// CloseStaleCronPlans). The rule they encode matters because both failure modes
// are silent and expensive:
//
//   - Settle too little and an abandoned cron plan stays 'active' forever — the
//     phantom "Running" card the boss found on 2026-07-27, still spinning on the
//     kanban hours after the weekly AI digest had been written and delivered.
//   - Settle too much and a plan genuinely waiting on the boss gets force-closed
//     into "completed", hiding a decision he needed to make. That is the
//     false-green pattern the codebase forbids outright.
//
// So the boundary is the contract, not an implementation detail.

// TestAbandonedSteps_SettlesPendingNotJustInProgress is the core regression
// guard. FinalizeSession only ever settled 'in_progress' steps, which is why the
// weekly-digest plan survived its own cron run: its last step was still
// 'pending', so nothing settled it, the plan never recomputed, and it stayed
// 'active'. For a session that will never resume, pending is exactly as
// stranded as in_progress.
func TestAbandonedSteps_SettlesPendingNotJustInProgress(t *testing.T) {
	p := &Plan{Steps: []Step{
		{ID: "s0", Idx: 0, Status: StepDone},
		{ID: "s1", Idx: 1, Status: StepSkipped},
		{ID: "s2", Idx: 2, Status: StepDone},
		{ID: "s3", Idx: 3, Status: StepDone},
		{ID: "s4", Idx: 4, Status: StepPending}, // the one that kept the plan alive
	}}
	got := abandonedSteps(p)
	if len(got) != 1 || got[0].ID != "s4" {
		t.Fatalf("want the trailing pending step s4 settled, got %+v", ids(got))
	}
}

// TestAbandonedSteps_StopsAtPendingCheckpoint proves we never steamroll a plan
// that is waiting on the boss. A pending checkpoint means "awaiting you";
// recompute maps that to 'paused' so it surfaces for a decision. Sweeping it
// (and the steps behind it) would report the work complete when it isn't.
func TestAbandonedSteps_StopsAtPendingCheckpoint(t *testing.T) {
	p := &Plan{Steps: []Step{
		{ID: "s0", Status: StepDone},
		{ID: "s1", Status: StepPending},
		{ID: "s2", Status: StepPending, IsCheckpoint: true}, // boss must approve
		{ID: "s3", Status: StepPending},
	}}
	got := abandonedSteps(p)
	if len(got) != 1 || got[0].ID != "s1" {
		t.Fatalf("must settle up to the checkpoint and stop, got %+v", ids(got))
	}
}

// TestAbandonedSteps_StopsAtBlocked: a blocked step is a real failure awaiting
// replan. Skipping it would erase the signal and let the plan read "completed".
func TestAbandonedSteps_StopsAtBlocked(t *testing.T) {
	p := &Plan{Steps: []Step{
		{ID: "s0", Status: StepBlocked},
		{ID: "s1", Status: StepPending},
	}}
	if got := abandonedSteps(p); len(got) != 0 {
		t.Fatalf("a blocked plan must be left for the boss, got %+v", ids(got))
	}
}

// TestAbandonedSteps_InProgressStillSettled: the pre-existing in_progress case
// must keep working — CloseSession widens FinalizeSession's net, it does not
// replace it.
func TestAbandonedSteps_InProgressStillSettled(t *testing.T) {
	p := &Plan{Steps: []Step{
		{ID: "s0", Status: StepDone},
		{ID: "s1", Status: StepInProgress},
		{ID: "s2", Status: StepPending},
	}}
	if got := abandonedSteps(p); len(got) != 2 {
		t.Fatalf("want both the in_progress and trailing pending step, got %+v", ids(got))
	}
}

// TestAbandonedSteps_FullyDonePlanIsNoOp: a plan that completed cleanly must
// report nothing abandoned, or every healthy cron run would be misclassified
// "stopped early" (the count feeds that classification).
func TestAbandonedSteps_FullyDonePlanIsNoOp(t *testing.T) {
	p := &Plan{Steps: []Step{
		{ID: "s0", Status: StepDone},
		{ID: "s1", Status: StepSkipped},
		{ID: "s2", Status: StepFailed},
	}}
	if got := abandonedSteps(p); len(got) != 0 {
		t.Fatalf("a settled plan must have nothing to abandon, got %+v", ids(got))
	}
}

func TestAbandonedSteps_NilPlan(t *testing.T) {
	if got := abandonedSteps(nil); got != nil {
		t.Fatalf("nil plan must be a no-op, got %+v", ids(got))
	}
}

func ids(steps []Step) []string {
	out := make([]string, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.ID)
	}
	return out
}
