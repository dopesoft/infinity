package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists skill metadata, version history, and run logs. The Registry
// can run without a Store - Store is optional and used only when DATABASE_URL
// is configured.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// UpsertSkill writes the skill row + appends a version record if this version
// is new. Active-version pointer is set when no row exists yet.
func (s *Store) UpsertSkill(ctx context.Context, sk *Skill) error {
	if s == nil || s.pool == nil || sk == nil {
		return nil
	}
	egress, _ := json.Marshal(sk.NetworkEgress)
	triggers, _ := json.Marshal(sk.TriggerPhrases)
	inputs, _ := json.Marshal(sk.Inputs)
	outputs, _ := json.Marshal(sk.Outputs)

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	importance := normalizeImportance(sk.Importance)
	reason := sk.ImportanceReason
	if sk.Importance <= 0 {
		importance = inferImportance(sk.Name, sk.Description, "", sk.Body)
		reason = inferImportanceReason(sk.Name, sk.Description, "", sk.Body)
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO mem_skills
		  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
		   confidence, status, source, last_evolved, importance, importance_reason, updated_at)
		VALUES ($1,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb,$7::jsonb,$8,$9,$10,
		        NULLIF($11,'')::timestamptz, $12, $13, NOW())
		ON CONFLICT (name) DO UPDATE SET
		  description = EXCLUDED.description,
		  risk_level  = EXCLUDED.risk_level,
		  network_egress = EXCLUDED.network_egress,
		  trigger_phrases = EXCLUDED.trigger_phrases,
		  inputs = EXCLUDED.inputs,
		  outputs = EXCLUDED.outputs,
		  confidence = EXCLUDED.confidence,
		  source = EXCLUDED.source,
		  last_evolved = EXCLUDED.last_evolved,
		  importance = EXCLUDED.importance,
		  importance_reason = EXCLUDED.importance_reason,
		  updated_at = NOW()
	`, sk.Name, sk.Description, string(sk.RiskLevel), egress, triggers, inputs, outputs,
		sk.Confidence, string(sk.Status), string(sk.Source), sk.LastEvolved, importance, reason)
	if err != nil {
		return fmt.Errorf("upsert mem_skills: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO mem_skill_versions
		  (skill_name, version, skill_md, implementation, confidence, source, parent_version)
		VALUES ($1, $2, $3, $4, $5, $6, NULL)
		ON CONFLICT (skill_name, version) DO UPDATE SET
		  skill_md = EXCLUDED.skill_md,
		  implementation = EXCLUDED.implementation
	`, sk.Name, sk.Version, sk.Body, sk.ImplPath, sk.Confidence, string(sk.Source))
	if err != nil {
		return fmt.Errorf("upsert mem_skill_versions: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO mem_skill_active (skill_name, active_version)
		VALUES ($1, $2)
		ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version, updated_at = NOW()
		WHERE NOT mem_skill_active.pinned
	`, sk.Name, sk.Version)
	if err != nil {
		return fmt.Errorf("upsert mem_skill_active: %w", err)
	}

	// When a skill becomes active, retire any pending "create this skill"
	// candidates of the same name so the Candidate panel self-cleans — this is
	// the only thing that reconciles mem_skill_proposals against an activation
	// that didn't go through the Promote button (chat install via Registry.Put,
	// boot-time Reload of an already-shipped skill, etc). Patch/improvement
	// candidates (parent_skill set: GEPA frontier, merged drafts, optimize)
	// target an already-active skill and stay pending by design.
	if sk.Status == StatusActive {
		if _, err = tx.Exec(ctx, `
			UPDATE mem_skill_proposals
			   SET status = 'superseded', decided_at = NOW()
			 WHERE name = $1 AND status = 'candidate' AND parent_skill IS NULL
		`, sk.Name); err != nil {
			return fmt.Errorf("supersede candidate proposals for %q: %w", sk.Name, err)
		}
	}
	return tx.Commit(ctx)
}

// InsertProposal drops a candidate skill into mem_skill_proposals for the
// boss to review and promote in the Skills tab. Used by skill_create when
// a skill is too risky to auto-activate (anything above risk_level=low, or
// anything carrying an executable implementation). Returns the proposal id.
func (s *Store) InsertProposal(ctx context.Context, name, description, reasoning, skillMD, riskLevel string) (string, error) {
	return s.InsertProposalWithImportance(ctx, name, description, reasoning, skillMD, riskLevel,
		inferImportance(name, description, reasoning, skillMD),
		inferImportanceReason(name, description, reasoning, skillMD))
}

func (s *Store) InsertProposalWithImportance(ctx context.Context, name, description, reasoning, skillMD, riskLevel string, importance int, importanceReason string) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("skills: no store configured")
	}
	if riskLevel == "" {
		riskLevel = "low"
	}
	id := uuid.NewString()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
			(id, name, description, reasoning, skill_md, risk_level, importance, importance_reason, status, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, 'candidate', NOW())
	`, id, name, description, reasoning, skillMD, riskLevel, normalizeImportance(importance), importanceReason)
	if err != nil {
		return "", fmt.Errorf("skills: insert proposal: %w", err)
	}
	return id, nil
}

