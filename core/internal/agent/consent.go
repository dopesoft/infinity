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

// stanceWait bounds how long the first work-tool call waits for the async
// classifier. Text-only turns never wait at all.
const stanceWait = 2500 * time.Millisecond

// consentToolPattern names the tools that do or start work: anything that
// creates a durable thing, sends, runs, spawns, or edits code.
var consentToolPattern = regexp.MustCompile(`^(` +
	`project_create|project_clone|code_agent|background_build|document_create|media_job|phone_call|` +
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
	return false, ""
}
