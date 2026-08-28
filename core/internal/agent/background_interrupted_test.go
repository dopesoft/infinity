package agent

import (
	"errors"
	"testing"
)

// classifyBackgroundRun is the seam that decides whether a detached build's run
// row closes as a FAILURE or as "no verdict yet". Getting it wrong is what put a
// red ❌ step and a "Build failed" push in front of the boss for code that had
// already landed on disk, so both directions are pinned here.
//
// The still-running direction is proved at the seam that owns the sentinel type
// (internal/tools/still_running_test.go — the concrete error is unexported there,
// so it cannot be constructed from this package). What this file guarantees is
// the other half, and the half that must never regress: anything that is NOT the
// sentinel is still reported as a genuine error.

func TestClassifyBackgroundRun_SuccessIsUntouched(t *testing.T) {
	summary, still, errText := classifyBackgroundRun("Built it. Tests pass.", nil)
	if summary != "Built it. Tests pass." {
		t.Fatalf("the worker's own summary must survive: %q", summary)
	}
	if still {
		t.Fatal("a clean finish is not 'still working'")
	}
	if errText != "" {
		t.Fatal("a clean finish carries no error")
	}
}

// The line we must not cross. A real failure has to keep reaching the run row as
// an error, so it goes red, surfaces, and feeds the self-improve backlog.
func TestClassifyBackgroundRun_GenuineErrorStaysAnError(t *testing.T) {
	for _, raw := range []string{
		"go build failed: undefined: foo",
		"launch via mac failed (status=404)",
		// Deliberately worded like the still-running sentinel. If detection were
		// substring-based, this would silently stop being a failure.
		"the job is still working and was not stopped",
	} {
		summary, still, errText := classifyBackgroundRun("partial output", errors.New(raw))
		if still {
			t.Fatalf("%q must NOT read as still-working - only the sentinel TYPE may", raw)
		}
		if errText != raw {
			t.Fatalf("the real error must reach the run row verbatim: got %q, want %q", errText, raw)
		}
		if summary != "partial output" {
			t.Fatalf("whatever the worker produced must survive alongside the error: %q", summary)
		}
	}
}

// BackgroundResult's contract: consumers branch on StillRunning, and Err stays
// empty in that case so every "did it error?" check answers no.
func TestBackgroundResult_StillRunningIsItsOwnField(t *testing.T) {
	r := BackgroundResult{StillRunning: true}
	if r.Err != "" {
		t.Fatal("a still-running result must default to no error text")
	}
	r2 := BackgroundResult{Err: "boom"}
	if r2.StillRunning {
		t.Fatal("an errored result must not default to still-running")
	}
}
