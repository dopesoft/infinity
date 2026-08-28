package pc

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence layer for the Psycho-Cybernetics experience.
//
// Rule #1b split: everything here is a MECHANIC. Day arithmetic, missed-day
// counting, cycle rollover, and the "which phase is due" derivation are
// guaranteed by this code, so a coaching run behaves identically whether or
// not the LLM remembers the recipe. The judgment (what the coaching actually
// says today) lives in the seeded skill and in the boss's own writing.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store over the shared pgx pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ErrNotPsychoCybernetics is returned when a caller points a pc endpoint at a
// pursuit that is not running this experience. Ordinary pursuits must never be
// mutated through this package.
var ErrNotPsychoCybernetics = errors.New("pursuit is not a psycho_cybernetics experience")

// ErrNoPursuit is returned when the pursuit id does not exist.
var ErrNoPursuit = errors.New("pursuit not found")

// maxTextLen caps any single free-text field the boss writes. Generous enough
// for long reflective answers, small enough that a runaway client cannot bloat
// a row. Applied at the store boundary so every caller inherits it.
const maxTextLen = 8000

// clampText trims and truncates a free-text field.
func clampText(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxTextLen {
		return s[:maxTextLen]
	}
	return s
}

// ── Header ────────────────────────────────────────────────────────────────

// Header loads the mem_pursuits row and verifies it runs this experience.
func (s *Store) Header(ctx context.Context, pursuitID string) (PursuitHeader, error) {
	var h PursuitHeader
	var configRaw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, title, cadence, experience, config, created_at
		FROM mem_pursuits
		WHERE id = $1::uuid
	`, pursuitID).Scan(&h.ID, &h.Title, &h.Cadence, &h.Experience, &configRaw, &h.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return h, ErrNoPursuit
	}
	if err != nil {
		return h, fmt.Errorf("load pursuit: %w", err)
	}
	if h.Experience != ExperiencePsychoCybernetics {
		return h, ErrNotPsychoCybernetics
	}
	h.Config = map[string]any{}
	if len(configRaw) > 0 {
		_ = json.Unmarshal(configRaw, &h.Config)
	}
	return h, nil
}

// ── State ─────────────────────────────────────────────────────────────────

// DefaultCycleLengthDays is the 21-day spine. Maltz's own framing: run the
// experiment for a minimum of 21 days before judging it. The stable spine is
// the point, so this is a constant, not a tunable.
const DefaultCycleLengthDays = 21

// EnsureState inserts the state row for a pursuit if none exists, then returns
// the current row. Idempotent so the cockpit can be opened before onboarding
// has run.
//
// The cycle length is seeded from mem_pursuits.config->>'cycle_length_days'
// when the pursuit carries one, so the config the pursuit was created with is
// actually load-bearing rather than decoration. Anything missing or
// non-numeric falls back to the 21-day spine.
func (s *Store) EnsureState(ctx context.Context, pursuitID string) (State, error) {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO mem_pursuit_pc_state (pursuit_id, cycle_length_days)
		SELECT p.id,
		       CASE WHEN jsonb_typeof(p.config -> 'cycle_length_days') = 'number'
		            THEN GREATEST(1, (p.config ->> 'cycle_length_days')::int)
		            ELSE $2 END
		FROM mem_pursuits p
		WHERE p.id = $1::uuid
		ON CONFLICT (pursuit_id) DO NOTHING
	`, pursuitID, DefaultCycleLengthDays); err != nil {
		return State{}, fmt.Errorf("ensure state: %w", err)
	}
	return s.loadState(ctx, pursuitID)
}

