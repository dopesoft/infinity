package tools

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// Forwarding a nested Claude Code job's OWN steps into the boss's chat.
//
// THE GAP THIS CLOSES. The 47-minute build of 2026-08-29 wrote 829 stream
// events: 112 Bash calls, 59 Reads, 28 Edits, 9 Writes, 37 file-changing calls
// across 7 files. Infinity read exactly ONE thing out of that per 15-second
// poll — the newest action — and turned it into a single progress label. The
// chat showed one spinning row for the whole 47 minutes while files appeared
// in the boss's Changes tab, which is how "he is coding but the UI tells me
// nothing" happens. His words: "TOTALLY NOT TRANSPARENT."
//
// Claude Code shows every step it takes. So does this now: each tool the
// nested job calls becomes a real row in the parent conversation, through the
// SAME `tool_call` / `tool_result` frames a first-party tool produces. Studio
// needs no new surface — `lib/chat/activity.ts` already speaks
// `claude_code__edit` / `__bash` / `__read`, already groups the reads, already
// renders a diff — and the rows persist as ordinary PostToolUse observations,
// so they survive a refresh like every other step.
//
// Rule #1b: this is a MECHANIC. It runs from the poll loop whether or not any
// prompt, skill or model remembers to narrate, and it cannot be dropped.

// NestedStep is ONE tool call made by the nested Claude Code job, addressed to
// the conversation that started it.
//
// It arrives twice: once when the call STARTS (Done=false, no output) and once
// when its result lands (Done=true). That split is deliberate — a nested
// `go test ./...` that runs for five minutes has to be visible for those five
// minutes, which is the entire complaint this exists to answer.
type NestedStep struct {
	// SessionID is the parent chat; RunID is the mem_runs row of the coding
	// job. Both are stamped by the runner, never guessed by the sink.
	SessionID string
	RunID     string
	// CallID is unique across jobs and never collides with a first-party tool
	// call id, so Studio's upsert-by-id is safe.
	CallID string
	// Tool is the name in the vocabulary Studio already speaks, e.g.
	// "claude_code__edit". Never a raw nested tool id.
	Tool      string
	Input     map[string]any
	Output    string
	IsError   bool
	Done      bool
	StartedAt time.Time
	EndedAt   time.Time
}

// StepSink delivers one nested step to the parent conversation. Wired once in
// serve.go; nil everywhere else, in which case forwarding is inert.
type StepSink func(ctx context.Context, step NestedStep)

// AttachStepSink installs the nested-step forwarder.
//
// It goes on the RUNNER for the same reason the live-run guard does: the
// runner is the single launcher `code_agent` and `background_build`-on-Mac
// both go through, so one wiring covers both engines by construction. Putting
// it on a tool is how you end up with a feature that works in the chat and is
// missing from every background build (Rule #1c).
func (r *ClaudeCodeRunner) AttachStepSink(fn StepSink) {
	if r != nil {
		r.steps = fn
	}
}

const (
	// maxNestedSteps is a runaway guard, not a coverage cap: the busiest real
	// build we have measured made ~250 tool calls, so a job that passes this
	// is malfunctioning rather than merely large. When it trips, the run row
	// is stamped so the truncation is a recorded fact and not a silent gap.
	maxNestedSteps = 1000
	// nestedOutputCap keeps one row's payload sane. Long enough for a build
	// log or a test failure to be readable in place, short enough that a
	// thousand of them are not a problem for the transcript.
	nestedOutputCap = 6000
)

// forwardSteps turns the new slice of the nested job's stream into parent-chat
// rows. Deduped on the nested call id, so a line read twice can never produce
// a second row.
func (p *claudePoll) forwardSteps(ctx context.Context, slice string) {
	if p == nil || p.sink == nil || strings.TrimSpace(slice) == "" {
		return
	}
	// A real conversation, or nothing. An ephemeral sub-agent session has no
	// chat to deliver into AND is not a uuid, so every step forwarded from one
	// would be a dropped frame plus a failed `mem_observations` write — a line
	// of log noise per step, hundreds per build, all of it saying nothing.
	if !isClaudeSessionID(p.parentSession) || isSubAgentSession(p.parentSession) {
		return
	}
	if p.sunk == nil {
		p.sunk = map[string]bool{}
	}
	for _, ev := range parseNestedEvents(slice) {
		if ev.callID == "" {
			continue
		}
		key := ev.callID
		if ev.result {
			key += "\x00done"
		}
		if p.sunk[key] {
			continue
		}
		if len(p.sunk) >= maxNestedSteps {
			if !p.sunkFull {
				p.sunkFull = true
				p.setMeta("nested_steps_truncated", "yes")
				codeAgentInfo().Printf("code_agent %s: past %d nested steps, stopping step forwarding for this run", p.jobID, maxNestedSteps)
			}
			return
		}
		p.sunk[key] = true

		step := NestedStep{
			SessionID: p.parentSession,
			RunID:     p.jobID,
			CallID:    p.sinkStamp + ev.callID,
			Tool:      ev.tool,
			StartedAt: time.Now().UTC(),
		}
		if ev.result {
			// The call half named the tool; a result on its own (the call
			// scrolled past before forwarding started) still gets a row rather
			// than being dropped, because a step nobody can see is the bug.
			step.Tool = firstNonBlank(p.sunkTool[ev.callID], ev.tool, "claude_code__bash")
			step.Done = true
			step.Output = clipRunes(ev.output, nestedOutputCap)
			step.IsError = ev.isError
			step.EndedAt = time.Now().UTC()
			if at, ok := p.sunkAt[ev.callID]; ok {
				step.StartedAt = at
				delete(p.sunkAt, ev.callID)
				delete(p.sunkTool, ev.callID)
			}
		} else {
			step.Input = ev.input
			if p.sunkAt == nil {
				p.sunkAt = map[string]time.Time{}
				p.sunkTool = map[string]string{}
			}
			p.sunkAt[ev.callID] = step.StartedAt
			p.sunkTool[ev.callID] = step.Tool
		}
		p.sink(ctx, step)
	}
}

