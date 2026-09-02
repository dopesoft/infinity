package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/pursuits/jh"
)

// Job Hunt cockpit API.
//
// One read and a set of writes, all scoped to a pursuit whose
// mem_pursuits.experience is 'job_hunt'. The read returns the whole board in a
// single payload (pipeline, interview corpus, contacts, artifacts, derived
// counts, and the vocabularies the client renders columns from) so the cockpit
// never assembles itself from several reads that could disagree with each
// other. Every write routes through jh.Store.Apply — the same chokepoint the
// pursuit_jh_write agent tool uses — so a role moved on the board and the same
// role moved from chat produce byte-identical state.
//
//	GET  /api/pursuits/jh/state?pursuit_id=<uuid>   → full cockpit
//	POST /api/pursuits/jh/role                      → file or re-file a role
//	POST /api/pursuits/jh/role/stage                → move a role between stages
//	POST /api/pursuits/jh/corpus                    → bank an interview answer
//	POST /api/pursuits/jh/contact                   → add or patch a contact
//	POST /api/pursuits/jh/contact/status            → move the outreach ladder
//	POST /api/pursuits/jh/artifact                  → file a document on a role
//	POST /api/pursuits/jh/artifact/status           → move the approval ladder
//
// Every write answers with the refreshed board rather than an acknowledgement,
// so a client is never one mutation ahead of what it is rendering and never has
// to follow a write with a read to stay in sync. It costs one extra query and
// removes a whole class of stale-board bug.
//
// Pointing any of this at an ordinary pursuit, or at one running a different
// experience, is a 409. It is never an empty board: empty-because-you-aimed-at
// -the-wrong-pursuit must not be mistakable for empty-because-nothing-is-filed
// -yet.
func (s *Server) handlePursuitsJH(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	action := strings.TrimPrefix(r.URL.Path, "/api/pursuits/jh/")
	store := jh.NewStore(s.pool)

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
		cockpit, err := store.Cockpit(r.Context(), pursuitID)
		if err != nil {
			writeJHError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, cockpit)
		return
	}

	// The route is registered as a prefix, so anything at all reaches here. A
	// suffix that is neither the read nor a known write is a 404 BEFORE the
	// method check, so a typo reads as "no such thing" rather than as "wrong
	// verb for a thing that does not exist".
	if !jh.IsWriteAction(action) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}
	// A write arriving as a GET is refused with the verb named. Without this a
	// link or a browser prefetch could move a role between stages.
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		return
	}

	// The wire payload is the shared WriteRequest plus the pursuit id.
	var body struct {
		PursuitID string `json:"pursuit_id"`
		jh.WriteRequest
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
		writeJHError(w, err)
		return
	}

	cockpit, err := store.Cockpit(r.Context(), pursuitID)
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
	case errors.Is(err, jh.ErrUnknownAction):
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
}
