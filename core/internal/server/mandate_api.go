package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/mandate"
)

// /api/mandates
//
//	GET            → recent mandates (definitions of done). ?active=1 narrows to
//	                 open/verifying. ?limit=N.
//
// /api/mandates/<id>
//
//	GET            → one mandate with its criteria + crosscheck verdict.
//
// Read-only; the agent writes mandates through the mandate_* tools, and the
// Studio dashboard/Memory surfaces render these.
func (s *Server) handleMandates(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	store := mandate.NewStore(s.pool)
	activeOnly := r.URL.Query().Get("active") == "1" || r.URL.Query().Get("active") == "true"
	limit := 50
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	items, err := store.List(r.Context(), activeOnly, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if items == nil {
		items = []*mandate.Mandate{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) handleMandate(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/mandates/")
	id = strings.Trim(id, "/")
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "mandate id required"})
		return
	}
	store := mandate.NewStore(s.pool)
	m, err := store.Get(r.Context(), id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "mandate not found"})
		return
	}
	writeJSON(w, http.StatusOK, m)
}
