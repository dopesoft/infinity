package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is plain pgx CRUD over mem_plans / mem_plan_steps. No execution
// engine - the agent drives steps from its own loop.
type Store struct {
	pool *pgxpool.Pool
}

// NewStore returns a Store bound to the pool. nil-safe: a nil pool makes every
// method a no-op / empty result so chat-only deployments don't break.
func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// Create supersedes any active/paused plan for the session, then inserts a new
// plan + its ordered steps. Returns the new plan with steps populated.
func (s *Store) Create(ctx context.Context, sessionID, title, goal, goalID string, steps []NewStepInput) (*Plan, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("plan store not configured")
	}
	if title == "" {
		return nil, errors.New("title required")
	}
	if len(steps) == 0 {
		return nil, errors.New("a plan needs at least one step")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	// Supersede the prior active plan for this session (one active plan per
	// session). Cancelled, not deleted, so history survives.
	if sessionID != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE mem_plans SET status = 'cancelled', updated_at = NOW()
			 WHERE session_id = $1::uuid AND status IN ('active','paused')
		`, sessionID); err != nil {
			return nil, fmt.Errorf("supersede active plan: %w", err)
		}
	}

	var (
		planID    string
		createdAt time.Time
		updatedAt time.Time
	)
	err = tx.QueryRow(ctx, `
		INSERT INTO mem_plans (session_id, goal_id, title, goal, status, current_step)
		VALUES (NULLIF($1,'')::uuid, NULLIF($2,'')::uuid, $3, $4, 'active', 0)
		RETURNING id::text, created_at, updated_at
	`, sessionID, goalID, title, goal).Scan(&planID, &createdAt, &updatedAt)
	if err != nil {
		return nil, fmt.Errorf("insert plan: %w", err)
	}

	out := &Plan{
		ID:        planID,
		SessionID: sessionID,
		GoalID:    goalID,
		Title:     title,
		Goal:      goal,
		Status:    PlanActive,
		CreatedAt: createdAt,
		UpdatedAt: updatedAt,
	}
	for i, st := range steps {
		var stepID string
		err = tx.QueryRow(ctx, `
			INSERT INTO mem_plan_steps
				(plan_id, idx, title, detail, status, is_checkpoint, verify_required)
			VALUES ($1::uuid, $2, $3, $4, 'pending', $5, $6)
			RETURNING id::text
		`, planID, i, st.Title, st.Detail, st.IsCheckpoint, st.VerifyRequired).Scan(&stepID)
		if err != nil {
			return nil, fmt.Errorf("insert step %d: %w", i, err)
		}
		out.Steps = append(out.Steps, Step{
			ID:             stepID,
			PlanID:         planID,
			Idx:            i,
			Title:          st.Title,
			Detail:         st.Detail,
			Status:         StepPending,
			IsCheckpoint:   st.IsCheckpoint,
			VerifyRequired: st.VerifyRequired,
		})
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return out, nil
}

// ChecklistItem is the flat {text, status} shape the todo_write alias passes.
// A checklist is just a plan with plain steps (no verify/checkpoint), so it
// writes through the SAME plan substrate - one durable concept, two ergonomic
// entry points (plan_create for rich plans, todo_write for a quick checklist).
type ChecklistItem struct {
	Text   string
	Status string
}

// SyncChecklist upserts the session's active plan to match a flat checklist:
// it keeps the existing active/paused plan (preserving its id so the dashboard
// card + chat dock don't flicker) and replaces its steps to mirror the list,
// or creates a new plan when none is active. This is the unification seam -
// todo_write maps onto it so its checklist and a plan are the same thing.
func (s *Store) SyncChecklist(ctx context.Context, sessionID, title string, items []ChecklistItem) (*Plan, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("plan store not configured")
	}
	if sessionID == "" {
		return nil, errors.New("session required")
	}
	if len(items) == 0 {
		return nil, errors.New("checklist needs at least one item")
	}

	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var planID string
	err = tx.QueryRow(ctx, `
		SELECT id::text FROM mem_plans
		 WHERE session_id = $1::uuid AND status IN ('active','paused')
		 ORDER BY updated_at DESC LIMIT 1
	`, sessionID).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		planID = ""
	} else if err != nil {
		return nil, err
	}

	if planID == "" {
		t := title
		if t == "" {
			t = "Checklist"
		}
		if err := tx.QueryRow(ctx, `
			INSERT INTO mem_plans (session_id, title, goal, status, current_step)
			VALUES ($1::uuid, $2, '', 'active', 0)
			RETURNING id::text
		`, sessionID, t).Scan(&planID); err != nil {
			return nil, fmt.Errorf("create checklist plan: %w", err)
		}
	} else if title != "" {
		if _, err := tx.Exec(ctx, `UPDATE mem_plans SET title = $2 WHERE id = $1::uuid`, planID, title); err != nil {
			return nil, err
		}
	}

	// Before replacing the step set, collect any still-running 'plan.step'
	// spinner runs on the steps we're about to delete. A foreground
	// plan_update(step,in_progress) books such a run; if that step is now being
	// replaced by the (background) checklist, the DELETE would ORPHAN the run —
	// nothing could ever close it and it would spin until the 45-min reaper.
	// That orphaned spinner was the "plan step spins forever" bug. We close them
	// (after commit) so the replace is clean.
	orphanRows, err := tx.Query(ctx, `
		SELECT run_id::text FROM mem_plan_steps
		 WHERE plan_id = $1::uuid AND run_id IS NOT NULL
		   AND status NOT IN ('done','failed','skipped')
	`, planID)
	if err != nil {
		return nil, err
	}
	var orphanRunIDs []string
	for orphanRows.Next() {
		var rid string
		if scanErr := orphanRows.Scan(&rid); scanErr != nil {
			orphanRows.Close()
			return nil, scanErr
		}
		if rid != "" {
			orphanRunIDs = append(orphanRunIDs, rid)
		}
	}
	orphanRows.Close()
	if err := orphanRows.Err(); err != nil {
		return nil, err
	}

	// Replace the step set to mirror the checklist. todo-driven steps carry no
	// runs/verification, so a wholesale replace is safe and keeps the call
	// idempotent (todo_write resends the full list each time).
	if _, err := tx.Exec(ctx, `DELETE FROM mem_plan_steps WHERE plan_id = $1::uuid`, planID); err != nil {
		return nil, err
	}
	for i, it := range items {
		if _, err := tx.Exec(ctx, `
			INSERT INTO mem_plan_steps (plan_id, idx, title, status)
			VALUES ($1::uuid, $2, $3, $4)
		`, planID, i, it.Text, NormalizeStepStatus(it.Status)); err != nil {
			return nil, fmt.Errorf("insert checklist step %d: %w", i, err)
		}
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	// Settle the orphaned spinner runs (best-effort; the step rows they tracked
	// are gone, superseded by the new checklist). FinishByID is pool-based, so
	// it runs independent of the committed tx.
	for _, rid := range orphanRunIDs {
		runs.FinishByID(ctx, rid, nil, "superseded by updated checklist")
	}
	if err := s.recompute(ctx, planID); err != nil {
		return nil, err
	}
	return s.Get(ctx, planID)
}

// GetActiveBySession returns the session's current active/paused plan (with
// steps), or nil when there is none.
func (s *Store) GetActiveBySession(ctx context.Context, sessionID string) (*Plan, error) {
	if s == nil || s.pool == nil || sessionID == "" {
		return nil, nil
	}
	// system_event crons use a non-UUID session id ("<name>-system"). Guard
	// before the SQL cast so a 22P02 Postgres error doesn't prevent the
	// GetAnyActive fallback in resolvePositionalStep from running.
	if _, err := uuid.Parse(sessionID); err != nil {
		return nil, nil
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM mem_plans
		 WHERE session_id = $1::uuid AND status IN ('active','paused')
		 ORDER BY updated_at DESC LIMIT 1
	`, sessionID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// GetAnyActive returns the most recently updated active/paused plan regardless
// of session. Used as a fallback when positional step resolution fails to find
// a plan for the current session (e.g. cross-session continuation, cron fires
// with a new session ID, or CLI/test contexts with no session in context).
func (s *Store) GetAnyActive(ctx context.Context) (*Plan, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM mem_plans
		 WHERE status IN ('active','paused')
		 ORDER BY updated_at DESC LIMIT 1
	`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return s.Get(ctx, id)
}

// Get loads one plan with its ordered steps.
func (s *Store) Get(ctx context.Context, planID string) (*Plan, error) {
	if s == nil || s.pool == nil || planID == "" {
		return nil, nil
	}
	p := &Plan{}
	var sessionID, goalID *string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, session_id::text, goal_id::text, title, goal, status, current_step, created_at, updated_at
		  FROM mem_plans WHERE id = $1::uuid
	`, planID).Scan(&p.ID, &sessionID, &goalID, &p.Title, &p.Goal, &p.Status, &p.CurrentStep, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if sessionID != nil {
		p.SessionID = *sessionID
	}
	if goalID != nil {
		p.GoalID = *goalID
	}
	steps, err := s.steps(ctx, planID)
	if err != nil {
		return nil, err
	}
	p.Steps = steps
	return p, nil
}

// ListByStatuses returns plans (with steps) whose status is in the set,
// most-recently-updated first.
//
// Batched on purpose: it issues exactly TWO queries (one for the plan rows,
// one for every step across all of them via plan_id = ANY(...)) and assembles
// the graph in Go. The previous shape selected the ids then looped Get(id),
// which fanned out to 1 + 2N serial round-trips (81 for a 40-plan dashboard
// poll) — at ~70ms per pooler round-trip that was the entire ~5.6s
// "dashboard: slow assembly". Same ANY($1::uuid[]) batch the dashboard already
// uses for workflow steps; do not re-introduce the per-plan Get loop here.
func (s *Store) ListByStatuses(ctx context.Context, statuses []string, limit int) ([]Plan, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, session_id::text, goal_id::text, title, goal, status, current_step, created_at, updated_at
		  FROM mem_plans
		 WHERE status = ANY($1)
		 ORDER BY updated_at DESC
		 LIMIT $2
	`, statuses, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Plan, 0, limit)
	indexByID := make(map[string]int) // plan id -> position in out, for step fan-in
	for rows.Next() {
		var p Plan
		var sessionID, goalID *string
		if err := rows.Scan(&p.ID, &sessionID, &goalID, &p.Title, &p.Goal,
			&p.Status, &p.CurrentStep, &p.CreatedAt, &p.UpdatedAt); err != nil {
			rows.Close()
			return nil, err
		}
		if sessionID != nil {
			p.SessionID = *sessionID
		}
		if goalID != nil {
			p.GoalID = *goalID
		}
		p.Steps = []Step{}
		indexByID[p.ID] = len(out)
		out = append(out, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return out, nil
	}

	ids := make([]string, 0, len(out))
	for i := range out {
		ids = append(ids, out[i].ID)
	}
	stepRows, err := s.pool.Query(ctx, `
		SELECT id::text, plan_id::text, idx, title, detail, status,
		       is_checkpoint, verify_required, verify_result::text,
		       result_summary, run_id::text, recovery_attempted,
		       started_at, ended_at
		  FROM mem_plan_steps
		 WHERE plan_id = ANY($1::uuid[])
		 ORDER BY plan_id, idx ASC
	`, ids)
	if err != nil {
		return nil, err
	}
	defer stepRows.Close()
	for stepRows.Next() {
		var (
			st        Step
			verifyRaw *string
			runID     *string
			started   *time.Time
			ended     *time.Time
		)
		if err := stepRows.Scan(&st.ID, &st.PlanID, &st.Idx, &st.Title, &st.Detail, &st.Status,
			&st.IsCheckpoint, &st.VerifyRequired, &verifyRaw,
			&st.ResultSummary, &runID, &st.RecoveryAttempted,
			&started, &ended); err != nil {
			return nil, err
		}
		if verifyRaw != nil && *verifyRaw != "" {
			_ = json.Unmarshal([]byte(*verifyRaw), &st.VerifyResult)
		}
		if runID != nil {
			st.RunID = *runID
		}
		st.StartedAt = started
		st.EndedAt = ended
		if i, ok := indexByID[st.PlanID]; ok {
			out[i].Steps = append(out[i].Steps, st)
		}
	}
	return out, stepRows.Err()
}

func (s *Store) steps(ctx context.Context, planID string) ([]Step, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, plan_id::text, idx, title, detail, status,
		       is_checkpoint, verify_required, verify_result::text,
		       result_summary, run_id::text, recovery_attempted,
		       started_at, ended_at
		  FROM mem_plan_steps WHERE plan_id = $1::uuid ORDER BY idx ASC
	`, planID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Step{}
	for rows.Next() {
		var (
			st        Step
			verifyRaw *string
			runID     *string
			started   *time.Time
			ended     *time.Time
		)
		if err := rows.Scan(&st.ID, &st.PlanID, &st.Idx, &st.Title, &st.Detail, &st.Status,
			&st.IsCheckpoint, &st.VerifyRequired, &verifyRaw,
			&st.ResultSummary, &runID, &st.RecoveryAttempted,
			&started, &ended); err != nil {
			return nil, err
		}
		if verifyRaw != nil && *verifyRaw != "" {
			_ = json.Unmarshal([]byte(*verifyRaw), &st.VerifyResult)
		}
		if runID != nil {
			st.RunID = *runID
		}
		st.StartedAt = started
		st.EndedAt = ended
		out = append(out, st)
	}
	return out, nil
}

// GetStep loads a single step.
func (s *Store) GetStep(ctx context.Context, stepID string) (*Step, error) {
	if s == nil || s.pool == nil || stepID == "" {
		return nil, nil
	}
	var planID string
	err := s.pool.QueryRow(ctx, `SELECT plan_id::text FROM mem_plan_steps WHERE id = $1::uuid`, stepID).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	steps, err := s.steps(ctx, planID)
	if err != nil {
		return nil, err
	}
	for i := range steps {
		if steps[i].ID == stepID {
			return &steps[i], nil
		}
	}
	return nil, nil
}

// MarkStep sets a step's status (+ optional result summary) and stamps
// started/ended timestamps on the right transitions, then recomputes the
// parent plan's current_step + lifecycle. Returns the refreshed plan.
func (s *Store) MarkStep(ctx context.Context, stepID, status, summary string) (*Plan, error) {
	if s == nil || s.pool == nil || stepID == "" {
		return nil, errors.New("plan store not configured")
	}
	status = NormalizeStepStatus(status)

	var planID string
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_plan_steps
		   SET status = $2,
		       result_summary = CASE WHEN $3 = '' THEN result_summary ELSE $3 END,
		       started_at = CASE WHEN $2 = 'in_progress' AND started_at IS NULL THEN NOW() ELSE started_at END,
		       ended_at   = CASE WHEN $2 IN ('done','failed','skipped') THEN NOW() ELSE ended_at END
		 WHERE id = $1::uuid
		RETURNING plan_id::text
	`, stepID, status, summary).Scan(&planID)
	if err != nil {
		return nil, fmt.Errorf("mark step: %w", err)
	}
	if err := s.recompute(ctx, planID); err != nil {
		return nil, err
	}
	return s.Get(ctx, planID)
}

// RecordVerify records a verification verdict on a step. A passing verdict
// leaves the step's status untouched (the caller marks it done); a failing
// verdict flips the step to 'blocked' so the agent replans. Returns the
// refreshed plan.
func (s *Store) RecordVerify(ctx context.Context, stepID, verdict, evidence, method string) (*Plan, error) {
	if s == nil || s.pool == nil || stepID == "" {
		return nil, errors.New("plan store not configured")
	}
	pass := verdict == "pass" || verdict == "passed" || verdict == "ok"
	result := map[string]any{
		"verdict":  map[bool]string{true: "pass", false: "fail"}[pass],
		"evidence": evidence,
		"method":   method,
		"at":       time.Now().UTC().Format(time.RFC3339),
	}
	resultJSON, _ := json.Marshal(result)

	var planID string
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_plan_steps
		   SET verify_result = $2::jsonb,
		       status = CASE WHEN $3 THEN status ELSE 'blocked' END
		 WHERE id = $1::uuid
		RETURNING plan_id::text
	`, stepID, string(resultJSON), pass).Scan(&planID)
	if err != nil {
		return nil, fmt.Errorf("record verify: %w", err)
	}
	if err := s.recompute(ctx, planID); err != nil {
		return nil, err
	}
	return s.Get(ctx, planID)
}

