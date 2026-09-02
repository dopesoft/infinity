package finish

import (
	"strings"
	"testing"
)

// The nudge loop must never turn into a wall of identical messages.
//
// What happened (2026-09-02): the boss deployed, walked away for twenty
// minutes, and came back to roughly thirty messages from Jarvis in his chat,
// one a minute, each saying a version of "not resuming, it is already
// committed". He had asked no questions. Twenty-four stranded coding jobs had
// piled up in that one conversation - a long build is a chain of small passes,
// and every pass is stranded the instant the next one starts - and each was
// entitled to its own pass budget. Every individual decision was correct and
// the result was unusable. "I asked for a robust job and now I have this
// bullshit mess."
//
// Two rules stop it, and both are pinned here because both are load-bearing.

// TestUnchanged_ARepoThatDidNotMoveHasNothingToContinue.
//
// This is the verdict that settles a job, and it is taken from the REPO rather
// than from Jarvis's wording. A sentence match ("not resuming") would be a
// mechanic living in prose: the model is free to phrase it differently
// tomorrow and the loop would start again (CLAUDE.md Rule #1b).
func TestUnchanged_ARepoThatDidNotMoveHasNothingToContinue(t *testing.T) {
	before := Report{Repo: "infinity", Head: "6044876", Dirty: []string{"a.go", "b.go"}}

	same := Report{Repo: "infinity", Head: "6044876", Dirty: []string{"a.go", "b.go"}}
	if !unchanged(before, same) {
		t.Fatal("identical repo state must read as 'nothing moved', or the job is asked about again")
	}

	// Any real movement keeps the job alive.
	for name, after := range map[string]Report{
		"a new commit":   {Head: "aaaaaaa", Dirty: []string{"a.go", "b.go"}},
		"a new edit":     {Head: "6044876", Dirty: []string{"a.go", "b.go", "c.go"}},
		"an edit landed": {Head: "6044876", Dirty: []string{"a.go"}},
		"different file": {Head: "6044876", Dirty: []string{"a.go", "z.go"}},
	} {
		if unchanged(before, after) {
			t.Errorf("%s is real progress and must not settle the job", name)
		}
	}
}

// TestClaim_OnlyTheNewestStrandedJobInAChatIsStillAQuestion.
//
// Asserted against the SQL because that is where the rule lives: the claim
// picks one row, and without this clause it works through every stranded job
// in the conversation one minute at a time.
func TestClaim_OnlyTheNewestStrandedJobInAChatIsStillAQuestion(t *testing.T) {
	sql := claimSQL()
	if !strings.Contains(sql, "newer.ended_at > r.ended_at") {
		t.Fatal("the claim will queue through every superseded job in the chat: " +
			"twenty-four stranded passes became thirty messages the boss never asked for")
	}
	// The guard that was already there must not be lost in the process.
	if !strings.Contains(sql, "live.status = 'running'") {
		t.Fatal("the don't-continue-while-work-is-live guard went missing")
	}
}