// RecordRun persists a single skill execution. Returns the assigned UUID.
func (s *Store) RecordRun(ctx context.Context, run *Run) (string, error) {
	if s == nil || s.pool == nil {
		return "", nil
	}
	if run.ID == "" {
		run.ID = uuid.NewString()
	}
	input, _ := json.Marshal(run.Input)

	var sessionID *string
	if run.SessionID != "" {
		if _, err := uuid.Parse(run.SessionID); err == nil {
			sessionID = &run.SessionID
		}
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_skill_runs
		  (id, skill_name, version, session_id, trigger_source, input, output,
		   success, duration_ms, started_at, ended_at)
		VALUES ($1,$2,NULLIF($3,''),$4::uuid,$5,$6::jsonb,$7,$8,$9,$10,$11)
	`, run.ID, run.SkillName, run.Version, sessionID, run.TriggerSource, input, run.Output,
		run.Success, run.DurationMS, run.StartedAt, run.EndedAt)
	if err != nil {
		return "", err
	}
	return run.ID, nil
}

// RecentRuns returns the last `limit` runs for a skill, newest first.
func (s *Store) RecentRuns(ctx context.Context, name string, limit int) ([]Run, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 25
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, skill_name, COALESCE(version,''), COALESCE(session_id::text,''),
		       trigger_source, input, output, success, duration_ms, started_at, ended_at
		  FROM mem_skill_runs
		 WHERE skill_name = $1
		 ORDER BY started_at DESC
		 LIMIT $2
	`, name, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var r Run
		var raw []byte
		if err := rows.Scan(&r.ID, &r.SkillName, &r.Version, &r.SessionID,
			&r.TriggerSource, &raw, &r.Output, &r.Success, &r.DurationMS,
			&r.StartedAt, &r.EndedAt); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			_ = json.Unmarshal(raw, &r.Input)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SuccessRate returns success_count/total over the last N runs. Used by the
// list view's "success rate" column.
func (s *Store) SuccessRate(ctx context.Context, name string, lastN int) (rate float64, total int, lastRun any, err error) {
	if s == nil || s.pool == nil {
		return 0, 0, nil, nil
	}
	if lastN <= 0 {
		lastN = 50
	}
	row := s.pool.QueryRow(ctx, `
		WITH recent AS (
			SELECT success, started_at FROM mem_skill_runs
			 WHERE skill_name = $1
			 ORDER BY started_at DESC LIMIT $2
		)
		SELECT
		  COALESCE(AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END), 0)::float8,
		  COUNT(*)::int,
		  MAX(started_at)
		FROM recent
	`, name, lastN)
	var last any
	if err := row.Scan(&rate, &total, &last); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return 0, 0, nil, nil
		}
		return 0, 0, nil, err
	}
	return rate, total, last, nil
}

func (s *Store) UpsertTest(ctx context.Context, skillName, description string, inputs map[string]any, expected, source string) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("skills: no store configured")
	}
	if source == "" {
		source = "synthetic"
	}
	raw, _ := json.Marshal(inputs)
	id := uuid.NewString()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_skill_tests (id, skill_name, description, inputs, expected, source)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6)
	`, id, skillName, description, string(raw), expected, source)
	if err != nil {
		return "", fmt.Errorf("skills: insert test: %w", err)
	}
	return id, nil
}

func (s *Store) ListTests(ctx context.Context, skillName string) ([]TestCase, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, skill_name, description, inputs, expected,
		       last_run_at, last_passed, source, created_at
		  FROM mem_skill_tests
		 WHERE skill_name = $1
		 ORDER BY created_at DESC
	`, skillName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TestCase
	for rows.Next() {
		var tc TestCase
		var raw []byte
		if err := rows.Scan(&tc.ID, &tc.SkillName, &tc.Description, &raw, &tc.Expected,
			&tc.LastRunAt, &tc.LastPassed, &tc.Source, &tc.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(raw, &tc.Inputs)
		out = append(out, tc)
	}
	return out, rows.Err()
}