func (s *Store) loadState(ctx context.Context, pursuitID string) (State, error) {
	var st State
	var pressureRaw []byte
	err := s.pool.QueryRow(ctx, `
		SELECT pursuit_id::text, cycle_number, cycle_length_days, current_day,
		       cycle_started_at, missed_days_count, current_identity,
		       current_objective, current_limiting_pattern, pressure_test,
		       timezone, last_morning_at, last_midday_at, last_evening_at,
		       created_at, updated_at
		FROM mem_pursuit_pc_state
		WHERE pursuit_id = $1::uuid
	`, pursuitID).Scan(
		&st.PursuitID, &st.CycleNumber, &st.CycleLengthDays, &st.CurrentDay,
		&st.CycleStartedAt, &st.MissedDaysCount, &st.CurrentIdentity,
		&st.CurrentObjective, &st.CurrentLimitingPattern, &pressureRaw,
		&st.Timezone, &st.LastMorningAt, &st.LastMiddayAt, &st.LastEveningAt,
		&st.CreatedAt, &st.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return st, ErrNoPursuit
	}
	if err != nil {
		return st, fmt.Errorf("load state: %w", err)
	}
	if len(pressureRaw) > 0 {
		_ = json.Unmarshal(pressureRaw, &st.PressureTest)
	}
	return st, nil
}

// DeriveDay computes the 1-based day in the current cycle from the cycle start
// and the pursuit timezone, capped at the cycle length. Pure so the arithmetic
// is unit-testable without a database.
func DeriveDay(cycleStartedAt, now time.Time, timezone string, cycleLengthDays int) int {
	if cycleLengthDays < 1 {
		cycleLengthDays = DefaultCycleLengthDays
	}
	loc := loadLocation(timezone)
	today := timeToLocalDate(now, loc)
	day := localDayDelta(cycleStartedAt, today, loc) + 1
	if day < 1 {
		day = 1
	}
	if day > cycleLengthDays {
		day = cycleLengthDays
	}
	return day
}

// DeriveMissedDays counts elapsed days in the cycle that closed with no session
// logged. Today is never counted as missed - the day is still live. Pure so the
// non-shaming semantics are pinned by a test.
func DeriveMissedDays(currentDay int, sessionDaysBeforeToday int) int {
	missed := (currentDay - 1) - sessionDaysBeforeToday
	if missed < 0 {
		return 0
	}
	return missed
}

// refreshState recomputes the derived fields (current_day, missed_days_count)
// and persists them. Called on every cockpit read so the programme advances on
// wall-clock time without needing a cron to tick it.
func (s *Store) refreshState(ctx context.Context, st State, now time.Time) (State, error) {
	day := DeriveDay(st.CycleStartedAt, now, st.Timezone, st.CycleLengthDays)

	// Distinct local days inside this cycle, before today, that recorded at
	// least one session. Timezone conversion happens in SQL so the day
	// boundary matches the coach's. "Today" is the caller's `now`, not SQL
	// NOW(), so one read cannot straddle two different clocks.
	//
	// The zone is normalised through the same function DeriveDay resolves in
	// Go. Passing the raw column would let the two disagree - and a zone
	// Postgres rejects outright ("time zone not recognized") would fail every
	// cockpit read, turning one bad string into a dead surface.
	tz := NormalizeTimezone(st.Timezone)
	var sessionDays int
	if err := s.pool.QueryRow(ctx, `
		SELECT COUNT(DISTINCT (occurred_at AT TIME ZONE $2)::date)
		FROM mem_pursuit_pc_sessions
		WHERE pursuit_id = $1::uuid
		  AND cycle_number = $3
		  AND (occurred_at AT TIME ZONE $2)::date < ($4::timestamptz AT TIME ZONE $2)::date
	`, st.PursuitID, tz, st.CycleNumber, now).Scan(&sessionDays); err != nil {
		return st, fmt.Errorf("count session days: %w", err)
	}

	missed := DeriveMissedDays(day, sessionDays)
	if day == st.CurrentDay && missed == st.MissedDaysCount {
		return st, nil
	}
	st.CurrentDay = day
	st.MissedDaysCount = missed
	if _, err := s.pool.Exec(ctx, `
		UPDATE mem_pursuit_pc_state
		   SET current_day = $2, missed_days_count = $3, updated_at = NOW()
		 WHERE pursuit_id = $1::uuid
	`, st.PursuitID, day, missed); err != nil {
		return st, fmt.Errorf("refresh state: %w", err)
	}
	return st, nil
}

