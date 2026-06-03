package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/google/uuid"
)

type workCancelReq struct {
	Kind      string `json:"kind"`       // work item kind (plan | cron_run | skill_run | workflow)
	ID        string `json:"id"`         // the item's id (plan id, or run id for a cron_run)
	SessionID string `json:"session_id"` // the turn's session, when known
}

// handleWorkCancel stops a work item from the Agent Work board — a running OR
// awaiting plan / cron run / agent turn. The boss had no way to kill one; this
// is it. It (1) aborts the in-flight agent turn for the session so the agent
// actually stops working (not just hides the card), (2) cancels the owning
// plan, and (3) closes any still-'running' mem_runs for the session so the card
// clears immediately. Every step is best-effort + idempotent: cancelling an
// already-finished item just closes its lingering bookkeeping.
func (s *Server) handleWorkCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]any{"error": "method not allowed"})
		return
	}
	var req workCancelReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "bad request"})
		return
	}
	if req.SessionID == "" && req.ID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id or id required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	// 1. Abort the live turn (cron / heartbeat / chat all run through Loop.Run).
	cancelledTurn := false
	if req.SessionID != "" && s.loop != nil {
		cancelledTurn = s.loop.CancelSession(req.SessionID)
	}

	// 2. Cancel the owning plan. Prefer the explicit plan id (works for running
	//    AND awaiting/paused plans); fall back to the session's active plan.
	if s.pool != nil {
		store := plan.NewStore(s.pool)
		if req.Kind == "plan" && req.ID != "" {
			_, _ = store.Cancel(ctx, req.ID)
		} else if req.SessionID != "" {
			_, _ = store.CancelActive(ctx, req.SessionID)
		}
		// 3. Close any still-running runs for this session (cron run + plan.step)
		//    so the spinner/card clears even if the turn already ended.
		s.closeRunsForCancel(ctx, req.SessionID, req.ID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "cancelled_turn": cancelledTurn})
}

// closeRunsForCancel marks any 'running' mem_runs tied to the cancelled item as
// errored with a "(cancelled)" summary: the session's own run (meta.session_id),
// its plan.step runs (via the plan join), and the explicit run id when given.
func (s *Server) closeRunsForCancel(ctx context.Context, sessionID, runID string) {
	const closeSQL = `status='error', ended_at=NOW(),
		duration_ms = COALESCE(duration_ms, LEAST(2147483647, GREATEST(0,
			EXTRACT(EPOCH FROM (NOW()-started_at))*1000))::int),
		error = COALESCE(NULLIF(error,''), 'cancelled by boss'),
		result_summary = COALESCE(NULLIF(result_summary,''), '(cancelled)')`

	if sessionID != "" {
		if _, err := uuid.Parse(sessionID); err == nil {
			// The cron/turn run (session id lives in meta) + any plan.step runs
			// whose step belongs to this session's plan.
			_, _ = s.pool.Exec(ctx, `
				UPDATE mem_runs SET `+closeSQL+`
				 WHERE status='running'
				   AND ( meta->>'session_id' = $1
				      OR id IN (
				           SELECT st.run_id FROM mem_plan_steps st
				             JOIN mem_plans p ON p.id = st.plan_id
				            WHERE p.session_id = $1::uuid AND st.run_id IS NOT NULL ) )
			`, sessionID)
		}
	}
	if runID != "" {
		if _, err := uuid.Parse(runID); err == nil {
			_, _ = s.pool.Exec(ctx, `UPDATE mem_runs SET `+closeSQL+` WHERE id = $1::uuid AND status='running'`, runID)
		}
	}
}
