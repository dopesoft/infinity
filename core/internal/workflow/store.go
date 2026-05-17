package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence boundary for workflows, runs, and steps. It is
// the only thing that touches the mem_workflow* tables.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{pool: pool, logger: logger}
}

// ── definitions ───────────────────────────────────────────────────────────

// UpsertWorkflow saves (or replaces) a reusable workflow definition by name.
func (s *Store) UpsertWorkflow(ctx context.Context, wf *Workflow) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("workflow: no pool")
	}
	stepsJSON, err := json.Marshal(wf.Steps)
	if err != nil {
		return "", fmt.Errorf("workflow: marshal steps: %w", err)
	}
	source := wf.Source
	if source == "" {
		source = "agent"
	}
	var id string
	err = s.pool.QueryRow(ctx, `
		INSERT INTO mem_workflows (name, description, steps, source, enabled)
		VALUES ($1, $2, $3::jsonb, $4, TRUE)
		ON CONFLICT (name) DO UPDATE SET
		  description = EXCLUDED.description,
		  steps       = EXCLUDED.steps,
		  source      = EXCLUDED.source,
		  updated_at  = NOW()
		RETURNING id::text
	`, wf.Name, wf.Description, string(stepsJSON), source).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("workflow: upsert: %w", err)
	}
	return id, nil
}

// GetWorkflow looks up a definition by name or id. Returns (nil, nil) when
// it doesn't exist.
func (s *Store) GetWorkflow(ctx context.Context, nameOrID string) (*Workflow, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("workflow: no pool")
	}
	nameOrID = strings.TrimSpace(nameOrID)
	q := `SELECT id::text, name, description, steps, source, enabled, created_at, updated_at
	        FROM mem_workflows WHERE name = $1`
	arg := any(nameOrID)
	if looksLikeUUID(nameOrID) {
		q = `SELECT id::text, name, description, steps, source, enabled, created_at, updated_at
		       FROM mem_workflows WHERE id = $1::uuid`
	}
	wf, err := scanWorkflow(s.pool.QueryRow(ctx, q, arg))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return wf, err
}

// ListWorkflows returns every saved definition, newest first.
func (s *Store) ListWorkflows(ctx context.Context) ([]*Workflow, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, name, description, steps, source, enabled, created_at, updated_at
		  FROM mem_workflows ORDER BY updated_at DESC LIMIT 200`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Workflow
	for rows.Next() {
		wf, err := scanWorkflow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, wf)
	}
	return out, rows.Err()
}

// ── runs ──────────────────────────────────────────────────────────────────

// StartRun creates a run plus its materialized step rows in one transaction.
// The run starts `pending` - the engine claims it on the next tick. A run
// is self-contained: it copies the step list at start, so editing the
// definition later never disturbs an in-flight run. dependsOn, when set to
// another run's id, holds this run back until that run is `done`.
func (s *Store) StartRun(ctx context.Context, workflowID, workflowName string, steps []StepDef, input map[string]any, trigger, dependsOn string) (*Run, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("workflow: no pool")
	}
	if len(steps) == 0 {
		return nil, errors.New("workflow: a run needs at least one step")
	}
	if trigger == "" {
		trigger = "manual"
	}
	if input == nil {
		input = map[string]any{}
	}
	inputJSON, _ := json.Marshal(input)
	contextJSON, _ := json.Marshal(map[string]any{"input": input, "steps": map[string]any{}})

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var runID string
	var wfID any
	if workflowID != "" {
		wfID = workflowID
	}
	var depID any
	if dependsOn != "" {
		depID = dependsOn
	}
	err = tx.QueryRow(ctx, `
		INSERT INTO mem_workflow_runs
		  (workflow_id, workflow_name, status, trigger_source, input, context, depends_on)
		VALUES ($1, $2, 'pending', $3, $4::jsonb, $5::jsonb, $6)
		RETURNING id::text
	`, wfID, workflowName, trigger, string(inputJSON), string(contextJSON), depID).Scan(&runID)
	if err != nil {
		return nil, fmt.Errorf("workflow: insert run: %w", err)
	}

	for i, st := range steps {
		if !st.Kind.Valid() {
			return nil, fmt.Errorf("workflow: step %d has invalid kind %q", i, st.Kind)
		}
		specJSON, _ := json.Marshal(st.Spec)
		maxAttempts := st.MaxAttempts
		if maxAttempts <= 0 {
			maxAttempts = 3
		}
		if st.Kind == KindCheckpoint {
			maxAttempts = 1
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO mem_workflow_steps
			  (run_id, step_index, name, kind, spec, status, max_attempts)
			VALUES ($1, $2, $3, $4, $5::jsonb, 'pending', $6)
		`, runID, i, st.Name, string(st.Kind), string(specJSON), maxAttempts)
		if err != nil {
			return nil, fmt.Errorf("workflow: insert step %d: %w", i, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, runID)
}