// ReconcileStranded closes plan steps left 'in_progress' after the mem_runs row
// tracking their execution has already terminated in error. This is the
// plan-layer analogue of runs.RecoverStranded.
//
// A step books a 'plan.step' run when plan_update marks it in_progress and only
// flips to a terminal status when plan_update is called again with done/failed/
// skipped (see tools/plan_tools.go). If the turn — or a delegate child doing the
// step's work — dies mid-step, the run gets closed 'error' (by FinishByID or the
// boot run-sweep) while the step is left 'in_progress' forever. recompute() never
// runs, so the whole plan sits 'active' — a permanent "Running" card on the Agent
// Work board with a dead step under it, while the cron/run that spawned it already
// shows "Done". That split is the exact confusion this fixes.
//
// A terminal-error run is unambiguous proof the execution finished and failed, so
// the step is marked 'failed'; recompute then pauses (or fails) the plan and it
// surfaces under "Awaiting you" for the boss to replan or close out. Safe to call
// on a live process precisely because it only acts on steps whose run has already
// ENDED — never one still legitimately executing (its run is still 'running').
// Returns the number of steps reconciled.
func (s *Store) ReconcileStranded(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT s.id::text,
		       COALESCE(NULLIF(r.result_summary, ''), NULLIF(r.error, ''), '') AS run_detail
		  FROM mem_plan_steps s
		  JOIN mem_runs r ON r.id = s.run_id
		  JOIN mem_plans p ON p.id = s.plan_id
		 WHERE s.status = 'in_progress'
		   AND r.status = 'error'
		   AND p.status IN ('active', 'paused')
	`)
	if err != nil {
		return 0, fmt.Errorf("scan stranded steps: %w", err)
	}
	type stranded struct{ id, detail string }
	var steps []stranded
	for rows.Next() {
		var st stranded
		if scanErr := rows.Scan(&st.id, &st.detail); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("scan stranded step id: %w", scanErr)
		}
		steps = append(steps, st)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	n := 0
	for _, st := range steps {
		// Surface the run's ACTUAL error so the boss sees what broke, not a
		// generic placeholder. MarkStep recomputes the parent plan's lifecycle
		// (failed step + later pending steps -> paused).
		summary := firstNonEmptySummary(st.detail,
			"step execution ended without recording a result — reconciled from its run, which had already failed")
		if _, err := s.MarkStep(ctx, st.id, StepFailed, summary); err != nil {
			return n, fmt.Errorf("reconcile stranded step %s: %w", st.id, err)
		}
		n++
	}
	return n, nil
}

// FinalizeSession closes a cron/isolated turn's plan bookkeeping the instant the
// turn ends, so the Agent Work board never shows a phantom "Running" card for a
// turn that already finished (e.g. the agent TaskCompleted but left a step
// in_progress). It (1) closes any plan.step mem_runs still 'running' for this
// session — the turn is over, so they're definitively stranded — then (2)
// reconciles: ReconcileStranded marks the now-errored steps failed and the plan
// recomputes (done / paused). Idempotent and safe; a session with no plan, or a
// plan whose steps all closed cleanly, is a no-op (a cleanly-completed plan was
// already recomputed to 'done' during the turn). Returns steps reconciled.
func (s *Store) FinalizeSession(ctx context.Context, sessionID string) (int, error) {
	if s == nil || s.pool == nil || sessionID == "" {
		return 0, nil
	}
	// 1. A step still 'in_progress' when the turn ends — whose run is still
	// 'running' (or has none) — was NOT a failure. The agent simply stopped
	// before finishing it: a clean turn end, often a deliberate "I'm not going
	// to act here" decision (e.g. the nightly self-improve run halting on a
	// dirty tree). Settle those as 'skipped' with a plain-English reason so the
	// card reads "I stopped here" instead of the misleading "step failed", and
	// close their still-running run rows cleanly (status 'ok', not 'error') so
	// /logs doesn't show a phantom failure either. This also keeps the parent
	// plan out of the failed→paused→"needs you" lane it doesn't belong in
	// (recompute only pauses on a real failed/blocked step). A step whose run
	// ALREADY ended in error is a genuine failure → left for ReconcileStranded.
	rows, err := s.pool.Query(ctx, `
		SELECT st.id::text, COALESCE(st.run_id::text, '')
		  FROM mem_plan_steps st
		  JOIN mem_plans p ON p.id = st.plan_id
		 WHERE p.session_id = $1::uuid
		   AND st.status = 'in_progress'
		   AND (st.run_id IS NULL OR EXISTS (
		         SELECT 1 FROM mem_runs r WHERE r.id = st.run_id AND r.status = 'running'))
	`, sessionID)
	if err != nil {
		return 0, fmt.Errorf("finalize session scan: %w", err)
	}
	type openStep struct{ id, runID string }
	var open []openStep
	for rows.Next() {
		var os openStep
		if scanErr := rows.Scan(&os.id, &os.runID); scanErr != nil {
			rows.Close()
			return 0, fmt.Errorf("finalize session scan row: %w", scanErr)
		}
		open = append(open, os)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}

	const skipReason = "I stopped here — the run ended before I got to this step."
	n := 0
	for _, os := range open {
		if os.runID != "" {
			// The step didn't fail; the turn ended. Close its run cleanly.
			_, _ = s.pool.Exec(ctx, `
				UPDATE mem_runs
				   SET status = 'ok',
				       ended_at = COALESCE(ended_at, NOW()),
				       duration_ms = COALESCE(duration_ms,
				           LEAST(2147483647, GREATEST(0,
				               EXTRACT(EPOCH FROM (NOW() - started_at)) * 1000))::int),
				       result_summary = COALESCE(NULLIF(result_summary, ''), '(skipped — turn ended before this step ran)')
				 WHERE id = $1::uuid AND status = 'running'
			`, os.runID)
		}
		if _, err := s.MarkStep(ctx, os.id, StepSkipped, skipReason); err != nil {
			return n, fmt.Errorf("finalize skip step %s: %w", os.id, err)
		}
		n++
	}

	// 2. Genuine failures remain failures: a step left 'in_progress' whose run
	// ALREADY ended in error is real, so ReconcileStranded marks it failed and
	// the plan recomputes (paused/failed) for the boss to act on.
	m, err := s.ReconcileStranded(ctx)
	return n + m, err
}

// SetStepRun links a step to the mem_runs row tracking its execution so the
// UI can show a navigation-proof live spinner.
func (s *Store) SetStepRun(ctx context.Context, stepID, runID string) error {
	if s == nil || s.pool == nil || stepID == "" || runID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE mem_plan_steps SET run_id = $2::uuid WHERE id = $1::uuid`, stepID, runID)
	return err
}

