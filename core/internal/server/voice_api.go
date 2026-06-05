package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/voice"
)

// Voice HTTP surface - now just two endpoints, because the realtime model is
// demoted to ears (mic + transcription + VAD) and the agent loop is the brain:
//
//   POST /session → mint an INPUT-ONLY ephemeral realtime key for the browser.
//                   No system prompt, no tools, no auto-response - cognition
//                   runs through Loop.Run over the chat WebSocket instead.
//   POST /error   → record a browser-side voice connection failure (SDP /
//                   quota / ICE / mic) so it surfaces in Heartbeat + chat.
//
// The old /tool and /turn endpoints are gone: tool execution and turn capture
// now happen inside Loop.Run exactly as in text mode (a voice utterance is a
// `message` frame with voice:true). One brain, one path.

// voiceSessionReq is the body the browser sends to /api/voice/session. The
// session_id is the canonical chat-session UUID (same one the WS uses) so
// voice + text share memory + provenance. No query is needed anymore - there's
// no frozen prompt to seed; the live agent loop retrieves per turn.
type voiceSessionReq struct {
	SessionID string `json:"session_id"`
}

func (s *Server) handleVoiceSession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.voice == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "voice not configured (OPENAI_API_KEY missing on this deployment)",
		})
		return
	}

	var body voiceSessionReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	sessionID := strings.TrimSpace(body.SessionID)
	if sessionID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id required"})
		return
	}

	// Mint an input-only realtime key. No prompt, no tools - the realtime
	// model only transcribes; the agent loop does everything else.
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.voice.Mint(ctx, voice.SessionRequest{SessionID: sessionID})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// voiceErrorReq carries a browser-side voice connection failure. The WebRTC
// SDP exchange runs browser→OpenAI directly, so quota / billing / network /
// permission failures never touch Core - this endpoint is the only way they
// become observable server-side. Studio's useVoice posts here once per failed
// attempt.
type voiceErrorReq struct {
	SessionID string `json:"session_id"`
	Kind      string `json:"kind"`    // "sdp" | "ice-failed" | "mic-permission" | ...
	Message   string `json:"message"` // already-classified human message from the client
}

func (s *Server) handleVoiceError(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body voiceErrorReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	kind := strings.TrimSpace(body.Kind)
	if kind == "" {
		kind = "unknown"
	}
	msg := strings.TrimSpace(body.Message)
	if msg == "" {
		msg = "voice connection failed"
	}
	sessionID := strings.TrimSpace(body.SessionID)

	// Failure → stderr so Railway tags it severity:error (this is exactly
	// the kind of thing the boss needs paged about, not buried in info).
	// log.Printf writes to stderr by default; correct usage for a failure.
	log.Printf("voice connect failure: kind=%s session=%s msg=%q", kind, sessionID, msg)

	// Raise a Finding so the failure surfaces in the Heartbeat tab AND -
	// because kind=security always passes shouldSurfaceFinding - gets
	// spoken into chat live. This is what makes a credit/quota wall visible
	// when the boss is on his phone with no devtools open. source_tag is
	// stable so repeated failures merge into one open row (occurrences++)
	// instead of piling up.
	if s.pool != nil {
		isQuota := strings.Contains(strings.ToLower(msg), "quota") ||
			strings.Contains(strings.ToLower(msg), "billing") ||
			strings.Contains(strings.ToLower(msg), "credit")
		title := "Voice failed to connect"
		if isQuota {
			title = "Voice is down: OpenAI quota / billing"
		}
		_, _, _, _ = proactive.UpsertFinding(r.Context(), s.pool, slog.Default(), proactive.FindingDraft{
			Kind:        "security",
			Source:      "voice_connect",
			SourceTag:   "voice_connect_failure",
			Title:       title,
			Detail:      msg,
			Sample:      fmt.Sprintf("kind=%s session=%s", kind, sessionID),
			Importance:  8,
			PreApproved: true,
		})
		if s.heartbeat != nil {
			s.onHeartbeatFinding(r.Context(), proactive.Finding{
				Kind:        "security",
				Source:      "voice_connect",
				SourceTag:   "voice_connect_failure",
				Title:       title,
				Detail:      msg,
				PreApproved: true,
			})
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
