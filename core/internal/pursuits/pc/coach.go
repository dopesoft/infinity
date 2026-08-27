package pc

import (
	"strings"
	"time"
)

// Guidance is the coach's next-step recommendation surfaced to the
// cockpit. Every field is safe to render as-is; no LLM roundtrip is
// required. Headline drives the action-first CTA button, Body is the
// author-framed prompt, Hints is the derived reasoning ("day 5 of 21,
// morning not yet logged").
type Guidance struct {
	// Phase names the coaching phase the boss should enter next
	// (onboarding, morning, midday, evening, recovery, review, or
	// idle when nothing is due). The cockpit branches on this.
	Phase string `json:"phase"`

	// Headline is the imperative CTA the cockpit renders on its
	// primary button ("Start morning rehearsal", "Log today's proof").
	Headline string `json:"headline"`

	// Body is the coach's author-framed prompt. Two to four sentences,
	// no em dashes or emojis, always attributes framing to Maltz where
	// it is not the boss's own words.
	Body string `json:"body"`

	// Hints are short informational chips. Deterministic derivations
	// only (day count, missed-day flag, current pattern).
	Hints []string `json:"hints"`

	// Prompt is the primary question the coach wants answered in this
	// phase. Kept separate from Body so the UI can render a labelled
	// input placeholder.
	Prompt string `json:"prompt"`

	// SecondaryPrompts are additional questions for phases that ask
	// more than one (evening splits fact / interpretation / lesson /
	// correction).
	SecondaryPrompts []GuidancePrompt `json:"secondary_prompts,omitempty"`
}

// GuidancePrompt is one labelled question the coach wants answered.
type GuidancePrompt struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
	Help        string `json:"help,omitempty"`
}

// Snapshot is the read-only view of a pursuit and its state the coach
// reasons over. Kept small and value-typed so the deterministic decision
// logic is testable without any DB.
type Snapshot struct {
	Pursuit PursuitHeader
	State   State
	Now     time.Time
	Today   time.Time
	// TookMorningToday reports whether today already recorded a morning
	// session (based on last_morning_at + local timezone). Derived by
	// NewSnapshot so the coach reasons on values not clocks.
	TookMorningToday bool
	TookMiddayToday  bool
	TookEveningToday bool
	// LastSessionDaysAgo is the number of local-day boundaries since the
	// most recent session of any kind. Zero means one landed today.
	LastSessionDaysAgo int
	// ProofPendingToday reports whether a proof is planned for today but
	// not yet marked taken. Drives the midday nudge phase.
	ProofPendingToday bool
	// EvidenceCountToday counts evidence + resistance rows captured
	// today. Feeds the evening prompt.
	EvidenceCountToday int
}

// NewSnapshot derives the Snapshot from raw values. All timezone-aware
// logic lands here so the coach's branch decisions are pure functions
// of ints + bools.
func NewSnapshot(header PursuitHeader, state State, now time.Time,
	proofs []Proof, evidence []Evidence, sessions []Session) Snapshot {
	loc := loadLocation(state.Timezone)
	today := timeToLocalDate(now, loc)

	s := Snapshot{
		Pursuit: header,
		State:   state,
		Now:     now,
		Today:   today,
	}

	if state.LastMorningAt != nil && isSameLocalDay(*state.LastMorningAt, today, loc) {
		s.TookMorningToday = true
	}
	if state.LastMiddayAt != nil && isSameLocalDay(*state.LastMiddayAt, today, loc) {
		s.TookMiddayToday = true
	}
	if state.LastEveningAt != nil && isSameLocalDay(*state.LastEveningAt, today, loc) {
		s.TookEveningToday = true
	}

	// Days since any session of any kind. Sessions arrive sorted DESC by
	// occurred_at; take the first one that has a timestamp.
	s.LastSessionDaysAgo = 9999
	for _, sess := range sessions {
		if sess.OccurredAt.IsZero() {
			continue
		}
		s.LastSessionDaysAgo = localDayDelta(sess.OccurredAt, today, loc)
		break
	}
	if len(sessions) == 0 {
		// No history at all - flag as "many days" so the recovery branch
		// does not trigger on the fresh onboarding case.
		s.LastSessionDaysAgo = 0
	}

	// Proof pending today = there exists a proof planned for today and it
	// is not yet marked taken. Small O(n) loop over the (small) today
	// slice; no DB roundtrip.
	for _, p := range proofs {
		if !isSameLocalDay(p.PlannedAt, today, loc) {
			continue
		}
		if !p.Taken {
			s.ProofPendingToday = true
		}
	}

	for _, e := range evidence {
		if isSameLocalDay(e.CapturedAt, today, loc) {
			s.EvidenceCountToday++
		}
	}

	return s
}

