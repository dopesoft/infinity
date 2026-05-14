package plasticity

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Snapshot is the Gym overview: examples become datasets, datasets become
// distillation runs, runs create candidate adapters, evals gate promotion, and
// routes decide where an approved reflex policy is allowed to act.
type Snapshot struct {
	Summary  Summary   `json:"summary"`
	Examples []Example `json:"examples"`
	Datasets []Dataset `json:"datasets"`
	Runs     []Run     `json:"runs"`
	Adapters []Adapter `json:"adapters"`
	Evals    []Eval    `json:"evals"`
	Routes   []Route   `json:"routes"`
}

type Summary struct {
	Ready       bool       `json:"ready"`
	ReflexOn    bool       `json:"reflex_on"`
	Examples    int        `json:"examples"`
	Datasets    int        `json:"datasets"`
	Runs        int        `json:"runs"`
	Candidates  int        `json:"candidates"`
	Active      int        `json:"active"`
	Regressions int        `json:"regressions"`
	LastRunAt   *time.Time `json:"last_run_at,omitempty"`
}

type Example struct {
	ID           string         `json:"id"`
	SourceKind   string         `json:"source_kind"`
	SourceID     string         `json:"source_id,omitempty"`
	TaskKind     string         `json:"task_kind"`
	Label        string         `json:"label"`
	Score        float64        `json:"score"`
	PrivacyClass string         `json:"privacy_class"`
	Metadata     map[string]any `json:"metadata,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type Dataset struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	Status       string         `json:"status"`
	ExampleCount int            `json:"example_count"`
	ArtifactURI  string         `json:"artifact_uri,omitempty"`
	Checksum     string         `json:"checksum,omitempty"`
	Filters      map[string]any `json:"filters,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at"`
}

type Run struct {
	ID          string         `json:"id"`
	DatasetID   string         `json:"dataset_id,omitempty"`
	AdapterID   string         `json:"adapter_id,omitempty"`
	Status      string         `json:"status"`
	Trigger     string         `json:"trigger"`
	Reason      string         `json:"reason,omitempty"`
	BaseModel   string         `json:"base_model,omitempty"`
	Metrics     map[string]any `json:"metrics,omitempty"`
	Error       string         `json:"error,omitempty"`
	StartedAt   *time.Time     `json:"started_at,omitempty"`
	CompletedAt *time.Time     `json:"completed_at,omitempty"`
	CreatedAt   time.Time      `json:"created_at"`
}

type Adapter struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	BaseModel    string         `json:"base_model"`
	Status       string         `json:"status"`
	TaskScope    []string       `json:"task_scope"`
	Metrics      map[string]any `json:"metrics,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
	PromotedAt   *time.Time     `json:"promoted_at,omitempty"`
	RolledBackAt *time.Time     `json:"rolled_back_at,omitempty"`
}

type Eval struct {
	ID              string         `json:"id"`
	AdapterID       string         `json:"adapter_id,omitempty"`
	EvalName        string         `json:"eval_name"`
	BaselineScore   float64        `json:"baseline_score"`
	CandidateScore  float64        `json:"candidate_score"`
	RegressionCount int            `json:"regression_count"`
	Passed          bool           `json:"passed"`
	Metrics         map[string]any `json:"metrics,omitempty"`
	CreatedAt       time.Time      `json:"created_at"`
}

type Route struct {
	ID              string         `json:"id"`
	Route           string         `json:"route"`
	TaskKind        string         `json:"task_kind"`
	ActiveAdapterID string         `json:"active_adapter_id,omitempty"`
	Status          string         `json:"status"`
	Confidence      float64        `json:"confidence"`
	MinScore        float64        `json:"min_score"`
	Metadata        map[string]any `json:"metadata,omitempty"`
	UpdatedAt       time.Time      `json:"updated_at"`
}

type Store struct {
	pool *pgxpool.Pool
}

