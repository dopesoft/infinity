package tools

import (
	"context"
	"errors"
	"log"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/plan"
)

// Mirroring a nested Claude Code job's OWN checklist onto Infinity's plan.
//
// THE GAP THIS CLOSES. Claude Code runs its own agent loop inside `claude -p`
// and keeps its own TodoWrite list — the checklist it shows in its terminal.
// Infinity read two things out of that whole stream: the newest tool call, and
// a count of distinct activities. `claudeProgressLabel` turned the first into
// "Claude Code · Bash sed -n '1,95p' studio/… · 31s" and `ProgressForSteps`
// turned the second into a percentage that is not a measurement of anything
// (0.15 + 0.10 per activity, capped at 0.90). So the strip above the composer
// — the SAME BackgroundJobDock that draws a real plan for every other brain —
// fell through to its no-plan fallback and showed the boss a raw shell command
// beside a fabricated 25%. His words: "why does claude code have this same
// interaction to show a progress bar, instead of translating its tasks into
// our plan UI?"
//
// A checklist and a plan are already the same thing here: `todo_write` writes
// through plan.Store.SyncChecklist, which is what makes the dock, the Agent
// Work board and the dashboard agree. The nested list now goes through that
// same seam, so a Claude Code build renders identically to any other model —
// real step titles, a real denominator, an expandable checklist — with no new
// surface and no second store.
//
// Rule #1b: this is a MECHANIC. It runs from the poll loop regardless of what
// any prompt, skill or model remembers, and there is no sentence anywhere that
// can be dropped to break it.

// NestedChecklist is one snapshot of a nested job's own todo list, addressed
// to the conversation that started the job.
type NestedChecklist struct {
	// SessionID is the parent chat; RunID is the mem_runs row of the coding
	// job, and it is what the plan is OWNED by so a delegated build can never
	// replace a checklist the boss's own brain laid out.
	SessionID string
	RunID     string
	// Title is the run's label, so the dock keeps the same headline it was
	// already showing rather than renaming itself the moment a plan lands.
	Title string
	Items []plan.ChecklistItem
}

// NestedPlanSink is the whole seam: mirror the nested job's checklist while it
// works, and settle the plan it owns the moment the job reaches a verdict.
//
// Both halves, because either alone is the half-built failure. Sync without
// Settle leaves a checklist on the dock for a build that ended twenty minutes
// ago - the stale-spinner shape of a false green. Settle without Sync has
// nothing to settle.
type NestedPlanSink interface {
	// Sync mirrors the job's current checklist onto the parent chat's plan.
	Sync(ctx context.Context, c NestedChecklist) error
	// Settle closes out the plan this run owns. failed drives the step in
	// flight to failed (plan pauses, surfaces under "Awaiting you"); otherwise
	// every unfinished step is driven done. A no-op when the run owns no plan.
	Settle(ctx context.Context, runID string, failed bool, summary string) error
}

// AttachPlanSink installs the nested-checklist mirror.
//
// On the RUNNER, for the same reason the step sink and the live-run guard are:
// it is the single launcher `code_agent` and `background_build`-on-Mac both go
// through, so one wiring covers both engines by construction (Rule #1c).
func (r *ClaudeCodeRunner) AttachPlanSink(sink NestedPlanSink) {
	if r != nil {
		r.plans = sink
	}
}

// settleNestedPlan closes out the plan this job authored. Called from the two
// places a job reaches a verdict - `finish` (it ended, well or badly) and
// `stopped` (the boss killed it) - and deliberately NOT from `abandoned`,
// where the job is still working and its checklist is still true.
//
// Cheap and unconditional: the store scopes the write by owner_run_id, so a
// job that never mirrored anything settles nothing.
func (p *claudePoll) settleNestedPlan(ctx context.Context, failed bool, summary string) {
	if p == nil || p.plans == nil || !isClaudeSessionID(p.jobID) {
		return
	}
	sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if err := p.plans.Settle(sctx, p.jobID, failed, summary); err != nil {
		log.Printf("code_agent %s: settle mirrored plan: %v", p.jobID, err)
	}
}

// maxNestedTodos caps one mirrored checklist. Same ceiling todo_write applies;
// a list longer than this is noise, not a plan.
const maxNestedTodos = 50

// nestedTodoTool is Claude Code's own checklist verb, in the lower-cased
// vocabulary nestedToolName produces.
const nestedTodoTool = "claude_code__todowrite"