// NextGuidance decides which coaching phase the boss should enter next
// and formats the coach's author-framed prompt. Deterministic; the LLM is
// never in the loop for this decision (Rule #1b: mechanic in code).
func NextGuidance(s Snapshot) Guidance {
	// Onboarding first. If the state row was never populated
	// (identity/objective blank), that is the only phase that runs.
	if strings.TrimSpace(s.State.CurrentIdentity) == "" ||
		strings.TrimSpace(s.State.CurrentObjective) == "" {
		return guidanceOnboarding(s)
	}

	// End-of-cycle review takes precedence over daily phases once the
	// cycle reaches its final day and no review has closed it. The
	// caller passes a State with a cycle_number that increments only on
	// review completion, so a due review sits here until closed.
	if s.State.CurrentDay >= s.State.CycleLengthDays {
		return guidanceReview(s)
	}

	// Non-shaming recovery. Trigger when the last session was two or
	// more local-day boundaries ago (i.e. an entire day was missed
	// with no morning/midday/evening/recovery landing). Never scolds;
	// always frames the missed day as data for the coach.
	if s.LastSessionDaysAgo >= 2 {
		return guidanceRecovery(s)
	}

	if !s.TookMorningToday {
		return guidanceMorning(s)
	}

	// Midday nudge for a pledged proof that has not been marked taken
	// yet. Only surfaced if morning happened AND a proof is pending;
	// otherwise the day flows straight to evening.
	if s.ProofPendingToday && !s.TookMiddayToday {
		return guidanceMidday(s)
	}

	if !s.TookEveningToday {
		return guidanceEvening(s)
	}

	// All three daily phases already logged. Idle guidance keeps the
	// cockpit non-empty so the boss sees "today is complete" instead of
	// an empty screen.
	return guidanceIdle(s)
}

// ── Phase authors ─────────────────────────────────────────────────────────

func guidanceOnboarding(s Snapshot) Guidance {
	return Guidance{
		Phase:    SessionOnboarding,
		Headline: "Start onboarding",
		Body: "In Maltz's framing, the 21-day cycle begins with three deliberate " +
			"choices, the abundance objective you are aiming at, the limiting " +
			"pattern you have noticed pulling you back, and the operating identity " +
			"you want to experiment with. Write each in your own words; you will " +
			"pressure test the identity in the next step.",
		Hints:  onboardingHints(s),
		Prompt: "What is the abundance objective you want this cycle to move you toward?",
		SecondaryPrompts: []GuidancePrompt{
			{
				Key:         "limiting_pattern",
				Label:       "Limiting pattern",
				Placeholder: "The reflex or story you have caught yourself repeating.",
				Help:        "In Maltz's language, this is the old self image talking. Naming it is what lets you rehearse a correction.",
			},
			{
				Key:         "identity",
				Label:       "Operating identity",
				Placeholder: "The person you are practising being for the next 21 days.",
				Help:        "Framed as an identity you are trying on, not a permanent claim about yourself.",
			},
		},
	}
}

func guidanceMorning(s Snapshot) Guidance {
	body := "In Maltz's framing, the morning routine has three parts. First, spend a " +
		"few breaths in the quiet room you have built in your mind. Second, vividly " +
		"rehearse the real situation you will face today as the person your operating " +
		"identity says you are; the nervous system will treat that rehearsal as " +
		"experience. Third, pledge one deliberate proof action you will take today to " +
		"back the identity with behaviour."
	hints := dayHints(s)
	if strings.TrimSpace(s.State.CurrentLimitingPattern) != "" {
		hints = append(hints, "limiting pattern: "+truncateForHint(s.State.CurrentLimitingPattern))
	}
	return Guidance{
		Phase:    SessionMorning,
		Headline: "Start morning rehearsal",
		Body:     body,
		Hints:    hints,
		Prompt:   "Which real situation today will you rehearse as the identity you are practising?",
		SecondaryPrompts: []GuidancePrompt{
			{
				Key:         "proof_pledge",
				Label:       "Today's proof action",
				Placeholder: "One deliberate action you will take today to prove the identity in behaviour.",
				Help:        "Kept small enough to be certain you will do it. A missed proof teaches you the pattern more than a heroic one you skipped.",
			},
		},
	}
}