// InProgressStepForSession returns the in_progress step of the session's
// active/paused plan, or (when none is in flight) the first non-terminal step,
// or nil if the session has no plan. This is the step a background build was
// executing when it finished — what the settle + recovery paths act on.
func (s *Store) InProgressStepForSession(ctx context.Context, sessionID string) (*Step, error) {
	if s == nil || s.pool == nil || sessionID == "" {
		return nil, nil
	}
	p, err := s.GetActiveBySession(ctx, sessionID)
	if err != nil || p == nil {
		return nil, err
	}
	var firstPending *Step
	for i := range p.Steps {
		if p.Steps[i].Status == StepInProgress {
			return &p.Steps[i], nil
		}
		if firstPending == nil && !isTerminalStep(p.Steps[i].Status) {
			firstPending = &p.Steps[i]
		}
	}
	return firstPending, nil
}

// MarkRecoveryAttempted flips the one-shot recovery guard so a re-dispatched
// background build can never be retried a second time.
func (s *Store) MarkRecoveryAttempted(ctx context.Context, stepID string) error {
	if s == nil || s.pool == nil || stepID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE mem_plan_steps SET recovery_attempted = TRUE WHERE id = $1::uuid`, stepID)
	return err
}

// SettlePlanForSession is the deterministic settle mechanic (Rule #1: in code,
// never via the LLM remembering to call plan_update). A detached background.build
// drives the parent session's ONE durable plan through todo_write; when the build
// finishes, this closes the loop regardless of whether the LLM's final todo_write
// landed or the task was trivial (no checklist at all):
//
//   - success: every non-terminal step -> done (the build ran the task to
//     completion), each step's open 'plan.step' spinner run closed ok, plan
//     recomputes to completed.
//   - failure: the in_progress (or first non-terminal) step -> failed carrying
//     the REAL error, its run closed error (human_error populated), plan pauses
//     and surfaces under "Awaiting you".
//
// No-op when the session has no active plan (the build wasn't plan-shaped).
// Returns the refreshed plan (nil when there was none).
func (s *Store) SettlePlanForSession(ctx context.Context, sessionID, status, summary string) (*Plan, error) {
	if s == nil || s.pool == nil || sessionID == "" {
		return nil, nil
	}
	p, err := s.GetActiveBySession(ctx, sessionID)
	if err != nil || p == nil {
		return nil, err
	}
	status = NormalizeStepStatus(status)

	if status == StepFailed {
		target := stepInFlight(p)
		if target == nil {
			return p, nil // nothing in flight to fail (already settled)
		}
		if _, err := s.MarkStep(ctx, target.ID, StepFailed, firstNonEmptySummary(summary, "background build failed")); err != nil {
			return nil, err
		}
		s.closeStepRun(ctx, target, StepFailed, summary)
		return s.Get(ctx, p.ID)
	}

	// Success: drive every non-terminal step to done and settle its run.
	for i := range p.Steps {
		st := p.Steps[i]
		if isTerminalStep(st.Status) {
			continue
		}
		stepSummary := ""
		if st.Status == StepInProgress {
			stepSummary = summary
		}
		if _, err := s.MarkStep(ctx, st.ID, StepDone, stepSummary); err != nil {
			return nil, err
		}
		s.closeStepRun(ctx, &st, StepDone, stepSummary)
	}
	return s.Get(ctx, p.ID)
}

// closeStepRun settles a step's own 'plan.step' spinner run so the live
// indicator stops instead of spinning until the reaper. Failure carries the
// real error so runs.FinishByID humanizes it into human_error.
func (s *Store) closeStepRun(ctx context.Context, st *Step, status, summary string) {
	if st == nil || st.RunID == "" {
		return
	}
	var runErr error
	if status == StepFailed {
		runErr = errors.New(firstNonEmptySummary(summary, "step failed"))
	}
	runs.FinishByID(ctx, st.RunID, runErr, summary)
}

// stepInFlight returns the in_progress step, else the first non-terminal step.
func stepInFlight(p *Plan) *Step {
	if p == nil {
		return nil
	}
	var firstPending *Step
	for i := range p.Steps {
		if p.Steps[i].Status == StepInProgress {
			return &p.Steps[i]
		}
		if firstPending == nil && !isTerminalStep(p.Steps[i].Status) {
			firstPending = &p.Steps[i]
		}
	}
	return firstPending
}

// isTerminalStep reports whether a step status is settled (no further work).
func isTerminalStep(status string) bool {
	switch status {
	case StepDone, StepFailed, StepSkipped:
		return true
	default:
		return false
	}
}

// firstNonEmptySummary returns the first non-blank string, or the last fallback.
func firstNonEmptySummary(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// SetStatus forces a plan's lifecycle status (used by checkpoint resolution +
// explicit cancel).
func (s *Store) SetStatus(ctx context.Context, planID, status string) error {
	if s == nil || s.pool == nil || planID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE mem_plans SET status = $2, updated_at = NOW() WHERE id = $1::uuid`, planID, status)
	return err
}

