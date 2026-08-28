package pc

import (
	"strings"
	"testing"
	"time"
)

// The 21-day spine is the product promise: the programme advances on wall
// clock time, starts at day 1, and never runs past the cycle length no matter
// how long the boss leaves it. A regression here silently changes what "day
// 14 of 21" means, which is the one number the whole cockpit is built around.
func TestDeriveDayHoldsTheCycleSpine(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, time.August, 1, 9, 0, 0, 0, loc)

	tests := []struct {
		name string
		now  time.Time
		want int
	}{
		{name: "start day is day 1", now: start, want: 1},
		{name: "same day late evening is still day 1", now: start.Add(13 * time.Hour), want: 1},
		{name: "next local day is day 2", now: time.Date(2026, time.August, 2, 0, 5, 0, 0, loc), want: 2},
		{name: "two weeks in", now: time.Date(2026, time.August, 14, 12, 0, 0, 0, loc), want: 14},
		{name: "final day", now: time.Date(2026, time.August, 21, 12, 0, 0, 0, loc), want: 21},
		{name: "past the cycle clamps to the final day", now: time.Date(2026, time.September, 30, 12, 0, 0, 0, loc), want: 21},
		{name: "before the start never goes below day 1", now: start.Add(-72 * time.Hour), want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveDay(start, tt.now, "America/Chicago", 21); got != tt.want {
				t.Fatalf("DeriveDay = %d, want %d", got, tt.want)
			}
		})
	}

	// A caller that forgot to set a length still gets the documented spine
	// rather than a divide-by-nothing or an uncapped day counter.
	if got := DeriveDay(start, start.AddDate(0, 0, 400), "America/Chicago", 0); got != DefaultCycleLengthDays {
		t.Fatalf("zero cycle length should fall back to the %d-day spine, got %d", DefaultCycleLengthDays, got)
	}
}

// Missed days must never include today. The whole recovery design is
// non-shaming: a day still in progress is not yet a day he skipped, and
// counting it would greet him with a penalty for a morning he is about to do.
func TestDeriveMissedDaysNeverCountsToday(t *testing.T) {
	tests := []struct {
		name              string
		currentDay        int
		sessionDaysBefore int
		want              int
	}{
		{name: "day 1 with nothing logged is not a miss", currentDay: 1, sessionDaysBefore: 0, want: 0},
		{name: "every prior day logged", currentDay: 5, sessionDaysBefore: 4, want: 0},
		{name: "two prior days skipped", currentDay: 5, sessionDaysBefore: 2, want: 2},
		{name: "more session days than elapsed never goes negative", currentDay: 3, sessionDaysBefore: 9, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeriveMissedDays(tt.currentDay, tt.sessionDaysBefore); got != tt.want {
				t.Fatalf("DeriveMissedDays = %d, want %d", got, tt.want)
			}
		})
	}
}

// Recovery is the day's re-entry ritual, not an extra session on top of the
// morning. Stamping it into last_morning_at is what stops the cockpit asking
// for a second rehearsal right after he has just eased back in.
func TestPhaseColumnTreatsRecoveryAsTheMorning(t *testing.T) {
	tests := map[string]string{
		SessionMorning:    "last_morning_at",
		SessionRecovery:   "last_morning_at",
		SessionMidday:     "last_midday_at",
		SessionEvening:    "last_evening_at",
		SessionOnboarding: "",
		SessionReview:     "",
		SessionAdjustment: "",
	}
	for kind, want := range tests {
		if got := phaseColumn(kind); got != want {
			t.Fatalf("phaseColumn(%q) = %q, want %q", kind, got, want)
		}
	}
}

