package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/pursuits/pc"
)

// Psycho-Cybernetics cockpit API.
//
// One read and a set of writes, all scoped to a pursuit whose
// mem_pursuits.experience is 'psycho_cybernetics'. Every write routes through
// pc.Store.Apply - the same chokepoint the pursuit_pc_write agent tool uses -
// so a decision made in chat and a decision made in the cockpit produce
// byte-identical state. Pointing these endpoints at an ordinary pursuit is a
// 409, never a silent mutation.
//
//	GET  /api/pursuits/pc/state?pursuit_id=<uuid>   → full cockpit + guidance
//	POST /api/pursuits/pc/identity                  → set/edit identity + objective
//	POST /api/pursuits/pc/session                   → log a coaching session
//	POST /api/pursuits/pc/proof                     → pledge a proof action
//	POST /api/pursuits/pc/proof/taken               → mark a proof taken/untaken
//	POST /api/pursuits/pc/evidence                  → capture evidence or resistance
//	POST /api/pursuits/pc/memory                    → bank a success memory
//	POST /api/pursuits/pc/pattern                   → log a pattern or correction
//	POST /api/pursuits/pc/review                    → close the cycle, open the next
//
// Writes return the refreshed cockpit so the client never has to follow a
// mutation with a second read to stay in sync.
func (s *Server) handlePursuitsPC(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/pursuits/pc/")
	store := pc.NewStore(s.pool)

	if action == "state" {
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
			return
		}
		pursuitID := strings.TrimSpace(r.URL.Query().Get("pursuit_id"))
		if pursuitID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pursuit_id required"})
			return
		}
		cockpit, err := store.Cockpit(r.Context(), pursuitID, time.Now())
		if err != nil {
			writePCError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cockpit)
		return
	}

	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// The wire payload is the shared WriteRequest plus the pursuit id.
	var body struct {
		PursuitID string `json:"pursuit_id"`
		pc.WriteRequest
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	pursuitID := strings.TrimSpace(body.PursuitID)
	if pursuitID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pursuit_id required"})
		return
	}

	if err := store.Apply(r.Context(), action, pursuitID, body.WriteRequest); err != nil {
		writePCError(w, err)
		return
	}

	cockpit, err := store.Cockpit(r.Context(), pursuitID, time.Now())
	if err != nil {
		writePCError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cockpit)
}

// writePCError maps store errors onto honest status codes. A pursuit that is
// not running this experience is a 409, never a silent success - the caller
// pointed at the wrong surface and needs to know.
func writePCError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pc.ErrNoPursuit):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pursuit not found"})
	case errors.Is(err, pc.ErrNotPsychoCybernetics):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, pc.ErrUnknownAction):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}
