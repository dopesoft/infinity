package server

import (
	"strings"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// Putting a nested Claude Code job's steps on screen.
//
// A `code_agent` build is a whole agent working inside one tool call: it
// reads, edits, runs commands, and writes hundreds of steps to its own stream
// on the Mac. None of that reached the chat. The boss watched ONE spinning row
// for 47 minutes while files appeared in his Changes tab, and said the only
// fair thing about it: "TOTALLY NOT TRANSPARENT."
//
// This is the delivery half of the fix. `tools.claudePoll` reads the nested
// steps off the stream as they happen and hands them here; this puts them on
// the exact same `tool_call` / `tool_result` frames a first-party tool
// produces, into the session that started the job.
//
// Deliberately NOT a new frame type and NOT a new Studio surface. Studio's
// activity vocabulary already speaks `claude_code__edit` / `__bash` / `__read`
// — it has verbs, glyphs, diff rendering and grouping for all of them — so a
// nested edit renders exactly like a first-party one, folds into the same
// ledger, and needs nothing new to be built. Reuse-first, per CLAUDE.md.

// BroadcastNestedStep delivers one step of a nested coding job into the
// conversation that started it.
//
// A step arrives twice: once when the call starts (a `tool_call` frame, so the
// row appears the moment the work does) and once when its result lands (a
// `tool_result` frame, upserted onto the same row by id). That split is what
// makes a five-minute nested `go test` visible for those five minutes instead
// of appearing only once it is over.
//
// Drops silently when the session has no live socket — the same contract every
// other frame has. Persistence is not this function's job: the sink also fires
// a PostToolUse hook, so the row is rebuilt from `mem_observations` on reload
// exactly like every other step (see serve.go).
func (s *Server) BroadcastNestedStep(step tools.NestedStep) {
	if s == nil {
		return
	}
	sessionID := tools.SessionForPublish(strings.TrimSpace(step.SessionID))
	if sessionID == "" || strings.TrimSpace(step.CallID) == "" || strings.TrimSpace(step.Tool) == "" {
		return
	}
	send := s.sessionSender(sessionID)
	if !step.Done {
		send(wsServerEvent{
			Type:      "tool_call",
			SessionID: sessionID,
			ToolCall: &wsToolEvent{
				ID:        step.CallID,
				Name:      step.Tool,
				Input:     step.Input,
				StartedAt: step.StartedAt,
				Nested:    true,
			},
		})
		return
	}
	send(wsServerEvent{
		Type:      "tool_result",
		SessionID: sessionID,
		ToolResult: &wsToolEvent{
			ID:        step.CallID,
			Name:      step.Tool,
			Output:    step.Output,
			IsError:   step.IsError,
			StartedAt: step.StartedAt,
			EndedAt:   step.EndedAt,
			Nested:    true,
		},
	})
}
