package agent

import (
	"context"
	"log"
	"os"
	"regexp"
	"strings"
	"time"
)

// Plan auto-start — the STRUCTURAL half of "the board never lies about what is
// happening right now".
//
// plan_continue.go stops the model quitting after it drafts a plan, and the
// settler closes the plan when the turn ends. The gap between them was the
// middle: the model would start actually working — edit files, run builds,
// commit — while every step of the plan still read 'pending', so the Agent Work
// board showed nothing in flight for minutes at a time. soul.md tells Jarvis to
// mark a step in_progress when he starts it, and the runtime brain (gpt-5.x)
// routinely drops that instruction (Rule #1b: a mechanic expressed as prose is a
// mechanic that silently vanishes).
//
// So the loop does it instead. Immediately before a consequential tool actually
// executes — and only AFTER the self-heal source guard, the consent gate and the
// Trust gate have all allowed the call — the loop asks the plan store to make
// sure exactly one step of this session's approved plan is in flight. Idempotent
// by construction (see plan.EnsureStepStarted): an already-running step is left
// exactly as it is, and a session with no approved actionable plan is a no-op.
//
// Off via INFINITY_PLAN_AUTOSTART=off.

// planAutoStartTimeout bounds the pre-execution probe. This sits on the critical
// path of every consequential tool call, so it stays short: a plan store that
// cannot answer in five seconds is a failure to surface, not something to wait
// out.
const planAutoStartTimeout = 5 * time.Second

// PlanStepStart is what the starter reports back: which step is now in flight,
// and whether this call is what put it there. Declared here rather than reusing
// plan.StepStart so the agent package doesn't drag a pgx/plan import into the
// loop — the same reason PlanContinuationChecker and PlanSettler are local.
type PlanStepStart struct {
	StepID  string
	Title   string
	Started bool
}

// PlanStepStarter guarantees a session's approved plan has a step in flight
// before consequential work begins. Implemented against plan.Store's
// EnsureStepStarted (adapted in serve.go). Nil-safe: when unset the loop simply
// never auto-starts, and the pre-existing behaviour stands.
type PlanStepStarter interface {
	EnsureStepStarted(ctx context.Context, sessionID string) (*PlanStepStart, error)
}

// PlanStepStarterFunc adapts a func to PlanStepStarter, mirroring
// CodeProposalFilerFunc.
type PlanStepStarterFunc func(ctx context.Context, sessionID string) (*PlanStepStart, error)

// EnsureStepStarted implements PlanStepStarter.
func (f PlanStepStarterFunc) EnsureStepStarted(ctx context.Context, sessionID string) (*PlanStepStart, error) {
	return f(ctx, sessionID)
}

// SetPlanStepStarter installs (or replaces) the auto-start mechanic. Safe to
// call after agent.New(); nil is fine (feature off).
func (l *Loop) SetPlanStepStarter(s PlanStepStarter) {
	if l == nil {
		return
	}
	l.planMu.Lock()
	l.planStarter = s
	l.planMu.Unlock()
}

func (l *Loop) planStepStarter() PlanStepStarter {
	if l == nil {
		return nil
	}
	l.planMu.RLock()
	defer l.planMu.RUnlock()
	return l.planStarter
}

// planAutoStartDisabled lets the boss kill the reflex.
func planAutoStartDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INFINITY_PLAN_AUTOSTART"))) {
	case "off", "false", "0", "no":
		return true
	}
	return false
}

// startPlanStepForTool marks the session's current plan step in flight before a
// consequential tool runs.
//
// Returns an error ONLY when the starter itself failed. That error is
// deliberately load-bearing: the caller must not execute the tool. Letting the
// work run anyway would be the silent-green shape this codebase keeps getting
// burned by — real work happening against a board that still says nothing has
// begun, with no signal that the two had diverged. Everything else (feature off,
// nothing wired, a synthetic sub-agent session, a non-work tool, no approved
// actionable plan) is a quiet nil.
func (l *Loop) startPlanStepForTool(ctx context.Context, sessionID, toolName string) error {
	if !isPlanTrackedWorkTool(toolName) || planAutoStartDisabled() {
		return nil
	}
	starter := l.planStepStarter()
	if starter == nil {
		return nil
	}
	// Delegate / peer / background children run in synthetic sessions that own
	// no plan; their work belongs to the parent's step, which the parent already
	// started when it called delegate.
	sid := strings.TrimSpace(sessionID)
	if sid == "" || IsSyntheticSessionID(sid) {
		return nil
	}

	sctx, cancel := context.WithTimeout(ctx, planAutoStartTimeout)
	defer cancel()
	res, err := starter.EnsureStepStarted(sctx, sid)
	if err != nil {
		log.Printf("plan auto-start: session=%s tool=%s err=%v", sid, toolName, err)
		return err
	}
	if res != nil && res.Started {
		planStartLog.Printf("plan auto-start: session=%s tool=%s step=%s now in progress", sid, toolName, res.StepID)
	}
	return nil
}

