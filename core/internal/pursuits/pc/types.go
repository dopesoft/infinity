// Package pc implements the Psycho-Cybernetics experience on top of the
// generic pursuits substrate.
//
// Design intent (see Rule #1 in CLAUDE.md): the ordinary pursuits surface
// stays intact and this package plugs in ONLY when mem_pursuits.experience
// equals ExperiencePsychoCybernetics. Adding a future experience is a new
// constant on mem_pursuits.experience plus a sibling coach adapter, never
// a fork of the pursuits table.
//
// Author framing: Maxwell Maltz's book is the source material for every
// coach prompt this package emits. Coaching copy is worded as Maltz's
// framing, not as clinical claims. Nothing in this package treats,
// diagnoses, or replaces mental-health care.
package pc

import (
	"strings"
	"time"
)

// ── Experience constants (on mem_pursuits.experience) ─────────────────────

const (
	// ExperienceOrdinary is the default: habits, weekly cadences, and
	// long-term goals rendered by the existing PursuitsCard. Rows created
	// before this migration land here.
	ExperienceOrdinary = "ordinary"

	// ExperiencePsychoCybernetics wires the pursuit into the cockpit and
	// coach flows in this package.
	ExperiencePsychoCybernetics = "psycho_cybernetics"
)

// ValidExperiences enumerates every accepted mem_pursuits.experience value.
// Kept here so the API and pursuit_create tool share one source of truth.
func ValidExperiences() []string {
	return []string{ExperienceOrdinary, ExperiencePsychoCybernetics}
}

// IsValidExperience reports whether the supplied string is a known
// experience discriminator. Case-sensitive on purpose so a typo is a
// caller error, not silently coerced.
func IsValidExperience(s string) bool {
	for _, v := range ValidExperiences() {
		if v == s {
			return true
		}
	}
	return false
}

// NormalizeExperience trims + lowercases the input and returns the default
// experience when the caller left it blank.
func NormalizeExperience(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ExperienceOrdinary
	}
	return s
}

// ── Session kinds ─────────────────────────────────────────────────────────

const (
	SessionOnboarding = "onboarding"
	SessionMorning    = "morning"
	SessionMidday     = "midday"
	SessionEvening    = "evening"
	SessionRecovery   = "recovery"
	SessionReview     = "review"
	SessionAdjustment = "adjustment"
)

// AllSessionKinds enumerates every session kind persisted in
// mem_pursuit_pc_sessions.kind.
func AllSessionKinds() []string {
	return []string{
		SessionOnboarding,
		SessionMorning,
		SessionMidday,
		SessionEvening,
		SessionRecovery,
		SessionReview,
		SessionAdjustment,
	}
}

// IsValidSessionKind guards writes so a typo becomes a caller error.
func IsValidSessionKind(kind string) bool {
	for _, v := range AllSessionKinds() {
		if v == kind {
			return true
		}
	}
	return false
}

// ── Evidence / pattern kinds ──────────────────────────────────────────────

const (
	EvidenceEvidence   = "evidence"
	EvidenceResistance = "resistance"
)

// IsValidEvidenceKind guards mem_pursuit_pc_evidence.kind writes.
func IsValidEvidenceKind(kind string) bool {
	return kind == EvidenceEvidence || kind == EvidenceResistance
}

const (
	PatternLimiting   = "limiting"
	PatternOperating  = "operating"
	PatternCorrection = "correction"
)

// IsValidPatternKind guards mem_pursuit_pc_patterns.kind writes.
func IsValidPatternKind(kind string) bool {
	switch kind {
	case PatternLimiting, PatternOperating, PatternCorrection:
		return true
	}
	return false
}

// ── DTOs (mirror studio/lib/pursuits/pc/types.ts) ─────────────────────────

// State is the one-row-per-pursuit cockpit state. Read on every dashboard
// paint; written by onboarding, session logging, and reviews.
type State struct {
	PursuitID              string       `json:"pursuit_id"`
	CycleNumber            int          `json:"cycle_number"`
	CycleLengthDays        int          `json:"cycle_length_days"`
	CurrentDay             int          `json:"current_day"`
	CycleStartedAt         time.Time    `json:"cycle_started_at"`
	MissedDaysCount        int          `json:"missed_days_count"`
	CurrentIdentity        string       `json:"current_identity"`
	CurrentObjective       string       `json:"current_objective"`
	CurrentLimitingPattern string       `json:"current_limiting_pattern"`
	PressureTest           PressureTest `json:"pressure_test"`
	Timezone               string       `json:"timezone"`
	LastMorningAt          *time.Time   `json:"last_morning_at,omitempty"`
	LastMiddayAt           *time.Time   `json:"last_midday_at,omitempty"`
	LastEveningAt          *time.Time   `json:"last_evening_at,omitempty"`
	CreatedAt              time.Time    `json:"created_at"`
	UpdatedAt              time.Time    `json:"updated_at"`
}

