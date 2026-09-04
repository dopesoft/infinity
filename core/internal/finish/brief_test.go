package finish

import (
	"strings"
	"testing"
	"time"
)

// These tests encode WHY the brief is shaped the way it is, not what it spells.
// Every assertion below maps to a way the boss has actually been let down: work
// silently redone from scratch, a failed probe read as a clean repo, a
// continuation loop with no end, a machine-worded status where a sentence
// belonged.

func strandedFixture() stranded {
	start := time.Date(2026, 8, 28, 14, 0, 0, 0, time.UTC)
	return stranded{
		runID:     "11111111-1111-4111-8111-111111111111",
		label:     "Claude Code: wire the continuation poller",
		sessionID: "22222222-2222-4222-8222-222222222222",
		repo:      "/Users/kai/Dev/infinity",
		claudeSes: "f4fcf5d1-dffc-407e-bf64-9330f8b4b329",
		reason:    "still_working",
		summary:   "Edited three files, was mid-way through the tests.",
		lastFile:  "core/internal/finish/finish.go",
		pass:      1,
		startedAt: start,
		endedAt:   start.Add(14*time.Minute + 22*time.Second),
	}
}

// The single most expensive failure this whole feature exists to prevent:
// picking a job back up by starting COLD, so Claude re-derives work already
// sitting on disk and the 14-minute build becomes two 14-minute builds. The
// session id and the "only what is LEFT" instruction must both be present.
func TestBrief_CarriesTheResumeHandleSoWorkIsNotRedone(t *testing.T) {
	s := strandedFixture()
	brief := buildBrief(s, Report{Repo: s.repo, Branch: "main", Head: "58a0054 add pursuit"}, 3)

	if !strings.Contains(brief, s.claudeSes) {
		t.Fatalf("the Claude session id is the whole point of resuming:\n%s", brief)
	}
	if !strings.Contains(brief, "resume_session") {
		t.Fatalf("the brief must name the parameter that continues it:\n%s", brief)
	}
	if !strings.Contains(brief, "ONLY what is left to do") {
		t.Fatalf("resuming with the ORIGINAL brief makes it redo finished work:\n%s", brief)
	}
	if !strings.Contains(brief, s.repo) {
		t.Fatalf("the repo must be named or a continuation lands nowhere:\n%s", brief)
	}
}

// A run with no session id can't be resumed, and quietly emitting a
// resume_session line with an empty value would send Jarvis to call the tool
// with garbage. Say what's true instead.
func TestBrief_SaysSoWhenThereIsNothingToResumeFrom(t *testing.T) {
	s := strandedFixture()
	s.claudeSes = ""
	brief := buildBrief(s, Report{Repo: s.repo}, 3)

	if strings.Contains(brief, "resume_session") {
		t.Fatalf("must not offer a resume it cannot perform:\n%s", brief)
	}
	if !strings.Contains(brief, "can't be resumed") {
		t.Fatalf("the missing capability has to be stated, not just omitted:\n%s", brief)
	}
}

// CLAUDE.md: "empty-because-broken must never read as empty-because-fine". A
// failed probe and a clean tree lead to OPPOSITE decisions - one means look
// again, the other may mean the work is already committed.
func TestBrief_NeverLetsAFailedProbeReadAsACleanRepo(t *testing.T) {
	broken := buildBrief(strandedFixture(), Report{Repo: "/Users/kai/Dev/infinity", Err: "the bridge answered 502"}, 3)
	if !strings.Contains(broken, "could not check") || !strings.Contains(broken, "UNKNOWN") {
		t.Fatalf("a failed probe must say so in as many words:\n%s", broken)
	}
	if strings.Contains(broken, "CLEAN") {
		t.Fatalf("a failed probe must never claim the tree is clean:\n%s", broken)
	}

	clean := buildBrief(strandedFixture(), Report{Repo: "/Users/kai/Dev/infinity", Branch: "main", Head: "abc1234 x"}, 3)
	if !strings.Contains(clean, "CLEAN") {
		t.Fatalf("a genuinely clean tree is a fact worth stating:\n%s", clean)
	}
	if strings.Contains(clean, "could not check") {
		t.Fatalf("a successful probe must not hedge:\n%s", clean)
	}
}

