package server

import (
	"errors"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/pursuits/jh"
)

// Job Hunt cockpit API — the read side.
//
// One endpoint, scoped to a pursuit whose mem_pursuits.experience is
// 'job_hunt'. It returns the whole board in a single payload (pipeline,
// interview corpus, contacts, artifacts, derived counts, and the vocabularies
// the client renders columns from) so the cockpit never assembles itself from
// several reads that could disagree with each other.
//
//	GET /api/pursuits/jh/state?pursuit_id=<uuid>   → full cockpit
//
// Pointing this at an ordinary pursuit, or at one running a different
// experience, is a 409. It is never an empty board: empty-because-you-aimed-at
// -the-wrong-pursuit must not be mistakable for empty-because-nothing-is-filed
// -yet.
func (s *Server) handlePursuitsJH(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/pursuits/jh/")
	if action != "state" {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}
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

	cockpit, err := jh.NewStore(s.pool).Cockpit(r.Context(), pursuitID)
	if err != nil {
		writeJHError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, cockpit)
}

// writeJHError maps store errors onto honest status codes, mirroring
// writePCError. errors.Is rather than == because the store wraps almost
// everything with fmt.Errorf("...: %w", err), and a mapper comparing directly
// would downgrade every wrong-experience rejection to a generic 400.
func writeJHError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, jh.ErrNoPursuit):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pursuit not found"})
	case errors.Is(err, jh.ErrNotJobHunt):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}
