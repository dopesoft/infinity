package agent

import (
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Session helpers for context economy. Every one of these edits the
// in-memory transcript only: the durable record (hooks, mem_observations,
// traces) already has the full content, so nothing here loses information,
// it only stops re-sending it.

// pinVolatile attaches this turn's context to the newest user message, the
// one that opened the turn, and remembers where it went. Providers render
// Message.Volatile as that message's trailing block, so it sits at a fixed
// byte offset for every call of the turn.
func (s *Session) pinVolatile(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.volatileAt = -1
	text = strings.TrimSpace(text)
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role != llm.RoleUser {
			continue
		}
		s.Messages[i].Volatile = text
		if text != "" {
			s.volatileAt = i
		}
		return
	}
}

// clearVolatile removes the pinned context when the turn ends, so the next
// turn's history carries the conversation and not a stale copy of last
// turn's retrieval. Sweeps every message, not only the remembered index:
// compaction can move things.
func (s *Session) clearVolatile() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.Messages {
		s.Messages[i].Volatile = ""
	}
	s.volatileAt = -1
}

// insertWorldState places a world-state message ahead of the newest user
// message (so the request stays last, with its pinned context), or at the
// end when the turn is resuming without a fresh user message.
func (s *Session) insertWorldState(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	msg := llm.Message{Role: llm.RoleUser, Content: text, Meta: map[string]any{MetaWorldState: true}}
	n := len(s.Messages)
	if n > 0 && s.Messages[n-1].Role == llm.RoleUser {
		s.Messages = append(s.Messages[:n-1], msg, s.Messages[n-1])
		if s.volatileAt == n-1 {
			s.volatileAt = n
		}
		return
	}
	s.Messages = append(s.Messages, msg)
}

// MetaWorldState marks a synthetic user message that carries world state.
// Never persisted (Meta is json:"-"); the durable transcript is built from
// hooks, which never see it. Lives in llm so providers can render it as
// context rather than as the boss's words.
const MetaWorldState = llm.MetaWorldState

// appendWindDown attaches the budget notice to the newest tool result (new
// content anyway, so no cached prefix moves). Reports false when the newest
// message is not a tool result, in which case the caller falls back to the
// per-call system overlay.
func (s *Session) appendWindDown() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.Messages)
	if n == 0 || s.Messages[n-1].Role != llm.RoleTool {
		return false
	}
	if strings.HasSuffix(s.Messages[n-1].Content, turnWindDownBlock) {
		return true
	}
	s.Messages[n-1].Content += "\n\n" + turnWindDownBlock
	return true
}

// clearedToolResultMarker is what replaces a cleared tool result. %s is the
// tool name, %d the chars removed. Names the recovery path so the model can
// get the output back if it needs it, the way Anthropic's server-side
// clear_tool_uses leaves a placeholder and Cline leaves a "[NOTE] …removed"
// line.
const clearedToolResultMarker = "[%s result cleared to save context: %d chars. The full output is in memory: traces_search / trace_inspect for this call.]"

// clearOldToolResults replaces the content of every tool result except the
// newest keep with a one-line marker, skipping tools in exclude and results
// already cleared. Returns how many chars it freed. It does NOT apply the
// edit when the total freed would be under minFree: a change that small
// still invalidates the cached prefix from the first cleared result on,
// and is not worth that (Anthropic's clear_at_least).
func (s *Session) clearOldToolResults(keep int, exclude map[string]bool, minFree int) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if keep < 0 {
		keep = 0
	}
	// Walk from the newest: the first `keep` tool results stay whole.
	kept := 0
	var idx []int
	for i := len(s.Messages) - 1; i >= 0; i-- {
		m := s.Messages[i]
		if m.Role != llm.RoleTool {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if exclude[m.ToolName] || strings.HasPrefix(m.Content, "[") && strings.Contains(m.Content, "result cleared to save context") {
			continue
		}
		idx = append(idx, i)
	}
	freed := 0
	for _, i := range idx {
		freed += len(s.Messages[i].Content)
	}
	if freed < minFree {
		return 0
	}
	for _, i := range idx {
		m := &s.Messages[i]
		name := m.ToolName
		if name == "" {
			name = "tool"
		}
		m.Content = fmt.Sprintf(clearedToolResultMarker, name, len(m.Content))
	}
	return freed
}

// degradeOldAttachments drops the bytes of attachments on every user message
// except the newest one, keeping the extracted text and the file's name. The
// model saw the image or document when it arrived; re-sending the bytes on
// every later call of every later turn is the single largest silent cost on
// an attachment-heavy session. Reports how many attachments were degraded.
func (s *Session) degradeOldAttachments() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	newest := -1
	for i := len(s.Messages) - 1; i >= 0; i-- {
		if s.Messages[i].Role == llm.RoleUser {
			newest = i
			break
		}
	}
	n := 0
	for i := range s.Messages {
		if i == newest || s.Messages[i].Role != llm.RoleUser {
			continue
		}
		for j := range s.Messages[i].Attachments {
			a := &s.Messages[i].Attachments[j]
			if len(a.Data) == 0 && len(a.Pages) == 0 {
				continue
			}
			a.Data = nil
			a.Pages = nil
			a.Note = strings.TrimSpace(strings.TrimSpace(a.Note) + " " + "(shown earlier in this conversation; the bytes are no longer re-sent, ask the boss to re-attach if you need to look again)")
			n++
		}
	}
	return n
}