// Dirty files ARE the evidence: they are what distinguishes "continue this"
// from "this never started". They must survive into the brief, with the count.
func TestBrief_ReportsWhatIsActuallyOnDisk(t *testing.T) {
	brief := buildBrief(strandedFixture(), Report{
		Repo:     "/Users/kai/Dev/infinity",
		Branch:   "main",
		Head:     "58a0054 add pursuit",
		Dirty:    []string{"core/internal/finish/finish.go", "core/cmd/infinity/serve.go"},
		DiffStat: " 2 files changed, 310 insertions(+)",
	}, 3)
	for _, want := range []string{"2 file(s) with uncommitted changes", "core/internal/finish/finish.go", "310 insertions"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("missing %q from the evidence block:\n%s", want, brief)
		}
	}
}

// The cap must be visible in the brief itself. A continuation loop the model
// can't see the end of is how "automatic recovery" becomes "it kept relaunching
// the same broken build all night".
func TestBrief_StatesTheAttemptBudget(t *testing.T) {
	s := strandedFixture()
	s.pass = 3
	brief := buildBrief(s, Report{Repo: s.repo}, 3)
	if !strings.Contains(brief, "attempt 3 of 3") {
		t.Fatalf("the pass budget has to be on the page:\n%s", brief)
	}
	if !strings.Contains(brief, "I stop offering") {
		t.Fatalf("the model must know the offer ends:\n%s", brief)
	}
}

// The brief leaves exactly ONE thing open - the judgment call - because every
// mechanic (noticing, evidence, the id, the cap) already ran in Go. Rule #1b.
func TestBrief_AsksForADecisionNotAProcedure(t *testing.T) {
	brief := buildBrief(strandedFixture(), Report{Repo: "/x"}, 3)
	for _, want := range []string{"continue it", "replan it", "needs him"} {
		if !strings.Contains(brief, want) {
			t.Fatalf("the three legitimate answers must all be offered, missing %q:\n%s", want, brief)
		}
	}
	// It must also allow the answer "it's actually done" - otherwise a
	// finished job gets a pointless extra build run at it.
	if !strings.Contains(brief, "the work is actually complete") {
		t.Fatalf("closing the step must be a permitted answer:\n%s", brief)
	}
}

// "still working" is not a failure and must never be worded as one - that
// exact laundering is what put a red ❌ and a "Build failed" push in front of
// the boss for code that was landing fine.
func TestReasonLine_NeverCallsAnInterruptionAFailure(t *testing.T) {
	still := reasonLine("still_working")
	if !strings.Contains(still, "never stopped and never failed") {
		t.Fatalf("still_working must be stated as not-a-failure: %q", still)
	}
	for _, r := range []string{"still_working", "interrupted", "", "something_new"} {
		if line := reasonLine(r); strings.Contains(strings.ToLower(line), "failed") &&
			!strings.Contains(line, "never failed") {
			t.Fatalf("reason %q reads as a failure: %q", r, line)
		}
	}
	// An unknown reason must still be legible rather than dropped.
	if got := reasonLine("box_restarted"); !strings.Contains(got, "box_restarted") {
		t.Fatalf("an unrecognised reason must still be surfaced: %q", got)
	}
}

func TestHumanDuration(t *testing.T) {
	cases := map[time.Duration]string{
		0:                               "an unknown time",
		-time.Second:                    "an unknown time",
		45 * time.Second:                "45s",
		14*time.Minute + 22*time.Second: "14m22s",
		2*time.Hour + 3*time.Minute:     "123m0s",
	}
	for d, want := range cases {
		if got := humanDuration(d); got != want {
			t.Fatalf("humanDuration(%s) = %q, want %q", d, got, want)
		}
	}
}

// ─── the job that FINISHED and was never reported ────────────────────────────

func completedFixture() candidate {
	return candidate{
		runID:     "33333333-3333-4333-8333-333333333333",
		label:     "Claude Code: redesign the coach conversation",
		sessionID: "22222222-2222-4222-8222-222222222222",
		repo:      "/Users/n0m4d/Dev/infinity",
		claudeSes: "f4fcf5d1-dffc-407e-bf64-9330f8b4b329",
		status:    "ok",
		reason:    "still_working",
		startedAt: time.Date(2026, 8, 29, 0, 15, 0, 0, time.UTC),
	}
}

