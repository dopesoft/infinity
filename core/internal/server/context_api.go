package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// Context usage endpoint - backs the circular meter in Studio's composer.
// Returns a per-category breakdown so the modal/drawer can render the same
// shape Claude Code / Codex CLI ship (system prompt, tools, messages, free
// space). Memory + skills prefixes are dynamic per-turn (they depend on
// the next user message), so we skip those here rather than paying their
// build cost on every meter poll.

type contextCategory struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Tokens int    `json:"tokens"`
}

type contextUsageResp struct {
	Model         string            `json:"model"`
	ContextWindow int               `json:"context_window"`
	UsedTokens    int               `json:"used_tokens"`
	Categories    []contextCategory `json:"categories"`
}

// estimateTokens uses the chars-divided-by-4 heuristic - accurate enough for
// a "how full is the context" meter without pulling a real tokenizer per
// model. Underestimates code/JSON slightly and overestimates non-English;
// good enough for the UI.
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// contextWindowFor returns the model's input context window in tokens.
// Order matters: more specific patterns first (e.g. "1m" suffix overrides
// the family default; gpt-4o-mini before gpt-4o). Mirrors the catalog in
// studio/lib/models-catalog.ts - keep in sync when adding entries there.
func contextWindowFor(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))

	// Anthropic - opus/sonnet/haiku 200K standard, opt-in 1M variants
	// carry a "1m" suffix or bracket. Match the suffix first.
	if strings.HasPrefix(m, "claude-") {
		if strings.Contains(m, "1m") {
			return 1_000_000
		}
		return 200_000
	}

	// OpenAI - every gpt-5.x flagship variant ships with a 400K input
	// window. o4 family stays at 200K. gpt-4.1 is the long-context one
	// at 1M; gpt-4o sits at 128K.
	if strings.HasPrefix(m, "gpt-5") {
		return 400_000
	}
	if strings.HasPrefix(m, "o4") || strings.HasPrefix(m, "o3") {
		return 200_000
	}
	if strings.HasPrefix(m, "gpt-4.1") {
		return 1_000_000
	}
	if strings.HasPrefix(m, "gpt-4") {
		return 128_000
	}

	// Google - Gemini 3 + 2.5 Pro at 2M; 2.5 Flash + 2.0 Flash at 1M.
	if strings.HasPrefix(m, "gemini-3") {
		return 2_000_000
	}
	if strings.HasPrefix(m, "gemini-2.5-pro") {
		return 2_000_000
	}
	if strings.HasPrefix(m, "gemini-2.5") {
		return 1_000_000
	}
	if strings.HasPrefix(m, "gemini-2.0") {
		return 1_000_000
	}

	return 200_000
}

// handleContextUsage serves GET /api/context/usage?session_id=…
//
// Returns real API-reported token usage for the session - NOT a preview of
// what would be sent next. Before any turn fires the session has zero
// reported usage, so the meter sits at 0%. After each turn the loop records
// resp.Usage.Input/Output via Session.RecordUsage; this endpoint reads
// LastInputTokens to render current context-window fill. Category breakdown
// is reconstructed by attributing the constant-overhead bits (system prompt,
// tool schemas) and treating whatever's left as messages.
func (s *Server) handleContextUsage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.loop == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "agent loop not configured"})
		return
	}
	provider := s.loop.Provider()
	if provider == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no provider"})
		return
	}

	modelID := provider.Model()
	if s.settings != nil {
		if override := s.settings.GetModel(r.Context()); override != "" {
			modelID = override
		}
	}
	window := contextWindowFor(modelID)

	// Pull the real API-reported usage for this session. If no session id
	// was supplied or the session has never sent a turn, snapshot.LastInputTokens
	// is 0 and every category reports 0 - exactly what we want for an
	// empty conversation.
	var snapshot agent.UsageSnapshot
	if sid := strings.TrimSpace(r.URL.Query().Get("session_id")); sid != "" {
		snapshot = s.loop.GetOrCreateSession(sid).UsageSnapshot()
	}

	used := snapshot.LastInputTokens
	free := window - used
	if free < 0 {
		free = 0
	}

	// Reconstruct the breakdown from the constant-overhead bits the
	// provider sent. When used == 0 (no turn yet), every category is 0.
	// When used > 0, system prompt + tools are the constants and the
	// remainder lands in messages.
	var systemTokens, toolsTokens, messageTokens int
	if used > 0 {
		systemTokens = estimateTokens(s.loop.SystemPrompt())
		if reg := s.loop.Tools(); reg != nil {
			if defs := reg.Definitions(); len(defs) > 0 {
				if blob, err := json.Marshal(defs); err == nil {
					toolsTokens = estimateTokens(string(blob))
				}
			}
		}
		messageTokens = used - systemTokens - toolsTokens
		// Estimates may overshoot the API's actual count; pin the deltas
		// to non-negative so a tight call doesn't render "-500 messages".
		if messageTokens < 0 {
			systemTokens += messageTokens // attribute the slack to system
			if systemTokens < 0 {
				systemTokens = 0
			}
			messageTokens = 0
		}
	}

	writeJSON(w, http.StatusOK, contextUsageResp{
		Model:         modelID,
		ContextWindow: window,
		UsedTokens:    used,
		Categories: []contextCategory{
			{ID: "system_prompt", Label: "System prompt", Tokens: systemTokens},
			{ID: "tools", Label: "System tools", Tokens: toolsTokens},
			{ID: "messages", Label: "Messages", Tokens: messageTokens},
			{ID: "free", Label: "Free space", Tokens: free},
		},
	})
}
