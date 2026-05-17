package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// CompactContext is the model-callable conversation compaction tool. The
// model invokes it (or the loop's auto-trigger calls it directly) when
// the conversation buffer has grown large and is at risk of either
// running into the context window cap or wasting cache on irrelevant
// history. It rewrites the active session's message list, promoting
// older turns into mem_observations (which the compress pipeline then
// folds into mem_memories) and replacing them with a single synthetic
// summary message.
//
// Tied to a specific Loop because it has to mutate Session.Messages in
// place. The Loop pointer is set at registration time in serve.go.
type CompactContext struct {
	Loop       *Loop
	Compactor  *memory.ConversationCompactor
}

func (c *CompactContext) Name() string { return "compact_context" }
func (c *CompactContext) Description() string {
	return "Compact older conversation turns into long-term memory. Keeps the most recent turns verbatim; " +
		"everything older becomes memory observations (promoted via the existing compress pipeline) and is " +
		"replaced by a single summary message. Use when the conversation has grown long and earlier turns " +
		"are no longer load-bearing. Safe to call mid-task - the recent context survives untouched."
}
func (c *CompactContext) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"keep_last_turns": map[string]any{
				"type":        "integer",
				"description": "How many recent user turns (and their assistant follow-ups) to preserve verbatim. Default 5.",
				"default":     5,
			},
			"min_turns_to_compact": map[string]any{
				"type":        "integer",
				"description": "Floor below which compaction is a no-op (summary would cost more than it saves). Default 8.",
				"default":     8,
			},
		},
	}
}

func (c *CompactContext) Execute(ctx context.Context, input map[string]any) (string, error) {
	if c.Loop == nil || c.Compactor == nil {
		return "", errors.New("compact_context not fully wired (Loop or Compactor nil)")
	}
	// Walk every active session - in practice the model is calling this
	// from inside one session and we use the loop's session registry to
	// find it via the ActiveSet pointer we stashed in the context.
	active := tools.ActiveSetFromContext(ctx)
	if active == nil {
		return `{"error":"compact_context must be called from within an active agent session"}`, nil
	}

	// Find the session whose Active pointer matches the one in our ctx.
	// This is the cheapest way to attribute the call to a session without
	// plumbing session-id into every tool - we already do this for
	// load_tools/unload_tools.
	var session *Session
	for _, s := range c.Loop.Sessions() {
		if s.Active == active {
			session = s
			break
		}
	}
	if session == nil {
		return `{"error":"could not locate calling session"}`, nil
	}

	cfg := &memory.CompactionConfig{}
	if v, ok := input["keep_last_turns"].(float64); ok && v > 0 {
		cfg.KeepLastTurns = int(v)
	}
	if v, ok := input["min_turns_to_compact"].(float64); ok && v > 0 {
		cfg.MinTurnsToCompact = int(v)
	}

	before := len(session.Messages)
	newMsgs, res, err := c.Compactor.Compact(ctx, session.ID, session.Snapshot(), cfg)
	if err != nil {
		return "", fmt.Errorf("compact: %w", err)
	}

	if res.CompactedTurns == 0 {
		body := map[string]any{
			"compacted":      false,
			"reason":         "below minimum turn threshold or nothing older than keep window",
			"original_turns": res.OriginalTurns,
		}
		b, _ := json.Marshal(body)
		return string(b), nil
	}

	session.ReplaceMessages(newMsgs)

	body := map[string]any{
		"compacted":       true,
		"original_turns":  res.OriginalTurns,
		"compacted_turns": res.CompactedTurns,
		"kept_turns":      res.KeptTurns,
		"messages_before": before,
		"messages_after":  len(newMsgs),
		"observations":    res.ObservationIDs,
		"summary_chars":   res.SummaryChars,
	}
	b, _ := json.Marshal(body)
	return string(b), nil
}