// SaveIdentity writes the operating identity, abundance objective, limiting
// pattern, and pressure test. Blank fields are left untouched so the cockpit's
// inline editors can patch one field at a time.
func (s *Store) SaveIdentity(ctx context.Context, pursuitID string, identity, objective, pattern string, pressure *PressureTest, timezone string) (State, error) {
	// Reject an unloadable zone at the point it is written. Stored, it would
	// silently move every future day boundary for this pursuit, and the boss
	// would see it as the programme counting days wrong rather than as the
	// typo it is.
	if tz := strings.TrimSpace(timezone); tz != "" && !IsValidTimezone(tz) {
		return State{}, fmt.Errorf("unknown timezone %q: use an IANA name such as America/Chicago", tz)
	}
	if _, err := s.EnsureState(ctx, pursuitID); err != nil {
		return State{}, err
	}
	var pressureJSON *string
	if pressure != nil {
		b, err := json.Marshal(pressure)
		if err != nil {
			return State{}, fmt.Errorf("marshal pressure test: %w", err)
		}
		v := string(b)
		pressureJSON = &v
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE mem_pursuit_pc_state
		   SET current_identity         = COALESCE(NULLIF($2, ''), current_identity),
		       current_objective        = COALESCE(NULLIF($3, ''), current_objective),
		       current_limiting_pattern = COALESCE(NULLIF($4, ''), current_limiting_pattern),
		       pressure_test            = COALESCE($5::jsonb, pressure_test),
		       timezone                 = COALESCE(NULLIF($6, ''), timezone),
		       updated_at               = NOW()
		 WHERE pursuit_id = $1::uuid
	`, pursuitID, clampText(identity), clampText(objective), clampText(pattern), pressureJSON, strings.TrimSpace(timezone)); err != nil {
		return State{}, fmt.Errorf("save identity: %w", err)
	}
	return s.loadState(ctx, pursuitID)
}

// ── Sessions ──────────────────────────────────────────────────────────────

// LogSession records one coaching session and stamps the matching
// last_<phase>_at column so the coach's phase derivation advances. Both writes
// happen in one transaction: a session that does not advance the phase would
// leave the cockpit asking the same question forever.
func (s *Store) LogSession(ctx context.Context, pursuitID, kind string, answers map[string]any, coachNote string) (Session, error) {
	if !IsValidSessionKind(kind) {
		return Session{}, fmt.Errorf("unknown session kind %q", kind)
	}
	st, err := s.EnsureState(ctx, pursuitID)
	if err != nil {
		return Session{}, err
	}
	st, err = s.refreshState(ctx, st, time.Now())
	if err != nil {
		return Session{}, err
	}
	if answers == nil {
		answers = map[string]any{}
	}
	answersJSON, err := json.Marshal(clampAnswers(answers))
	if err != nil {
		return Session{}, fmt.Errorf("marshal answers: %w", err)
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Session{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var sess Session
	var rawAnswers []byte
	if err := tx.QueryRow(ctx, `
		INSERT INTO mem_pursuit_pc_sessions
			(pursuit_id, kind, cycle_number, day_in_cycle, answers, coach_note)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6)
		RETURNING id::text, pursuit_id::text, kind, cycle_number, day_in_cycle,
		          answers, coach_note, occurred_at, created_at
	`, pursuitID, kind, st.CycleNumber, st.CurrentDay, string(answersJSON), clampText(coachNote)).Scan(
		&sess.ID, &sess.PursuitID, &sess.Kind, &sess.CycleNumber, &sess.DayInCycle,
		&rawAnswers, &sess.CoachNote, &sess.OccurredAt, &sess.CreatedAt,
	); err != nil {
		return Session{}, fmt.Errorf("insert session: %w", err)
	}
	sess.Answers = map[string]any{}
	if len(rawAnswers) > 0 {
		_ = json.Unmarshal(rawAnswers, &sess.Answers)
	}

	// Stamp the phase clock. Recovery counts as the day's morning: it is the
	// re-entry ritual, so the cockpit should move on to the day's proof rather
	// than asking for a second rehearsal.
	if col := phaseColumn(kind); col != "" {
		if _, err := tx.Exec(ctx, fmt.Sprintf(`
			UPDATE mem_pursuit_pc_state
			   SET %s = $2, updated_at = NOW()
			 WHERE pursuit_id = $1::uuid
		`, col), pursuitID, sess.OccurredAt); err != nil {
			return Session{}, fmt.Errorf("stamp phase: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return Session{}, fmt.Errorf("commit session: %w", err)
	}
	return sess, nil
}

// phaseColumn maps a session kind onto the state column that records it.
// Onboarding, review, and adjustment do not advance the daily clock.
func phaseColumn(kind string) string {
	switch kind {
	case SessionMorning, SessionRecovery:
		return "last_morning_at"
	case SessionMidday:
		return "last_midday_at"
	case SessionEvening:
		return "last_evening_at"
	default:
		return ""
	}
}

// clampAnswers bounds every string value in the freeform answers blob.
func clampAnswers(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if str, ok := v.(string); ok {
			out[k] = clampText(str)
			continue
		}
		out[k] = v
	}
	return out
}

func (s *Store) listSessions(ctx context.Context, pursuitID string, limit int) ([]Session, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, pursuit_id::text, kind, cycle_number, day_in_cycle,
		       answers, coach_note, occurred_at, created_at
		FROM mem_pursuit_pc_sessions
		WHERE pursuit_id = $1::uuid
		ORDER BY occurred_at DESC
		LIMIT $2
	`, pursuitID, limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var s Session
		var raw []byte
		if err := rows.Scan(&s.ID, &s.PursuitID, &s.Kind, &s.CycleNumber, &s.DayInCycle,
			&raw, &s.CoachNote, &s.OccurredAt, &s.CreatedAt); err != nil {
			return nil, err
		}
		s.Answers = map[string]any{}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &s.Answers)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ── Proofs ────────────────────────────────────────────────────────────────