type ExtractResult struct {
	Inserted int `json:"inserted"`
	Evals    int `json:"evals"`
	Lessons  int `json:"lessons"`
	Surprise int `json:"surprise"`
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// ExtractExamples mines already-grounded Infinity artifacts into the Gym
// training-example ledger. It is deliberately deterministic and provenance
// backed: every row keeps source_kind/source_id so future datasets can trace
// back to the exact eval, reflection, or prediction that taught the reflex
// layer something.
func (s *Store) ExtractExamples(ctx context.Context, limit int) (ExtractResult, error) {
	var out ExtractResult
	if s == nil || s.pool == nil {
		return out, nil
	}
	if ok, err := s.tableExists(ctx, "mem_training_examples"); err != nil {
		return out, err
	} else if !ok {
		return out, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)

	if err := tx.QueryRow(ctx, `
		WITH src AS (
			SELECT id::text AS source_id,
			       subject_kind AS task_kind,
			       CONCAT(subject_kind, ': ', subject_name, E'\n', notes) AS input_text,
			       CONCAT('outcome=', outcome, E'\nscore=', COALESCE(score::text, ''), E'\n', notes) AS output_text,
			       CASE outcome
			         WHEN 'success' THEN 'accepted'
			         WHEN 'failure' THEN 'rejected'
			         ELSE 'corrected'
			       END AS label,
			       CASE
			         WHEN score IS NOT NULL THEN GREATEST(0, LEAST(1, score::float8 / 100.0))
			         WHEN outcome = 'success' THEN 1.0
			         WHEN outcome = 'partial' THEN 0.5
			         ELSE 0.0
			       END AS score,
			       jsonb_build_object(
			         'subject_kind', subject_kind,
			         'subject_name', subject_name,
			         'run_id', run_id,
			         'source', source
			       ) AS metadata,
			       created_at
			  FROM mem_evals
			 ORDER BY created_at DESC
			 LIMIT $1
		), ins AS (
			INSERT INTO mem_training_examples
				(source_kind, source_id, task_kind, input_text, output_text,
				 label, score, privacy_class, metadata, created_at)
			SELECT 'eval', source_id, task_kind, input_text, output_text,
			       label, score, 'private', metadata, created_at
			  FROM src
			 WHERE NOT EXISTS (
			       SELECT 1 FROM mem_training_examples e
			        WHERE e.source_kind = 'eval'
			          AND e.source_id = src.source_id
			          AND e.task_kind = src.task_kind
			 )
			RETURNING 1
		)
		SELECT COUNT(*)::int FROM ins
	`, limit).Scan(&out.Evals); err != nil {
		return out, err
	}

	if err := tx.QueryRow(ctx, `
		WITH src AS (
			SELECT id::text AS source_id,
			       kind AS task_kind,
			       critique AS input_text,
			       COALESCE((
			         SELECT string_agg(elem->>'text', E'\n')
			           FROM jsonb_array_elements(lessons) elem
			       ), '') AS output_text,
			       quality_score::float8 AS score,
			       jsonb_build_object(
			         'session_id', COALESCE(session_id::text, ''),
			         'importance', importance,
			         'quality_score', quality_score
			       ) AS metadata,
			       created_at
			  FROM mem_reflections
			 WHERE lessons <> '[]'::jsonb OR critique <> ''
			 ORDER BY created_at DESC
			 LIMIT $1
		), ins AS (
			INSERT INTO mem_training_examples
				(source_kind, source_id, task_kind, input_text, output_text,
				 label, score, privacy_class, metadata, created_at)
			SELECT 'reflection', source_id, task_kind, input_text, output_text,
			       'accepted', score, 'private', metadata, created_at
			  FROM src
			 WHERE NOT EXISTS (
			       SELECT 1 FROM mem_training_examples e
			        WHERE e.source_kind = 'reflection'
			          AND e.source_id = src.source_id
			          AND e.task_kind = src.task_kind
			 )
			RETURNING 1
		)
		SELECT COUNT(*)::int FROM ins
	`, limit).Scan(&out.Lessons); err != nil {
		return out, err
	}

	if err := tx.QueryRow(ctx, `
		WITH src AS (
			SELECT id::text AS source_id,
			       tool_name,
			       expected AS input_text,
			       COALESCE(actual, '') AS output_text,
			       COALESCE(surprise_score, 0)::float8 AS score,
			       jsonb_build_object(
			         'session_id', COALESCE(session_id::text, ''),
			         'tool_call_id', tool_call_id,
			         'tool_name', tool_name,
			         'matched', COALESCE(matched, false)
			       ) AS metadata,
			       created_at
			  FROM mem_predictions
			 WHERE COALESCE(surprise_score, 0) >= 0.7
			   AND actual IS NOT NULL
			 ORDER BY surprise_score DESC, created_at DESC
			 LIMIT $1
		), ins AS (
			INSERT INTO mem_training_examples
				(source_kind, source_id, task_kind, input_text, output_text,
				 label, score, privacy_class, metadata, created_at)
			SELECT 'prediction', source_id, 'tool_prediction', input_text, output_text,
			       'corrected', score, 'private', metadata, created_at
			  FROM src
			 WHERE NOT EXISTS (
			       SELECT 1 FROM mem_training_examples e
			        WHERE e.source_kind = 'prediction'
			          AND e.source_id = src.source_id
			          AND e.task_kind = 'tool_prediction'
			 )
			RETURNING 1
		)
		SELECT COUNT(*)::int FROM ins
	`, limit).Scan(&out.Surprise); err != nil {
		return out, err
	}

	out.Inserted = out.Evals + out.Lessons + out.Surprise
	return out, tx.Commit(ctx)
}

func (s *Store) Snapshot(ctx context.Context, limit int) (Snapshot, error) {
	var snap Snapshot
	if s == nil || s.pool == nil {
		return snap, nil
	}
	if ok, err := s.tableExists(ctx, "mem_training_examples"); err != nil {
		return snap, err
	} else if !ok {
		return snap, nil
	}
	snap.Summary.Ready = true
	snap.Summary.ReflexOn = true
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	if err := s.summary(ctx, &snap.Summary); err != nil {
		return snap, err
	}
	var err error
	if snap.Examples, err = s.examples(ctx, limit); err != nil {
		return snap, err
	}
	if snap.Datasets, err = s.datasets(ctx, limit); err != nil {
		return snap, err
	}
	if snap.Runs, err = s.runs(ctx, limit); err != nil {
		return snap, err
	}
	if snap.Adapters, err = s.adapters(ctx, limit); err != nil {
		return snap, err
	}
	if snap.Evals, err = s.evals(ctx, limit); err != nil {
		return snap, err
	}
	if snap.Routes, err = s.routes(ctx, limit); err != nil {
		return snap, err
	}
	return snap, nil
}

func (s *Store) tableExists(ctx context.Context, name string) (bool, error) {
	var ok bool
	err := s.pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, "public."+name).Scan(&ok)
	return ok, err
}