// planStartLog carries the success line. Railway tags stdout as info and stderr
// as error, so a "step is now in progress" written with the stdlib logger would
// show up red next to genuine failures.
var planStartLog = log.New(os.Stdout, "", log.LstdFlags)

// planStartRefusal is the tool result the model sees when the auto-start failed
// and the call was therefore not run. It names what happened in plain terms and
// gives the model the one recovery that actually resolves it, so it doesn't
// simply retry the same call into the iteration cap.
func planStartRefusal(toolName string, err error) string {
	return "BLOCKED: " + toolName + " did not run. I could not mark the step you are on as in progress (" +
		err.Error() + "), so running this now would do real work while the plan still shows nothing started — " +
		"the board would be describing a state that isn't true. Do not retry this call blindly. Read the plan with " +
		"plan_get, set the step you are working on to in_progress with plan_update, and then run this again. " +
		"If the plan itself is unreachable, say so plainly to the boss rather than working on blind."
}

// planTrackedWorkTools is the conservative allowlist of tools whose execution
// means "the work of a plan step has begun".
//
// The bar for membership is that the call CHANGES something outside the
// conversation: it writes a file, runs a command, moves git history, creates a
// project or a document, spends money, calls a person, or hands the work to a
// sub-agent. Anything that only looks (search, list, get, status, diff, read),
// anything that only manages the plan itself, and anything that only touches
// memory or the context window is deliberately absent — those routinely happen
// while the agent is still deciding what to do, and starting a step on them
// would put the board ahead of the work rather than in step with it.
//
// Deliberate exclusions worth naming, because each is a judgement call:
//   - plan_* and todo_write write the plan substrate itself. Starting a step
//     because the model touched the plan would make this mechanic self-firing.
//   - deploy_status / deploy_status_refresh are the only deploy-shaped tools in
//     the registry and both are read-only probes. There is no deploy-execution
//     tool to include; deploys are the boss's.
//   - skills_invoke returns a recipe for the model to execute, not a result
//     (see the LLM-only skill execution contract). The real work lands on the
//     tools below a moment later, and those start the step.
//   - git_pull and project_open change local state without producing any of the
//     step's output.
//   - browser_navigate / _open / _observe / _extract are looking; browser_act
//     clicks and types, which is doing.
var planTrackedWorkTools = map[string]bool{
	// Code and build execution.
	"code_agent":       true,
	"background_build": true,
	"code_exec":        true,

	// File mutation.
	"fs_save":       true,
	"fs_edit":       true,
	"artifact_save": true,

	// Shell.
	"bash_run": true,

	// Git history the boss will see.
	"git_stage":  true,
	"git_commit": true,
	"git_push":   true,

	// Project lifecycle.
	"project_create": true,
	"project_clone":  true,

	// Content the step is meant to produce.
	"document_create": true,
	"media_job":       true,

	// Automation the agent fires off as the work itself.
	"workflow_run": true,
	"cron_run_now": true,

	// Acting in the world.
	"phone_call":       true,
	"purchase_execute": true,
	"browser_act":      true,

	// Handing the work to a child. These run in synthetic sessions that can
	// never start the parent's step themselves, so if the parent doesn't start
	// it here, an entire delegated body of work happens invisibly.
	"delegate":          true,
	"delegate_parallel": true,
	"agent_team_start":  true,
}

// claudeCodeWorkSuffixes are the mutating tools on the Mac coding bridge. Read,
// Grep, Glob and LS are absent: reading the codebase is how the agent decides
// what to do, not the doing. Matched lowercase because the registry name is
// capitalised (claude_code__Edit) while the model and our own recipes routinely
// spell it claude_code__edit.
var claudeCodeWorkSuffixes = map[string]bool{
	"edit":         true,
	"multiedit":    true,
	"write":        true,
	"bash":         true,
	"notebookedit": true,
}

// composioWorkVerb matches the mutating half of the dynamically-named Composio
// verbs — the same send/create/delete shape the consent gate already treats as
// work (consent.go), reused rather than re-derived so the two can't drift.
var composioWorkVerb = regexp.MustCompile(`(?i)(SEND|CREATE|DELETE|REPLY|FORWARD|INSERT|UPDATE|PATCH)`)

// isPlanTrackedWorkTool reports whether executing this tool means a plan step's
// work has actually begun.
func isPlanTrackedWorkTool(name string) bool {
	n := strings.TrimSpace(name)
	if n == "" {
		return false
	}
	if planTrackedWorkTools[n] {
		return true
	}
	if suffix, ok := strings.CutPrefix(n, "claude_code__"); ok {
		return claudeCodeWorkSuffixes[strings.ToLower(suffix)]
	}
	if verb, ok := strings.CutPrefix(n, "composio__"); ok {
		return composioWorkVerb.MatchString(verb)
	}
	return false
}
