package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/llm"
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
	// CacheReadTokens / CacheWriteTokens are the prompt-cache split of the
	// last turn (a subset of UsedTokens). The modal shows how much of the
	// window was served from cache - the caching EFFECT, for any model. 0 on
	// models/turns with no cache hit.
	CacheReadTokens  int `json:"cache_read_tokens"`
	CacheWriteTokens int `json:"cache_write_tokens"`
}

// estimateTokens uses the chars-divided-by-4 heuristic - accurate enough for
// a "how full is the context" meter without pulling a real tokenizer per
// model. Underestimates code/JSON slightly and overestimates non-English;
// good enough for the UI.
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// contextWindowFor returns the model's EFFECTIVE input context window in
// tokens - the size our client actually gets, which is what the meter must
// divide by. Numbers verified against vendor model cards (last checked
// 2026-06-19); update in lock step with studio/lib/models-catalog.ts when a
// card changes. Order matters: most specific patterns first.
func contextWindowFor(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	// Some paths carry a "vendor:model" form (e.g. "openai_oauth:gpt-5.4");
	// match on the bare model id so a prefix can't silently drop the lookup
	// to the default window.
	if i := strings.LastIndex(m, ":"); i >= 0 {
		m = m[i+1:]
	}

	// Anthropic - effective 200K. Opus 4.6+/Sonnet 4.6 advertise a 1M window
	// but it requires the context-1m beta header, which anthropic.go does NOT
	// send, so >200K input is rejected -> effective 200K. The boss opts into
	// 1M by picking a model id carrying the "1m" marker (then we'd add the
	// header). Showing 1M here while the client caps at 200K would UNDER-report
	// fill (dangerous), so 200K is the honest default.
	if strings.HasPrefix(m, "claude-") {
		if strings.Contains(m, "1m") {
			return 1_000_000
		}
		// Claude 5 family (Opus 5, Sonnet 5, Fable 5) ships a 1M window as
		// standard, no beta header (model pages, checked 2026-08-26). Matched
		// on the family boundary so "claude-sonnet-4-5-…" stays 200K.
		if isClaude5Family(m) {
			return 1_000_000
		}
		return 200_000
	}

	// OpenAI gpt-5.x - window differs by MINOR version AND tier. Verified vs
	// OpenAI cards (all 128K max output):
	//   gpt-5.4, gpt-5.4-pro, gpt-5.5, gpt-5.5-pro, gpt-5.6(+sol/terra/luna): 1,050,000
	//   gpt-5.4-mini/-nano, gpt-5.2, gpt-5.1, gpt-5(+pro/mini/nano): 400,000
	if strings.HasPrefix(m, "gpt-5") {
		// mini / nano stay at 400K on every minor version (gpt-5.4-mini is
		// 400K even though gpt-5.4 is 1.05M) - exclude them first.
		if strings.Contains(m, "-mini") || strings.Contains(m, "-nano") {
			return 400_000
		}
		if strings.HasPrefix(m, "gpt-5.4") || strings.HasPrefix(m, "gpt-5.5") || strings.HasPrefix(m, "gpt-5.6") {
			return 1_050_000
		}
		return 400_000
	}
	// o-series reasoning: o1/o3/o4 are 200K; o1-mini is the 128K exception.
	if strings.HasPrefix(m, "o1-mini") {
		return 128_000
	}
	if strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return 200_000
	}
	// gpt-4.1 is the long-context one at ~1M (1,047,576); gpt-4o / gpt-4 at 128K.
	if strings.HasPrefix(m, "gpt-4.1") {
		return 1_000_000
	}
	if strings.HasPrefix(m, "gpt-4") {
		return 128_000
	}

	// DeepSeek - V4 flash/pro ship a 1M window (not in the Studio catalog yet,
	// kept for when the boss wires it).
	if strings.HasPrefix(m, "deepseek") {
		return 1_000_000
	}

	// Google Gemini (verified vs Google's cards):
	//   Gemini 3 Flash: 200K (the small one) - check before the 3-family default
	//   Gemini 3 Pro / Deep Think: 1M
	//   Gemini 2.5 Pro / 2.5 Flash / 2.5 Flash-Lite / 2.0 Flash: 1M
	// (2.5 Pro is 1M, NOT 2M - that was the old Gemini 1.5 Pro.)
	if strings.HasPrefix(m, "gemini-3-flash") {
		return 200_000
	}
	if strings.HasPrefix(m, "gemini-3") {
		return 1_000_000
	}
	if strings.HasPrefix(m, "gemini-2.5") || strings.HasPrefix(m, "gemini-2.0") {
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
	override := ""
	if s.settings != nil {
		if override = s.settings.GetModel(r.Context()); override != "" {
			modelID = override
		}
	}
	// Measure against the brain that is ANSWERING: while the chosen plan is
	// spent and a standby carries the session, the window is the standby's.
	if st := llm.EffectiveBrain(provider, override); st.OnStandby && st.Model != "" {
		modelID = st.Model
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
		CacheReadTokens:  snapshot.LastCacheReadTokens,
		CacheWriteTokens: snapshot.LastCacheWriteTokens,
	})
}

// isClaude5Family reports a Claude 5 model id: "claude-<family>-5" followed
// by the end of the id, a date/variant suffix ("-…") or the 1M marker ("[…").
func isClaude5Family(m string) bool {
	for _, fam := range []string{"opus", "sonnet", "haiku", "fable"} {
		p := "claude-" + fam + "-5"
		if !strings.HasPrefix(m, p) {
			continue
		}
		rest := m[len(p):]
		if rest == "" || rest[0] == '-' || rest[0] == '[' || rest[0] == '.' {
			return true
		}
	}
	return strings.Contains(m, "mythos")
}
