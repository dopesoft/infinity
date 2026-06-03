package dashboard

import (
	"context"
	"net/http"
	"time"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// PlanStep is the camelCase DTO Studio renders inside a plan WorkItem's step
// timeline (ObjectViewer). It mirrors plan.Step but follows the dashboard
// package's JSON convention so the frontend types stay uniform.
//
// A plan is agent work, so it surfaces on the Agent Work board (loadWork) as a
// WorkItem of kind "plan" rather than on a separate page - the steps ride
// inline exactly like a workflow run's WorkflowSteps.
type PlanStep struct {
	ID             string         `json:"id"`
	Idx            int            `json:"idx"`
	Title          string         `json:"title"`
	Detail         string         `json:"detail,omitempty"`
	Status         string         `json:"status"`
	IsCheckpoint   bool           `json:"isCheckpoint"`
	VerifyRequired bool           `json:"verifyRequired"`
	VerifyResult   map[string]any `json:"verifyResult,omitempty"`
	ResultSummary  string         `json:"resultSummary,omitempty"`
	RunID          string         `json:"runId,omitempty"`
	StartedAt      *time.Time     `json:"startedAt,omitempty"`
	EndedAt        *time.Time     `json:"endedAt,omitempty"`
}

func toPlanSteps(steps []plan.Step) (out []PlanStep, done int) {
	out = make([]PlanStep, 0, len(steps))
	for _, s := range steps {
		if s.Status == plan.StepDone || s.Status == plan.StepSkipped {
			done++
		}
		out = append(out, PlanStep{
			ID:             s.ID,
			Idx:            s.Idx,
			Title:          s.Title,
			Detail:         s.Detail,
			Status:         s.Status,
			IsCheckpoint:   s.IsCheckpoint,
			VerifyRequired: s.VerifyRequired,
			VerifyResult:   s.VerifyResult,
			ResultSummary:  s.ResultSummary,
			RunID:          s.RunID,
			StartedAt:      s.StartedAt,
			EndedAt:        s.EndedAt,
		})
	}
	return out, done
}

// planSessionIDs returns the set of session_ids for every plan that
// planWorkItems would surface (active/paused, or terminal-but-updated-today).
//
// This is the key to NOT double-counting one job. A plan is the canonical,
// rich representation of a unit of agent work — it carries the title, the step
// timeline, the verdicts. But the SAME unit of work ALSO booked run rows: the
// cron that fired it (mem_runs kind 'cron' / mem_crons.last_run), the plan.step
// spinners, maybe a skill run. Left alone, loadWork draws a card for each, so a
// single nightly triage shows up as "inbox-triage (Done)" AND "Gmail triage…
// (Running)" — two cards, two names, one job. loadWork uses this set to suppress
// any run/cron/skill card whose session produced a surfaced plan: the plan wins,
// everything else in that session folds into it.
func (a *API) planSessionIDs(ctx context.Context) (map[string]bool, error) {
	if a == nil || a.Pool == nil {
		return map[string]bool{}, nil
	}
	rows, err := a.Pool.Query(ctx, `
		SELECT DISTINCT session_id
		FROM mem_plans
		WHERE session_id IS NOT NULL AND session_id <> ''
		  AND (
		        status IN ('active', 'paused')
		     OR (status IN ('completed', 'failed', 'cancelled')
		         AND updated_at >= date_trunc('day', NOW()))
		      )
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]bool{}
	for rows.Next() {
		var sid string
		if err := rows.Scan(&sid); err != nil {
			return nil, err
		}
		set[sid] = true
	}
	return set, rows.Err()
}

// planSkillsBySession returns, per session id, the distinct skills the agent
// invoked in that session — read from the durable observation record of
// skills_invoke tool calls (payload {"name":"skills_invoke","input":{"name":…}}).
// This is the generic, contract-driven source (mem_skill_runs isn't reliably
// session-linked), so the plan card can show "skills: inbox-triage" underneath
// its job headline with zero per-skill wiring. Best-effort: any error yields an
// empty map and the chip simply doesn't render.
func (a *API) planSkillsBySession(ctx context.Context, sessionIDs []string) map[string][]string {
	out := map[string][]string{}
	if a == nil || a.Pool == nil || len(sessionIDs) == 0 {
		return out
	}
	rows, err := a.Pool.Query(ctx, `
		SELECT session_id, payload->'input'->>'name' AS skill
		FROM mem_observations
		WHERE session_id = ANY($1)
		  AND hook_name = 'PreToolUse'
		  AND payload->>'name' = 'skills_invoke'
		  AND COALESCE(payload->'input'->>'name', '') <> ''
		GROUP BY session_id, payload->'input'->>'name'
	`, sessionIDs)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var sid, skill string
		if err := rows.Scan(&sid, &skill); err != nil {
			return out
		}
		out[sid] = append(out[sid], skill)
	}
	return out
}

// planWorkItems turns the agent's in-flight + finished-today plans into Agent
// Work board items. Mapping:
//   - active   -> Running   (with the doneCount/totalCount progress bar)
//   - paused   -> Awaiting you (paused at a checkpoint, needs the boss)
//   - completed/failed/cancelled, updated today -> Done today
//
// Steps ride inline so tapping the card opens the full timeline in ObjectViewer
// with no second fetch.
func (a *API) planWorkItems(ctx context.Context) ([]WorkItem, error) {
	if a == nil || a.Pool == nil {
		return nil, nil
	}
	store := plan.NewStore(a.Pool)
	plans, err := store.ListByStatuses(ctx,
		[]string{plan.PlanActive, plan.PlanPaused, plan.PlanCompleted, plan.PlanFailed, plan.PlanCancelled},
		40)
	if err != nil {
		return nil, err
	}

	sessionIDs := make([]string, 0, len(plans))
	for _, p := range plans {
		if p.SessionID != "" {
			sessionIDs = append(sessionIDs, p.SessionID)
		}
	}
	skillsBySession := a.planSkillsBySession(ctx, sessionIDs)

	startOfDay := time.Now().UTC().Truncate(24 * time.Hour)
	out := make([]WorkItem, 0, len(plans))
	for _, p := range plans {
		// Terminal plans only surface if they finished today, so the Done
		// column reflects "today" like every other work item.
		terminal := p.Status == plan.PlanCompleted || p.Status == plan.PlanFailed || p.Status == plan.PlanCancelled
		if terminal && p.UpdatedAt.Before(startOfDay) {
			continue
		}

		steps, done := toPlanSteps(p.Steps)
		total := len(steps)

		column := "running"
		sub := ""
		switch p.Status {
		case plan.PlanActive:
			column = "running"
			if next := firstOpenStep(p.Steps); next != "" {
				sub = "next: " + next
			}
		case plan.PlanPaused:
			column = "awaiting"
			sub = "paused at checkpoint"
		case plan.PlanCompleted:
			column = "done"
			sub = "completed"
		case plan.PlanFailed:
			column = "done"
			sub = "failed"
		case plan.PlanCancelled:
			column = "done"
			sub = "cancelled"
		}

		doneCount := done
		totalCount := total
		created := p.CreatedAt
		updated := p.UpdatedAt
		out = append(out, WorkItem{
			ID:         "plan-" + p.ID,
			Kind:       "plan",
			Title:      p.Title,
			Subtitle:   sub,
			Engine:     "Plan",
			Column:     column,
			SessionID:  p.SessionID,
			StartedAt:  &created,
			FinishedAt: &updated,
			PlanSteps:  steps,
			DoneCount:  &doneCount,
			TotalCount: &totalCount,
			Skills:     skillsBySession[p.SessionID],
			// The goal carries the model's own description of the work (and, for
			// job-launched plans, the descriptive title it would have used) — so
			// the card explains what it's doing beneath the job headline.
			Instruction: p.Goal,
		})
	}
	return out, nil
}

// Plan is the camelCase DTO for the single-plan read used by the chat dock
// (GET /api/plans/active). The dashboard board carries steps inline on the
// WorkItem instead; this endpoint serves the dock's session-scoped view so the
// chat and the dashboard render the exact same plan.
type Plan struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Goal        string     `json:"goal,omitempty"`
	Status      string     `json:"status"`
	CurrentStep int        `json:"currentStep"`
	DoneCount   int        `json:"doneCount"`
	TotalCount  int        `json:"totalCount"`
	Steps       []PlanStep `json:"steps"`
}

// handlePlanActive serves GET /api/plans/active?session_id= - the active or
// paused plan for a chat session (with steps), or {plan:null} when there's
// none. Powers the pinned chat dock; the dashboard board uses loadWork instead.
func (a *API) handlePlanActive(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	sid := r.URL.Query().Get("session_id")
	if sid == "" || a.Pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plan": nil})
		return
	}
	p, err := plan.NewStore(a.Pool).GetActiveBySession(r.Context(), sid)
	if err != nil {
		a.Logger.Warn("dashboard: active plan", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to load plan")
		return
	}
	if p == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plan": nil})
		return
	}
	steps, done := toPlanSteps(p.Steps)
	writeJSON(w, http.StatusOK, map[string]any{"plan": Plan{
		ID:          p.ID,
		Title:       p.Title,
		Goal:        p.Goal,
		Status:      p.Status,
		CurrentStep: p.CurrentStep,
		DoneCount:   done,
		TotalCount:  len(steps),
		Steps:       steps,
	}})
}

func firstOpenStep(steps []plan.Step) string {
	for _, s := range steps {
		if s.Status == plan.StepInProgress || s.Status == plan.StepBlocked {
			return s.Title
		}
	}
	for _, s := range steps {
		if s.Status == plan.StepPending {
			return s.Title
		}
	}
	return ""
}