// nestedEvent is one tool_use / tool_result found in the nested stream.
type nestedEvent struct {
	callID  string
	tool    string
	input   map[string]any
	output  string
	isError bool
	// result: this is the RESULT half of the pair, not the call.
	result bool
}

// parseNestedEvents reads a slice of Claude Code stream-json and returns the
// tool calls and results in it, oldest first.
//
// Lines that do not decode are skipped in silence on purpose: the incremental
// reader caps line length, so a `Read` that returned a 400KB file arrives
// truncated and unparseable. Its CALL is a separate, small line and does
// survive, which is the half that carries the file name worth showing.
func parseNestedEvents(slice string) []nestedEvent {
	var out []nestedEvent
	for _, line := range strings.Split(slice, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type      string          `json:"type"`
					ID        string          `json:"id"`
					Name      string          `json:"name"`
					Input     map[string]any  `json:"input"`
					ToolUseID string          `json:"tool_use_id"`
					Content   json.RawMessage `json:"content"`
					IsError   bool            `json:"is_error"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if ev.Type != "assistant" && ev.Type != "user" {
			continue
		}
		for _, c := range ev.Message.Content {
			switch c.Type {
			case "tool_use":
				name := nestedToolName(c.Name)
				if c.ID == "" || name == "" {
					continue
				}
				out = append(out, nestedEvent{callID: c.ID, tool: name, input: c.Input})
			case "tool_result":
				if c.ToolUseID == "" {
					continue
				}
				out = append(out, nestedEvent{
					callID:  c.ToolUseID,
					output:  toolResultText(c.Content),
					isError: c.IsError,
					result:  true,
				})
			}
		}
	}
	return out
}

// toolResultText renders a tool_result's `content`, which the protocol allows
// to be either a bare string or an array of content blocks.
func toolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		parts := make([]string, 0, len(blocks))
		for _, b := range blocks {
			if t := strings.TrimSpace(b.Text); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

// nestedToolName maps a nested tool id onto the vocabulary Studio speaks.
//
// Claude Code's own tools become `claude_code__<name>`, which
// `lib/chat/activity.ts` already has real verbs and glyphs for. A tool the
// nested run reached through ITS own MCP servers keeps its server prefix
// (`mcp__filesystem__read_file` → `filesystem__read_file`), which the
// humanizer renders as "Filesystem · read file" rather than burying it under a
// second "Claude Code ·".
// Lower-cased on the way out because those vocabulary keys are lower-case:
// resolving `Edit` relies on the consumer normalising, and a row that silently
// degrades to a generic "Claude Code · Edit" because one lookup somewhere was
// case-sensitive is precisely the kind of thing nobody notices for months.
func nestedToolName(name string) string {
	n := strings.TrimSpace(name)
	if n == "" {
		return ""
	}
	if rest := strings.TrimPrefix(n, "mcp__"); rest != n && rest != "" {
		return strings.ToLower(rest)
	}
	return "claude_code__" + strings.ToLower(n)
}

// NestedStepID namespaces one forwarded call id against its job, so the cloud
// path builds the same ids the Mac path does rather than inventing a second
// scheme. Exported for the settings-model background loop, which forwards its
// child session's steps the same way.
func NestedStepID(jobID, callID string) string {
	return nestedStepPrefix(jobID) + strings.TrimSpace(callID)
}

// nestedStepPrefix namespaces a job's forwarded call ids. Claude's own
// `toolu_…` ids are unique inside ONE session, and a resumed session reuses
// that space — so without this, a second pass could upsert onto the first
// pass's rows in the transcript.
func nestedStepPrefix(jobID string) string {
	id := strings.TrimSpace(jobID)
	if len(id) > 8 {
		id = id[:8]
	}
	if id == "" {
		return "cc-"
	}
	return "cc-" + id + "-"
}

func firstNonBlank(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// clipRunes trims to n bytes without splitting a rune, and says it was cut.
func clipRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && !isUTF8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "\n…[truncated]"
}

func isUTF8Start(b byte) bool { return b&0xC0 != 0x80 }
