package dashboard

import (
	"context"
	"net/http"
	"time"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// Plan + PlanStep are the camelCase DTOs Studio renders (the dashboard PlanCard,
// the /plans page, and the detail timeline). They mirror plan.Plan / plan.Step
// but follow the dashboard package's JSON convention so the frontend types stay
// uniform across sections.
type Plan struct {
	ID          string     `json:"id"`
	Title       string     `json:"title"`
	Goal        string     `json:"goal,omitempty"`
	Status      string     `json:"status"`
	CurrentStep int        `json:"currentStep"`
	CreatedAt   time.Time  `json:"createdAt"`
	UpdatedAt   time.Time  `json:"updatedAt"`
	Steps       []PlanStep `json:"steps"`
	// Derived counts so the card can render "3/6" without walking steps.
	DoneCount  int `json:"doneCount"`
	TotalCount int `json:"totalCount"`
}

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

func toPlanDTO(p plan.Plan) Plan {
	out := Plan{
		ID:          p.ID,
		Title:       p.Title,
		Goal:        p.Goal,
		Status:      p.Status,
		CurrentStep: p.CurrentStep,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
		Steps:       make([]PlanStep, 0, len(p.Steps)),
		TotalCount:  len(p.Steps),
	}
	for _, s := range p.Steps {
		if s.Status == plan.StepDone || s.Status == plan.StepSkipped {
			out.DoneCount++
		}
		out.Steps = append(out.Steps, PlanStep{
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
	return out
}

// loadPlans returns the active + paused plans for the dashboard card.
func (a *API) loadPlans(ctx context.Context) ([]Plan, error) {
	if a == nil || a.Pool == nil {
		return nil, nil
	}
	store := plan.NewStore(a.Pool)
	plans, err := store.ListByStatuses(ctx, []string{plan.PlanActive, plan.PlanPaused}, 10)
	if err != nil {
		return nil, err
	}
	out := make([]Plan, 0, len(plans))
	for _, p := range plans {
		out = append(out, toPlanDTO(p))
	}
	return out, nil
}

// handlePlans serves GET /api/plans?status= for the /plans page. Default
// (no status) returns active + paused; pass status to filter (completed,
// failed, cancelled, ...).
func (a *API) handlePlans(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if a.Pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plans": []Plan{}})
		return
	}
	statuses := []string{plan.PlanActive, plan.PlanPaused}
	if s := r.URL.Query().Get("status"); s != "" {
		statuses = []string{s}
	}
	store := plan.NewStore(a.Pool)
	plans, err := store.ListByStatuses(r.Context(), statuses, 100)
	if err != nil {
		a.Logger.Warn("dashboard: plans list", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to load plans")
		return
	}
	out := make([]Plan, 0, len(plans))
	for _, p := range plans {
		out = append(out, toPlanDTO(p))
	}
	writeJSON(w, http.StatusOK, map[string]any{"plans": out})
}

// handlePlanGet serves GET /api/plans/get?id= for the detail modal.
func (a *API) handlePlanGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	id := r.URL.Query().Get("id")
	if id == "" {
		writeErr(w, http.StatusBadRequest, "id required")
		return
	}
	if a.Pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plan": nil})
		return
	}
	store := plan.NewStore(a.Pool)
	p, err := store.Get(r.Context(), id)
	if err != nil {
		a.Logger.Warn("dashboard: plan get", "err", err)
		writeErr(w, http.StatusInternalServerError, "failed to load plan")
		return
	}
	if p == nil {
		writeJSON(w, http.StatusOK, map[string]any{"plan": nil})
		return
	}
	dto := toPlanDTO(*p)
	writeJSON(w, http.StatusOK, map[string]any{"plan": dto})
}
