package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/dopesoft/infinity/core/internal/plasticity"
	"github.com/dopesoft/infinity/core/internal/runs"
)

func (s *Server) handleGym(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.handleGymAction(w, r)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusOK, plasticity.Snapshot{})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	snap, err := plasticity.NewStore(s.pool).Snapshot(r.Context(), limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, snap)
}

func (s *Server) handleGymAction(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusOK, plasticity.ExtractResult{})
		return
	}
	var body struct {
		Action string `json:"action"`
		Limit  int    `json:"limit"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body.Action != "extract_examples" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unknown gym action"})
		return
	}
	// runs.Track surfaces "gym extract is running" to every device,
	// because the extract pass can chew through 50+ artifacts and is
	// worth seeing live. See CLAUDE.md → "Server-tracked progress".
	var (
		result plasticity.ExtractResult
		err    error
	)
	_ = runs.Track(r.Context(), runs.KindGymExtract, "", "extract examples", runs.SourceManual, func(ctx context.Context) error {
		result, err = plasticity.NewStore(s.pool).ExtractExamples(ctx, body.Limit)
		return err
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}