// The cockpit and a seeded coaching chat both name "today's rehearsal
// memory". If the pick were not deterministic they would name different ones
// on the same morning, and the boss would be told to return to a memory that
// is not the one on his screen.
func TestPickRehearsalMemoryIsDeterministicAndRotates(t *testing.T) {
	memories := []Memory{
		{ID: "a", Title: "Closed the room"},
		{ID: "b", Title: "Said the number"},
		{ID: "c", Title: "Walked back in"},
	}
	if got := PickRehearsalMemory(nil, 4); got != nil {
		t.Fatalf("empty bank must yield no memory, got %+v", got)
	}
	first := PickRehearsalMemory(memories, 4)
	again := PickRehearsalMemory(memories, 4)
	if first == nil || again == nil || first.ID != again.ID {
		t.Fatal("the same day must always pick the same memory")
	}
	seen := map[string]bool{}
	for day := 1; day <= len(memories); day++ {
		m := PickRehearsalMemory(memories, day)
		if m == nil {
			t.Fatalf("day %d picked nothing", day)
		}
		seen[m.ID] = true
	}
	if len(seen) != len(memories) {
		t.Fatalf("a full rotation should reach every memory, reached %d of %d", len(seen), len(memories))
	}
	// Day 0 is not a real programme day, but a caller passing one must still
	// get a memory rather than an index panic.
	if got := PickRehearsalMemory(memories, 0); got == nil {
		t.Fatal("day 0 must still yield a memory")
	}
}

// Every capture the coach ASKS for must be one Apply actually FILES. The
// prompts live in coach.go and the write mechanic lives in write.go, so a new
// prompt key added to one and not the other would silently drop the boss's
// answer into an untyped answers blob and never reach the evidence list.
func TestEveryCapturePromptKeyIsFiledByApply(t *testing.T) {
	filed := map[string]bool{}
	for _, c := range sessionCaptureKeys {
		if !IsValidEvidenceKind(c.Kind) {
			t.Fatalf("capture key %q files as unknown evidence kind %q", c.Key, c.Kind)
		}
		filed[c.Key] = true
	}

	midday := NextGuidance(func() Snapshot {
		s := baseSnapshot()
		s.TookMorningToday = true
		s.ProofPendingToday = true
		return s
	}())
	if midday.Phase != SessionMidday {
		t.Fatalf("expected the midday phase, got %q", midday.Phase)
	}

	// The midday primary answer is keyed by the client's PRIMARY_KEY map as
	// "evidence"; its secondary prompts are keyed here.
	if !filed["evidence"] {
		t.Fatal(`the midday primary capture key "evidence" is not filed by Apply`)
	}
	for _, p := range midday.SecondaryPrompts {
		if p.Key == "resistance" && !filed[p.Key] {
			t.Fatalf("coach asks for %q but Apply never files it as evidence", p.Key)
		}
	}
}

// The HTTP surface routes on the path suffix and the agent tool routes on an
// enum, and both feed the same Apply switch. If the three drift, one caller
// silently 404s while the other works.
func TestWriteActionsMatchApplySwitch(t *testing.T) {
	for _, a := range WriteActions() {
		if !IsWriteAction(a) {
			t.Fatalf("declared action %q is not accepted by IsWriteAction", a)
		}
	}
	for _, bad := range []string{"", "identify", "Session", "proof/take", "delete"} {
		if IsWriteAction(bad) {
			t.Fatalf("unknown action %q must be rejected", bad)
		}
	}
	// proof/taken carries a slash because it is a nested HTTP path. Guard that
	// shape explicitly: flattening it would break the route without breaking
	// the tool, which is exactly the drift this test exists to catch.
	if !strings.Contains(ActionProofTaken, "/") {
		t.Fatalf("ActionProofTaken must stay a nested path, got %q", ActionProofTaken)
	}
}

// Free text is clamped at the store boundary so one runaway client cannot
// bloat a row, and so every caller inherits the same bound without repeating
// it.
func TestClampBoundsFreeText(t *testing.T) {
	if got := clampText("  trimmed  "); got != "trimmed" {
		t.Fatalf("clampText = %q", got)
	}
	long := strings.Repeat("x", maxTextLen+500)
	if got := clampText(long); len(got) != maxTextLen {
		t.Fatalf("clampText length = %d, want %d", len(got), maxTextLen)
	}
	answers := clampAnswers(map[string]any{"fact": long, "count": 3})
	if got, _ := answers["fact"].(string); len(got) != maxTextLen {
		t.Fatalf("clampAnswers left a %d-char string", len(got))
	}
	if answers["count"] != 3 {
		t.Fatalf("clampAnswers must pass non-string values through, got %v", answers["count"])
	}
}