// AddProof records the deliberate proof action pledged for today. The unique
// index on (pursuit, cycle, day, label) makes a duplicate pledge a no-op
// update rather than a second row.
func (s *Store) AddProof(ctx context.Context, pursuitID, label string, sessionID *string) (Proof, error) {
	label = clampText(label)
	if label == "" {
		return Proof{}, errors.New("proof label required")
	}
	st, err := s.EnsureState(ctx, pursuitID)
	if err != nil {
		return Proof{}, err
	}
	st, err = s.refreshState(ctx, st, time.Now())
	if err != nil {
		return Proof{}, err
	}
	var p Proof
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_pursuit_pc_proofs
			(pursuit_id, session_id, label, cycle_number, day_in_cycle)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5)
		ON CONFLICT (pursuit_id, cycle_number, day_in_cycle, label)
		DO UPDATE SET updated_at = NOW()
		RETURNING id::text, pursuit_id::text, session_id::text, label, cycle_number,
		          day_in_cycle, planned_at, taken, taken_at, note, created_at, updated_at
	`, pursuitID, sessionID, label, st.CycleNumber, st.CurrentDay).Scan(
		&p.ID, &p.PursuitID, &p.SessionID, &p.Label, &p.CycleNumber,
		&p.DayInCycle, &p.PlannedAt, &p.Taken, &p.TakenAt, &p.Note, &p.CreatedAt, &p.UpdatedAt,
	); err != nil {
		return Proof{}, fmt.Errorf("insert proof: %w", err)
	}
	return p, nil
}

// SetProofTaken marks a proof taken or untaken. taken_at is cleared on untake
// so the chk_pc_proof_taken_at constraint always holds.
//
// Scoped to the pursuit on purpose. Apply verifies that the PURSUIT runs this
// experience, so an update keyed on the proof id alone would inherit an
// authorisation it never actually performed: a caller could pass a valid
// psycho_cybernetics pursuit and a proof id belonging to a different one, and
// mark that other programme's action taken. Matching both means the check that
// was made is the check that applies.
func (s *Store) SetProofTaken(ctx context.Context, pursuitID, proofID string, taken bool, note string) (Proof, error) {
	var p Proof
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_pursuit_pc_proofs
		   SET taken    = $3,
		       taken_at = CASE WHEN $3 THEN COALESCE(taken_at, NOW()) ELSE NULL END,
		       note     = COALESCE(NULLIF($4, ''), note),
		       updated_at = NOW()
		 WHERE id = $2::uuid AND pursuit_id = $1::uuid
		RETURNING id::text, pursuit_id::text, session_id::text, label, cycle_number,
		          day_in_cycle, planned_at, taken, taken_at, note, created_at, updated_at
	`, pursuitID, proofID, taken, clampText(note)).Scan(
		&p.ID, &p.PursuitID, &p.SessionID, &p.Label, &p.CycleNumber,
		&p.DayInCycle, &p.PlannedAt, &p.Taken, &p.TakenAt, &p.Note, &p.CreatedAt, &p.UpdatedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return p, errors.New("proof not found on this pursuit")
	}
	if err != nil {
		return p, fmt.Errorf("update proof: %w", err)
	}
	return p, nil
}

