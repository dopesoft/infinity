package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/hooks"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/voice"
)

// Voice HTTP surface — three endpoints, all under /api/voice/*:
//
//   POST /session   → mint an ephemeral OpenAI client_secret for the browser.
//                     Builds the session config from the same memory + skills
//                     + tools the text loop would use, plus the British-RP
//                     accent line.
//   POST /tool      → run a tool call coming from the realtime data channel.
//                     Goes through the same gate chain as the text loop, so
//                     high-risk tools land in the Trust queue like normal.
//   POST /turn      → persist a finalised user or assistant utterance via the
//                     hooks pipeline. Memory capture + Sessions tab work
//                     identically to text mode.
//
// All three are nil-safe: missing voice minter → 503; missing pipeline → still
// runs tools but skips memory capture; missing loop → tool endpoint 503s.

// voiceSessionReq is the body the browser sends to /api/voice/session. The
// session_id is the canonical chat-session UUID (same one the WS uses) so
// voice + text share memory + provenance.
type voiceSessionReq struct {
	SessionID string `json:"session_id"`
	// Query is the user's first prompt, used to build the memory prefix
	// for relevant retrievals. Empty on cold-mic-tap (the boss just tapped
	// the mic before saying anything); we still include the soul prompt +
	// skills + tools, just without query-conditioned memory.
	Query string `json:"query,omitempty"`
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
	if s.loop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent loop not configured"})
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

	// Build instructions the same way the agent loop would for a turn —
	// soul prompt + memory prefix + skills + accounts + tool catalog.
	// Voice runs without the lazy active-set so the model sees the full
	// tool surface; we still inject the catalog block for parity (it
	// describes the toolkit in plain English which helps voice routing).
	systemPrompt := s.loop.SystemPrompt()
	if mem := buildMemoryPrefix(r.Context(), s.loop, sessionID, body.Query); mem != "" {
		systemPrompt = mem + "\n\n" + systemPrompt
	}
	if sk := s.loop.Skills(); sk != nil {
		if skillsPrefix := sk.MatchAndPrefix(body.Query, 5); skillsPrefix != "" {
			systemPrompt = skillsPrefix + "\n\n" + systemPrompt
		}
	}

	tools := s.loop.Tools()
	var defs []llm.ToolDef
	if tools != nil {
		defs = tools.Definitions()
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	resp, err := s.voice.Mint(ctx, voice.SessionRequest{
		SessionID:    sessionID,
		SystemPrompt: systemPrompt,
		Tools:        defs,
	})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// buildMemoryPrefix calls the agent loop's MemoryProvider if one is wired.
// We can't access the field directly from outside the package, so we drive
// it via a dedicated method added in this PR. Returns "" on any failure —
// memory is best-effort context.
func buildMemoryPrefix(ctx context.Context, loop *agent.Loop, sessionID, query string) string {
	if loop == nil {
		return ""
	}
	prefix, _ := loop.MemoryPrefix(ctx, sessionID, query)
	return prefix
}

// voiceToolReq is the body the browser sends to /api/voice/tool when the
// realtime data channel emits `response.function_call_arguments.done`.
type voiceToolReq struct {
	SessionID string         `json:"session_id"`
	CallID    string         `json:"call_id"`
	Name      string         `json:"name"`
	Input     map[string]any `json:"input"`
}

type voiceToolResp struct {
	CallID         string `json:"call_id"`
	Output         string `json:"output"`
	IsError        bool   `json:"is_error,omitempty"`
	GatedForTrust  bool   `json:"gated_for_trust,omitempty"`
	ContractID     string `json:"contract_id,omitempty"`
	Preview        string `json:"preview,omitempty"`
}

func (s *Server) handleVoiceTool(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.loop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent loop not configured"})
		return
	}

	var body voiceToolReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	name := strings.TrimSpace(body.Name)
	sessionID := strings.TrimSpace(body.SessionID)
	callID := strings.TrimSpace(body.CallID)
	if name == "" || sessionID == "" || callID == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id, call_id, name required"})
		return
	}

	gate := s.loop.GateForVoice()
	registry := s.loop.Tools()
	if registry == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "tool registry not configured"})
		return
	}

	pipeline := s.loop.Hooks()
	if pipeline != nil {
		pipeline.Emit(string(hooks.PreToolUse), sessionID, "", name, map[string]any{
			"name":         name,
			"input":        body.Input,
			"tool_call_id": callID,
			"voice":        true,
		})
	}

	decision := gate.Authorize(r.Context(), sessionID, "", name, body.Input)
	if !decision.Allow {
		if decision.WaitForApproval && decision.ContractID != "" {
			// Voice can't usefully block for the boss to walk to Studio
			// and approve. Return the contract id + a synthesized
			// "awaiting approval" output so the model can verbally tell
			// the boss what's parked and move on. The Trust toast still
			// fires in any open Studio tab.
			writeJSON(w, http.StatusOK, voiceToolResp{
				CallID:        callID,
				Output:        fmt.Sprintf("Tool %s is awaiting your approval. Open the Trust tab on a screen to allow it.", name),
				IsError:       false,
				GatedForTrust: true,
				ContractID:    decision.ContractID,
				Preview:       decision.Preview,
			})
			return
		}
		// Hard deny — synthesize reason as the model-visible output.
		writeJSON(w, http.StatusOK, voiceToolResp{
			CallID:  callID,
			Output:  fmt.Sprintf("Tool %s was denied: %s", name, decision.Reason),
			IsError: true,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	output, err := registry.Execute(ctx, llm.ToolCall{ID: callID, Name: name, Input: body.Input})
	isErr := false
	if err != nil {
		isErr = true
		output = err.Error()
	}

	if pipeline != nil {
		evName := hooks.PostToolUse
		if isErr {
			evName = hooks.PostToolUseFailure
		}
		pipeline.Emit(string(evName), sessionID, "", output, map[string]any{
			"name":         name,
			"input":        body.Input,
			"output":       output,
			"is_error":     isErr,
			"tool_call_id": callID,
			"voice":        true,
		})
	}

	writeJSON(w, http.StatusOK, voiceToolResp{
		CallID:  callID,
		Output:  output,
		IsError: isErr,
	})
}

// voiceTurnReq carries a finalised utterance from the browser. The realtime
// data channel emits `conversation.item.input_audio_transcription.completed`
// for the user side and `response.done` (with output items containing the
// audio_transcript) for the assistant side. The browser forwards the text
// here so memory capture matches text mode exactly.
type voiceTurnReq struct {
	SessionID string `json:"session_id"`
	Role      string `json:"role"` // "user" | "assistant"
	Text      string `json:"text"`
}

func (s *Server) handleVoiceTurn(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body voiceTurnReq
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
		return
	}
	sessionID := strings.TrimSpace(body.SessionID)
	text := strings.TrimSpace(body.Text)
	role := strings.ToLower(strings.TrimSpace(body.Role))
	if sessionID == "" || text == "" || (role != "user" && role != "assistant") {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "session_id, text, role (user|assistant) required",
		})
		return
	}

	if s.loop == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped"})
		return
	}
	pipeline := s.loop.Hooks()
	if pipeline == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped"})
		return
	}
	evName := hooks.UserPromptSubmit
	if role == "assistant" {
		evName = hooks.TaskCompleted
	}
	pipeline.Emit(string(evName), sessionID, "", text, map[string]any{
		"voice": true,
		"role":  role,
	})

	// Also keep the in-memory Session.Messages in sync so subsequent
	// text turns see the voice exchange in conversation history. We
	// don't fire StripSecrets here because the hooks capture chain
	// already runs it on the way to the DB.
	sess := s.loop.GetOrCreateSession(sessionID)
	switch role {
	case "user":
		sess.Append(llm.Message{Role: llm.RoleUser, Content: text})
	case "assistant":
		sess.Append(llm.Message{Role: llm.RoleAssistant, Content: text})
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Keep `agent` import live for the buildMemoryPrefix helper.
var _ = (*agent.Loop)(nil)
