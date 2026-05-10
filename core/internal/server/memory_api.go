package server

import (
	"net/http"

	"github.com/dopesoft/infinity/core/internal/memory"
)

func (s *Server) handleMemoryCounts(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, map[string]int{"observations": 0, "memories": 0, "graph_nodes": 0, "stale": 0, "sessions": 0})
		return
	}
	counts, err := s.store.Counts(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, counts)
}

func (s *Server) handleMemorySearch(w http.ResponseWriter, r *http.Request) {
	if s.searcher == nil {
		writeJSON(w, http.StatusOK, []memory.SearchResult{})
		return
	}
	q := r.URL.Query().Get("q")
	if q == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "q required"})
		return
	}
	results, err := s.searcher.Search(r.Context(), q, memory.SearchOpts{Limit: 25, IncludeBreakdown: true})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, results)
}

func (s *Server) handleObservations(w http.ResponseWriter, r *http.Request) {
	if s.store == nil {
		writeJSON(w, http.StatusOK, []memory.Observation{})
		return
	}
	obs, err := s.store.RecentObservations(r.Context(), 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, obs)
}