func (s *Store) listProofs(ctx context.Context, pursuitID string, limit int) ([]Proof, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, pursuit_id::text, session_id::text, label, cycle_number,
		       day_in_cycle, planned_at, taken, taken_at, note, created_at, updated_at
		FROM mem_pursuit_pc_proofs
		WHERE pursuit_id = $1::uuid
		ORDER BY planned_at DESC
		LIMIT $2
	`, pursuitID, limit)
	if err != nil {
		return nil, fmt.Errorf("list proofs: %w", err)
	}
	defer rows.Close()
	out := []Proof{}
	for rows.Next() {
		var p Proof
		if err := rows.Scan(&p.ID, &p.PursuitID, &p.SessionID, &p.Label, &p.CycleNumber,
			&p.DayInCycle, &p.PlannedAt, &p.Taken, &p.TakenAt, &p.Note, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── Evidence ──────────────────────────────────────────────────────────────

// AddEvidence captures a daytime observation: evidence the identity worked, or
// resistance where the old pattern showed up.
func (s *Store) AddEvidence(ctx context.Context, pursuitID, kind, body string, tags []string, sessionID *string) (Evidence, error) {
	if !IsValidEvidenceKind(kind) {
		return Evidence{}, fmt.Errorf("unknown evidence kind %q", kind)
	}
	body = clampText(body)
	if body == "" {
		return Evidence{}, errors.New("evidence body required")
	}
	st, err := s.EnsureState(ctx, pursuitID)
	if err != nil {
		return Evidence{}, err
	}
	st, err = s.refreshState(ctx, st, time.Now())
	if err != nil {
		return Evidence{}, err
	}
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	var e Evidence
	var rawTags []byte
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_pursuit_pc_evidence
			(pursuit_id, session_id, kind, body, tags, cycle_number, day_in_cycle)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7)
		RETURNING id::text, pursuit_id::text, session_id::text, kind, body, tags,
		          cycle_number, day_in_cycle, captured_at
	`, pursuitID, sessionID, kind, body, string(tagsJSON), st.CycleNumber, st.CurrentDay).Scan(
		&e.ID, &e.PursuitID, &e.SessionID, &e.Kind, &e.Body, &rawTags,
		&e.CycleNumber, &e.DayInCycle, &e.CapturedAt,
	); err != nil {
		return Evidence{}, fmt.Errorf("insert evidence: %w", err)
	}
	e.Tags = decodeTags(rawTags)
	return e, nil
}