// recompute sets current_step to the first non-terminal step and rolls the
// plan to a terminal status when every step is resolved.
// CancelActive cancels the session's current active/paused plan - the boss's
// "kill the plan". Cancelled (not deleted) so history survives, and PlanProvider
// stops injecting it so it drops out of the agent's context and off the live
// plan dock. Returns the cancelled plan, or nil if the session had none.
func (s *Store) CancelActive(ctx context.Context, sessionID string) (*Plan, error) {
	if s == nil || s.pool == nil || sessionID == "" {
		return nil, nil
	}
	var planID string
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_plans SET status = 'cancelled', updated_at = NOW()
		 WHERE session_id = $1::uuid AND status IN ('active','paused')
		RETURNING id::text
	`, sessionID).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("cancel active plan: %w", err)
	}
	return s.Get(ctx, planID)
}

// Cancel cancels a specific plan by id (no-op if it's already terminal).
func (s *Store) Cancel(ctx context.Context, planID string) (*Plan, error) {
	if s == nil || s.pool == nil || planID == "" {
		return nil, errors.New("plan store not configured")
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE mem_plans SET status = 'cancelled', updated_at = NOW()
		 WHERE id = $1::uuid AND status NOT IN ('completed','failed','cancelled')
	`, planID); err != nil {
		return nil, fmt.Errorf("cancel plan: %w", err)
	}
	return s.Get(ctx, planID)
}

