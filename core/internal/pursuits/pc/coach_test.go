package pc

import (
	"strings"
	"testing"
	"time"
)

func baseSnapshot() Snapshot {
	return Snapshot{
		Pursuit: PursuitHeader{Experience: ExperiencePsychoCybernetics},
		State: State{
			CycleNumber:            1,
			CycleLengthDays:        21,
			CurrentDay:             3,
			CurrentIdentity:        "I act before fear negotiates me out of the opportunity.",
			CurrentObjective:       "Create one new revenue source.",
			CurrentLimitingPattern: "I wait for certainty.",
			Timezone:               "America/Chicago",
		},
		LastSessionDaysAgo: 0,
	}
}

func TestExperienceValidation(t *testing.T) {
	if got := NormalizeExperience("  PSYCHO_CYBERNETICS  "); got != ExperiencePsychoCybernetics {
		t.Fatalf("NormalizeExperience = %q", got)
	}
	if got := NormalizeExperience(" "); got != ExperienceOrdinary {
		t.Fatalf("blank experience = %q", got)
	}
	if !IsValidExperience(ExperienceOrdinary) || !IsValidExperience(ExperiencePsychoCybernetics) {
		t.Fatal("known experiences must validate")
	}
	if IsValidExperience("psycho-cybernetics") {
		t.Fatal("unknown experience must not validate")
	}
}

func TestKindValidation(t *testing.T) {
	for _, kind := range AllSessionKinds() {
		if !IsValidSessionKind(kind) {
			t.Fatalf("session kind %q must validate", kind)
		}
	}
	if IsValidSessionKind("therapy") {
		t.Fatal("unknown session kind must not validate")
	}
	if !IsValidEvidenceKind(EvidenceEvidence) || !IsValidEvidenceKind(EvidenceResistance) {
		t.Fatal("known evidence kinds must validate")
	}
	if IsValidEvidenceKind("judgement") {
		t.Fatal("unknown evidence kind must not validate")
	}
	for _, kind := range []string{PatternLimiting, PatternOperating, PatternCorrection} {
		if !IsValidPatternKind(kind) {
			t.Fatalf("pattern kind %q must validate", kind)
		}
	}
}

func TestNextGuidanceOnboardingPrecedesDailyFlow(t *testing.T) {
	s := baseSnapshot()
	s.State.CurrentIdentity = ""
	s.State.CurrentDay = 21
	s.LastSessionDaysAgo = 4
	if got := NextGuidance(s).Phase; got != SessionOnboarding {
		t.Fatalf("phase = %q, want onboarding", got)
	}
}

func TestNextGuidanceReviewPrecedesRecovery(t *testing.T) {
	s := baseSnapshot()
	s.State.CurrentDay = 21
	s.LastSessionDaysAgo = 4
	if got := NextGuidance(s).Phase; got != SessionReview {
		t.Fatalf("phase = %q, want review", got)
	}
}

func TestNextGuidanceRecoveryIsNonShaming(t *testing.T) {
	s := baseSnapshot()
	s.LastSessionDaysAgo = 2
	g := NextGuidance(s)
	if g.Phase != SessionRecovery {
		t.Fatalf("phase = %q, want recovery", g.Phase)
	}
	body := strings.ToLower(g.Body)
	for _, forbidden := range []string{"streak broken", "failed", "start over", "penalty"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("recovery copy contains shaming phrase %q", forbidden)
		}
	}
	if !strings.Contains(body, "no restart") {
		t.Fatal("recovery must explicitly preserve the cycle")
	}
}