func guidanceMidday(s Snapshot) Guidance {
	return Guidance{
		Phase:    SessionMidday,
		Headline: "Log evidence or resistance",
		Body: "Two brief captures while the day is still live. Evidence is any moment " +
			"the identity worked, however small. Resistance is any moment the old " +
			"pattern showed up. Both count as data; neither is scored.",
		Hints:  dayHints(s),
		Prompt: "What happened in the last few hours that looks like evidence for the identity, or like the old pattern?",
		SecondaryPrompts: []GuidancePrompt{
			{
				Key:         "resistance",
				Label:       "Any resistance you noticed",
				Placeholder: "Where did the old pattern try to run today?",
				Help:        "Naming resistance is a correction in itself, in Maltz's framing.",
			},
		},
	}
}

func guidanceEvening(s Snapshot) Guidance {
	body := "One question at bedtime, in four parts. First, name the fact of what " +
		"actually happened today with the identity, plainly. Second, name your " +
		"interpretation of that fact, separately, so the two are not fused. Third, " +
		"note the lesson the day taught you. Fourth, write the correction you will " +
		"try tomorrow. This is Maltz's negative feedback loop in your own words: " +
		"error, correction, then the servo mechanism keeps working."
	return Guidance{
		Phase:    SessionEvening,
		Headline: "Close today with the one question",
		Body:     body,
		Hints:    dayHints(s),
		Prompt:   "What is the FACT of what happened today with your identity, stated plainly and without interpretation?",
		SecondaryPrompts: []GuidancePrompt{
			{
				Key:         "interpretation",
				Label:       "Your interpretation of that fact",
				Placeholder: "How you are reading the fact, kept separate from the fact itself.",
				Help:        "Held separate so the interpretation can be revised without denying the fact.",
			},
			{
				Key:         "lesson",
				Label:       "The lesson the day taught you",
				Placeholder: "One line, in Maltz's framing this is the servo's correction signal.",
			},
			{
				Key:         "correction",
				Label:       "The correction you will try tomorrow",
				Placeholder: "The specific adjustment for tomorrow's morning rehearsal.",
			},
		},
	}
}

func guidanceRecovery(s Snapshot) Guidance {
	return Guidance{
		Phase:    SessionRecovery,
		Headline: "Ease back in",
		Body: "A day was missed. In Maltz's framing that is data for the servo, not a " +
			"grade on you. There is no restart. Continue from wherever you pick it up, " +
			"write one line about what pulled you away, then decide the smallest possible " +
			"morning rehearsal that will be certain to land today.",
		Hints:  append([]string{"recovery day"}, dayHints(s)...),
		Prompt: "What pulled you away from the practice, in one line and without judgement?",
		SecondaryPrompts: []GuidancePrompt{
			{
				Key:         "smallest_next_step",
				Label:       "The smallest morning rehearsal you can do now",
				Placeholder: "A shorter version of the morning that you are certain to complete.",
			},
		},
	}
}

func guidanceReview(s Snapshot) Guidance {
	return Guidance{
		Phase:    SessionReview,
		Headline: "Run the deliberate identity review",
		Body: "The 21 day cycle is complete. In Maltz's framing, this is the moment to " +
			"look at the data the servo collected and decide the next cycle's " +
			"identity, objective, and pattern with deliberate judgement rather than " +
			"drift. Nothing needs to change if the current framing is still doing the " +
			"work; every field is optional and blank means keep it.",
		Hints:  []string{"cycle " + itoa(s.State.CycleNumber) + " complete"},
		Prompt: "What are the concrete wins this cycle produced, in your own words?",
		SecondaryPrompts: []GuidancePrompt{
			{Key: "misses", Label: "What did not land", Placeholder: "The specific misses, held separate from any judgement."},
			{Key: "next_identity", Label: "Next cycle's identity", Placeholder: "Blank to keep the current one."},
			{Key: "next_objective", Label: "Next cycle's objective", Placeholder: "Blank to keep the current one."},
			{Key: "next_pattern", Label: "Next cycle's limiting pattern", Placeholder: "The pattern you now want to rehearse a correction against."},
		},
	}
}