func (s *Store) GenerateSyntheticTests(ctx context.Context, sk *Skill) ([]TestCase, error) {
	if sk == nil {
		return nil, errors.New("skills: skill required")
	}
	inputs := map[string]any{}
	for _, in := range sk.Inputs {
		if in.Default != nil {
			inputs[in.Name] = in.Default
			continue
		}
		switch in.Type {
		case "int", "integer":
			inputs[in.Name] = 1
		case "float", "number":
			inputs[in.Name] = 1.0
		case "bool", "boolean":
			inputs[in.Name] = true
		case "object":
			inputs[in.Name] = map[string]any{}
		default:
			inputs[in.Name] = "example"
		}
	}
	expected := "The skill should complete without errors and produce output aligned with its description: " + sk.Description
	desc := "Synthetic smoke test for " + sk.Name
	id, err := s.UpsertTest(ctx, sk.Name, desc, inputs, expected, "synthetic")
	if err != nil {
		return nil, err
	}
	return []TestCase{{
		ID: id, SkillName: sk.Name, Description: desc, Inputs: inputs,
		Expected: expected, Source: "synthetic", CreatedAt: time.Now().UTC(),
	}}, nil
}

// ListSummaries returns a denormalised list view: skill row + last_run_at +
// success_rate over the last 50 runs.
func (s *Store) ListSummaries(ctx context.Context) ([]SkillSummary, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  s.name, COALESCE(a.active_version, ''), s.description, s.risk_level, s.confidence,
		  s.source, s.status, s.network_egress::text, COALESCE(s.importance, 50), COALESCE(s.importance_reason, ''),
		  (SELECT MAX(started_at) FROM mem_skill_runs r WHERE r.skill_name = s.name) AS last_run,
		  COALESCE((
		     SELECT AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END)
		       FROM (SELECT success FROM mem_skill_runs r2
		              WHERE r2.skill_name = s.name
		              ORDER BY started_at DESC LIMIT 50) sub
		  ), 0)::float8 AS success_rate
		FROM mem_skills s
		LEFT JOIN mem_skill_active a ON a.skill_name = s.name
		ORDER BY COALESCE(s.importance, 50) DESC, s.name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SkillSummary
	for rows.Next() {
		var x SkillSummary
		var egressJSON string
		if err := rows.Scan(&x.Name, &x.Version, &x.Description, &x.RiskLevel, &x.Confidence,
			&x.Source, &x.Status, &egressJSON, &x.Importance, &x.ImportanceReason, &x.LastRunAt, &x.SuccessRate); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(egressJSON), &x.NetworkEgress)
		out = append(out, x)
	}
	return out, rows.Err()
}

func normalizeImportance(v int) int {
	if v <= 0 {
		return 50
	}
	if v > 100 {
		return 100
	}
	return v
}

func inferImportance(name, description, reasoning, body string) int {
	hay := strings.ToLower(name + " " + description + " " + reasoning + " " + body)
	switch {
	case containsAny(hay, "self-improve", "autoskill", "repair", "evolve-skill", "propose-skill", "memory-ground", "procedural", "reflection", "curiosity", "surprise", "world model", "connector identit"):
		return 95
	case containsAny(hay, "triage", "follow up", "dashboard", "surface", "inbox", "gmail", "schedule", "deadline", "open loop"):
		return 85
	case containsAny(hay, "scaffold", "build app", "vite", "nextjs", "swift", "capacitor"):
		return 65
	default:
		return 60
	}
}

