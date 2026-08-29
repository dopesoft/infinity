package finish

import (
	"strings"
	"testing"
)

// The evidence gatherer is the first classifier in the system that reads the
// WORLD rather than a receipt. Everything else asks the worker how it went,
// which is exactly what is missing when a worker was killed before it could
// answer. These tests pin the parsing that turns four git queries into that
// fact, and the honesty rule that a failed look never reads as an empty repo.

const sampleProbe = `===BRANCH===
main
===HEAD===
58a0054 Add adaptive Psycho-Cybernetics pursuit experience
===DIRTY===
 M core/cmd/infinity/serve.go
?? core/internal/finish/finish.go
R  core/old/name.go -> core/new/name.go
A  "core/internal/with space.go"
===STAT===
 2 files changed, 310 insertions(+), 4 deletions(-)
`

func TestParseEvidence_ReadsTheRepoStateOffOneRoundTrip(t *testing.T) {
	var rep Report
	parseEvidence(sampleProbe, &rep)

	if rep.Branch != "main" {
		t.Fatalf("branch = %q", rep.Branch)
	}
	if !strings.HasPrefix(rep.Head, "58a0054 ") {
		t.Fatalf("head = %q", rep.Head)
	}
	if !strings.Contains(rep.DiffStat, "310 insertions") {
		t.Fatalf("diffstat = %q", rep.DiffStat)
	}
	want := []string{
		"core/cmd/infinity/serve.go",
		"core/internal/finish/finish.go",
		// A rename reports the NEW name: that is the file that exists on disk
		// now, and the one a continuation has to reason about.
		"core/new/name.go",
		// Git quotes paths with spaces; the quotes are not part of the path.
		"core/internal/with space.go",
	}
	if len(rep.Dirty) != len(want) {
		t.Fatalf("dirty = %#v, want %d entries", rep.Dirty, len(want))
	}
	for i, w := range want {
		if rep.Dirty[i] != w {
			t.Fatalf("dirty[%d] = %q, want %q", i, rep.Dirty[i], w)
		}
	}
}

// A clean tree still reports a branch and a HEAD. That is what distinguishes
// it from a probe that never ran - the distinction the whole Err field exists
// to preserve.
func TestParseEvidence_CleanTreeIsNotAnEmptyProbe(t *testing.T) {
	var rep Report
	parseEvidence("===BRANCH===\nmain\n===HEAD===\nabc1234 something\n===DIRTY===\n===STAT===\n", &rep)
	if rep.Branch == "" || rep.Head == "" {
		t.Fatalf("a clean repo still has a branch and a head: %#v", rep)
	}
	if len(rep.Dirty) != 0 {
		t.Fatalf("nothing is uncommitted here: %#v", rep.Dirty)
	}
	if !rep.Gathered() {
		t.Fatal("a clean tree was successfully gathered")
	}
}

func TestReport_GatheredDistinguishesLookedFromCouldNotLook(t *testing.T) {
	if (Report{Err: "the bridge answered 502"}).Gathered() {
		t.Fatal("a failed probe must never report as gathered")
	}
	if !(Report{Branch: "main"}).Gathered() {
		t.Fatal("a successful probe reports as gathered")
	}
}

// Truncated or garbage output must not produce a confident empty report - the
// Gather caller turns "everything blank" into an explicit Err for this reason.
func TestParseEvidence_GarbageProducesNothingRatherThanNonsense(t *testing.T) {
	var rep Report
	parseEvidence("bash: git: command not found\n", &rep)
	if rep.Branch != "" || rep.Head != "" || len(rep.Dirty) != 0 || rep.DiffStat != "" {
		t.Fatalf("unmarked output must not be read as sections: %#v", rep)
	}
}

// A synthetic session id (a delegate, a background job) is not a chat and is a
// 22P02 on every mem_* write besides. The poller must skip those rather than
// try to deliver into them.
func TestIsChatSession(t *testing.T) {
	if !isChatSession("22222222-2222-4222-8222-222222222222") {
		t.Fatal("a uuid is a chat session")
	}
	for _, bad := range []string{"", "background:abc", "delegate-3", "22222222222242228222222222222222"} {
		if isChatSession(bad) {
			t.Fatalf("%q is not a chat session", bad)
		}
	}
}

// Gather must refuse rather than probe when it has nothing to probe, and must
// say WHY - a blank Err would make the brief claim it looked.
func TestGather_RefusesHonestlyWithNothingToLookAt(t *testing.T) {
	var nilGatherer *BridgeEvidence
	if rep := nilGatherer.Gather(t.Context(), "s", "/repo"); rep.Gathered() || rep.Err == "" {
		t.Fatalf("no router must be reported, not swallowed: %#v", rep)
	}
	// A router with no repo recorded on the run: same rule.
	e := &BridgeEvidence{router: nil}
	if rep := e.Gather(t.Context(), "s", ""); rep.Gathered() || rep.Err == "" {
		t.Fatalf("a missing repo must be reported: %#v", rep)
	}
}
