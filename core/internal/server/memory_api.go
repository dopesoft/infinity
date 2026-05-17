package server

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/dopesoft/infinity/core/internal/embed"
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

// /api/memory/cite/<memoryID>
func (s *Server) handleMemoryCite(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/memory/cite/")
	id = strings.TrimSpace(id)
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "memory id required"})
		return
	}
	chain, err := memory.Cite(r.Context(), s.pool, id)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, chain)
}

// /api/memory/memories - list memories (filtered by tier/project/q)
func (s *Server) handleMemoryList(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, []memory.Memory{})
		return
	}
	mems, err := memory.ListMemories(r.Context(), s.pool, memory.ListOpts{
		Tier:    r.URL.Query().Get("tier"),
		Project: r.URL.Query().Get("project"),
		Limit:   50,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, mems)
}

// /api/memory/reflections - recent metacognitive session critiques.
func (s *Server) handleMemoryReflections(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, []memory.Reflection{})
		return
	}
	limit := intQuery(r, "limit", 25)
	reflector := memory.NewReflector(s.pool, embed.NewStub(), nil)
	rows, err := reflector.Reflections(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

// /api/memory/predictions - high-surprise predict-then-act rows.
func (s *Server) handleMemoryPredictions(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, []memory.PredictionRow{})
		return
	}
	limit := intQuery(r, "limit", 25)
	threshold := floatQuery(r, "threshold", 0.7)
	rows, err := memory.NewPredictionStore(s.pool).HighSurprise(r.Context(), threshold, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func intQuery(r *http.Request, key string, fallback int) int {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func floatQuery(r *http.Request, key string, fallback float64) float64 {
	v := strings.TrimSpace(r.URL.Query().Get(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return fallback
	}
	return n
}
