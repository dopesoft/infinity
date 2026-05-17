package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// CompactionResult reports what happened in a compact pass so callers
// (manual /compact tool, auto-trigger in the loop) can surface a clean
// status without re-implementing the math.
type CompactionResult struct {
	OriginalTurns      int      `json:"original_turns"`
	KeptTurns          int      `json:"kept_turns"`
	CompactedTurns     int      `json:"compacted_turns"`
	SummaryChars       int      `json:"summary_chars"`
	ObservationIDs     []string `json:"observation_ids"`
	SummaryMarkdown    string   `json:"summary_markdown"`
}

// CompactionConfig tunes the heuristic. Zero values pick sensible defaults
// so callers can pass &CompactionConfig{} and it still does the right thing.
type CompactionConfig struct {
	// KeepLastTurns is how many of the most recent user/assistant turn
	// pairs to preserve verbatim. Anything older is summarised. Default 5.
	KeepLastTurns int
	// MinTurnsToCompact is the floor below which compaction is a no-op -
	// summarising a 3-turn conversation costs more than it saves. Default 8.
	MinTurnsToCompact int
	// Model is an optional override for the summariser call. Empty =
	// the provider's default (usually Sonnet); pass a Haiku model id
	// for the cheap path.
	Model string
}

// ConversationCompactor takes a session's message history and produces a
// summary covering everything older than the keep-window, persists the
// older turns as `mem_observations` (so the compress pipeline can
// promote durable knowledge to `mem_memories`), and returns both the
// summary text and the new shorter message list the caller can swap in.
//
// The promotion path is the AGI-trajectory move: conversation compaction
// isn't a lossy summary inside the conversation, it's a *transfer* from
// short-term buffer to long-term memory. RRF retrieval surfaces compacted
// turns on future requests if relevant. The synthetic summary message we
// inject just keeps the in-conversation continuity tight.
type ConversationCompactor struct {
	store    *Store
	provider llm.Provider
}

// NewConversationCompactor wires the compactor with the durable store
// (for observation inserts) and an LLM provider (for the summariser
// call). Both are required; a nil dep here is a programmer error.
func NewConversationCompactor(store *Store, provider llm.Provider) *ConversationCompactor {
	return &ConversationCompactor{store: store, provider: provider}
}

// Compact runs one compaction pass. Returns the new message list to swap
// into the session, the result metadata, and any error. The caller (the
// agent loop) is responsible for atomically replacing Session.Messages -
// we don't mutate the session here so this stays test-friendly.
func (c *ConversationCompactor) Compact(
	ctx context.Context,
	sessionID string,
	messages []llm.Message,
	cfg *CompactionConfig,
) (newMessages []llm.Message, res CompactionResult, err error) {
	if c == nil || c.store == nil || c.provider == nil {
		return messages, CompactionResult{}, errors.New("compactor not configured")
	}
	if cfg == nil {
		cfg = &CompactionConfig{}
	}
	keep := cfg.KeepLastTurns
	if keep <= 0 {
		keep = 5
	}
	minTurns := cfg.MinTurnsToCompact
	if minTurns <= 0 {
		minTurns = 8
	}

	// Count user+assistant pairs (one "turn" = one user + N assistant +
	// tool messages until the next user). We don't compact mid-turn - a
	// turn is the atomic unit so summaries don't strand orphan tool
	// results without their preceding call.
	turnBoundaries := turnStartIndices(messages)
	if len(turnBoundaries) < minTurns {
		return messages, CompactionResult{
			OriginalTurns: len(turnBoundaries),
			KeptTurns:     len(turnBoundaries),
		}, nil
	}
	if len(turnBoundaries) <= keep {
		return messages, CompactionResult{
			OriginalTurns: len(turnBoundaries),
			KeptTurns:     len(turnBoundaries),
		}, nil
	}
	splitAt := turnBoundaries[len(turnBoundaries)-keep]
	older := messages[:splitAt]
	kept := messages[splitAt:]
	if len(older) == 0 {
		return messages, CompactionResult{
			OriginalTurns: len(turnBoundaries),
			KeptTurns:     len(turnBoundaries),
		}, nil
	}

	// Build a transcript blob the summariser can chew on.
	transcript := renderTranscript(older)

	// Run the summariser. We use a fresh dummy channel so the provider's
	// streaming API is satisfied; we read the final Response.Text
	// after the channel closes.
	out := make(chan llm.StreamEvent, 64)
	go func() {
		for range out {
			// drain - we don't care about deltas, just the final Response
		}
	}()
	resp, sumErr := c.provider.Stream(
		ctx,
		cfg.Model, // empty = provider default
		compactionSystemPrompt,
		[]llm.Message{{Role: llm.RoleUser, Content: "Summarize this conversation segment.\n\n---\n" + transcript}},
		nil,
		out,
	)
	close(out)
	if sumErr != nil {
		return messages, CompactionResult{}, fmt.Errorf("summarize: %w", sumErr)
	}
	summary := strings.TrimSpace(resp.Text)
	if summary == "" {
		return messages, CompactionResult{}, errors.New("summarizer returned empty body")
	}

	// Persist each older turn as an observation so the compress
	// pipeline can promote durable knowledge to mem_memories. We bundle
	// per-turn rather than one-blob-per-segment so granularity matches
	// the rest of the memory substrate.
	obsIDs := make([]string, 0, len(turnBoundaries)-keep)
	for i := 0; i < len(turnBoundaries)-keep; i++ {
		startIdx := turnBoundaries[i]
		endIdx := splitAt
		if i+1 < len(turnBoundaries)-keep {
			endIdx = turnBoundaries[i+1]
		}
		turnText := renderTranscript(messages[startIdx:endIdx])
		id, ierr := c.store.InsertObservation(ctx, ObservationInput{
			SessionID:  sessionID,
			HookName:   "conversation_compaction",
			RawText:    turnText,
			Importance: 6, // mid-high so the compressor prioritises it
			Payload: map[string]any{
				"source":           "conversation_compaction",
				"compacted_at":     "now",
				"original_session": sessionID,
				"turn_index":       i,
			},
		})
		if ierr != nil {
			// Skip the failure but keep going - losing one row beats
			// losing the whole compaction pass. The summary still
			// survives in the synthetic message below.
			continue
		}
		obsIDs = append(obsIDs, id)
	}

	// Build the replacement message list: synthetic system note +
	// kept tail. The synthetic message is RoleSystem so the model
	// treats it as instructional context rather than user/assistant
	// dialogue.
	synth := llm.Message{
		Role:    llm.RoleSystem,
		Content: buildCompactionNote(summary, len(turnBoundaries)-keep, len(obsIDs)),
	}
	newMessages = append([]llm.Message{synth}, kept...)

	return newMessages, CompactionResult{
		OriginalTurns:   len(turnBoundaries),
		KeptTurns:       keep,
		CompactedTurns:  len(turnBoundaries) - keep,
		SummaryChars:    len(summary),
		ObservationIDs:  obsIDs,
		SummaryMarkdown: summary,
	}, nil
}

