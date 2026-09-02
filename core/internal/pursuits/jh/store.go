package jh

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

// Store is the persistence layer for the Job Hunt experience.
//
// Rule #1b split: everything here is a MECHANIC. Which stage a role sits in,
// that a repeat sweep updates rather than duplicates, and that moving a role
// stamps the clock are guaranteed by this code, so a nightly sweep behaves
// identically whether or not the LLM remembers the recipe. The judgment (is
// this role worth applying to, what does the outreach say) lives in the skill
// and in the boss's own decisions.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore builds a Store over the shared pgx pool.
func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// ErrNotJobHunt is returned when a caller points a jh endpoint at a pursuit
// that is not running this experience. Ordinary pursuits, and pursuits running
// a different experience, must never be mutated through this package.
var ErrNotJobHunt = errors.New("pursuit is not a job_hunt experience")

// ErrNoPursuit is returned when the pursuit id does not exist.
var ErrNoPursuit = errors.New("pursuit not found")

// maxTextLen caps any single free-text field written here. Generous enough for
// a full job posting's notes, small enough that a runaway scraper cannot bloat
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

// ---- Constrained values ---------------------------------------------------
//
// These mirror the CHECK constraints in 206_jobhunt_roles.sql exactly. They are
// validated in Go BEFORE the write so a bad value reads as a named error the
// caller can act on, rather than as a raw constraint violation surfacing from
// the driver several layers away from whoever chose the value.

// Pipeline stages, in the order a role moves through them. The order is
// load-bearing: it is what Roles orders by, so the caller can group into a
// kanban without knowing the sequence itself.
const (
	StageDiscovered   = "discovered"
	StageReviewed     = "reviewed"
	StageTailoring    = "tailoring"
	StageApplied      = "applied"
	StageOutreached   = "outreached"
	StageResponded    = "responded"
	StageInterviewing = "interviewing"
	StageOffer        = "offer"
	StageDead         = "dead"
)

// RoleStages enumerates every accepted mem_jobhunt_roles.stage value, in
// pipeline order.
func RoleStages() []string {
	return []string{
		StageDiscovered, StageReviewed, StageTailoring, StageApplied,
		StageOutreached, StageResponded, StageInterviewing, StageOffer, StageDead,
	}
}

// IsValidRoleStage guards mem_jobhunt_roles.stage writes. Case-sensitive on
// purpose so a typo is a caller error rather than silently coerced.
func IsValidRoleStage(stage string) bool {
	for _, v := range RoleStages() {
		if v == stage {
			return true
		}
	}
	return false
}

// Where a posting was found.
const (
	SourceLinkedIn   = "linkedin"
	SourceBuiltIn    = "builtin"
	SourceGoogleJobs = "google_jobs"
	SourceWellfound  = "wellfound"
	SourceYC         = "yc"
)

// RoleSources enumerates every accepted mem_jobhunt_roles.source value.
func RoleSources() []string {
	return []string{SourceLinkedIn, SourceBuiltIn, SourceGoogleJobs, SourceWellfound, SourceYC}
}

// IsValidRoleSource guards mem_jobhunt_roles.source writes.
func IsValidRoleSource(source string) bool {
	for _, v := range RoleSources() {
		if v == source {
			return true
		}
	}
	return false
}

// ---- DTOs -----------------------------------------------------------------

// PursuitHeader is the minimum mem_pursuits row this package needs to reason
// about a pursuit: id, title, cadence, experience, config, created_at.
type PursuitHeader struct {
	ID         string         `json:"id"`
	Title      string         `json:"title"`
	Cadence    string         `json:"cadence"`
	Experience string         `json:"experience"`
	Config     map[string]any `json:"config"`
	CreatedAt  time.Time      `json:"created_at"`
}