func (s *Store) listEvidence(ctx context.Context, pursuitID string, limit int) ([]Evidence, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, pursuit_id::text, session_id::text, kind, body, tags,
		       cycle_number, day_in_cycle, captured_at
		FROM mem_pursuit_pc_evidence
		WHERE pursuit_id = $1::uuid
		ORDER BY captured_at DESC
		LIMIT $2
	`, pursuitID, limit)
	if err != nil {
		return nil, fmt.Errorf("list evidence: %w", err)
	}
	defer rows.Close()
	out := []Evidence{}
	for rows.Next() {
		var e Evidence
		var rawTags []byte
		if err := rows.Scan(&e.ID, &e.PursuitID, &e.SessionID, &e.Kind, &e.Body, &rawTags,
			&e.CycleNumber, &e.DayInCycle, &e.CapturedAt); err != nil {
			return nil, err
		}
		e.Tags = decodeTags(rawTags)
		out = append(out, e)
	}
	return out, rows.Err()
}

func decodeTags(raw []byte) []string {
	tags := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &tags)
	}
	if tags == nil {
		tags = []string{}
	}
	return tags
}

// ── Memories ──────────────────────────────────────────────────────────────

// AddMemory banks a success memory the coach can pull into morning rehearsal.
func (s *Store) AddMemory(ctx context.Context, pursuitID, title, body string, tags []string, weight int) (Memory, error) {
	title = clampText(title)
	if title == "" {
		return Memory{}, errors.New("memory title required")
	}
	if weight < 0 || weight > 100 {
		weight = 50
	}
	if tags == nil {
		tags = []string{}
	}
	tagsJSON, _ := json.Marshal(tags)
	var m Memory
	var rawTags []byte
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_pursuit_pc_memories (pursuit_id, title, body, tags, weight)
		VALUES ($1::uuid, $2, $3, $4::jsonb, $5)
		RETURNING id::text, pursuit_id::text, title, body, tags, weight, saved_at
	`, pursuitID, title, clampText(body), string(tagsJSON), weight).Scan(
		&m.ID, &m.PursuitID, &m.Title, &m.Body, &rawTags, &m.Weight, &m.SavedAt,
	); err != nil {
		return Memory{}, fmt.Errorf("insert memory: %w", err)
	}
	m.Tags = decodeTags(rawTags)
	return m, nil
}

func (s *Store) listMemories(ctx context.Context, pursuitID string, limit int) ([]Memory, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, pursuit_id::text, title, body, tags, weight, saved_at
		FROM mem_pursuit_pc_memories
		WHERE pursuit_id = $1::uuid
		ORDER BY weight DESC, saved_at DESC
		LIMIT $2
	`, pursuitID, limit)
	if err != nil {
		return nil, fmt.Errorf("list memories: %w", err)
	}
	defer rows.Close()
	out := []Memory{}
	for rows.Next() {
		var m Memory
		var rawTags []byte
		if err := rows.Scan(&m.ID, &m.PursuitID, &m.Title, &m.Body, &rawTags, &m.Weight, &m.SavedAt); err != nil {
			return nil, err
		}
		m.Tags = decodeTags(rawTags)
		out = append(out, m)
	}
	return out, rows.Err()
}

// ── Patterns ──────────────────────────────────────────────────────────────

// AddPattern logs a limiting pattern, an operating identity, or a deliberate
// correction.
func (s *Store) AddPattern(ctx context.Context, pursuitID, kind, body string, refs map[string]any) (Pattern, error) {
	if !IsValidPatternKind(kind) {
		return Pattern{}, fmt.Errorf("unknown pattern kind %q", kind)
	}
	body = clampText(body)
	if body == "" {
		return Pattern{}, errors.New("pattern body required")
	}
	st, err := s.EnsureState(ctx, pursuitID)
	if err != nil {
		return Pattern{}, err
	}
	if refs == nil {
		refs = map[string]any{}
	}
	refsJSON, _ := json.Marshal(refs)
	var p Pattern
	var rawRefs []byte
	if err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_pursuit_pc_patterns
			(pursuit_id, kind, body, refs, cycle_number, day_in_cycle)
		VALUES ($1::uuid, $2, $3, $4::jsonb, $5, $6)
		RETURNING id::text, pursuit_id::text, kind, body, refs, cycle_number,
		          day_in_cycle, created_at
	`, pursuitID, kind, body, string(refsJSON), st.CycleNumber, st.CurrentDay).Scan(
		&p.ID, &p.PursuitID, &p.Kind, &p.Body, &rawRefs, &p.CycleNumber,
		&p.DayInCycle, &p.CreatedAt,
	); err != nil {
		return Pattern{}, fmt.Errorf("insert pattern: %w", err)
	}
	p.Refs = map[string]any{}
	if len(rawRefs) > 0 {
		_ = json.Unmarshal(rawRefs, &p.Refs)
	}
	return p, nil
}