// GetRun returns a run with its steps loaded. (nil, nil) when not found.
func (s *Store) GetRun(ctx context.Context, id string) (*Run, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("workflow: no pool")
	}
	run, err := scanRun(s.pool.QueryRow(ctx, runSelect+` WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	steps, err := s.loadSteps(ctx, id)
	if err != nil {
		return nil, err
	}
	run.Steps = steps
	return run, nil
}

// ListRuns returns recent runs (without step detail) for the work board.
func (s *Store) ListRuns(ctx context.Context, limit int) ([]*Run, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, runSelect+` ORDER BY created_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

// ClaimRunnable locks and returns the oldest runnable run (pending or
// running), transitioning pending → running and touching updated_at so
// runs round-robin fairly. Returns (nil, nil) when nothing is runnable.
func (s *Store) ClaimRunnable(ctx context.Context) (*Run, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// A run is claimable only when its dependency (if any) has finished -
	// dependency-aware scheduling: "run B after run A is done."
	var id string
	err = tx.QueryRow(ctx, `
		SELECT r.id::text FROM mem_workflow_runs r
		 WHERE r.status IN ('pending', 'running')
		   AND (
		        r.depends_on IS NULL
		     OR EXISTS (
		          SELECT 1 FROM mem_workflow_runs d
		           WHERE d.id = r.depends_on AND d.status = 'done'
		        )
		   )
		 ORDER BY r.updated_at ASC
		 LIMIT 1
		 FOR UPDATE SKIP LOCKED
	`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
		UPDATE mem_workflow_runs
		   SET status = 'running',
		       started_at = COALESCE(started_at, NOW()),
		       updated_at = NOW()
		 WHERE id = $1::uuid
	`, id)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, id)
}

// ── step transitions ──────────────────────────────────────────────────────

// StartStep marks a step running and increments its attempt counter.
func (s *Store) StartStep(ctx context.Context, stepID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_workflow_steps
		   SET status = 'running', attempt = attempt + 1,
		       started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		 WHERE id = $1::uuid
	`, stepID)
	return err
}

// CompleteStep marks a step done and stores its output.
func (s *Store) CompleteStep(ctx context.Context, stepID, output string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_workflow_steps
		   SET status = 'done', output = $2, error = '',
		       finished_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid
	`, stepID, output)
	return err
}

// FailStep handles a failed step. If the step has retries left it goes back
// to 'pending' (the engine re-runs it next tick) and retried=true. Once
// retries are exhausted it is marked 'failed' and retried=false - the
// caller then fails the run.
func (s *Store) FailStep(ctx context.Context, st *Step, errMsg string) (retried bool, err error) {
	if st.Attempt < st.MaxAttempts {
		_, err = s.pool.Exec(ctx, `
			UPDATE mem_workflow_steps
			   SET status = 'pending', error = $2, updated_at = NOW()
			 WHERE id = $1::uuid
		`, st.ID, errMsg)
		return err == nil, err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE mem_workflow_steps
		   SET status = 'failed', error = $2, finished_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid
	`, st.ID, errMsg)
	return false, err
}

// AwaitStep marks a checkpoint step as awaiting boss approval.
func (s *Store) AwaitStep(ctx context.Context, stepID string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_workflow_steps
		   SET status = 'awaiting', started_at = COALESCE(started_at, NOW()), updated_at = NOW()
		 WHERE id = $1::uuid
	`, stepID)
	return err
}

// ResolveCheckpoint clears an awaiting checkpoint step: approve → 'done',
// reject → 'skipped'. Returns the step's run id.
func (s *Store) ResolveCheckpoint(ctx context.Context, stepID string, approve bool) (string, error) {
	status := "skipped"
	if approve {
		status = "done"
	}
	var runID string
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_workflow_steps
		   SET status = $2, finished_at = NOW(), updated_at = NOW()
		 WHERE id = $1::uuid AND status = 'awaiting'
		RETURNING run_id::text
	`, stepID, status).Scan(&runID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("workflow: step %s is not an awaiting checkpoint", stepID)
	}
	return runID, err
}

// ── run transitions ───────────────────────────────────────────────────────

