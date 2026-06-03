package plan

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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
func (s *Store) ListByStatuses(ctx context.Context, statuses []string, limit int) ([]Plan, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text FROM mem_plans
		 WHERE status = ANY($1)
		 ORDER BY updated_at DESC
		 LIMIT $2
	`, statuses, limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()

	out := make([]Plan, 0, len(ids))
	for _, id := range ids {
		p, err := s.Get(ctx, id)
		if err != nil || p == nil {
			continue
		}
		out = append(out, *p)
	}
	return out, nil
}

func (s *Store) steps(ctx context.Context, planID string) ([]Step, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, plan_id::text, idx, title, detail, status,
		       is_checkpoint, verify_required, verify_result::text,
		       result_summary, run_id::text, started_at, ended_at
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
			&st.ResultSummary, &runID, &started, &ended); err != nil {
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

// SetStepRun links a step to the mem_runs row tracking its execution so the
// UI can show a navigation-proof live spinner.
func (s *Store) SetStepRun(ctx context.Context, stepID, runID string) error {
	if s == nil || s.pool == nil || stepID == "" || runID == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `UPDATE mem_plan_steps SET run_id = $2::uuid WHERE id = $1::uuid`, stepID, runID)
	return err
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
	case anyBlocked || anyCheckpointPause:
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
