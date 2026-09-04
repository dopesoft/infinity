package agent

import (
	"context"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Context economy, in the order the vendors apply it: clear old tool
// results first (Anthropic's clear_tool_uses, Claude Code's "clears older
// tool outputs first"), summarise only when that is not enough. Both are
// gated on the MEASURED window fill the provider reported for the previous
// call, never on a count of turns: a single 150-iteration turn used to keep
// every 12KB tool result because the old gate wanted 8 user turns.

const (
	// clearToolResultsAtPercent is the window fill at which older tool
	// results are replaced by markers. Anthropic's server-side default is
	// an absolute 100K tokens; on a 1M window that would clear at 10%,
	// which is too eager for a brain that answers in long arcs, so it is a
	// fraction of the window, below the compaction threshold.
	clearToolResultsAtPercent = 50
	// keepToolResults is how many of the newest results stay whole (the
	// vendor default is 3).
	keepToolResults = 3
	// minClearChars is the least a clearing pass must free to be worth the
	// prefix rewrite it causes (~10K tokens; Anthropic's clear_at_least).
	minClearChars = 40_000
)

// clearExcludedTools are results the model keeps referring back to by
// content; clearing them costs a re-read.
var clearExcludedTools = map[string]bool{
	"plan_get": true, "plan_create": true, "surface_list": true, "recall": true,
}

// maintainContext runs the two passes before an LLM call when the last
// call's window fill says they are due. Nothing here touches the durable
// record; only the in-memory transcript changes.
func (l *Loop) maintainContext(ctx context.Context, s *Session, provider llm.Provider, model, stableSystem string, toolDefs []llm.ToolDef) {
	fill := s.UsageSnapshot().LastInputTokens
	if fill <= 0 {
		return
	}
	compactAt := l.compactAt()
	if compactAt <= 0 {
		return
	}
	clearAt := compactAt * clearToolResultsAtPercent / 60
	if fill >= clearAt {
		if freed := s.clearOldToolResults(keepToolResults, clearExcludedTools, minClearChars); freed > 0 {
			infoLog.Printf("context: session=%s fill=%d cleared older tool results (%d chars freed, newest %d kept)", s.ID, fill, freed, keepToolResults)
			// The fill number now describes a window that no longer exists;
			// let the next call measure before deciding anything else.
			s.InvalidateUsage()
			return
		}
	}
	if fill >= compactAt {
		if l.compactTurnNow(ctx, s, provider, stableSystem, toolDefs) {
			infoLog.Printf("context: session=%s fill=%d compacted mid-turn", s.ID, fill)
		}
	}
}

// joinBlocks joins non-empty prompt blocks with a blank line.
func joinBlocks(parts ...string) string {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, "\n\n")
}
