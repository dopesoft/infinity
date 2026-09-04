package server

import (
	"context"
	"net/http"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/runs"
)

// handleSessionCompact is POST /api/sessions/{id}/compact: the Compact
// action on the context meter. Booked through runs.Track so the spinner is
// server state (survives navigation, refresh, a second device) and the run
// lands in the Runs lens like every other long action.
func (s *Server) handleSessionCompact(w http.ResponseWriter, r *http.Request, sessionID string) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.loop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent loop not configured"})
		return
	}
	var (
		res memory.CompactionResult
		err error
	)
	_ = runs.Track(r.Context(), runs.KindCompact, sessionID, "Compact this conversation", runs.SourceManual, func(ctx context.Context) error {
		res, err = s.loop.CompactSession(ctx, sessionID)
		return err
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"compacted":       res.CompactedTurns > 0,
		"compacted_turns": res.CompactedTurns,
		"kept_turns":      res.KeptTurns,
		"summary_chars":   res.SummaryChars,
		"observations":    len(res.ObservationIDs),
	})
}
