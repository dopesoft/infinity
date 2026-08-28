package pc

import (
	"strings"
	"testing"
	"time"
)

func fullCockpit() Cockpit {
	now := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	state := State{
		PursuitID:              "p1",
		CycleNumber:            2,
		CycleLengthDays:        21,
		CurrentDay:             9,
		MissedDaysCount:        2,
		CurrentIdentity:        "I say the number out loud and let the silence sit.",
		CurrentObjective:       "Sign two retainer clients this cycle.",
		CurrentLimitingPattern: "I discount before anyone asks me to.",
		PressureTest: PressureTest{
			Fear:      "The Thursday pricing call.",
			Doubt:     "That the number is actually justified.",
			Alternate: "Someone who negotiates rather than quotes.",
		},
		Timezone: "America/Chicago",
	}
	c := Cockpit{
		Pursuit: PursuitHeader{ID: "p1", Title: "Psycho-Cybernetics", Experience: ExperiencePsychoCybernetics},
		State:   state,
		TodayProofs: []Proof{
			{ID: "pr1", Label: "Quote the full number on the Thursday call", DayInCycle: 9},
		},
		TodayEvidence: []Evidence{
			{ID: "e1", Kind: EvidenceEvidence, Body: "Held the price on the intro call.", DayInCycle: 9},
		},
		RecentEvidence: []Evidence{
			{ID: "e1", Kind: EvidenceEvidence, Body: "Held the price on the intro call.", DayInCycle: 9},
			{ID: "e0", Kind: EvidenceResistance, Body: "Offered a discount nobody asked for.", DayInCycle: 7},
		},
		Memories: []Memory{
			{ID: "m1", Title: "Closed the room in Dallas", Body: "Nobody spoke for four seconds."},
		},
		Patterns: []Pattern{
			{ID: "pt1", Kind: PatternLimiting, Body: "Discounting to fill the silence."},
		},
		Corrections: []Pattern{
			{ID: "pt2", Kind: PatternCorrection, Body: "Count to five before filling the pause."},
		},
		RecentSessions: []Session{
			{
				ID: "s1", Kind: SessionMorning, DayInCycle: 9, CycleNumber: 2,
				OccurredAt: now,
				Answers: map[string]any{
					"rehearsal":    "The Thursday pricing call.",
					"proof_pledge": "Quote the full number on the Thursday call",
				},
			},
		},
		CycleReviews: []Review{
			{ID: "r1", CycleNumber: 1, Wins: "Stopped apologising for the rate.", Misses: "Skipped four evenings."},
		},
	}
	c.RehearsalMemory = PickRehearsalMemory(c.Memories, state.CurrentDay)
	c.Guidance = NextGuidance(NewSnapshot(c.Pursuit, state, now, c.TodayProofs, c.RecentEvidence, c.RecentSessions))
	return c
}

// "Discuss with Jarvis" has to land the boss in a conversation that already
// knows his programme. If any of these fall out of the context block, the
// coach opens by interrogating him for things he already wrote down, which is
// the exact failure this seeding exists to prevent.
func TestFormatChatContextCarriesTheWholeProgramme(t *testing.T) {
	out := FormatChatContext(fullCockpit())

	required := map[string]string{
		"identity":          "I say the number out loud and let the silence sit.",
		"objective":         "Sign two retainer clients this cycle.",
		"limiting pattern":  "I discount before anyone asks me to.",
		"programme day":     "day 9 of 21",
		"cycle number":      "cycle 2",
		"missed days":       "Days missed this cycle: 2",
		"pressure test":     "The Thursday pricing call.",
		"today's proof":     "Quote the full number on the Thursday call",
		"today's evidence":  "Held the price on the intro call.",
		"earlier captures":  "Offered a discount nobody asked for.",
		"success memory":    "Closed the room in Dallas",
		"rehearsal memory":  "Today's rehearsal memory",
		"pattern history":   "Discounting to fill the silence.",
		"corrections":       "Count to five before filling the pause.",
		"session history":   "morning on day 9 of cycle 2",
		"previous review":   "Stopped apologising for the rate.",
		"coaching phase":    "Phase:",
		"write-back tool":   "pursuit_pc_write",
		"never invent rule": "Never invent evidence",
	}
	for label, want := range required {
		if !strings.Contains(out, want) {
			t.Fatalf("chat context is missing the %s (%q)", label, want)
		}
	}
}

// Identity is the boss's to write. A fresh programme must say plainly that
// onboarding has not run, never invent a placeholder identity, and never
// carry a name: hardcoding one is what would turn a general experience into
// a single-user script.
func TestFormatChatContextNeverInventsAnIdentity(t *testing.T) {
	now := time.Date(2026, time.August, 27, 15, 0, 0, 0, time.UTC)
	state := State{CycleNumber: 1, CycleLengthDays: 21, CurrentDay: 1, Timezone: "America/Chicago"}
	c := Cockpit{
		Pursuit:  PursuitHeader{ID: "p1", Title: "Psycho-Cybernetics", Experience: ExperiencePsychoCybernetics},
		State:    state,
		Guidance: NextGuidance(NewSnapshot(PursuitHeader{}, state, now, nil, nil, nil)),
	}
	out := FormatChatContext(c)

	if !strings.Contains(out, "onboarding has not run") {
		t.Fatal("a blank programme must say onboarding has not run")
	}
	if !strings.Contains(out, "none pledged yet today") ||
		!strings.Contains(out, "nothing captured yet today") {
		t.Fatal("empty sections must read as empty, not be omitted")
	}
	if c.Guidance.Phase != SessionOnboarding {
		t.Fatalf("a blank programme must route to onboarding, got %q", c.Guidance.Phase)
	}
	if strings.Contains(strings.ToLower(out), "kai") {
		t.Fatal("the context block must never hardcode a person's name")
	}
}

// The programme is reflective self-experimentation, not care. The context
// block is what sets the model's frame for the whole conversation, so the
// disclaimer and the "his words, not yours" rule have to survive edits.
func TestFormatChatContextHoldsTheNonClinicalFrame(t *testing.T) {
	out := strings.ToLower(FormatChatContext(fullCockpit()))
	for _, want := range []string{
		"nothing here is clinical",
		"do not diagnose",
		"reflective self experimentation",
		"do not restate",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("chat context lost the framing phrase %q", want)
		}
	}
}

// The adaptive note is the one piece of coaching that reacts to how the day
// actually went. It rides in the same context block so a chat and the cockpit
// give the same advice about shrinking tomorrow's proof.
func TestFormatChatContextCarriesTheAdaptiveNote(t *testing.T) {
	c := fullCockpit()
	adj := AdaptForResistance(NewSnapshot(c.Pursuit, c.State, time.Now(), nil, nil, nil), 1, 3)
	if adj.Phase != SessionAdjustment {
		t.Fatalf("a resistance-heavy day should adapt, got %q", adj.Phase)
	}
	c.Adjustment = &adj
	if !strings.Contains(FormatChatContext(c), "Adaptive note:") {
		t.Fatal("an adaptive note must reach the seeded conversation")
	}
}