func TestNextGuidanceDailyPhases(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Snapshot)
		want string
	}{
		{name: "morning", edit: func(s *Snapshot) {}, want: SessionMorning},
		{name: "midday pending proof", edit: func(s *Snapshot) {
			s.TookMorningToday = true
			s.ProofPendingToday = true
		}, want: SessionMidday},
		{name: "evening without pending proof", edit: func(s *Snapshot) {
			s.TookMorningToday = true
		}, want: SessionEvening},
		{name: "evening after midday", edit: func(s *Snapshot) {
			s.TookMorningToday = true
			s.TookMiddayToday = true
			s.ProofPendingToday = true
		}, want: SessionEvening},
		{name: "idle", edit: func(s *Snapshot) {
			s.TookMorningToday = true
			s.TookMiddayToday = true
			s.TookEveningToday = true
		}, want: "idle"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := baseSnapshot()
			tt.edit(&s)
			if got := NextGuidance(s).Phase; got != tt.want {
				t.Fatalf("phase = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAdaptForResistance(t *testing.T) {
	s := baseSnapshot()
	if got := AdaptForResistance(s, 2, 1); got.Phase != "" {
		t.Fatalf("balanced day should not adapt, got %q", got.Phase)
	}
	if got := AdaptForResistance(s, 1, 2); got.Phase != SessionAdjustment {
		t.Fatalf("resistance-heavy day phase = %q", got.Phase)
	}
}

func TestNewSnapshotUsesChicagoLocalDay(t *testing.T) {
	now := time.Date(2026, time.August, 27, 4, 30, 0, 0, time.UTC)
	morning := time.Date(2026, time.August, 26, 13, 0, 0, 0, time.UTC)
	state := baseSnapshot().State
	state.LastMorningAt = &morning
	proof := Proof{PlannedAt: time.Date(2026, time.August, 26, 18, 0, 0, 0, time.UTC)}
	evidence := Evidence{CapturedAt: time.Date(2026, time.August, 27, 3, 45, 0, 0, time.UTC)}
	s := NewSnapshot(baseSnapshot().Pursuit, state, now, []Proof{proof}, []Evidence{evidence}, nil)
	if !s.TookMorningToday {
		t.Fatal("Chicago date should still be Aug 26 at 11:30pm CDT")
	}
	if !s.ProofPendingToday || s.EvidenceCountToday != 1 {
		t.Fatalf("local-day derivation failed: proof=%v evidence=%d", s.ProofPendingToday, s.EvidenceCountToday)
	}
}

func TestLocalDayDeltaAcrossDST(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		from  time.Time
		today time.Time
		want  int
	}{
		{
			name:  "spring forward",
			from:  time.Date(2026, time.March, 7, 12, 0, 0, 0, loc),
			today: time.Date(2026, time.March, 9, 0, 0, 0, 0, loc),
			want:  2,
		},
		{
			name:  "fall back",
			from:  time.Date(2026, time.October, 31, 12, 0, 0, 0, loc),
			today: time.Date(2026, time.November, 2, 0, 0, 0, 0, loc),
			want:  2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := localDayDelta(tt.from, tt.today, loc); got != tt.want {
				t.Fatalf("localDayDelta = %d, want %d", got, tt.want)
			}
		})
	}
}

// A timestamp AHEAD of today has not elapsed. Postgres NOW() and Go's
// time.Now() can disagree by enough to stamp a session marginally in the
// future, and measuring the magnitude of the gap instead of the elapsed days
// made that read as "two days missed" - opening the recovery prompt on a
// morning the boss had just logged.
func TestLocalDayDeltaClampsFutureTimestamps(t *testing.T) {
	loc, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Fatal(err)
	}
	today := time.Date(2026, time.August, 27, 0, 0, 0, 0, loc)
	future := time.Date(2026, time.August, 30, 9, 0, 0, 0, loc)
	if got := localDayDelta(future, today, loc); got != 0 {
		t.Fatalf("a future timestamp must report 0 elapsed days, got %d", got)
	}

	// The same clamp is what keeps a clock-skewed session out of recovery.
	s := baseSnapshot()
	s = NewSnapshot(s.Pursuit, s.State, today,
		nil, nil, []Session{{OccurredAt: future}})
	if s.LastSessionDaysAgo != 0 {
		t.Fatalf("LastSessionDaysAgo = %d, want 0", s.LastSessionDaysAgo)
	}
	if got := NextGuidance(s).Phase; got == SessionRecovery {
		t.Fatal("a session stamped in the future must not trigger recovery")
	}
}
