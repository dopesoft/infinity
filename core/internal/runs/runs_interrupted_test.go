package runs

import (
	"strings"
	"testing"
)

// Why: a coding job that was merely INTERRUPTED was being reported as FAILED,
// because Finish was binary (err==nil → ok, else → error) and every sweep had
// to pick one of those two. These tests pin the third state's contract, because
// the whole no-false-red fix hangs off it: the row closes 'ok' (no new status
// enum, so no reader anywhere has to learn a fourth value) and carries
// meta.stopped_reason as the machine-readable "this never reached a verdict".

func TestCodingKinds_CoversBothDetachedCodingJobs(t *testing.T) {
	got := CodingKinds()
	want := map[string]bool{"background.build": false, "code_agent": false}
	for _, k := range got {
		if _, ok := want[k]; !ok {
			t.Fatalf("unexpected coding kind %q - this list drives a REAPER EXEMPTION, keep it exact", k)
		}
		want[k] = true
	}
	for k, seen := range want {
		if !seen {
			t.Fatalf("%q must be a coding kind: it is a detached job that legitimately outlives the 45-min reaper", k)
		}
	}
	if !IsCodingKind("code_agent") {
		t.Fatal("code_agent must be exempt the way background.build already is - a detached `claude -p` outlives 45 minutes")
	}
	if IsCodingKind("cron") || IsCodingKind("plan.step") || IsCodingKind("") {
		t.Fatal("only detached coding jobs may be exempt; everything else must keep getting reaped")
	}
}

// The blanket reaper is a FAILURE path (status='error', "(stalled)"). Coding
// kinds must be out of its WHERE clause entirely, and its close must stay a
// plain un-excused error so a genuinely stalled run still fails its plan step.
func TestReapTimedOutSQL_ExemptsCodingKindsAndStaysAFailure(t *testing.T) {
	if !strings.Contains(reapTimedOutSQL, "NOT (kind = ANY($3::text[]))") {
		t.Fatal("the reaper must exclude the coding-kind LIST, not a single kind")
	}
	if !strings.Contains(reapTimedOutSQL, "status = 'error'") {
		t.Fatal("a reaped run is a genuine failure and must stay red")
	}
	if strings.Contains(reapTimedOutSQL, "stopped_reason") {
		t.Fatal("a reaped run must NOT be excused with a stopped_reason: blowing a time budget with nothing recorded IS a failure and must still fail its step")
	}
}

// Boot sweep: coding jobs close honest-not-failed; everything else keeps its
// existing red close. BOTH record why they closed, so the plan layer can tell a
// restart apart from a build that actually broke.
func TestRecoverStrandedSQL_TwoLanesBothRecordTheReason(t *testing.T) {
	if !strings.Contains(recoverStrandedCodingSQL, "status = 'ok'") {
		t.Fatal("a coding job that outlived a restart is very likely still running on the Mac - closing it 'error' is the false red we are fixing")
	}
	if !strings.Contains(recoverStrandedCodingSQL, "kind = ANY($1::text[])") {
		t.Fatal("the coding lane must be scoped to the coding kinds")
	}
	if !strings.Contains(recoverStrandedSQL, "status = 'error'") {
		t.Fatal("every non-coding kind must keep the exact red close it had before - this change must not stop anything from going red")
	}
	if !strings.Contains(recoverStrandedSQL, "NOT (kind = ANY($1::text[]))") {
		t.Fatal("the two lanes must partition on the same list, or a row gets closed twice or not at all")
	}
	for name, sql := range map[string]string{
		"coding": recoverStrandedCodingSQL,
		"rest":   recoverStrandedSQL,
	} {
		if !strings.Contains(sql, recoverStrandedReasonPatch) {
			t.Fatalf("%s lane must stamp stopped_reason: it is the ONLY thing that tells plan.ReconcileStranded a restart apart from a real failure", name)
		}
		if !strings.Contains(sql, "progress_label") {
			t.Fatalf("%s lane must keep the last checkpoint in the summary - it is all the boss has to see where it got to", name)
		}
	}
}

// The floor exists so a no-verdict run never renders as a wordless green card
// the boss reads as success.
func TestInterruptedSummary_AlwaysSaysThereIsNoVerdict(t *testing.T) {
	own := "Edited 4 files, then the box went away."
	if got := InterruptedSummary(StoppedInterrupted, own); got != own {
		t.Fatalf("a caller's own narrative must win: %q", got)
	}
	interrupted := InterruptedSummary(StoppedInterrupted, "   ")
	if interrupted == "" {
		t.Fatal("an interrupted run must never close with a blank summary")
	}
	// It must not CLAIM a failure (that is the false red), and it must not let a
	// green row read as a clean success either: it has to say there is no result.
	for _, banned := range []string{"failed.", "it failed", "build failed", "error"} {
		if strings.Contains(strings.ToLower(interrupted), banned) {
			t.Fatalf("an interruption must not read as a failure: %q", interrupted)
		}
	}
	if !strings.Contains(strings.ToLower(interrupted), "verdict") &&
		!strings.Contains(strings.ToLower(interrupted), "no result") {
		t.Fatalf("an interrupted run closes 'ok', so its summary MUST say there is no verdict or it reads as success: %q", interrupted)
	}
	still := InterruptedSummary(StoppedStillWorking, "")
	if !strings.Contains(strings.ToLower(still), "still working") {
		t.Fatalf("the still-working floor must say so plainly: %q", still)
	}
	if still == interrupted {
		t.Fatal("'still going' and 'I lost track of it' are different facts and must read differently")
	}
}

// nil-safe in every no-pool shape, like every other method here: unit tests and
// migrate-only runs must not panic.
func TestFinishInterrupted_NilSafe(t *testing.T) {
	var h *Handle
	h.FinishInterrupted(t.Context(), StoppedInterrupted, "x")

	tr := New(nil)
	h2 := tr.Begin(t.Context(), KindBackgroundBuild, "t", "l", SourceAgent)
	h2.FinishInterrupted(t.Context(), StoppedStillWorking, "still going")
	// Second call must no-op rather than double-write.
	h2.FinishInterrupted(t.Context(), StoppedStillWorking, "still going")
}