// syncNestedPlan mirrors the newest checklist in this slice of the nested
// stream onto the parent chat's plan.
//
// Only the NEWEST matters: Claude Code resends its complete list on every
// TodoWrite call, exactly as todo_write does, so the last one in the slice is
// the current state and the earlier ones are history nobody needs. The
// fingerprint then skips the write when nothing actually changed, so a
// forty-minute build does not rewrite an unchanged plan every fifteen seconds.
func (p *claudePoll) syncNestedPlan(ctx context.Context, slice string) {
	if p == nil || p.plans == nil || p.planDeclined || strings.TrimSpace(slice) == "" {
		return
	}
	// No run row, no owner: a job that fell back to a synthetic id cannot own
	// a plan, and attempting one would be a cast error on every poll.
	if !isClaudeSessionID(p.jobID) {
		return
	}
	// A real conversation, or nothing: an ephemeral sub-agent session has no
	// dock to draw into and is not a uuid, so a plan written for it would be
	// an ownerless card on the boss's board (the 2026-08-26 orphans).
	if !isClaudeSessionID(p.parentSession) || isSubAgentSession(p.parentSession) {
		return
	}
	items, ok := newestNestedChecklist(slice)
	if !ok {
		return
	}
	fp := checklistFingerprint(items)
	if fp == p.planPrint {
		return
	}
	err := p.plans.Sync(ctx, NestedChecklist{
		SessionID: p.parentSession,
		RunID:     p.jobID,
		Title:     p.label,
		Items:     items,
	})
	if errors.Is(err, plan.ErrPlanNotOwned) {
		// The conversation already owns a plan this job did not author - the
		// boss's own checklist, or a concurrent build's. Stop trying for the
		// life of this job rather than re-attempting the same refusal every
		// poll: the plan on screen is the right one, and this job's steps are
		// already visible in the activity ledger above it.
		p.planDeclined = true
		codeAgentInfo().Printf("code_agent %s: the conversation already owns a plan, so this job's checklist is not mirrored", p.jobID)
		return
	}
	if err != nil {
		// Never fatal: a build must not fail because its checklist could not
		// be mirrored. Logged loudly, because a dock that quietly stopped
		// tracking is the false-green shape we don't ship.
		log.Printf("code_agent %s: mirror nested checklist: %v", p.jobID, err)
		return
	}
	p.planPrint = fp
}

// newestNestedChecklist pulls the LAST TodoWrite call out of a slice of the
// nested stream and maps it onto plan checklist items.
func newestNestedChecklist(slice string) ([]plan.ChecklistItem, bool) {
	evs := parseNestedEvents(slice)
	for i := len(evs) - 1; i >= 0; i-- {
		ev := evs[i]
		if ev.result || ev.tool != nestedTodoTool {
			continue
		}
		if items := checklistFromTodos(ev.input); len(items) > 0 {
			return items, true
		}
	}
	return nil, false
}

// checklistFromTodos maps a TodoWrite input onto plan checklist items.
//
// Claude Code writes each item as {content, status, activeForm}; `text` is
// accepted too so this reads a list in Infinity's own todo_write shape
// unchanged, and neither side has to know which agent wrote it.
func checklistFromTodos(input map[string]any) []plan.ChecklistItem {
	raw, _ := input["todos"].([]any)
	items := make([]plan.ChecklistItem, 0, len(raw))
	for _, v := range raw {
		m, ok := v.(map[string]any)
		if !ok {
			continue
		}
		text := strings.TrimSpace(strString(m, "content"))
		if text == "" {
			text = strings.TrimSpace(strString(m, "text"))
		}
		if text == "" {
			continue
		}
		if len(text) > 200 {
			text = text[:200] + "…"
		}
		// NormalizeStepStatus is the ONE mapping from loose status words onto
		// step statuses, so "completed" / "complete" / "finished" all land the
		// same whichever agent said them.
		items = append(items, plan.ChecklistItem{
			Text:   text,
			Status: plan.NormalizeStepStatus(strings.TrimSpace(strString(m, "status"))),
		})
		if len(items) >= maxNestedTodos {
			break
		}
	}
	return items
}

// checklistFingerprint is what makes an unchanged list a no-op. It covers the
// STATUSES as well as the text, because a checklist that only ticks a box is
// exactly the change the dock exists to show.
func checklistFingerprint(items []plan.ChecklistItem) string {
	var b strings.Builder
	for _, it := range items {
		b.WriteString(it.Status)
		b.WriteByte(1)
		b.WriteString(it.Text)
		b.WriteByte(0)
	}
	return b.String()
}
