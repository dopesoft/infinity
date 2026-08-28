package pc

import (
	"strings"
	"testing"
	"time"
)

// The coach ASKS the onboarding questions and Apply PROMOTES the answers into
// the programme state. They are in different files, keyed by hand, and there is
// no database in these tests to catch the mismatch at runtime. If a prompt key
// is ever renamed on one side only, the boss answers onboarding, the answer
// lands in an untyped blob, the state stays blank, and NextGuidance opens
// onboarding again on the very next read. He would never reach day 1, and
// nothing would error.
func TestEveryOnboardingPromptKeyIsPromoted(t *testing.T) {
	promoted := map[string]bool{}
	for _, keys := range [][]string{
		onboardingAnswerKeys.Identity,
		onboardingAnswerKeys.Objective,
		onboardingAnswerKeys.Pattern,
	} {
		for _, k := range keys {
			promoted[k] = true
		}
	}

	g := NextGuidance(func() Snapshot {
		s := baseSnapshot()
		s.State.CurrentIdentity = ""
		s.State.CurrentObjective = ""
		return s
	}())
	if g.Phase != SessionOnboarding {
		t.Fatalf("expected the onboarding phase, got %q", g.Phase)
	}

	// The primary onboarding answer is keyed "objective" by the cockpit's
	// PRIMARY_KEY map; the secondary prompts carry their own keys.
	if !promoted["objective"] {
		t.Fatal(`the onboarding primary key "objective" is never promoted into the state`)
	}
	for _, p := range g.SecondaryPrompts {
		if !promoted[p.Key] {
			t.Fatalf("onboarding asks for %q but Apply never promotes it into the state", p.Key)
		}
	}
}

// Onboarding and review are the two phases whose completion is decided in one
// file and acted on in another. Both must read the state the same way, or the
// programme either repeats a phase forever or advances through it twice.
func TestPhaseGatesAgreeWithTheCoach(t *testing.T) {
	t.Run("onboarding gate matches the phase the coach offers", func(t *testing.T) {
		cases := []struct {
			name              string
			identity          string
			objective         string
			wantNeedsOnboard  bool
			wantOnboardingCTA bool
		}{
			{name: "both blank", wantNeedsOnboard: true, wantOnboardingCTA: true},
			{name: "identity only", identity: "I say the number.", objective: "", wantNeedsOnboard: true, wantOnboardingCTA: true},
			{name: "objective only", objective: "Two retainers.", wantNeedsOnboard: true, wantOnboardingCTA: true},
			{name: "whitespace is still blank", identity: "   ", objective: "  ", wantNeedsOnboard: true, wantOnboardingCTA: true},
			{name: "both set", identity: "I say the number.", objective: "Two retainers.", wantNeedsOnboard: false, wantOnboardingCTA: false},
		}
		for _, tt := range cases {
			t.Run(tt.name, func(t *testing.T) {
				s := baseSnapshot()
				s.State.CurrentIdentity = tt.identity
				s.State.CurrentObjective = tt.objective
				if got := NeedsOnboarding(s.State); got != tt.wantNeedsOnboard {
					t.Fatalf("NeedsOnboarding = %v, want %v", got, tt.wantNeedsOnboard)
				}
				gotCTA := NextGuidance(s).Phase == SessionOnboarding
				if gotCTA != tt.wantOnboardingCTA {
					t.Fatalf("coach offering onboarding = %v, want %v", gotCTA, tt.wantOnboardingCTA)
				}
			})
		}
	})

	// The double-close guard. Apply only completes a review from a logged
	// session while the cycle is still due, and CompleteReview resets
	// current_day to 1 - so the second write of the same review is inert. If
	// ReviewDue ever stopped tracking current_day, one review would roll the
	// cycle twice and the boss would silently lose 21 days of programme.
	t.Run("review gate closes once the cycle rolls", func(t *testing.T) {
		st := baseSnapshot().State
		st.CurrentDay = st.CycleLengthDays
		if !ReviewDue(st) {
			t.Fatal("the final day of the cycle must be review-due")
		}
		if NextGuidance(Snapshot{State: st}).Phase != SessionReview {
			t.Fatal("the coach must offer the review on the final day")
		}

		// What CompleteReview leaves behind.
		rolled := st
		rolled.CurrentDay = 1
		if ReviewDue(rolled) {
			t.Fatal("a rolled cycle must no longer be review-due, or a review could close it twice")
		}

		mid := st
		mid.CurrentDay = st.CycleLengthDays - 1
		if ReviewDue(mid) {
			t.Fatal("a cycle still in progress must not be review-due")
		}
	})
}

