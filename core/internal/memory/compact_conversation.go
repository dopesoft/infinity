package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/untrusted"
)

// CompactionResult reports what happened in a compact pass so callers
// (manual /compact tool, auto-trigger in the loop) can surface a clean
// status without re-implementing the math.
type CompactionResult struct {
	OriginalTurns   int      `json:"original_turns"`
	KeptTurns       int      `json:"kept_turns"`
	CompactedTurns  int      `json:"compacted_turns"`
	SummaryChars    int      `json:"summary_chars"`
	ObservationIDs  []string `json:"observation_ids"`
	SummaryMarkdown string   `json:"summary_markdown"`
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
	// Force compacts even below MinTurnsToCompact: the caller measured the
	// window (tokens, not turns) and knows it is worth it. When the history
	// cannot be split by turns (one long tool-heavy turn), Force compacts
	// INSIDE the last turn: the oldest tool exchanges are summarised and the
	// newest KeepToolExchanges stay verbatim.
	Force bool
	// KeepToolExchanges is how many of the newest assistant+tool exchanges
	// of the current turn survive an in-turn compaction. Default 3.
	KeepToolExchanges int
	// StableSystem and Tools are the conversation's own cached prefix. When
	// set, the summariser runs against that exact prefix (same system, same
	// tools, tool calls disabled) plus one instruction, so it READS the warm
	// cache instead of paying for the whole transcript again. This is how
	// Claude Code compacts: "the exact same system prompt … and tool
	// definitions as the parent conversation", with the instruction as a
	// final user message. Unset = the standalone summariser prompt.
	StableSystem string
	Tools        []llm.ToolDef
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
	if len(turnBoundaries) < minTurns && !cfg.Force {
		return messages, CompactionResult{
			OriginalTurns: len(turnBoundaries),
			KeptTurns:     len(turnBoundaries),
		}, nil
	}
	if len(turnBoundaries) <= keep {
		if cfg.Force {
			return c.compactInTurn(ctx, sessionID, messages, cfg)
		}
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

	obsIDs := c.persistCompactedObservations(ctx, sessionID, messages, turnBoundaries, splitAt, keep)
	compactedTurns := len(turnBoundaries) - keep

	if cp, ok := c.provider.(llm.CompactingProvider); ok {
		compacted, _, compactErr := cp.CompactContext(ctx, cfg.Model, messages)
		if compactErr == nil && len(compacted) > 0 {
			return compacted, CompactionResult{
				OriginalTurns:   len(turnBoundaries),
				KeptTurns:       keep,
				CompactedTurns:  compactedTurns,
				ObservationIDs:  obsIDs,
				SummaryMarkdown: "Provider-native Responses compaction.",
			}, nil
		}
		if compactErr != nil && !errors.Is(compactErr, llm.ErrNotImplemented) {
			// The old summarizer path is still a valid compaction fallback. If
			// it also fails, report that original provider-native failure below.
			defer func() {
				if err != nil {
					err = fmt.Errorf("provider compact: %v; %w", compactErr, err)
				}
			}()
		}
	}

	summary, sumErr := c.summarize(ctx, cfg, messages, older)
	if sumErr != nil {
		return messages, CompactionResult{}, sumErr
	}

	// Build the replacement message list: the summary + kept tail. The
	// summary is a USER message: every provider renders user/assistant/tool
	// and silently DROPS a RoleSystem message mid-transcript (anthropic.go,
	// openai.go, openai_oauth.go all switch on the three roles), so the old
	// RoleSystem note never reached the model at all. Codex and the OpenAI
	// Agents SDK inject their summaries as user messages for the same reason.
	synth := llm.Message{
		Role:    llm.RoleUser,
		Content: buildCompactionNote(summary, compactedTurns, len(obsIDs)),
	}
	newMessages = append([]llm.Message{synth}, kept...)

	return newMessages, CompactionResult{
		OriginalTurns:   len(turnBoundaries),
		KeptTurns:       keep,
		CompactedTurns:  compactedTurns,
		SummaryChars:    len(summary),
		ObservationIDs:  obsIDs,
		SummaryMarkdown: summary,
	}, nil
}

func (c *ConversationCompactor) persistCompactedObservations(
	ctx context.Context,
	sessionID string,
	messages []llm.Message,
	turnBoundaries []int,
	splitAt int,
	keep int,
) []string {
	// Persist each older turn as an observation so the compress pipeline can
	// promote durable knowledge to mem_memories. We bundle per-turn rather
	// than one-blob-per-segment so granularity matches the rest of the memory
	// substrate.
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
	return obsIDs
}

// compactionInstruction is what the summariser is asked for. The sections
// are Claude Code's own list of what auto-compact preserves ("your requests
// and intent, key technical concepts, files examined or modified with
// important code snippets, errors and how they were fixed, pending tasks,
// and current work"), plus the OpenAI Agents SDK rules that keep a summary
// honest: quote error strings exactly, most recent update wins, mark
// unknowns UNVERIFIED.
const compactionInstruction = `CONTEXT CHECKPOINT. Write a handoff summary of the conversation so far so that you (or another model) can continue it without re-asking anything. Do not call tools. Do not answer the last message; summarise.

Markdown, these sections, omit any that are empty:

## Requests and intent
What the boss asked for, in his words where it matters, and what he ultimately wants.

## Decisions and constraints
Choices made and why; rules, preferences and limits he stated.

## Files and artifacts
Paths, repos, ids, URLs, env var NAMES (never secret values); the important snippets verbatim.

## Errors and fixes
Exact error strings, what caused them, how (or whether) they were resolved.

## Pending
What was promised or deferred and is not done.

## Current state
Where the work stands right now and the very next step.

Rules: be terse; the most recent update wins when facts conflict; quote error text exactly; mark anything you are not sure of UNVERIFIED.`

// compactionSystemPrompt frames the standalone (cold) summariser, used when
// the caller has no cached prefix to reuse.
const compactionSystemPrompt = "You are compressing a conversation between a user (the \"boss\") and his AI assistant, Jarvis, into a handoff summary. Preserve everything the assistant would otherwise have to re-ask."

// summarize runs the summariser. With a StableSystem it rides the
// conversation's own cached prefix (same system, same tools, calls
// disabled) plus the instruction, so the whole history is read from cache;
// without one it summarises a rendered transcript of `older` cold.
func (c *ConversationCompactor) summarize(ctx context.Context, cfg *CompactionConfig, full, older []llm.Message) (string, error) {
	out := make(chan llm.StreamEvent, 64)
	go func() {
		for range out {
			// drain - only the final Response matters
		}
	}()
	var (
		resp llm.Response
		err  error
	)
	cp, caching := c.provider.(llm.CachingProvider)
	if strings.TrimSpace(cfg.StableSystem) != "" && caching {
		msgs := append(append([]llm.Message{}, full...), llm.Message{Role: llm.RoleUser, Content: compactionInstruction})
		resp, err = cp.StreamCached(llm.WithNoToolCalls(ctx), cfg.Model, llm.SystemPrompt{Stable: cfg.StableSystem}, msgs, cfg.Tools, out)
	} else {
		transcript := renderTranscript(older)
		resp, err = c.provider.Stream(
			ctx,
			cfg.Model, // empty = provider default
			compactionSystemPrompt,
			[]llm.Message{{Role: llm.RoleUser, Content: compactionInstruction + "\n\n---\n" + transcript}},
			nil,
			out,
		)
	}
	close(out)
	if err != nil {
		return "", fmt.Errorf("summarize: %w", err)
	}
	summary := strings.TrimSpace(resp.Text)
	if summary == "" {
		return "", errors.New("summarizer returned empty body")
	}
	return summary, nil
}

// compactInTurn compacts INSIDE the last turn: everything after the turn's
// user message except the newest KeepToolExchanges assistant+tool exchanges
// is summarised, and the summary lands right after the user message as a
// user-role note. Tool pairs are never split: the cut is always at an
// assistant message boundary.
func (c *ConversationCompactor) compactInTurn(ctx context.Context, sessionID string, messages []llm.Message, cfg *CompactionConfig) ([]llm.Message, CompactionResult, error) {
	keepEx := cfg.KeepToolExchanges
	if keepEx <= 0 {
		keepEx = 3
	}
	user := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser {
			user = i
			break
		}
	}
	turns := len(turnStartIndices(messages))
	nothing := CompactionResult{OriginalTurns: turns, KeptTurns: turns}
	if user < 0 {
		return messages, nothing, nil
	}
	// Assistant boundaries after the user message, newest last.
	var bounds []int
	for i := user + 1; i < len(messages); i++ {
		if messages[i].Role == llm.RoleAssistant {
			bounds = append(bounds, i)
		}
	}
	if len(bounds) <= keepEx {
		return messages, nothing, nil
	}
	cut := bounds[len(bounds)-keepEx]
	older := messages[user+1 : cut]
	if len(older) == 0 {
		return messages, nothing, nil
	}
	var obsIDs []string
	if id, err := c.store.InsertObservation(ctx, ObservationInput{
		SessionID:  sessionID,
		HookName:   "conversation_compaction",
		RawText:    renderTranscript(older),
		Importance: 6,
		Payload: map[string]any{
			"source":           "conversation_compaction",
			"in_turn":          true,
			"original_session": sessionID,
		},
	}); err == nil {
		obsIDs = append(obsIDs, id)
	}
	summary, err := c.summarize(ctx, cfg, messages, older)
	if err != nil {
		return messages, CompactionResult{}, err
	}
	note := llm.Message{
		Role:    llm.RoleUser,
		Content: "[ Earlier tool work in this turn compacted. What it established: ]\n\n" + summary,
	}
	out := make([]llm.Message, 0, len(messages)-len(older)+1)
	out = append(out, messages[:user+1]...)
	out = append(out, note)
	out = append(out, messages[cut:]...)
	return out, CompactionResult{
		OriginalTurns:   turns,
		KeptTurns:       turns,
		CompactedTurns:  1,
		SummaryChars:    len(summary),
		ObservationIDs:  obsIDs,
		SummaryMarkdown: summary,
	}, nil
}

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
			// Drop the untrusted-content boundary before truncating. This is a
			// summary for our own compactor, and the banner is ~230 characters
			// of boilerplate that would otherwise crowd the actual result out
			// of a 600-character budget on every fetched item.
			b.WriteString(truncate(untrusted.StripWrapper(strings.TrimSpace(m.Content)), 600))
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