// Why: this is the 2026-08-29 failure. A 47-minute build succeeded, wrote a
// full report, and the boss was told it had FAILED. The card that tells him has
// to lead with the correction, carry the report, and never read as a job to
// re-run. And it is a CARD: on 2026-09-02 this notice was a model turn into a
// 900K-token chat once a minute, and half of a week's Claude plan went on it.
func TestBuildCompletedNotice_LeadsWithTheCorrectionAndCarriesTheReport(t *testing.T) {
	title, body := buildCompletedNotice(completedFixture(), Verdict{
		Found:  true,
		Done:   true,
		Report: "Done. Rewrote the coach panel and the four call sites; go build and pnpm test both clean.",
		Files:  []string{"studio/components/CoachConversation.tsx", "core/internal/coach/panel.go"},
	}, "")

	if title != "Build finished: redesign the coach conversation" {
		t.Fatalf("the title is the plain fact, without the engine prefix the run label carries: %q", title)
	}
	if !strings.Contains(body, "it had not") {
		t.Fatalf("the notice must open by correcting what he was last told:\n%s", body)
	}
	if !strings.Contains(body, "Rewrote the coach panel") {
		t.Fatalf("Claude's own report is the whole point of reading the transcript:\n%s", body)
	}
	if !strings.Contains(body, "CoachConversation.tsx") {
		t.Fatalf("the files it touched are the evidence the boss can check:\n%s", body)
	}
	for _, dev := range []string{"resume_session", "plan_update", "code_agent", "[Automatic check"} {
		if strings.Contains(body, dev) || strings.Contains(title, dev) {
			t.Fatalf("this is addressed to the boss, not to the model; %q has no place on his card:\n%s", dev, body)
		}
	}
}

// Why: a run that ran to the end and REPORTED a failure is not a success with
// an awkward summary. The correction must not launder it.
func TestBuildCompletedNotice_KeepsAFailureAFailure(t *testing.T) {
	title, body := buildCompletedNotice(completedFixture(), Verdict{
		Found: true, Done: true, IsError: true,
		Report: "The production build failed: type error in CoachConversation.tsx:212.",
	}, "")
	if !strings.Contains(title, "with errors") {
		t.Fatalf("a reported failure must be named as one in the title: %q", title)
	}
	if !strings.Contains(body, "type error in CoachConversation.tsx:212") {
		t.Fatalf("its own account of what went wrong is what makes it actionable:\n%s", body)
	}
	if strings.Contains(body, "it had not") {
		t.Fatalf("a failure must not carry the 'it did not fail' correction:\n%s", body)
	}
}

// Why: "it finished" with no report must never read as "it finished cleanly".
// An empty report is a reason to go and look, not a reason to reassure him.
func TestBuildCompletedNotice_RefusesToInventAReport(t *testing.T) {
	_, body := buildCompletedNotice(completedFixture(), Verdict{Found: true, Done: true}, "")
	if !strings.Contains(body, "wrote no closing summary") {
		t.Fatalf("a missing report must be stated plainly:\n%s", body)
	}
	if !strings.Contains(body, "Check the repo") {
		t.Fatalf("with no report, the instruction is to go and check:\n%s", body)
	}
}

// Why: the plan step that was sitting `blocked` for finished work is the
// visible half of the 2026-08-29 bug. The card names it only when there is one
// to name, so a chat with no plan is not sent looking for a step to close.
func TestBuildCompletedNotice_NamesAWaitingPlanOnlyWhenThereIsOne(t *testing.T) {
	_, withPlan := buildCompletedNotice(completedFixture(), Verdict{Found: true, Done: true, Report: "Done."},
		"44444444-4444-4444-8444-444444444444")
	if !strings.Contains(withPlan, "plan step") {
		t.Fatalf("a chat with an active plan must be told a step may be waiting:\n%s", withPlan)
	}
	_, without := buildCompletedNotice(completedFixture(), Verdict{Found: true, Done: true, Report: "Done."}, "")
	if strings.Contains(without, "plan step") {
		t.Fatalf("no plan, no mention of one:\n%s", without)
	}
}