// A coached pursuit is not a habit, and the whole write surface is scoped to
// one. Every action must therefore be reachable by both callers - the HTTP path
// suffix and the tool's action enum - and proof/taken must keep its slash.
func TestActionsCoverEveryWriteTheCoachCanMake(t *testing.T) {
	required := []string{
		ActionIdentity, ActionSession, ActionProof, ActionProofTaken,
		ActionEvidence, ActionMemory, ActionPattern, ActionReview,
	}
	declared := map[string]bool{}
	for _, a := range WriteActions() {
		declared[a] = true
	}
	for _, a := range required {
		if !declared[a] {
			t.Fatalf("action %q is not declared in WriteActions, so the tool enum would omit it", a)
		}
	}
	if len(WriteActions()) != len(required) {
		t.Fatalf("WriteActions has %d entries, expected %d - Apply's switch and the enum have drifted",
			len(WriteActions()), len(required))
	}
}

// Free-text answers are the boss's own writing and go straight into a JSONB
// blob. The onboarding promotion reads them back out, so the reader has to
// tolerate the shapes a JSON decode actually produces rather than assuming
// every value is a string.
func TestFirstAnswerToleratesRealAnswerBlobs(t *testing.T) {
	answers := map[string]any{
		"identity":         "  I say the number out loud.  ",
		"limiting_pattern": "",
		"pattern":          "I discount before anyone asks.",
		"pressure_fear":    42, // a non-string value must not panic or leak
	}
	if got := firstAnswer(answers, onboardingAnswerKeys.Identity); got != "I say the number out loud." {
		t.Fatalf("firstAnswer(identity) = %q", got)
	}
	// Falls through an empty first key to the alias that carries the answer.
	if got := firstAnswer(answers, onboardingAnswerKeys.Pattern); got != "I discount before anyone asks." {
		t.Fatalf("firstAnswer(pattern) = %q, want the aliased key's value", got)
	}
	if got := firstAnswer(answers, onboardingAnswerKeys.Objective); got != "" {
		t.Fatalf("a missing answer must read as empty, got %q", got)
	}
	if got := answerString(answers, "pressure_fear"); got != "" {
		t.Fatalf("a non-string answer must read as empty, got %q", got)
	}
	if got := firstAnswer(nil, onboardingAnswerKeys.Identity); got != "" {
		t.Fatalf("a nil answers blob must read as empty, got %q", got)
	}
}

// The cockpit writes the identity through action='identity' and THEN logs the
// onboarding session with the same answers. The promotion has to be inert on
// that second write, or every cockpit onboarding would file a duplicate copy of
// the limiting pattern and the operating identity into the pattern history.
func TestOnboardingPromotionIsInertOnceOnboarded(t *testing.T) {
	onboarded := State{
		CurrentIdentity:        "I say the number out loud.",
		CurrentObjective:       "Sign two retainer clients.",
		CurrentLimitingPattern: "I discount before anyone asks.",
		CycleLengthDays:        21,
		CurrentDay:             1,
	}
	if NeedsOnboarding(onboarded) {
		t.Fatal("a fully onboarded programme must not be promoted again")
	}
	// Partially onboarded still promotes, so a half-written state can recover.
	partial := onboarded
	partial.CurrentObjective = ""
	if !NeedsOnboarding(partial) {
		t.Fatal("a half-written onboarding must still be completable")
	}
}

// Recovery is the only phase that opens by telling the boss he missed
// something. It may fire on evidence he did, never on the absence of evidence:
// a fresh programme and a history we could not read both mean "we do not know".
func TestRecoveryNeverFiresWithoutReadableHistory(t *testing.T) {
	state := baseSnapshot().State

	// No sessions at all - a brand new programme.
	fresh := NewSnapshot(PursuitHeader{}, state, nowForTest(), nil, nil, nil)
	if fresh.LastSessionDaysAgo != 0 {
		t.Fatalf("a programme with no history must report 0 days ago, got %d", fresh.LastSessionDaysAgo)
	}
	if NextGuidance(fresh).Phase == SessionRecovery {
		t.Fatal("a brand new programme must never open in recovery")
	}

	// Sessions exist but carry no usable timestamp.
	unreadable := NewSnapshot(PursuitHeader{}, state, nowForTest(), nil, nil,
		[]Session{{ID: "s1", Kind: SessionMorning}, {ID: "s2", Kind: SessionEvening}})
	if unreadable.LastSessionDaysAgo != 0 {
		t.Fatalf("unreadable history must report 0 days ago, got %d", unreadable.LastSessionDaysAgo)
	}
	if got := NextGuidance(unreadable).Phase; got == SessionRecovery {
		t.Fatalf("unreadable history must not accuse him of missing a day, got phase %q", got)
	}

	// And the recovery copy itself stays non-shaming when it does fire.
	missed := baseSnapshot()
	missed.LastSessionDaysAgo = 3
	g := NextGuidance(missed)
	if g.Phase != SessionRecovery {
		t.Fatalf("three days of silence should reach recovery, got %q", g.Phase)
	}
	if strings.Contains(strings.ToLower(g.Body), "restart") &&
		!strings.Contains(strings.ToLower(g.Body), "no restart") {
		t.Fatal("recovery must never ask him to restart the cycle")
	}
}

// nowForTest is a fixed clock so day-boundary assertions never depend on when
// the suite happens to run.
func nowForTest() time.Time {
	return time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
}
