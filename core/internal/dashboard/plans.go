package dashboard

import (
	"context"
	"encoding/json"
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
// planStaleAfter is how long a non-terminal plan may sit untouched before the
// board stops calling it live.
//
// The bound comes from the runtime, not taste: a cron agent turn runs under a
// 30 minute context budget, so a genuinely-working plan updates a step well
// inside that. 45 minutes gives a slow step half again as long as the longest
// legitimate one before we call it quiet. Anything past that is not running -
// it is a row nobody closed.
const planStaleAfter = 45 * time.Minute

// planStaleness answers the two questions that decide whether a plan belongs
// on the board at all, given only its status and when it was last touched.
//
// Pure and separate from the query so the rule can be tested against the exact
// rows that were lying in production, rather than living as three inline
// branches nobody can exercise without a database.
func planStaleness(status string, updatedAt, now time.Time) (terminal, stale bool) {
	terminal = status == plan.PlanCompleted || status == plan.PlanFailed || status == plan.PlanCancelled

	// A PROPOSAL is exempt, and this is the important carve-out. It is not
	// work that stalled, it is a question addressed to the boss, and it waits
	// as long as he takes. Its age says nothing about whether it is still
	// live, so ageing one out would quietly retire a decision he never made -
	// which is the same crime as the phantom "Running" row, pointed at
	// something he actually cares about.
	if status == plan.PlanProposed {
		return terminal, false
	}

	stale = !terminal && updatedAt.Before(now.Add(-planStaleAfter))
	return terminal, stale
}

func (a *API) planWorkItems(ctx context.Context) ([]WorkItem, error) {
	if a == nil || a.Pool == nil {
		return nil, nil
	}
	store := plan.NewStore(a.Pool)
	plans, err := store.ListByStatuses(ctx,
		[]string{plan.PlanProposed, plan.PlanActive, plan.PlanPaused, plan.PlanCompleted, plan.PlanFailed, plan.PlanCancelled},
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
	// The cron run that drove each plan carries the boss-facing narrative +
	// outcome class on its mem_runs row. Fold that onto the plan card so the
	// reason ("stopped early because the tree was dirty") is visible instead of
	// stranded on the folded-away run card.
	runInfo := a.cronRunsBySession(ctx, sessionIDs)

	now := time.Now().UTC()
	startOfDay := now.Truncate(24 * time.Hour)
	out := make([]WorkItem, 0, len(plans))
	for _, p := range plans {
		// Terminal plans only surface if they finished today, so the Done
		// column reflects "today" like every other work item.
		// A plan that has not been touched in `planStaleAfter` is NOT running,
		// whatever its row still says. This is the same law as "empty because
		// broken must never read as empty because fine", pointed the other
		// way: DEAD MUST NEVER READ AS ALIVE.
		//
		// The rescue below can only fire for a plan whose session produced a
		// finished run. A plan orphaned hard enough - the process died, the
		// session was never written, the retry minted a new one and left this
		// row behind - has no run to consult, so nothing could ever move it
		// and it sat in Running forever. Verified in prod on 2026-08-29: four
		// plans stuck 'active' for three days with no session id at all, plus
		// ten 'paused' rows in the needs-you lane, the oldest quiet since 8
		// June. Every one of them read to the boss as work in flight.
		//
		// Time is the one signal that does not depend on any of that
		// bookkeeping having survived, so time is what decides.
		terminal, stale := planStaleness(p.Status, p.UpdatedAt, now)

		if (terminal || stale) && p.UpdatedAt.Before(startOfDay) {
			// Not today's work. The board is what is going on now, and a job
			// that went quiet days ago is history, not status.
			continue
		}

		steps, done := toPlanSteps(p.Steps)
		total := len(steps)

		column := "running"
		sub := ""
		switch p.Status {
		case plan.PlanProposed:
			column = "awaiting"
			sub = "proposed, waiting for your go"
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

		// Went quiet today: it is honestly finished-without-finishing. Not
		// "done" (it completed nothing) and not "awaiting" (it is not waiting
		// on him) - it stopped, and the row says so in those words.
		if stale {
			column = "done"
			sub = "stopped without finishing"
		}

		// Outcome-driven placement + narrative fold. When the cron run that
		// drove this plan has FINISHED (not still in-flight), its outcome class
		// decides where the card lands: only a genuine pending decision
		// (needs_you) sits in "awaiting you"; a plan the agent left incomplete
		// on its own reads as "stopped early" in Done with its reason on the
		// card — never a cryptic "paused at checkpoint" in the needs-you lane.
		summary := ""
		if info, ok := runInfo[p.SessionID]; ok && !stale {
			summary = info.summary
			finished := info.status != "" && info.status != "running"
			if finished && (p.Status == plan.PlanActive || p.Status == plan.PlanPaused) {
				switch info.outcome {
				case "needs_you":
					column = "awaiting"
					sub = "needs your okay"
				case "failed":
					column = "done"
					sub = "failed"
				case "nothing_needed":
					column = "done"
					sub = "nothing to do"
				default:
					column = "done"
					sub = "stopped early"
				}
			}
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
			Summary:    summary,
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

// cronRunInfo is the slice of a cron's mem_runs row the plan card needs: the
// run's lifecycle status, its boss-facing outcome class, and the narrative the
// executor wrote. Keyed by session id so it folds onto the plan that shares it.
type cronRunInfo struct {
	status  string
	outcome string
	summary string
}

// cronRunsBySession returns the most-recent cron run per session id, so a plan
// card can show that run's outcome + narrative. DISTINCT ON keeps the latest
// fire when a cron has run the same session more than once.
func (a *API) cronRunsBySession(ctx context.Context, sessionIDs []string) map[string]cronRunInfo {
	if a == nil || a.Pool == nil || len(sessionIDs) == 0 {
		return nil
	}
	rows, err := a.Pool.Query(ctx, `
		SELECT DISTINCT ON (meta->>'session_id')
		       meta->>'session_id',
		       status,
		       COALESCE(meta->>'outcome', ''),
		       COALESCE(result_summary, '')
		  FROM mem_runs
		 WHERE kind = 'cron' AND meta->>'session_id' = ANY($1)
		 ORDER BY meta->>'session_id', started_at DESC
	`, sessionIDs)
	if err != nil {
		return nil
	}
	defer rows.Close()
	out := map[string]cronRunInfo{}
	for rows.Next() {
		var sid string
		var info cronRunInfo
		if err := rows.Scan(&sid, &info.status, &info.outcome, &info.summary); err != nil {
			return out
		}
		if sid != "" {
			out[sid] = info
		}
	}
	return out
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

// planDecisionBody is the JSON Studio posts to approve / discard a plan.
type planDecisionBody struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
}

func (a *API) writePlanDTO(w http.ResponseWriter, p *plan.Plan) {
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

// handlePlanGet serves GET /api/plans/get?id= - one plan with steps, any
// status. The chat proposal card reads it on mount so a decision made
// elsewhere (work board, Jarvis via plan_approve) shows on reload.
func (a *API) handlePlanGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" || a.Pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plan": nil})
		return
	}
	p, err := plan.NewStore(a.Pool).Get(r.Context(), id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to load plan")
		return
	}
	a.writePlanDTO(w, p)
}

// handlePlanApprove serves POST /api/plans/approve {id, session_id?}: the
// boss's "Go ahead" on a proposed plan. Flips it to active + stamps
// approved_at; the chat message Studio sends alongside is what makes Jarvis
// start on it in the same conversation.
func (a *API) handlePlanApprove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body planDecisionBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil || body.ID == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	if a.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	p, err := plan.NewStore(a.Pool).Approve(r.Context(), body.ID, body.SessionID)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a.writePlanDTO(w, p)
}

// handlePlanDiscard serves POST /api/plans/discard {id}: the boss's "Not
// yet". The proposal is cancelled (kept for history), nothing was built.
func (a *API) handlePlanDiscard(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body planDecisionBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<14)).Decode(&body); err != nil || body.ID == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	if a.Pool == nil {
		writeErr(w, http.StatusServiceUnavailable, "no database")
		return
	}
	p, err := plan.NewStore(a.Pool).Cancel(r.Context(), body.ID)
	if err != nil {
		writeErr(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	a.writePlanDTO(w, p)
}