func guidanceIdle(s Snapshot) Guidance {
	return Guidance{
		Phase:    "idle",
		Headline: "Today is complete",
		Body: "Morning, midday, and evening are all logged. In Maltz's framing that is " +
			"the servo running with clean feedback. Come back tomorrow morning for the " +
			"next rehearsal, or capture any late evidence that surfaces.",
		Hints: dayHints(s),
	}
}

// ── Adjustment / adaptation ───────────────────────────────────────────────

// AdaptForResistance emits an Adjustment guidance the coach can offer
// when today's evidence has piled up more resistance than evidence. Kept
// as a pure helper: caller decides when to offer it.
func AdaptForResistance(s Snapshot, evidenceCountToday, resistanceCountToday int) Guidance {
	if evidenceCountToday+resistanceCountToday == 0 {
		return Guidance{}
	}
	if resistanceCountToday <= evidenceCountToday {
		return Guidance{}
	}
	return Guidance{
		Phase:    SessionAdjustment,
		Headline: "Consider a smaller proof for tomorrow",
		Body: "Today logged more resistance than evidence. In Maltz's framing this is " +
			"not failure, it is the servo asking for a smaller correction. Consider " +
			"shrinking tomorrow's proof action so it is certain to land, and leave the " +
			"identity itself where it is.",
		Hints:  dayHints(s),
		Prompt: "What is a smaller proof action you are certain to complete tomorrow?",
	}
}

// ── Small deterministic helpers (kept internal so the coach reasons in
// value types instead of dragging in a full clock/tz module). ─────────────

func onboardingHints(s Snapshot) []string {
	hints := []string{"day 1 of " + itoa(s.State.CycleLengthDays)}
	if s.State.CycleNumber > 1 {
		hints = append(hints, "cycle "+itoa(s.State.CycleNumber))
	}
	return hints
}

func dayHints(s Snapshot) []string {
	hints := []string{
		"day " + itoa(s.State.CurrentDay) + " of " + itoa(s.State.CycleLengthDays),
	}
	if s.State.CycleNumber > 1 {
		hints = append(hints, "cycle "+itoa(s.State.CycleNumber))
	}
	if s.State.MissedDaysCount > 0 {
		hints = append(hints, itoa(s.State.MissedDaysCount)+" missed this cycle")
	}
	return hints
}

func truncateForHint(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 60 {
		return s[:57] + "..."
	}
	return s
}

func itoa(n int) string {
	// small ints only; avoid importing strconv for this one helper.
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	buf := [20]byte{}
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// loadLocation resolves the IANA timezone with a safe fallback.
func loadLocation(tz string) *time.Location {
	if tz == "" {
		tz = "America/Chicago"
	}
	if loc, err := time.LoadLocation(tz); err == nil {
		return loc
	}
	// Fallback: UTC (deterministic, never nil).
	return time.UTC
}

// timeToLocalDate returns the midnight-local time.Time for the date the
// input falls on in the given location. Used as a canonical "today" for
// day-boundary comparisons.
func timeToLocalDate(t time.Time, loc *time.Location) time.Time {
	local := t.In(loc)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
}

// isSameLocalDay reports whether two timestamps land on the same calendar
// day in the supplied location.
func isSameLocalDay(t time.Time, localDate time.Time, loc *time.Location) bool {
	return timeToLocalDate(t, loc).Equal(localDate)
}

// localDayDelta returns the number of local-day boundaries between the
// supplied timestamp and today. today must already be midnight-local.
func localDayDelta(t time.Time, today time.Time, loc *time.Location) int {
	tDay := timeToLocalDate(t, loc)
	// Compare civil dates through UTC midnight. Using elapsed local hours
	// miscounts the 23-hour and 25-hour days around daylight-saving changes.
	fromCivil := time.Date(tDay.Year(), tDay.Month(), tDay.Day(), 0, 0, 0, 0, time.UTC)
	toCivil := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, time.UTC)
	days := int(toCivil.Sub(fromCivil).Hours() / 24)
	if days < 0 {
		days = -days
	}
	return days
}
