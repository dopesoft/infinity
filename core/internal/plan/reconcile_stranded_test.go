package plan

import (
	"strings"
	"testing"
)

// This file pins the single decision that produced the bug: ReconcileStranded
// used to believe "a terminal-error run is unambiguous proof the execution
// finished and failed". It was false, because 'error' was ALSO what every sweep
// wrote — the boot recovery, a cancel, a reaper — since runs.Handle.Finish had
// only ok|error to choose from. So a coding job that was merely INTERRUPTED
// (Railway redeployed under it; its worker outlived the turn) came through here
// indistinguishable from a build that actually broke, and the boss got a red ❌
// step and a "Build failed" push for work that had landed on disk.
//
// meta.stopped_reason is the discriminator. These tests hold both directions:
// an interruption must NOT fail its step, and a genuine failure still MUST.

// The false RED we are fixing.
func TestStrandedStepOutcome_InterruptedRunDoesNotFailItsStep(t *testing.T) {
	cases := []struct {
		name          string
		runStatus     string
		stoppedReason string
	}{
		{"boot sweep closed a coding job 'ok' + interrupted", "ok", "interrupted"},
		{"the worker outlived the wait window", "ok", "still_working"},
		{"a sweep stamped 'error' but recorded WHY it closed", "error", "interrupted"},
	}
	for _, c := range cases {
		if runIsGenuineFailure(c.runStatus, c.stoppedReason) {
			t.Fatalf("%s: an interrupted run must never read as proof the step failed", c.name)
		}
		status, summary := strandedStepOutcome(c.runStatus, c.stoppedReason, "")
		if status == StepFailed {
			t.Fatalf("%s: step marked FAILED - this is the red ❌ for work that actually landed", c.name)
		}
		if status != StepBlocked {
			t.Fatalf("%s: an interrupted step must be 'blocked' (plan pauses, still resumable, reads incomplete), got %q", c.name, status)
		}
		if summary == "" {
			t.Fatalf("%s: the boss must be told why there is no result", c.name)
		}
		if strings.Contains(strings.ToLower(summary), "failed") {
			t.Fatalf("%s: the summary must not call an interruption a failure: %q", c.name, summary)
		}
	}
}

// The line we must not cross. Making failures read honestly must NEVER make a
// real failure look fine: a run that genuinely errored still fails its step, so
// the plan still pauses, still surfaces, still feeds the backlog.
func TestStrandedStepOutcome_GenuineErrorStillFailsItsStep(t *testing.T) {
	if !runIsGenuineFailure("error", "") {
		t.Fatal("a run that errored with no recorded stop reason IS a failure and must stay one")
	}
	if !runIsGenuineFailure("error", "   ") {
		t.Fatal("a blank stopped_reason is no reason at all - it must not excuse a failure")
	}
	status, summary := strandedStepOutcome("error", "", "go build failed: undefined: foo")
	if status != StepFailed {
		t.Fatalf("a genuine failure must still mark the step failed, got %q", status)
	}
	if summary != "go build failed: undefined: foo" {
		t.Fatalf("the run's REAL error must reach the boss, not a placeholder: %q", summary)
	}
	// And with nothing recorded, it still fails - silence is not an excuse.
	if s, _ := strandedStepOutcome("error", "", ""); s != StepFailed {
		t.Fatalf("an errored run with no detail must still fail its step, got %q", s)
	}
}

// The SQL has to select BOTH lanes, or the Go decision above is dead code that
// never sees the rows it exists to judge (CLAUDE.md Rule #1c).
func TestReconcileStrandedSQL_SelectsBothLanes(t *testing.T) {
	if !strings.Contains(reconcileStrandedSQL, "stopped_reason") {
		t.Fatal("the query must read stopped_reason, or the Go predicate can never tell the lanes apart")
	}
	if !strings.Contains(reconcileStrandedSQL, "r.status IN ('error', 'ok')") {
		t.Fatal("interrupted runs close 'ok', so 'error'-only selection would silently skip them and leave the step spinning forever")
	}
	if !strings.Contains(reconcileStrandedSQL, "r.status = 'error' OR COALESCE(r.meta->>'stopped_reason', '') <> ''") {
		t.Fatal("a cleanly-finished 'ok' run with no stop reason must NOT be swept here - inventing a verdict for it is the guessing we are removing")
	}
	if !strings.Contains(reconcileStrandedSQL, "s.status = 'in_progress'") {
		t.Fatal("only in_progress steps are stranded")
	}
}

// 'blocked' is deliberate, not incidental: it keeps the plan non-terminal, so
// recompute pauses it (surfaces under "Awaiting you") and it can never roll up
// to a false "completed".
func TestBlockedStepKeepsThePlanHonest(t *testing.T) {
	if isTerminalStep(StepBlocked) {
		t.Fatal("a blocked step must stay non-terminal so the plan cannot recompute to 'completed' - that would be a false green")
	}
	if !isTerminalStep(StepFailed) {
		t.Fatal("failed is terminal; this test guards the contrast the choice of 'blocked' rests on")
	}
}
