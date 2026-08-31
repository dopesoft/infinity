package agent

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/turnctx"
)

// Consent gate (Rule #1b mechanic behind soul.md "talk before you build").
//
// 2026-08-26: "Don't build.. lets discuss first" lost to the soul's "act, don't
// ask", a resumed plan from memory, and an injected self-heal directive. The
// runtime brain drops prose; this is the part it can't drop. When the boss's
// message reads as a conversation (turnctx.StanceDiscuss, judged by the
// IntentFlow classifier), the loop refuses tools that START work or CREATE
// something durable, and tells the model to propose instead. Reads, memory,
// tool loading and plan proposals stay open. Only interactive turns are
// gated; crons and sub-agents always have a work stance.
//
// The gate has exactly two doors out, and both are the boss's own word:
// plan_approve ("go ahead" on a proposal) and plan_resume ("carry on" with a
// plan he ALREADY approved). Both are deliberately absent from
// consentToolPattern — gating the verb that grants consent would deadlock the
// gate, which is what stranded a paused build on 2026-08-28 ("please continue
// the build and finish up" was read as discussion, plan_update was refused, and
// Jarvis reported that he had "blocked me from reopening the step"). Neither
// door can promote an UNAPPROVED proposal: plan_resume refuses one outright and
// plan.Store.MarkStep refuses steps of a proposed plan at the store chokepoint.

// stanceWait bounds how long the first work-tool call waits for the async
// classifier. Text-only turns never wait at all.
const stanceWait = 2500 * time.Millisecond

// consentToolPattern names the tools that do or start work: anything that
// creates a durable thing, sends, runs, spawns, or edits code.
var consentToolPattern = regexp.MustCompile(`^(` +
	`project_create|project_clone|code_agent|background_build|document_create|media_job|phone_call|` +
	// purchase_execute spends real money, so it is the last tool that should
	// ever fire during a "let's discuss" turn. purchase_propose is absent on
	// purpose: binding what a purchase WOULD be is exactly the kind of
	// groundwork discussing is for, and it charges nothing.
	`purchase_execute|` +
	`claude_code__(Edit|Write|Bash|NotebookEdit)|fs_save|fs_edit|git_commit|git_push|` +
	`delegate|delegate_parallel|agent_team_start|skills_invoke|todo_write|mandate_open|` +
	`plan_update|plan_verify|workflow_run|cron_run_now|preview_start|` +
	`(cron|sentinel|workflow|skill|calendar)_create\w*|` +
	`composio__\w*(SEND|CREATE|DELETE|REPLY|FORWARD|INSERT|UPDATE|PATCH)\w*` +
	`)$`)

// isConsentTool reports whether a tool needs the boss's go before it runs
// in a conversation. plan_create is deliberately NOT here: while discussing
// it produces a proposal (see tools/plan_tools.go), which is the right move.
func isConsentTool(name string) bool {
	return consentToolPattern.MatchString(strings.TrimSpace(name))
}

// discussRefusal is the tool result the model sees instead of running a
// work tool while the boss is talking it through.
func discussRefusal(toolName, reason string) string {
	var b strings.Builder
	b.WriteString("HOLD (talk first): the boss is talking this through, not asking for work yet")
	if reason != "" {
		b.WriteString(" (" + reason + ")")
	}
	b.WriteString(", so " + toolName + " did not run. Answer him: think with him, ask what he wants, ")
	b.WriteString("and when a plan takes shape lay it out with plan_create, which becomes a PROPOSAL he can approve ")
	b.WriteString("(or wait for his go). Do not retry this call until he says to go ahead.")
	return b.String()
}

// consentBlocks reports whether this call must be held for the turn's
// stance, waiting briefly for the first classification. Returns the reason
// for the tool result. Never blocks autonomous turns (no holder on ctx).
func consentBlocks(ctx context.Context, toolName string) (bool, string) {
	if !isConsentTool(toolName) {
		return false, ""
	}
	h := turnctx.StanceFromContext(ctx)
	if h == nil {
		return false, ""
	}
	st, why := h.Wait(ctx, stanceWait)
	if st == turnctx.StanceDiscuss {
		return true, why
	}
	// This call is about to DO work with the boss's consent, so latch the
	// turn: a chatty mid-build steer ("how's it going?") must not be able to
	// re-classify an approved build as a conversation and shut the gate on the
	// rest of it. The latch itself lives in the holder (turnctx/stance.go) so
	// no caller can forget it; this is the one place that knows a work tool
	// actually ran.
	h.MarkWorked(toolName)
	return false, ""
}

// turnIsDiscuss reports whether the boss is talking it through right now.
// Used to switch OFF the finishing reflexes for the turn: a conversation
// cannot "fail to deliver", so self-heal, plan-continue and the verify pass
// must not turn a reply that asks him a question into a 10,000-character
// doctrine (2026-08-26 16:42). Waits briefly for the first reading; nil
// holder (autonomous turn) is never discuss.
func turnIsDiscuss(ctx context.Context) bool {
	h := turnctx.StanceFromContext(ctx)
	if h == nil {
		return false
	}
	st, _ := h.Wait(ctx, stanceWait)
	return st == turnctx.StanceDiscuss
}

// discussSystemOverlay is appended to the volatile system prompt on every
// iteration of a discuss turn. It frames the register, not the content.
const discussSystemOverlay = `<conversation>
The boss is talking this through with you, not ordering work. Reply the way a sharp person talks across a table: a few short paragraphs at most, one or two ideas, plain prose (no headers, no numbered systems, no complete programmes), and end with the one question that moves the thinking forward. Your first reply to an idea is never the finished answer; it is your side of the conversation. Do not create, run, or build anything, and do not "fix" a reply that asked him a question: asking was the point.
</conversation>`

// firstIterationStanceWait bounds how long the FIRST LLM call of a turn waits
// for the classifier so a pure-text reply (which is the whole answer in a
// conversation) gets the conversation register. Later iterations read the
// current value instantly.
const firstIterationStanceWait = 900 * time.Millisecond

// discussOverlayFor returns the conversation overlay for this iteration's
// system prompt, or "" when the boss is not discussing (or the turn is
// autonomous).
func discussOverlayFor(ctx context.Context, firstIteration bool) string {
	h := turnctx.StanceFromContext(ctx)
	if h == nil {
		return ""
	}
	var st turnctx.Stance
	if firstIteration {
		st, _ = h.Wait(ctx, firstIterationStanceWait)
	} else {
		st, _ = h.Get()
	}
	if st == turnctx.StanceDiscuss {
		return discussSystemOverlay
	}
	return ""
}