func (s *Store) summary(ctx context.Context, out *Summary) error {
	var last pgtype.Timestamptz
	if err := s.pool.QueryRow(ctx, `
		SELECT
			(SELECT COUNT(*)::int FROM mem_training_examples),
			(SELECT COUNT(*)::int FROM mem_distillation_datasets),
			(SELECT COUNT(*)::int FROM mem_distillation_runs),
			(SELECT COUNT(*)::int FROM mem_model_adapters WHERE status = 'candidate'),
			(SELECT COUNT(*)::int FROM mem_model_adapters WHERE status = 'active'),
			(SELECT COALESCE(SUM(regression_count), 0)::int FROM mem_adapter_evals),
			(SELECT MAX(created_at) FROM mem_distillation_runs)
	`).Scan(&out.Examples, &out.Datasets, &out.Runs, &out.Candidates, &out.Active, &out.Regressions, &last); err != nil {
		return err
	}
	out.LastRunAt = timePtr(last)
	return nil
}

func (s *Store) examples(ctx context.Context, limit int) ([]Example, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, source_kind, source_id, task_kind, label,
		       score::float8, privacy_class, metadata::text, created_at
		  FROM mem_training_examples
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Example
	for rows.Next() {
		var x Example
		var meta string
		if err := rows.Scan(&x.ID, &x.SourceKind, &x.SourceID, &x.TaskKind, &x.Label, &x.Score, &x.PrivacyClass, &meta, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.Metadata = jsonMap(meta)
		out = append(out, x)
	}
	return empty(out), rows.Err()
}

func (s *Store) datasets(ctx context.Context, limit int) ([]Dataset, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, status, example_count, artifact_uri, checksum,
		       filters::text, updated_at
		  FROM mem_distillation_datasets
		 ORDER BY updated_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Dataset
	for rows.Next() {
		var x Dataset
		var filters string
		if err := rows.Scan(&x.ID, &x.Name, &x.Status, &x.ExampleCount, &x.ArtifactURI, &x.Checksum, &filters, &x.UpdatedAt); err != nil {
			return nil, err
		}
		x.Filters = jsonMap(filters)
		out = append(out, x)
	}
	return empty(out), rows.Err()
}

func (s *Store) runs(ctx context.Context, limit int) ([]Run, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(dataset_id::text, ''), COALESCE(adapter_id::text, ''),
		       status, trigger, reason, base_model, metrics::text, error,
		       started_at, completed_at, created_at
		  FROM mem_distillation_runs
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Run
	for rows.Next() {
		var x Run
		var metrics string
		var started, completed pgtype.Timestamptz
		if err := rows.Scan(&x.ID, &x.DatasetID, &x.AdapterID, &x.Status, &x.Trigger, &x.Reason, &x.BaseModel, &metrics, &x.Error, &started, &completed, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.StartedAt = timePtr(started)
		x.CompletedAt = timePtr(completed)
		x.Metrics = jsonMap(metrics)
		out = append(out, x)
	}
	return empty(out), rows.Err()
}

func (s *Store) adapters(ctx context.Context, limit int) ([]Adapter, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, base_model, status, task_scope, metrics::text,
		       created_at, promoted_at, rolled_back_at
		  FROM mem_model_adapters
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Adapter
	for rows.Next() {
		var x Adapter
		var metrics string
		var promoted, rolledBack pgtype.Timestamptz
		if err := rows.Scan(&x.ID, &x.Name, &x.BaseModel, &x.Status, &x.TaskScope, &metrics, &x.CreatedAt, &promoted, &rolledBack); err != nil {
			return nil, err
		}
		x.PromotedAt = timePtr(promoted)
		x.RolledBackAt = timePtr(rolledBack)
		x.Metrics = jsonMap(metrics)
		out = append(out, x)
	}
	return empty(out), rows.Err()
}

func (s *Store) evals(ctx context.Context, limit int) ([]Eval, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(adapter_id::text, ''), eval_name,
		       baseline_score::float8, candidate_score::float8,
		       regression_count, passed, metrics::text, created_at
		  FROM mem_adapter_evals
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Eval
	for rows.Next() {
		var x Eval
		var metrics string
		if err := rows.Scan(&x.ID, &x.AdapterID, &x.EvalName, &x.BaselineScore, &x.CandidateScore, &x.RegressionCount, &x.Passed, &metrics, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.Metrics = jsonMap(metrics)
		out = append(out, x)
	}
	return empty(out), rows.Err()
}

func (s *Store) routes(ctx context.Context, limit int) ([]Route, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, route, task_kind, COALESCE(active_adapter_id::text, ''),
		       status, confidence::float8, min_score::float8, metadata::text, updated_at
		  FROM mem_policy_routes
		 ORDER BY updated_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Route
	for rows.Next() {
		var x Route
		var meta string
		if err := rows.Scan(&x.ID, &x.Route, &x.TaskKind, &x.ActiveAdapterID, &x.Status, &x.Confidence, &x.MinScore, &meta, &x.UpdatedAt); err != nil {
			return nil, err
		}
		x.Metadata = jsonMap(meta)
		out = append(out, x)
	}
	return empty(out), rows.Err()
}

func jsonMap(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		return map[string]any{"parse_error": fmt.Sprint(err)}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

func empty[T any](in []T) []T {
	if in == nil {
		return []T{}
	}
	return in
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	return &t.Time
}
