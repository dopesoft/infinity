package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/memory"
)

// handleMessageFeedback serves POST /api/messages/{id}/feedback.
//
// Body:
//
//	{"rating": "up" | "down" | null}
//
// A non-null rating writes an observation tagged hook_name="MessageFeedback"
// so the memory layer's retrieval surface can lift "responses the boss liked
// (or didn't)" into future system prompts. Setting rating=null is a no-op on
// the server (the UI just stops decorating the message); we don't try to
// retract prior observations.
//
// The observation's session_id is derived from the request body if provided,
// or left empty (which causes InsertObservation to reject). For now we don't
// look the session up by message_id because messages aren't first-class
// rows - they live as observations themselves, so id correlation would be
// brittle. Frontend is updated to send the active session id.
func (s *Server) handleMessageFeedback(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Path is /api/messages/{id}/feedback
	rest := strings.TrimPrefix(r.URL.Path, "/api/messages/")
	parts := strings.Split(rest, "/")
	if len(parts) < 2 || parts[1] != "feedback" {
		http.NotFound(w, r)
		return
	}
	messageID := strings.TrimSpace(parts[0])
	if messageID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message id required"})
		return
	}

	var body struct {
		Rating    *string `json:"rating"`     // "up" | "down" | nil
		SessionID string  `json:"session_id"` // optional; helps scope the observation
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}

	// Clearing the rating is a no-op on the server. UI handles the optimistic
	// state via localStorage; we don't need a tombstone observation.
	if body.Rating == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
		return
	}
	rating := strings.ToLower(strings.TrimSpace(*body.Rating))
	if rating != "up" && rating != "down" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "rating must be 'up' or 'down'"})
		return
	}

	if s.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "memory store not configured"})
		return
	}

	// Bump importance on positive feedback so the retrieval layer is more
	// likely to surface this. Negative feedback still captures but at lower
	// weight so the agent sees "don't repeat this kind of answer" without
	// crowding out other observations.
	importance := 6
	if rating == "down" {
		importance = 3
	}

	rawText := "boss rated assistant reply: " + rating
	_, err := s.store.InsertObservation(r.Context(), memory.ObservationInput{
		SessionID: body.SessionID,
		HookName:  "MessageFeedback",
		Payload: map[string]any{
			"message_id": messageID,
			"rating":     rating,
		},
		RawText:    rawText,
		Importance: importance,
	})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "captured", "rating": rating})
}
