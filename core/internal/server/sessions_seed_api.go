package server

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

// handleSessionsSeed creates a fresh session pre-bound to a dashboard
// item so the agent loop can hydrate the artifact as a sticky Context
// Block on the first turn.
//
//	POST /api/sessions/seed
//	  { "kind": "followup", "id": "<uuid>" }
//	→ { "id": "<new-session-uuid>" }
//
// The seeded_from JSONB column carries {kind, id}. The agent loop reads
// it when building the system prompt for turn 1 and emits a "Context"
// block with the artifact's native form — the same shape Studio's
// ObjectViewer renders.
//
// The Discuss-with-Jarvis CTA in ObjectViewer hits this endpoint and
// navigates the user to /live?session=<id>. From there everything is
// normal session behavior — replies stream over the WS, future turns
// can call task_done / followup_dismiss / etc. to mutate the source
// artifact back from chat.
func (s *Server) handleSessionsSeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if s.pool == nil {
		writeError(w, http.StatusServiceUnavailable, "no database")
		return
	}
	var body struct {
		Kind string `json:"kind"`
		ID   string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}
	if body.Kind == "" {
		writeError(w, http.StatusBadRequest, "kind required")
		return
	}
	// id can be empty for kinds where the artifact doesn't have a stable
	// uuid yet (e.g. "scratch" seed). For everything else, require it so
	// the agent loop can actually fetch the artifact.
	if !isSeedKindWithoutID(body.Kind) && body.ID == "" {
		writeError(w, http.StatusBadRequest, "id required for this kind")
		return
	}

	seedJSON, _ := json.Marshal(map[string]any{
		"kind": body.Kind,
		"id":   body.ID,
	})

	id := uuid.New()
	_, err := s.pool.Exec(r.Context(), `
		INSERT INTO mem_sessions (id, started_at, seeded_from)
		VALUES ($1, NOW(), $2::jsonb)
	`, id, string(seedJSON))
	if err != nil {
		writeError(w, http.StatusInternalServerError, fmt.Sprintf("create session: %v", err))
		return
	}

	writeJSONResponse(w, http.StatusOK, map[string]any{
		"id":       id.String(),
		"seededFrom": map[string]any{"kind": body.Kind, "id": body.ID},
	})
}

// isSeedKindWithoutID lists discriminated-union kinds that can spawn a
// seeded session without a concrete artifact id (e.g. "scratch" notes).
// Most dashboard items DO require an id — keep this list short and
// explicit.
func isSeedKindWithoutID(kind string) bool {
	switch kind {
	case "scratch":
		return true
	default:
		return false
	}
}

// Local helpers — server.go has its own writeJSON/writeErr but they're
// package-internal and named differently. Mirror them here so this file
// stays self-contained without cross-file knowledge.

func writeJSONResponse(w http.ResponseWriter, status int, body any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSONResponse(w, status, map[string]any{"error": msg})
}
