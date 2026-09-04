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
	// Measured says the fill is a real reading taken on the brain that is
	// answering now. False means nobody has measured THIS brain on THIS
	// thread yet: a fresh conversation, a thread just compacted, or the boss
	// switching models mid-conversation. The fill is then unknown, and the
	// dial says so instead of rendering the last brain's number against this
	// brain's window (which is how it sat full-red on a window he had barely
	// touched).
	Measured bool `json:"measured"`
}

// estimateTokens uses the chars-divided-by-4 heuristic - accurate enough for
// a "how full is the context" meter without pulling a real tokenizer per
// model. Underestimates code/JSON slightly and overestimates non-English;
// good enough for the UI.
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
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
	window := llm.ContextWindow(modelID)

	// Pull the real API-reported usage for this session. If no session id
	// was supplied or the session has never sent a turn, snapshot.LastInputTokens
	// is 0 and every category reports 0 - exactly what we want for an
	// empty conversation.
	var snapshot agent.UsageSnapshot
	if sid := strings.TrimSpace(r.URL.Query().Get("session_id")); sid != "" {
		snapshot = s.loop.GetOrCreateSession(sid).UsageSnapshot()
	}

	// A fill is a measurement of one prompt sent to one model. It only
	// describes the brain that took it.
	measured := snapshot.LastInputTokens > 0 &&
		(snapshot.LastMeasuredModel == "" || sameBrain(snapshot.LastMeasuredModel, modelID))
	used := snapshot.LastInputTokens
	if !measured {
		used = 0
	}
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
			// Size what the loop SENDS: the session's active set, not the
			// whole registry (444 tools vs ~41). Unknown session: fall back
			// to the full registry rather than report zero.
			var defs []llm.ToolDef
			if sid := strings.TrimSpace(r.URL.Query().Get("session_id")); sid != "" {
				if names := s.loop.ActiveToolNames(sid); len(names) > 0 {
					defs = reg.DefinitionsFor(names)
				}
			}
			if len(defs) == 0 {
				defs = reg.Definitions()
			}
			if len(defs) > 0 {
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
		CacheReadTokens:  cacheIf(measured, snapshot.LastCacheReadTokens),
		CacheWriteTokens: cacheIf(measured, snapshot.LastCacheWriteTokens),
		Measured:         measured,
	})
}

// sameBrain reports whether two model ids name the same brain. Compared
// loosely on purpose: the id that answers can carry a date suffix or a window
// marker ("claude-opus-5" vs "opus[1m]") without being a different brain, and
// the cost of a false difference is one unmeasured poll, while the cost of a
// false match is showing him another model's fill as his own.
func sameBrain(a, b string) bool {
	na, nb := brainKey(a), brainKey(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb || strings.HasPrefix(na, nb) || strings.HasPrefix(nb, na)
}

func brainKey(m string) string {
	m = strings.ToLower(strings.TrimSpace(m))
	if i := strings.LastIndex(m, ":"); i >= 0 {
		m = m[i+1:]
	}
	return strings.SplitN(m, "[", 2)[0]
}

func cacheIf(measured bool, n int) int {
	if !measured {
		return 0
	}
	return n
}