// EditStep rewrites a step's title and/or detail in place (empty = unchanged).
// This is how a plan adapts when work diverts - a step is repurposed rather than
// faked done or the whole plan thrown away.
func (s *Store) EditStep(ctx context.Context, stepID, title, detail string) error {
	if s == nil || s.pool == nil || stepID == "" {
		return errors.New("plan store not configured")
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_plan_steps
		   SET title  = CASE WHEN $2 = '' THEN title  ELSE $2 END,
		       detail = CASE WHEN $3 = '' THEN detail ELSE $3 END
		 WHERE id = $1::uuid
	`, stepID, title, detail)
	return err
}

// RemoveStep deletes a step (prune). Returns the parent plan id so the caller
// can renumber + recompute.
func (s *Store) RemoveStep(ctx context.Context, stepID string) (string, error) {
	if s == nil || s.pool == nil || stepID == "" {
		return "", errors.New("plan store not configured")
	}
	var planID string
	err := s.pool.QueryRow(ctx, `
		DELETE FROM mem_plan_steps WHERE id = $1::uuid RETURNING plan_id::text
	`, stepID).Scan(&planID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return planID, err
}

// AppendStep adds a new pending step at the end of a plan.
func (s *Store) AppendStep(ctx context.Context, planID string, in NewStepInput) error {
	if s == nil || s.pool == nil || planID == "" {
		return errors.New("plan store not configured")
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_plan_steps (plan_id, idx, title, detail, status, is_checkpoint, verify_required)
		VALUES ($1::uuid,
		        (SELECT COALESCE(MAX(idx)+1, 0) FROM mem_plan_steps WHERE plan_id = $1::uuid),
		        $2, $3, 'pending', $4, $5)
	`, planID, in.Title, in.Detail, in.IsCheckpoint, in.VerifyRequired)
	return err
}