func inferImportanceReason(name, description, reasoning, body string) string {
	hay := strings.ToLower(name + " " + description + " " + reasoning + " " + body)
	switch {
	case containsAny(hay, "self-improve", "autoskill", "repair", "evolve-skill", "propose-skill"):
		return "Core self-improvement loop: turns failures, drift, or repeated patterns into better skills."
	case containsAny(hay, "memory-ground", "procedural", "reflection", "curiosity", "surprise", "world model"):
		return "Core cognition loop: improves memory, reflection, or learning behavior across turns."
	case containsAny(hay, "connector identit", "account identit"):
		return "Core tool reliability loop: keeps connected accounts understandable and routable."
	case containsAny(hay, "triage", "follow up", "dashboard", "surface", "inbox", "gmail", "schedule", "deadline", "open loop"):
		return "Executive-assistant automation: notices and surfaces work without waiting for a prompt."
	case containsAny(hay, "scaffold", "build app", "vite", "nextjs", "swift", "capacitor"):
		return "Useful coding/build capability, but not part of the agent cognition core."
	default:
		return "General reusable skill."
	}
}

func containsAny(s string, needles ...string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

// LoadDetail fetches the persistent fields that complement the in-memory
// Skill: created_at / updated_at from mem_skills, plus the most recent
// successful (or any if none successful) run. Used by GET /api/skills/:name
// to populate the Studio detail surface's Overview tab.
func (s *Store) LoadDetail(ctx context.Context, name string) (*SkillDetailDTO, error) {
	if s == nil || s.pool == nil {
		return &SkillDetailDTO{}, nil
	}
	out := &SkillDetailDTO{}
	var (
		created, updated *time.Time
	)
	err := s.pool.QueryRow(ctx, `
		SELECT created_at, updated_at
		  FROM mem_skills
		 WHERE name = $1
	`, name).Scan(&created, &updated)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return nil, err
	}
	out.CreatedAt = created
	out.UpdatedAt = updated

	// Pull the most recent run as the "last run sample" for Overview 2b.
	// Preference: most-recent successful; fall back to most-recent any.
	runs, err := s.RecentRuns(ctx, name, 1)
	if err == nil && len(runs) > 0 {
		out.LastRun = &runs[0]
	}

	// Total count over all time so the Overview header can say "47 runs".
	var total int
	if err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM mem_skill_runs WHERE skill_name = $1`,
		name,
	).Scan(&total); err == nil {
		out.TotalRuns = total
	}
	return out, nil
}

// ListVersions returns every row in mem_skill_versions for `name`, newest
// first, with `Active` flagged on the row matching mem_skill_active.
func (s *Store) ListVersions(ctx context.Context, name string) ([]VersionEntry, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	var activeVersion string
	_ = s.pool.QueryRow(ctx,
		`SELECT COALESCE(active_version,'') FROM mem_skill_active WHERE skill_name = $1`,
		name,
	).Scan(&activeVersion)

	rows, err := s.pool.Query(ctx, `
		SELECT version, COALESCE(skill_md, ''), created_at,
		       COALESCE(source, ''), COALESCE(confidence, 0.5)
		  FROM mem_skill_versions
		 WHERE skill_name = $1
		   AND archived_at IS NULL
		 ORDER BY created_at DESC
	`, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []VersionEntry
	for rows.Next() {
		var v VersionEntry
		if err := rows.Scan(&v.Version, &v.SkillMD, &v.CreatedAt, &v.Source, &v.Confidence); err != nil {
			return nil, err
		}
		v.Active = (v.Version == activeVersion)
		out = append(out, v)
	}
	return out, rows.Err()
}

// PromoteVersion flips mem_skill_active.active_version to the requested
// version (must exist in mem_skill_versions). Returns an error if the
// version row doesn't exist - safer than silently activating a missing
// row that would break the loader on next refresh.
func (s *Store) PromoteVersion(ctx context.Context, name, version string) error {
	if s == nil || s.pool == nil {
		return fmt.Errorf("no database")
	}
	var exists bool
	if err := s.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM mem_skill_versions WHERE skill_name = $1 AND version = $2)`,
		name, version,
	).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("version %q not found for skill %q", version, name)
	}
	// An active version must never be archived (archived_at hides it from the
	// version history; the active row is the one thing that should always show).
	// Clearing it here keeps the invariant true even when promoting a version
	// that was previously soft-retired.
	_, _ = s.pool.Exec(ctx,
		`UPDATE mem_skill_versions SET archived_at = NULL WHERE skill_name = $1 AND version = $2`,
		name, version,
	)
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_skill_active (skill_name, active_version)
		VALUES ($1, $2)
		ON CONFLICT (skill_name) DO UPDATE
		  SET active_version = EXCLUDED.active_version,
		      updated_at     = NOW()
	`, name, version)
	return err
}