// Role is one row of the pipeline.
//
// Nullable text reads back as the empty string and ghost_flags as an empty
// slice, never null, so a client can render without null-guards. The genuinely
// absent numerics and timestamps stay pointers, because "no compensation
// stated" and "a band of zero" are different facts and flattening them would
// put a wrong number on a card.
type Role struct {
	ID           string     `json:"id"`
	PursuitID    string     `json:"pursuit_id"`
	Company      string     `json:"company"`
	RoleTitle    string     `json:"role_title"`
	Source       string     `json:"source"`
	URL          string     `json:"url"`
	Location     string     `json:"location"`
	CompMin      *int       `json:"comp_min,omitempty"`
	CompMax      *int       `json:"comp_max,omitempty"`
	CompText     string     `json:"comp_text"`
	PostedAt     *time.Time `json:"posted_at,omitempty"`
	DiscoveredAt time.Time  `json:"discovered_at"`
	FitScore     *int       `json:"fit_score,omitempty"`
	FitReasoning string     `json:"fit_reasoning"`
	GhostScore   *int       `json:"ghost_score,omitempty"`
	GhostFlags   []string   `json:"ghost_flags"`
	Stage        string     `json:"stage"`
	StageChanged time.Time  `json:"stage_changed_at"`
	Notes        string     `json:"notes"`
	ExternalID   string     `json:"external_id"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// RoleInput is the upsert payload. A struct rather than a long positional
// parameter list because most fields are optional and a sweep fills whatever
// the posting happened to state: at fifteen arguments, a caller silently
// transposing two of them is a matter of time.
type RoleInput struct {
	Company      string
	RoleTitle    string
	Source       string
	URL          string
	Location     string
	CompMin      *int
	CompMax      *int
	CompText     string
	PostedAt     *time.Time
	FitScore     *int
	FitReasoning string
	GhostScore   *int
	GhostFlags   []string
	Notes        string
	// ExternalID is the source's own id for the posting. Empty for a role
	// entered by hand, which is what keeps hand-entered rows from colliding:
	// the unique constraint is on (source, external_id) and Postgres does not
	// treat NULLs as equal, so each one inserts fresh.
	ExternalID string
	// Stage sets the starting stage. Blank means 'discovered'. It is only ever
	// applied on INSERT: see UpsertRole for why a re-sweep must not move a role.
	Stage string
}

// ---- Header ---------------------------------------------------------------

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
	if h.Experience != ExperienceJobHunt {
		return h, ErrNotJobHunt
	}
	h.Config = map[string]any{}
	if len(configRaw) > 0 {
		_ = json.Unmarshal(configRaw, &h.Config)
	}
	return h, nil
}

// ---- Roles ----------------------------------------------------------------

// roleColumns is the SELECT list every role read shares, so a column added to
// one read cannot go missing from another. COALESCE on the text columns is
// what lets the DTO hold plain strings.
const roleColumns = `
	id::text, pursuit_id::text, company, role_title, source,
	COALESCE(url, ''), COALESCE(location, ''),
	comp_min, comp_max, COALESCE(comp_text, ''),
	posted_at, discovered_at,
	fit_score, COALESCE(fit_reasoning, ''),
	ghost_score, ghost_flags,
	stage, stage_changed_at,
	COALESCE(notes, ''), COALESCE(external_id, ''),
	created_at, updated_at`

// scanRole reads one row in roleColumns order.
func scanRole(row pgx.Row) (Role, error) {
	var r Role
	var flagsRaw []byte
	if err := row.Scan(
		&r.ID, &r.PursuitID, &r.Company, &r.RoleTitle, &r.Source,
		&r.URL, &r.Location,
		&r.CompMin, &r.CompMax, &r.CompText,
		&r.PostedAt, &r.DiscoveredAt,
		&r.FitScore, &r.FitReasoning,
		&r.GhostScore, &flagsRaw,
		&r.Stage, &r.StageChanged,
		&r.Notes, &r.ExternalID,
		&r.CreatedAt, &r.UpdatedAt,
	); err != nil {
		return Role{}, err
	}
	r.GhostFlags = decodeFlags(flagsRaw)
	return r, nil
}

// decodeFlags turns the ghost_flags JSONB array into a slice that is never nil,
// so a caller can range over it without a guard.
func decodeFlags(raw []byte) []string {
	flags := []string{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &flags)
	}
	if flags == nil {
		flags = []string{}
	}
	return flags
}

// Roles returns every role on the pursuit, ordered by pipeline stage and then
// by fit, so the caller can walk the slice once and cut it into kanban columns.
//
// The stage order comes from RoleStages() passed as a parameter rather than
// written into the SQL: one source of truth, so adding a stage cannot leave the
// ordering disagreeing with the validation.
func (s *Store) Roles(ctx context.Context, pursuitID string) ([]Role, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+roleColumns+`
		FROM mem_jobhunt_roles
		WHERE pursuit_id = $1::uuid
		ORDER BY array_position($2::text[], stage),
		         fit_score DESC NULLS LAST,
		         discovered_at DESC
	`, pursuitID, RoleStages())
	if err != nil {
		return nil, fmt.Errorf("list roles: %w", err)
	}
	defer rows.Close()
	out := []Role{}
	for rows.Next() {
		r, err := scanRole(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertRole files a role, updating in place when the same posting is seen
// again. The conflict target is the (source, external_id) unique constraint, so
// running the nightly sweep twice leaves one row per posting rather than two.
//
// What the update deliberately does NOT touch is the pipeline position. A sweep
// re-reading a posting the boss has already applied to knows nothing about that
// application, and letting it write stage would drag the card back to
// 'discovered' and lose the date it moved. Stage is set on INSERT and changed
// only through SetRoleStage, which is the one place that decision is made.
//
// Every enrichment column is COALESCEd onto the existing value for the same
// reason at a smaller scale: a thin re-scrape that did not parse the salary
// must not erase the band a richer earlier pass already stored.
func (s *Store) UpsertRole(ctx context.Context, pursuitID string, in RoleInput) (Role, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Role{}, err
	}
	company := clampText(in.Company)
	if company == "" {
		return Role{}, errors.New("company required")
	}
	title := clampText(in.RoleTitle)
	if title == "" {
		return Role{}, errors.New("role_title required")
	}
	source := strings.TrimSpace(in.Source)
	if !IsValidRoleSource(source) {
		return Role{}, fmt.Errorf("unknown role source %q, expected one of: %s",
			in.Source, strings.Join(RoleSources(), ", "))
	}
	stage := strings.TrimSpace(in.Stage)
	if stage == "" {
		stage = StageDiscovered
	}
	if !IsValidRoleStage(stage) {
		return Role{}, fmt.Errorf("unknown role stage %q, expected one of: %s",
			in.Stage, strings.Join(RoleStages(), ", "))
	}
	if err := checkScore("fit_score", in.FitScore); err != nil {
		return Role{}, err
	}
	if err := checkScore("ghost_score", in.GhostScore); err != nil {
		return Role{}, err
	}

	flags, err := json.Marshal(nonNilFlags(in.GhostFlags))
	if err != nil {
		return Role{}, fmt.Errorf("encode ghost_flags: %w", err)
	}

	// The nullable text columns go to the database as NULL rather than '' when
	// blank, so "not stated" stays distinguishable from "stated as empty" and
	// the COALESCE in the update below has something to fall back on.
	r, err := scanRole(s.pool.QueryRow(ctx, `
		INSERT INTO mem_jobhunt_roles
			(pursuit_id, company, role_title, source, url, location,
			 comp_min, comp_max, comp_text, posted_at,
			 fit_score, fit_reasoning, ghost_score, ghost_flags,
			 stage, notes, external_id)
		VALUES ($1::uuid, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''),
		        $7, $8, NULLIF($9, ''), $10,
		        $11, NULLIF($12, ''), $13, $14::jsonb,
		        $15, NULLIF($16, ''), NULLIF($17, ''))
		ON CONFLICT (pursuit_id, source, external_id) DO UPDATE SET
			company       = EXCLUDED.company,
			role_title    = EXCLUDED.role_title,
			url           = COALESCE(EXCLUDED.url, mem_jobhunt_roles.url),
			location      = COALESCE(EXCLUDED.location, mem_jobhunt_roles.location),
			comp_min      = COALESCE(EXCLUDED.comp_min, mem_jobhunt_roles.comp_min),
			comp_max      = COALESCE(EXCLUDED.comp_max, mem_jobhunt_roles.comp_max),
			comp_text     = COALESCE(EXCLUDED.comp_text, mem_jobhunt_roles.comp_text),
			posted_at     = COALESCE(EXCLUDED.posted_at, mem_jobhunt_roles.posted_at),
			fit_score     = COALESCE(EXCLUDED.fit_score, mem_jobhunt_roles.fit_score),
			fit_reasoning = COALESCE(EXCLUDED.fit_reasoning, mem_jobhunt_roles.fit_reasoning),
			ghost_score   = COALESCE(EXCLUDED.ghost_score, mem_jobhunt_roles.ghost_score),
			ghost_flags   = CASE WHEN EXCLUDED.ghost_flags = '[]'::jsonb
			                     THEN mem_jobhunt_roles.ghost_flags
			                     ELSE EXCLUDED.ghost_flags END,
			notes         = COALESCE(EXCLUDED.notes, mem_jobhunt_roles.notes),
			updated_at    = NOW()
		RETURNING `+roleColumns+`
	`, pursuitID, company, title, source, clampText(in.URL), clampText(in.Location),
		in.CompMin, in.CompMax, clampText(in.CompText), in.PostedAt,
		in.FitScore, clampText(in.FitReasoning), in.GhostScore, flags,
		stage, clampText(in.Notes), strings.TrimSpace(in.ExternalID)))
	if err != nil {
		return Role{}, fmt.Errorf("upsert role: %w", err)
	}
	return r, nil
}

// PatchRole corrects a card already on the board, addressed by its own id
// rather than by the posting's identity.
//
// UpsertRole cannot do this job. It is keyed on the
// (pursuit_id, source, external_id) constraint, and Postgres does not treat
// NULLs as equal, so a row filed without an external_id can never be matched by
// a later write: supplying the real posting id produces a SECOND card rather
// than correcting the first. That is exactly the state the early sweeps left
// the board in, and it is also the shape of every hand correction - "this band
// is wrong", "here is the real link" - where the caller knows which card he
// means and nothing about how it was originally found.
//
// Only the columns actually supplied move. Blank text and nil numbers mean
// "leave this alone", never "clear it", so a caller fixing one field cannot
// silently blank the rest of the card.
//
// Stage is absent by design, for the same reason UpsertRole refuses it: where a
// card sits in the pipeline is a decision, and SetRoleStage is the one place it
// is made. Correcting a salary band must never move a card he has applied to.
//
// Scoped to the pursuit as well as the id, so the authorisation performed in
// Header is the authorisation that applies - an update keyed on the role id
// alone would let a caller holding one valid job_hunt pursuit edit another
// board's card.
func (s *Store) PatchRole(ctx context.Context, pursuitID, roleID string, in RoleInput) (Role, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Role{}, err
	}
	roleID = strings.TrimSpace(roleID)
	if roleID == "" {
		return Role{}, errors.New("role_id required")
	}
	if source := strings.TrimSpace(in.Source); source != "" && !IsValidRoleSource(source) {
		return Role{}, fmt.Errorf("unknown role source %q, expected one of: %s",
			in.Source, strings.Join(RoleSources(), ", "))
	}
	if err := checkScore("fit_score", in.FitScore); err != nil {
		return Role{}, err
	}
	if err := checkScore("ghost_score", in.GhostScore); err != nil {
		return Role{}, err
	}

	flags, err := json.Marshal(nonNilFlags(in.GhostFlags))
	if err != nil {
		return Role{}, fmt.Errorf("encode ghost_flags: %w", err)
	}

	r, err := scanRole(s.pool.QueryRow(ctx, `
		UPDATE mem_jobhunt_roles SET
			company       = COALESCE(NULLIF($3, ''),  company),
			role_title    = COALESCE(NULLIF($4, ''),  role_title),
			source        = COALESCE(NULLIF($5, ''),  source),
			url           = COALESCE(NULLIF($6, ''),  url),
			location      = COALESCE(NULLIF($7, ''),  location),
			comp_min      = COALESCE($8,              comp_min),
			comp_max      = COALESCE($9,              comp_max),
			comp_text     = COALESCE(NULLIF($10, ''), comp_text),
			posted_at     = COALESCE($11,             posted_at),
			fit_score     = COALESCE($12,             fit_score),
			fit_reasoning = COALESCE(NULLIF($13, ''), fit_reasoning),
			ghost_score   = COALESCE($14,             ghost_score),
			ghost_flags   = CASE WHEN $15::jsonb = '[]'::jsonb
			                     THEN ghost_flags ELSE $15::jsonb END,
			notes         = COALESCE(NULLIF($16, ''), notes),
			external_id   = COALESCE(NULLIF($17, ''), external_id),
			updated_at    = NOW()
		 WHERE id = $2::uuid AND pursuit_id = $1::uuid
		RETURNING `+roleColumns+`
	`, pursuitID, roleID,
		clampText(in.Company), clampText(in.RoleTitle), strings.TrimSpace(in.Source),
		clampText(in.URL), clampText(in.Location),
		in.CompMin, in.CompMax, clampText(in.CompText), in.PostedAt,
		in.FitScore, clampText(in.FitReasoning), in.GhostScore, flags,
		clampText(in.Notes), strings.TrimSpace(in.ExternalID)))
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, errors.New("role not found on this pursuit")
	}
	if err != nil {
		return Role{}, fmt.Errorf("patch role: %w", err)
	}
	return r, nil
}

// SetRoleStage moves a role between kanban columns and stamps when it moved, so
// the board can show how long a card has been sitting.
//
// Scoped to the pursuit on purpose. The Header check above authorises THIS
// pursuit, so an update keyed on the role id alone would inherit an
// authorisation it never performed: a caller could pass a valid job_hunt
// pursuit and a role id belonging to a different one, and move that other
// board's card. Matching both means the check that was made is the check that
// applies.
//
// stage_changed_at only moves when the stage actually changes. Re-filing a role
// into the column it is already in is a no-op, not a reason to reset the clock
// that says how long it has been stalled there.
func (s *Store) SetRoleStage(ctx context.Context, pursuitID, roleID, stage string) (Role, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return Role{}, err
	}
	stage = strings.TrimSpace(stage)
	if !IsValidRoleStage(stage) {
		return Role{}, fmt.Errorf("unknown role stage %q, expected one of: %s",
			stage, strings.Join(RoleStages(), ", "))
	}
	r, err := scanRole(s.pool.QueryRow(ctx, `
		UPDATE mem_jobhunt_roles
		   SET stage            = $3,
		       stage_changed_at = CASE WHEN stage IS DISTINCT FROM $3
		                               THEN NOW() ELSE stage_changed_at END,
		       updated_at       = NOW()
		 WHERE id = $2::uuid AND pursuit_id = $1::uuid
		RETURNING `+roleColumns+`
	`, pursuitID, roleID, stage))
	if errors.Is(err, pgx.ErrNoRows) {
		return Role{}, errors.New("role not found on this pursuit")
	}
	if err != nil {
		return Role{}, fmt.Errorf("update role stage: %w", err)
	}
	return r, nil
}

// checkScore rejects a fit or ghost score outside 0 to 100. The database has
// the same bound; catching it here names the field that was wrong.
func checkScore(field string, v *int) error {
	if v == nil {
		return nil
	}
	if *v < 0 || *v > 100 {
		return fmt.Errorf("%s must be between 0 and 100, got %d", field, *v)
	}
	return nil
}

// nonNilFlags keeps an absent ghost_flags out of the JSON encoder's null path:
// the column is NOT NULL, and the upsert reads '[]' as "the caller had nothing
// to say" rather than "clear what is there".
func nonNilFlags(flags []string) []string {
	if flags == nil {
		return []string{}
	}
	return flags
}

// ---- Corpus ---------------------------------------------------------------
//
// The banked interview material: one row per question and the answer the boss
// actually gave. A tailoring pass pulls from here rather than inventing a
// story, so the same mechanic split applies as everywhere else in this file —
// that an entry is filed under a real theme, that its source is one of the two
// the schema accepts, and that free text is clamped are guaranteed here. Which
// story answers a given question is judgment, and stays in the skill.

// Where a corpus entry came from. Mirrors the chk_jobhunt_corpus_source CHECK
// in 207_jobhunt_support.sql exactly.
const (
	// CorpusSourceInterview is material that came out of a sit-down interview
	// session with the coach.
	CorpusSourceInterview = "interview"
	// CorpusSourceAdhoc is material dropped in on its own, outside a session.
	CorpusSourceAdhoc = "adhoc"
)

// CorpusSources enumerates every accepted mem_jobhunt_corpus.source value.
func CorpusSources() []string {
	return []string{CorpusSourceInterview, CorpusSourceAdhoc}
}

// IsValidCorpusSource guards mem_jobhunt_corpus.source writes. Case-sensitive
// on purpose, same as IsValidRoleSource: a typo is a caller error rather than
// something silently coerced into the wrong bucket.
func IsValidCorpusSource(source string) bool {
	for _, v := range CorpusSources() {
		if v == source {
			return true
		}
	}
	return false
}

// CorpusEntry is one banked question-and-answer.
//
// Like Role, the nullable-shaped fields read back as empty rather than nil —
// tags as an empty slice, metrics as an empty map — so a client can render a
// card without null-guards. Nothing here is genuinely absent-versus-zero, so
// no pointers are needed.
type CorpusEntry struct {
	ID        string         `json:"id"`
	PursuitID string         `json:"pursuit_id"`
	Theme     string         `json:"theme"`
	Question  string         `json:"question"`
	Answer    string         `json:"answer"`
	Metrics   map[string]any `json:"metrics"`
	Tags      []string       `json:"tags"`
	Source    string         `json:"source"`
	CreatedAt time.Time      `json:"created_at"`
}

// CorpusInput is the write payload. A struct for the same reason RoleInput is
// one: most of it is optional and filled from whatever the session happened to
// produce, so positional arguments would be a transposition waiting to happen.
type CorpusInput struct {
	Theme    string
	Question string
	Answer   string
	Metrics  map[string]any
	Tags     []string
	// Source must be one of CorpusSources(). Blank defaults to adhoc, which is
	// the honest reading of an entry that arrived without a session around it.
	Source string
}

// corpusColumns is the SELECT list every corpus read shares, so a column added
// to one read cannot go missing from another.
const corpusColumns = `
	id::text, pursuit_id::text, theme, question, answer,
	metrics, tags, source, created_at`

// scanCorpusEntry reads one row in corpusColumns order.
func scanCorpusEntry(row pgx.Row) (CorpusEntry, error) {
	var e CorpusEntry
	var metricsRaw []byte
	var tags []string
	if err := row.Scan(
		&e.ID, &e.PursuitID, &e.Theme, &e.Question, &e.Answer,
		&metricsRaw, &tags, &e.Source, &e.CreatedAt,
	); err != nil {
		return CorpusEntry{}, err
	}
	e.Metrics = decodeMetrics(metricsRaw)
	e.Tags = nonNilFlags(tags)
	return e, nil
}

// decodeMetrics turns the metrics JSONB object into a map that is never nil,
// so a caller can index it without a guard.
func decodeMetrics(raw []byte) map[string]any {
	metrics := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &metrics)
	}
	if metrics == nil {
		metrics = map[string]any{}
	}
	return metrics
}

// CorpusEntries returns every banked entry on the pursuit, ordered by theme and
// then newest-first within the theme, so the caller can walk the slice once and
// cut it into theme groups without sorting or bucketing it itself.
//
// Theme leads the ordering because that is how the corpus is read: grouped by
// theme with counts. Unlike role stages there is no fixed sequence to impose —
// themes emerge from the boss's own material, which is why the schema leaves
// theme as free text — so alphabetical is the only stable order available, and
// a stable order is what keeps the groups from reshuffling between loads.
func (s *Store) CorpusEntries(ctx context.Context, pursuitID string) ([]CorpusEntry, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+corpusColumns+`
		FROM mem_jobhunt_corpus
		WHERE pursuit_id = $1::uuid
		ORDER BY theme, created_at DESC
	`, pursuitID)
	if err != nil {
		return nil, fmt.Errorf("list corpus entries: %w", err)
	}
	defer rows.Close()
	out := []CorpusEntry{}
	for rows.Next() {
		e, err := scanCorpusEntry(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// AddCorpusEntry banks one question-and-answer against the pursuit.
//
// This is a plain insert rather than an upsert, unlike UpsertRole: a posting
// seen twice is one posting, but the same question asked twice gets a different
// answer each time and both are worth keeping. The schema agrees — there is no
// unique constraint here to conflict on.
//
// Every constrained value is checked in Go before the write so a bad source
// reads as a named error naming the accepted values, rather than as a raw
// chk_jobhunt_corpus_source violation surfacing from the driver several layers
// away from whoever chose it.
func (s *Store) AddCorpusEntry(ctx context.Context, pursuitID string, in CorpusInput) (CorpusEntry, error) {
	if _, err := s.Header(ctx, pursuitID); err != nil {
		return CorpusEntry{}, err
	}
	// The three NOT NULL text columns carry the whole value of the row: an
	// entry missing its question or answer is not a thinner record, it is an
	// unusable one, so it is rejected here rather than banked.
	theme := clampText(in.Theme)
	if theme == "" {
		return CorpusEntry{}, errors.New("theme required")
	}
	question := clampText(in.Question)
	if question == "" {
		return CorpusEntry{}, errors.New("question required")
	}
	answer := clampText(in.Answer)
	if answer == "" {
		return CorpusEntry{}, errors.New("answer required")
	}
	source := strings.TrimSpace(in.Source)
	if source == "" {
		source = CorpusSourceAdhoc
	}
	if !IsValidCorpusSource(source) {
		return CorpusEntry{}, fmt.Errorf("unknown corpus source %q, expected one of: %s",
			in.Source, strings.Join(CorpusSources(), ", "))
	}

	metrics := in.Metrics
	if metrics == nil {
		// The column is NOT NULL DEFAULT '{}', so an absent map has to reach
		// the database as an empty object rather than as JSON null.
		metrics = map[string]any{}
	}
	metricsJSON, err := json.Marshal(metrics)
	if err != nil {
		return CorpusEntry{}, fmt.Errorf("encode metrics: %w", err)
	}

	e, err := scanCorpusEntry(s.pool.QueryRow(ctx, `
		INSERT INTO mem_jobhunt_corpus
			(pursuit_id, theme, question, answer, metrics, tags, source)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6::text[], $7)
		RETURNING `+corpusColumns+`
	`, pursuitID, theme, question, answer, metricsJSON, nonNilFlags(in.Tags), source))
	if err != nil {
		return CorpusEntry{}, fmt.Errorf("add corpus entry: %w", err)
	}
	return e, nil
}