// RenumberSteps compacts idx to 0..n-1 in current idx order (call after prunes
// so positions stay contiguous).
func (s *Store) RenumberSteps(ctx context.Context, planID string) error {
	if s == nil || s.pool == nil || planID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		WITH ordered AS (
			SELECT id, ROW_NUMBER() OVER (ORDER BY idx ASC) - 1 AS new_idx
			  FROM mem_plan_steps WHERE plan_id = $1::uuid
		)
		UPDATE mem_plan_steps s SET idx = o.new_idx
		  FROM ordered o WHERE s.id = o.id AND s.idx <> o.new_idx
	`, planID)
	return err
}

// Recompute exposes lifecycle recomputation so tools can refresh a plan's
// status/current_step after structural edits.
func (s *Store) Recompute(ctx context.Context, planID string) error {
	return s.recompute(ctx, planID)
}

func (s *Store) recompute(ctx context.Context, planID string) error {
	steps, err := s.steps(ctx, planID)
	if err != nil {
		return err
	}
	current := len(steps)
	anyFailed := false
	anyBlocked := false
	anyCheckpointPause := false
	allTerminal := true
	for _, st := range steps {
		switch st.Status {
		case StepDone, StepSkipped:
			// terminal-ok
		case StepFailed:
			anyFailed = true
		case StepBlocked:
			anyBlocked = true
			allTerminal = false
			if current == len(steps) {
				current = st.Idx
			}
		default: // pending / in_progress
			allTerminal = false
			if current == len(steps) {
				current = st.Idx
			}
			if st.IsCheckpoint && st.Status == StepPending {
				anyCheckpointPause = true
			}
		}
	}

	status := PlanActive
	switch {
	case allTerminal && anyFailed:
		status = PlanFailed
	case allTerminal:
		status = PlanCompleted
	case anyBlocked || anyCheckpointPause || anyFailed:
		// A failed step pauses the whole plan even while later steps are still
		// pending, so a half-abandoned plan surfaces as needs-attention instead
		// of lingering silently as 'active' forever. The agent then either
		// replans the failure or closes the plan out (marks the rest skipped ->
		// terminal -> it drops out of context).
		status = PlanPaused
	}
	if current > len(steps)-1 {
		current = max(0, len(steps)-1)
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE mem_plans SET current_step = $2, status = $3, updated_at = NOW() WHERE id = $1::uuid
	`, planID, current, status)
	return err
}