// PressureTest is the three-part identity pressure test captured at
// onboarding. The coach reflects each part back at morning rehearsal so
// the boss sees where the identity might crack under real load.
type PressureTest struct {
	Fear      string `json:"fear"`
	Doubt     string `json:"doubt"`
	Alternate string `json:"alternate"`
}

// Session is one coaching session row (onboarding, morning, midday,
// evening, recovery, review, adjustment).
type Session struct {
	ID          string         `json:"id"`
	PursuitID   string         `json:"pursuit_id"`
	Kind        string         `json:"kind"`
	CycleNumber int            `json:"cycle_number"`
	DayInCycle  int            `json:"day_in_cycle"`
	Answers     map[string]any `json:"answers"`
	CoachNote   string         `json:"coach_note"`
	OccurredAt  time.Time      `json:"occurred_at"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Proof is the deliberate proof action pledged in the morning session.
type Proof struct {
	ID          string     `json:"id"`
	PursuitID   string     `json:"pursuit_id"`
	SessionID   *string    `json:"session_id,omitempty"`
	Label       string     `json:"label"`
	CycleNumber int        `json:"cycle_number"`
	DayInCycle  int        `json:"day_in_cycle"`
	PlannedAt   time.Time  `json:"planned_at"`
	Taken       bool       `json:"taken"`
	TakenAt     *time.Time `json:"taken_at,omitempty"`
	Note        string     `json:"note"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
}

// Evidence is a daytime capture: kind='evidence' (identity worked) or
// kind='resistance' (old pattern showed up).
type Evidence struct {
	ID          string    `json:"id"`
	PursuitID   string    `json:"pursuit_id"`
	SessionID   *string   `json:"session_id,omitempty"`
	Kind        string    `json:"kind"`
	Body        string    `json:"body"`
	Tags        []string  `json:"tags"`
	CycleNumber int       `json:"cycle_number"`
	DayInCycle  int       `json:"day_in_cycle"`
	CapturedAt  time.Time `json:"captured_at"`
}

// Memory is one victory-memory bank row. The coach pulls a memory into
// morning rehearsal to seed Maltz's "winning feeling" (author framing).
type Memory struct {
	ID        string    `json:"id"`
	PursuitID string    `json:"pursuit_id"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	Tags      []string  `json:"tags"`
	Weight    int       `json:"weight"`
	SavedAt   time.Time `json:"saved_at"`
}

// Pattern is one limiting-pattern / operating-identity / correction row.
type Pattern struct {
	ID          string         `json:"id"`
	PursuitID   string         `json:"pursuit_id"`
	Kind        string         `json:"kind"`
	Body        string         `json:"body"`
	Refs        map[string]any `json:"refs"`
	CycleNumber int            `json:"cycle_number"`
	DayInCycle  int            `json:"day_in_cycle"`
	CreatedAt   time.Time      `json:"created_at"`
}

// Review is one end-of-cycle deliberate identity review.
type Review struct {
	ID            string         `json:"id"`
	PursuitID     string         `json:"pursuit_id"`
	CycleNumber   int            `json:"cycle_number"`
	Wins          string         `json:"wins"`
	Misses        string         `json:"misses"`
	NextIdentity  string         `json:"next_identity"`
	NextObjective string         `json:"next_objective"`
	NextPattern   string         `json:"next_pattern"`
	Adjustments   map[string]any `json:"adjustments"`
	CompletedAt   time.Time      `json:"completed_at"`
	CreatedAt     time.Time      `json:"created_at"`
}

// PursuitHeader is the minimum mem_pursuits row the coach needs to reason
// about a pursuit: id, title, cadence, experience, config, created_at.
type PursuitHeader struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Cadence    string         `json:"cadence"`
	Experience string         `json:"experience"`
	Config     map[string]any `json:"config"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Cockpit is the composed read the /api/pursuits/pc/state endpoint and
// the Studio cockpit render against. Nil sections are always empty
// slices, never null, so the client can render without null-guards.
type Cockpit struct {
	Pursuit        PursuitHeader `json:"pursuit"`
	State          State         `json:"state"`
	TodayProofs    []Proof       `json:"today_proofs"`
	RecentProofs   []Proof       `json:"recent_proofs"`
	TodayEvidence  []Evidence    `json:"today_evidence"`
	RecentEvidence []Evidence    `json:"recent_evidence"`
	Memories       []Memory      `json:"memories"`
	Patterns       []Pattern     `json:"patterns"`
	Corrections    []Pattern     `json:"corrections"`
	RecentSessions []Session     `json:"recent_sessions"`
	CycleReviews   []Review      `json:"cycle_reviews"`
	// Guidance is the coach's next-step recommendation for the boss. It
	// carries a headline (the action-first CTA), a body (author-framed
	// prompt), and hints (the coach's derived reasoning).
	Guidance Guidance `json:"guidance"`
}