// AdvanceRun bumps current_step and merges a step's output into the run
// context under steps.<index>.
func (s *Store) AdvanceRun(ctx context.Context, runID string, nextStep int, stepIndex int, output string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_workflow_runs
		   SET current_step = $2,
		       context = jsonb_set(
		           context,
		           ARRAY['steps', $3::text],
		           to_jsonb($4::text),
		           true
		       ),
		       updated_at = NOW()
		 WHERE id = $1::uuid
	`, runID, nextStep, stepIndex, output)
	return err
}

// SetRunStatus moves a run to a new status, stamping finished_at for
// terminal states and started_at when entering 'running'.
func (s *Store) SetRunStatus(ctx context.Context, runID string, status RunStatus, errMsg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_workflow_runs
		   SET status = $2,
		       error = CASE WHEN $3 <> '' THEN $3 ELSE error END,
		       started_at = CASE WHEN $2 = 'running' THEN COALESCE(started_at, NOW()) ELSE started_at END,
		       finished_at = CASE WHEN $2 IN ('done','failed','cancelled') THEN NOW() ELSE finished_at END,
		       updated_at = NOW()
		 WHERE id = $1::uuid
	`, runID, string(status), errMsg)
	return err
}

// ReclaimOrphans resets steps left 'running' by a process that died, back
// to 'pending', and bumps their run to 'running' so the engine retries.
// Called once on engine boot. Returns the count reclaimed.
func (s *Store) ReclaimOrphans(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_workflow_steps
		   SET status = 'pending', updated_at = NOW()
		 WHERE status = 'running'
	`)
	if err != nil {
		return 0, err
	}
	// Any run that was 'running' stays runnable; nothing else to do -
	// the engine re-claims it and re-runs the reclaimed step.
	return int(ct.RowsAffected()), nil
}

// ── helpers ───────────────────────────────────────────────────────────────

func (s *Store) loadSteps(ctx context.Context, runID string) ([]Step, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, run_id::text, step_index, name, kind, spec, status,
		       attempt, max_attempts, output, error, started_at, finished_at
		  FROM mem_workflow_steps
		 WHERE run_id = $1::uuid
		 ORDER BY step_index ASC
	`, runID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Step
	for rows.Next() {
		var (
			st      Step
			kind    string
			status  string
			specRaw []byte
		)
		if err := rows.Scan(
			&st.ID, &st.RunID, &st.StepIndex, &st.Name, &kind, &specRaw, &status,
			&st.Attempt, &st.MaxAttempts, &st.Output, &st.Error,
			&st.StartedAt, &st.FinishedAt,
		); err != nil {
			return nil, err
		}
		st.Kind = StepKind(kind)
		st.Status = StepStatus(status)
		if len(specRaw) > 0 {
			_ = json.Unmarshal(specRaw, &st.Spec)
		}
		if st.Spec == nil {
			st.Spec = map[string]any{}
		}
		out = append(out, st)
	}
	return out, rows.Err()
}

const runSelect = `SELECT id::text, COALESCE(workflow_id::text,''), workflow_name,
	status, trigger_source, input, context, current_step, error,
	started_at, finished_at, created_at FROM mem_workflow_runs`

func scanRun(row pgx.Row) (*Run, error) {
	var (
		run        Run
		status     string
		inputRaw   []byte
		contextRaw []byte
	)
	if err := row.Scan(
		&run.ID, &run.WorkflowID, &run.WorkflowName, &status, &run.TriggerSource,
		&inputRaw, &contextRaw, &run.CurrentStep, &run.Error,
		&run.StartedAt, &run.FinishedAt, &run.CreatedAt,
	); err != nil {
		return nil, err
	}
	run.Status = RunStatus(status)
	if len(inputRaw) > 0 {
		_ = json.Unmarshal(inputRaw, &run.Input)
	}
	if len(contextRaw) > 0 {
		_ = json.Unmarshal(contextRaw, &run.Context)
	}
	if run.Input == nil {
		run.Input = map[string]any{}
	}
	if run.Context == nil {
		run.Context = map[string]any{}
	}
	return &run, nil
}

func scanWorkflow(row pgx.Row) (*Workflow, error) {
	var (
		wf       Workflow
		stepsRaw []byte
	)
	if err := row.Scan(
		&wf.ID, &wf.Name, &wf.Description, &stepsRaw, &wf.Source, &wf.Enabled,
		&wf.CreatedAt, &wf.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(stepsRaw) > 0 {
		_ = json.Unmarshal(stepsRaw, &wf.Steps)
	}
	return &wf, nil
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

var _ = time.Now // time used via pgx scans into *time.Time