// compactionSystemPrompt is the summariser prompt. Tuned to preserve the
// load-bearing details that re-asking would expose: decisions, file
// paths, identifiers, constraints, user preferences, open follow-ups.
const compactionSystemPrompt = `You are compressing a slice of a conversation between a user (the "boss") and an AI assistant. The goal is a tight summary that preserves everything the assistant would otherwise re-ask about.

Output format: markdown with these sections, omit any that have no content:

## Decisions
- One bullet per decision made (what was chosen and why).

## Context
- File paths, project names, repo names, IDs, URLs, env vars, credentials referenced (NOT the credential values themselves - just that they exist).
- User preferences and constraints expressed (e.g. "prefer Tailwind over inline CSS").

## Open follow-ups
- Things the assistant said it would do later or check back on.
- Questions the user asked but the assistant deferred.

## Errors and gotchas
- Specific error messages or failures encountered, and how (or whether) they were resolved.

Be terse. No prose intro, no "to summarize" lines. Just the sections.`

// buildCompactionNote wraps the LLM summary in a clear delimiter so the
// model understands this isn't a normal turn - it's a compressed pointer
// to memory plus a verbatim digest of what was lost from the buffer.
func buildCompactionNote(summary string, compactedTurns int, obsCount int) string {
	var b strings.Builder
	b.WriteString("[ Earlier conversation compacted to memory - ")
	b.WriteString(itoa(compactedTurns))
	b.WriteString(" turns folded into ")
	b.WriteString(itoa(obsCount))
	b.WriteString(" memory observations. Relevant facts will surface via retrieval as needed. ]\n\n")
	b.WriteString(summary)
	return b.String()
}

// turnStartIndices walks the message list and returns the index of every
// user message - those are the "turn starts." Assistant + tool messages
// that follow belong to that turn until the next user message appears.
func turnStartIndices(messages []llm.Message) []int {
	out := make([]int, 0)
	for i, m := range messages {
		if m.Role == llm.RoleUser {
			out = append(out, i)
		}
	}
	return out
}

// renderTranscript flattens messages into a plain-text transcript for the
// summariser. Tool calls get one line per call with truncated input;
// tool results get one line with truncated output. The goal is "the
// summariser sees what happened" not "perfect reconstruction" - verbose
// outputs are exactly what we're trying to compress.
func renderTranscript(messages []llm.Message) string {
	var b strings.Builder
	for _, m := range messages {
		switch m.Role {
		case llm.RoleUser:
			b.WriteString("USER: ")
			b.WriteString(truncate(m.Content, 4000))
			b.WriteString("\n\n")
		case llm.RoleAssistant:
			if c := strings.TrimSpace(m.Content); c != "" {
				b.WriteString("ASSISTANT: ")
				b.WriteString(truncate(c, 4000))
				b.WriteString("\n")
			}
			for _, tc := range m.ToolCalls {
				b.WriteString("  → tool ")
				b.WriteString(tc.Name)
				b.WriteString("\n")
			}
			b.WriteString("\n")
		case llm.RoleTool:
			b.WriteString("  result(")
			b.WriteString(m.ToolName)
			b.WriteString("): ")
			b.WriteString(truncate(strings.TrimSpace(m.Content), 600))
			b.WriteString("\n")
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
