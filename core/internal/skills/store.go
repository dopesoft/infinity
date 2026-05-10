package skills

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists skill metadata, version history, and run logs. The Registry
// can run without a Store — Store is optional and used only when DATABASE_URL
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

	_, err = tx.Exec(ctx, `
		INSERT INTO mem_skills
		  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
		   confidence, status, source, last_evolved, updated_at)
		VALUES ($1,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb,$7::jsonb,$8,$9,$10,
		        NULLIF($11,'')::timestamptz, NOW())
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
		  updated_at = NOW()
	`, sk.Name, sk.Description, string(sk.RiskLevel), egress, triggers, inputs, outputs,
		sk.Confidence, string(sk.Status), string(sk.Source), sk.LastEvolved)
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
	return tx.Commit(ctx)
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

// ListSummaries returns a denormalised list view: skill row + last_run_at +
// success_rate over the last 50 runs.
func (s *Store) ListSummaries(ctx context.Context) ([]SkillSummary, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT
		  s.name, COALESCE(a.active_version, ''), s.description, s.risk_level, s.confidence,
		  s.source, s.status, s.network_egress::text,
		  (SELECT MAX(started_at) FROM mem_skill_runs r WHERE r.skill_name = s.name) AS last_run,
		  COALESCE((
		     SELECT AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END)
		       FROM (SELECT success FROM mem_skill_runs r2
		              WHERE r2.skill_name = s.name
		              ORDER BY started_at DESC LIMIT 50) sub
		  ), 0)::float8 AS success_rate
		FROM mem_skills s
		LEFT JOIN mem_skill_active a ON a.skill_name = s.name
		ORDER BY s.name
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
			&x.Source, &x.Status, &egressJSON, &x.LastRunAt, &x.SuccessRate); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(egressJSON), &x.NetworkEgress)
		out = append(out, x)
	}
	return out, rows.Err()
}