func (s *Store) listPatterns(ctx context.Context, pursuitID string, limit int) ([]Pattern, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, pursuit_id::text, kind, body, refs, cycle_number,
		       day_in_cycle, created_at
		FROM mem_pursuit_pc_patterns
		WHERE pursuit_id = $1::uuid
		ORDER BY created_at DESC
		LIMIT $2
	`, pursuitID, limit)
	if err != nil {
		return nil, fmt.Errorf("list patterns: %w", err)
	}
	defer rows.Close()
	out := []Pattern{}
	for rows.Next() {
		var p Pattern
		var rawRefs []byte
		if err := rows.Scan(&p.ID, &p.PursuitID, &p.Kind, &p.Body, &rawRefs,
			&p.CycleNumber, &p.DayInCycle, &p.CreatedAt); err != nil {
			return nil, err
		}
		p.Refs = map[string]any{}
		if len(rawRefs) > 0 {
			_ = json.Unmarshal(rawRefs, &p.Refs)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ── Reviews ───────────────────────────────────────────────────────────────

// CompleteReview closes the current cycle and opens the next one. Blank
// next_* fields mean "keep the current framing" - the review is a deliberate
// decision point, not a forced rewrite. The whole rollover is one transaction
// so a cycle can never be half-advanced.
func (s *Store) CompleteReview(ctx context.Context, pursuitID, wins, misses, nextIdentity, nextObjective, nextPattern string, adjustments map[string]any) (Review, error) {
	st, err := s.EnsureState(ctx, pursuitID)
	if err != nil {
		return Review{}, err
	}
	if adjustments == nil {
		adjustments = map[string]any{}
	}
	adjJSON, _ := json.Marshal(adjustments)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return Review{}, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var rv Review
	var rawAdj []byte
	if err := tx.QueryRow(ctx, `
		INSERT INTO mem_pursuit_pc_reviews
			(pursuit_id, cycle_number, wins, misses, next_identity,
			 next_objective, next_pattern, adjustments)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, $8::jsonb)
		ON CONFLICT (pursuit_id, cycle_number) DO UPDATE SET
			wins = EXCLUDED.wins, misses = EXCLUDED.misses,
			next_identity = EXCLUDED.next_identity,
			next_objective = EXCLUDED.next_objective,
			next_pattern = EXCLUDED.next_pattern,
			adjustments = EXCLUDED.adjustments,
			completed_at = NOW()
		RETURNING id::text, pursuit_id::text, cycle_number, wins, misses,
		          next_identity, next_objective, next_pattern, adjustments,
		          completed_at, created_at
	`, pursuitID, st.CycleNumber, clampText(wins), clampText(misses),
		clampText(nextIdentity), clampText(nextObjective), clampText(nextPattern),
		string(adjJSON)).Scan(
		&rv.ID, &rv.PursuitID, &rv.CycleNumber, &rv.Wins, &rv.Misses,
		&rv.NextIdentity, &rv.NextObjective, &rv.NextPattern, &rawAdj,
		&rv.CompletedAt, &rv.CreatedAt,
	); err != nil {
		return Review{}, fmt.Errorf("insert review: %w", err)
	}
	rv.Adjustments = map[string]any{}
	if len(rawAdj) > 0 {
		_ = json.Unmarshal(rawAdj, &rv.Adjustments)
	}

	// Roll the cycle forward. The phase clocks reset so the new cycle opens on
	// a clean morning rather than inheriting yesterday's stamps.
	if _, err := tx.Exec(ctx, `
		UPDATE mem_pursuit_pc_state
		   SET cycle_number             = cycle_number + 1,
		       current_day              = 1,
		       cycle_started_at         = NOW(),
		       missed_days_count        = 0,
		       current_identity         = COALESCE(NULLIF($2, ''), current_identity),
		       current_objective        = COALESCE(NULLIF($3, ''), current_objective),
		       current_limiting_pattern = COALESCE(NULLIF($4, ''), current_limiting_pattern),
		       last_morning_at          = NULL,
		       last_midday_at           = NULL,
		       last_evening_at          = NULL,
		       updated_at               = NOW()
		 WHERE pursuit_id = $1::uuid
	`, pursuitID, clampText(nextIdentity), clampText(nextObjective), clampText(nextPattern)); err != nil {
		return Review{}, fmt.Errorf("advance cycle: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Review{}, fmt.Errorf("commit review: %w", err)
	}
	return rv, nil
}

func (s *Store) listReviews(ctx context.Context, pursuitID string, limit int) ([]Review, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, pursuit_id::text, cycle_number, wins, misses,
		       next_identity, next_objective, next_pattern, adjustments,
		       completed_at, created_at
		FROM mem_pursuit_pc_reviews
		WHERE pursuit_id = $1::uuid
		ORDER BY cycle_number DESC
		LIMIT $2
	`, pursuitID, limit)
	if err != nil {
		return nil, fmt.Errorf("list reviews: %w", err)
	}
	defer rows.Close()
	out := []Review{}
	for rows.Next() {
		var rv Review
		var rawAdj []byte
		if err := rows.Scan(&rv.ID, &rv.PursuitID, &rv.CycleNumber, &rv.Wins, &rv.Misses,
			&rv.NextIdentity, &rv.NextObjective, &rv.NextPattern, &rawAdj,
			&rv.CompletedAt, &rv.CreatedAt); err != nil {
			return nil, err
		}
		rv.Adjustments = map[string]any{}
		if len(rawAdj) > 0 {
			_ = json.Unmarshal(rawAdj, &rv.Adjustments)
		}
		out = append(out, rv)
	}
	return out, rows.Err()
}

// ── Cockpit ───────────────────────────────────────────────────────────────

// Cockpit is the single composed read the cockpit UI and the seeded chat both
// render from. One call returns the pursuit, the refreshed state, today's and
// recent proofs/evidence, the memory bank, patterns, session history, reviews,
// and the coach's next-step guidance.
func (s *Store) Cockpit(ctx context.Context, pursuitID string, now time.Time) (Cockpit, error) {
	var c Cockpit
	header, err := s.Header(ctx, pursuitID)
	if err != nil {
		return c, err
	}
	state, err := s.EnsureState(ctx, pursuitID)
	if err != nil {
		return c, err
	}
	state, err = s.refreshState(ctx, state, now)
	if err != nil {
		return c, err
	}

	proofs, err := s.listProofs(ctx, pursuitID, 60)
	if err != nil {
		return c, err
	}
	evidence, err := s.listEvidence(ctx, pursuitID, 80)
	if err != nil {
		return c, err
	}
	sessions, err := s.listSessions(ctx, pursuitID, 40)
	if err != nil {
		return c, err
	}
	memories, err := s.listMemories(ctx, pursuitID, 40)
	if err != nil {
		return c, err
	}
	patterns, err := s.listPatterns(ctx, pursuitID, 40)
	if err != nil {
		return c, err
	}
	reviews, err := s.listReviews(ctx, pursuitID, 12)
	if err != nil {
		return c, err
	}

	loc := loadLocation(state.Timezone)
	today := timeToLocalDate(now, loc)

	c.Pursuit = header
	c.State = state
	c.TodayProofs = []Proof{}
	c.RecentProofs = proofs
	c.TodayEvidence = []Evidence{}
	c.RecentEvidence = evidence
	c.Memories = memories
	c.RecentSessions = sessions
	c.CycleReviews = reviews
	c.Patterns = []Pattern{}
	c.Corrections = []Pattern{}

	for _, p := range proofs {
		if isSameLocalDay(p.PlannedAt, today, loc) {
			c.TodayProofs = append(c.TodayProofs, p)
		}
	}
	todayEvidenceCount, todayResistanceCount := 0, 0
	for _, e := range evidence {
		if !isSameLocalDay(e.CapturedAt, today, loc) {
			continue
		}
		c.TodayEvidence = append(c.TodayEvidence, e)
		if e.Kind == EvidenceResistance {
			todayResistanceCount++
		} else {
			todayEvidenceCount++
		}
	}
	for _, p := range patterns {
		if p.Kind == PatternCorrection {
			c.Corrections = append(c.Corrections, p)
			continue
		}
		c.Patterns = append(c.Patterns, p)
	}

	c.RehearsalMemory = PickRehearsalMemory(memories, state.CurrentDay)

	snap := NewSnapshot(header, state, now, proofs, evidence, sessions)
	c.Guidance = NextGuidance(snap)
	if adj := AdaptForResistance(snap, todayEvidenceCount, todayResistanceCount); adj.Phase != "" {
		c.Adjustment = &adj
	}
	return c, nil
}
