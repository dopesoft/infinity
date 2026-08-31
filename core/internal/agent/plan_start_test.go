package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// These tests pin the auto-start boundary. Both failure modes are silent, which
// is why they are worth a test rather than a reading:
//
//   - Never start, and real work runs against a plan whose every step still
//     reads 'pending'. The board describes a state that isn't true, and nothing
//     anywhere says the two diverged.
//   - Start on the wrong call — a refused one, a lookup, a plan edit — and the
//     board claims work began that never did, which is the same lie pointing the
//     other way.
//
// The order assertion is the load-bearing one: "the starter ran" is not the
// contract, "the starter ran BEFORE the work did" is.

// recordingStarter is a PlanStepStarter that records its calls and can be made
// to fail or to report a step that was already running.
type recordingStarter struct {
	mu       sync.Mutex
	sessions []string
	err      error
	result   *PlanStepStart
	// onCall runs inside EnsureStepStarted, so a tool can observe whether the
	// start had already happened by the time it executed.
	onCall func()
}

func (r *recordingStarter) EnsureStepStarted(_ context.Context, sessionID string) (*PlanStepStart, error) {
	r.mu.Lock()
	r.sessions = append(r.sessions, sessionID)
	r.mu.Unlock()
	if r.onCall != nil {
		r.onCall()
	}
	if r.err != nil {
		return nil, r.err
	}
	return r.result, nil
}

func (r *recordingStarter) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.sessions)
}

// namedTool is a countingTool with a configurable name, so a test can drive the
// loop with a real registry entry called "bash_run" or "fs_read" and exercise the
// classifier on the live path rather than in isolation.
type namedTool struct {
	name            string
	calls           atomic.Int64
	startedBeforeMe atomic.Bool
	observe         func() bool
}

func (t *namedTool) Name() string           { return t.name }
func (t *namedTool) Description() string    { return "test tool" }
func (t *namedTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *namedTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	if t.observe != nil {
		t.startedBeforeMe.Store(t.observe())
	}
	t.calls.Add(1)
	return "ok", nil
}

// refusingGate blocks every call outright with no Trust contract — the
// wrong-bridge / hard-refusal shape, where nothing runs and nobody is asked.
type refusingGate struct{}

func (refusingGate) Authorize(_ context.Context, _, _, _ string, _ map[string]any) GateDecision {
	return GateDecision{Allow: false, Reason: "wrong bridge for this call", Redirect: true}
}

func (refusingGate) WaitForDecision(_ context.Context, _ string, _ time.Duration) (bool, string) {
	return false, "not asked"
}

// denyingGate parks the call on a Trust contract and then reports that the boss
// declined it.
type denyingGate struct{}

func (denyingGate) Authorize(_ context.Context, _, _, _ string, _ map[string]any) GateDecision {
	return GateDecision{Allow: false, WaitForApproval: true, ContractID: "ct-1", WaitTimeout: time.Second}
}

func (denyingGate) WaitForDecision(_ context.Context, _ string, _ time.Duration) (bool, string) {
	return false, "denied by the boss"
}

// approvingGate parks the call on a Trust contract and then reports that the
// boss approved it — the second place in the loop where work actually executes.
type approvingGate struct{}

func (approvingGate) Authorize(_ context.Context, _, _, _ string, _ map[string]any) GateDecision {
	return GateDecision{Allow: false, WaitForApproval: true, ContractID: "ct-ok", WaitTimeout: time.Second}
}

func (approvingGate) WaitForDecision(_ context.Context, _ string, _ time.Duration) (bool, string) {
	return true, ""
}

// runLoopOnce drives one turn whose model calls the given tool, with the loop
// capped to a single iteration+segment so the test ends deterministically.
func runLoopOnce(t *testing.T, tool tools.Tool, gate ToolGate, starter PlanStepStarter) (toolResult string, runErr error) {
	t.Helper()
	t.Setenv("INFINITY_MAX_TOOL_ITERATIONS", "1")
	t.Setenv("INFINITY_MAX_TURN_SEGMENTS", "1")
	t.Setenv("INFINITY_PLAN_AUTOSTART", "")

	reg := tools.NewRegistry()
	reg.Register(tool)
	cfg := Config{
		LLM:   &fakeProvider{resp: llm.Response{ToolCalls: []llm.ToolCall{{ID: "c1", Name: tool.Name(), Input: map[string]any{}}}}},
		Tools: reg,
	}
	if gate != nil {
		cfg.Gate = gate
	}
	l := New(cfg)
	if starter != nil {
		l.SetPlanStepStarter(starter)
	}

	out := make(chan RunEvent, 256)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ev := range out {
			if ev.Kind == EventToolResult && ev.ToolResult != nil {
				toolResult = ev.ToolResult.Output
			}
		}
	}()
	runErr = l.Run(context.Background(), "test-session", "go", "", nil, out)
	close(out)
	wg.Wait()
	return toolResult, runErr
}

// TestAutoStart_AllowedWorkStartsStepBeforeExecuting is the core case. A
// consequential tool the gates allowed must find its plan step already in flight
// by the time it runs — starting it afterwards would leave a window where the
// work is real and the board still says nothing began.
func TestAutoStart_AllowedWorkStartsStepBeforeExecuting(t *testing.T) {
	var started atomic.Bool
	starter := &recordingStarter{
		result: &PlanStepStart{StepID: "s1", Title: "Build it", Started: true},
		onCall: func() { started.Store(true) },
	}
	tool := &namedTool{name: "bash_run", observe: started.Load}

	if _, err := runLoopOnce(t, tool, nil, starter); err == nil {
		t.Fatal("the capped loop is expected to end on the iteration cap")
	}
	if tool.calls.Load() != 1 {
		t.Fatalf("allowed work must still run: tool calls = %d, want 1", tool.calls.Load())
	}
	if starter.calls() != 1 {
		t.Fatalf("starter calls = %d, want 1", starter.calls())
	}
	if !tool.startedBeforeMe.Load() {
		t.Fatal("the step must be in flight BEFORE the work executes, not after")
	}
	if starter.sessions[0] != "test-session" {
		t.Fatalf("starter got session %q, want the live session id", starter.sessions[0])
	}
}

// TestAutoStart_RefusedWorkNeverStarts: a gate refusal means nothing ran, so
// nothing may be marked in flight. Starting here would show the boss a step
// working on something that was blocked.
func TestAutoStart_RefusedWorkNeverStarts(t *testing.T) {
	starter := &recordingStarter{result: &PlanStepStart{StepID: "s1", Started: true}}
	tool := &namedTool{name: "bash_run"}

	runLoopOnce(t, tool, refusingGate{}, starter)

	if tool.calls.Load() != 0 {
		t.Fatalf("a refused call must not execute, ran %d times", tool.calls.Load())
	}
	if starter.calls() != 0 {
		t.Fatalf("a refused call must not start a plan step, starter calls = %d", starter.calls())
	}
}

// TestAutoStart_BossDeniedNeverStarts: the boss declining a Trust contract is
// the same shape — the work did not happen, so the plan must not say it did.
func TestAutoStart_BossDeniedNeverStarts(t *testing.T) {
	starter := &recordingStarter{result: &PlanStepStart{StepID: "s1", Started: true}}
	tool := &namedTool{name: "git_push"}

	runLoopOnce(t, tool, denyingGate{}, starter)

	if tool.calls.Load() != 0 {
		t.Fatalf("a denied call must not execute, ran %d times", tool.calls.Load())
	}
	if starter.calls() != 0 {
		t.Fatalf("a denied call must not start a plan step, starter calls = %d", starter.calls())
	}
}

// TestAutoStart_ApprovedContractStartsBeforeExecuting covers the OTHER place the
// loop executes a tool: the branch taken when the boss approves a Trust card.
// Wiring the auto-start into only the plain-allow path would leave every gated
// call — the coding and shell work most likely to belong to a plan step —
// running with the board still showing nothing in flight.
func TestAutoStart_ApprovedContractStartsBeforeExecuting(t *testing.T) {
	var started atomic.Bool
	starter := &recordingStarter{
		result: &PlanStepStart{StepID: "s1", Title: "Ship it", Started: true},
		onCall: func() { started.Store(true) },
	}
	tool := &namedTool{name: "git_push", observe: started.Load}

	runLoopOnce(t, tool, approvingGate{}, starter)

	if tool.calls.Load() != 1 {
		t.Fatalf("approved work must run, ran %d times", tool.calls.Load())
	}
	if starter.calls() != 1 {
		t.Fatalf("starter calls = %d, want 1 on the approved path", starter.calls())
	}
	if !tool.startedBeforeMe.Load() {
		t.Fatal("the step must be in flight BEFORE approved work executes, not after")
	}
}

// TestAutoStart_ApprovedContractStarterFailureBlocks: the honesty guard has to
// hold on the approved path too, or a Trust-gated call becomes the one way to
// run work past a plan store that isn't answering.
func TestAutoStart_ApprovedContractStarterFailureBlocks(t *testing.T) {
	starter := &recordingStarter{err: errors.New("plan store unreachable")}
	tool := &namedTool{name: "git_push"}

	result, _ := runLoopOnce(t, tool, approvingGate{}, starter)

	if tool.calls.Load() != 0 {
		t.Fatalf("approved work must not run when the step could not be started, ran %d times", tool.calls.Load())
	}
	if !strings.Contains(result, "BLOCKED") || !strings.Contains(result, "plan store unreachable") {
		t.Fatalf("the failure must reach the model naming what went wrong, got %q", result)
	}
}

// TestAutoStart_ReadOnlyToolDoesNotStart: looking is not working. Reading a file
// is how the agent decides what to do, and a plan that flips to in_progress on a
// lookup is a plan running ahead of the work.
func TestAutoStart_ReadOnlyToolDoesNotStart(t *testing.T) {
	starter := &recordingStarter{result: &PlanStepStart{StepID: "s1", Started: true}}
	tool := &namedTool{name: "fs_read"}

	runLoopOnce(t, tool, nil, starter)

	if tool.calls.Load() != 1 {
		t.Fatalf("a read-only tool must still run, ran %d times", tool.calls.Load())
	}
	if starter.calls() != 0 {
		t.Fatalf("a read-only tool must not start a plan step, starter calls = %d", starter.calls())
	}
}

// TestAutoStart_PlanToolDoesNotStart: the plan verbs write the plan substrate
// itself. Auto-starting because the model edited the plan would make this
// mechanic fire on its own bookkeeping.
func TestAutoStart_PlanToolDoesNotStart(t *testing.T) {
	for _, name := range []string{"plan_create", "plan_update", "plan_get", "todo_write"} {
		t.Run(name, func(t *testing.T) {
			starter := &recordingStarter{result: &PlanStepStart{StepID: "s1", Started: true}}
			runLoopOnce(t, &namedTool{name: name}, nil, starter)
			if starter.calls() != 0 {
				t.Fatalf("%s must not start a plan step, starter calls = %d", name, starter.calls())
			}
		})
	}
}

// TestAutoStart_StarterFailureBlocksTheWork is the honesty guard. If the plan
// store cannot be reached, the work must NOT run: executing anyway is the exact
// silent-divergence this mechanic exists to prevent, and it would leave no
// signal that the board had stopped tracking reality.
func TestAutoStart_StarterFailureBlocksTheWork(t *testing.T) {
	starter := &recordingStarter{err: errors.New("plan store unreachable")}
	tool := &namedTool{name: "code_agent"}

	result, _ := runLoopOnce(t, tool, nil, starter)

	if tool.calls.Load() != 0 {
		t.Fatalf("work must not run when the step could not be started, ran %d times", tool.calls.Load())
	}
	if starter.calls() != 1 {
		t.Fatalf("starter calls = %d, want 1", starter.calls())
	}
	if !strings.Contains(result, "BLOCKED") || !strings.Contains(result, "plan store unreachable") {
		t.Fatalf("the failure must reach the model naming what went wrong, got %q", result)
	}
}

// TestAutoStart_ExistingInProgressAccepted: a step already running comes back
// with Started false, which is a success, not a reason to hold the work. The
// store leaves it untouched (pinned in plan/start_step_test.go); the loop's job
// is simply not to treat it as a failure.
func TestAutoStart_ExistingInProgressAccepted(t *testing.T) {
	starter := &recordingStarter{result: &PlanStepStart{StepID: "s1", Title: "Build it", Started: false}}
	tool := &namedTool{name: "fs_save"}

	result, _ := runLoopOnce(t, tool, nil, starter)

	if tool.calls.Load() != 1 {
		t.Fatalf("an already-running step must not block the work, ran %d times", tool.calls.Load())
	}
	if strings.Contains(result, "BLOCKED") {
		t.Fatalf("an already-running step is not a failure, got %q", result)
	}
}

// TestAutoStart_NoPlanIsSilent: a session with no approved actionable plan gets
// a nil result, which must read as "nothing to do here", never as an error that
// stops the boss's work.
func TestAutoStart_NoPlanIsSilent(t *testing.T) {
	starter := &recordingStarter{result: nil}
	tool := &namedTool{name: "bash_run"}

	result, _ := runLoopOnce(t, tool, nil, starter)

	if tool.calls.Load() != 1 {
		t.Fatalf("no plan must not block the work, ran %d times", tool.calls.Load())
	}
	if strings.Contains(result, "BLOCKED") {
		t.Fatalf("no plan is not a failure, got %q", result)
	}
}

// TestStartPlanStepForTool_Guards covers the cheap exits directly: they must all
// be silent no-ops rather than errors the loop has to special-case.
func TestStartPlanStepForTool_Guards(t *testing.T) {
	newLoop := func(t *testing.T, s PlanStepStarter) *Loop {
		t.Helper()
		l := &Loop{}
		l.SetPlanStepStarter(s)
		return l
	}

	t.Run("no starter wired", func(t *testing.T) {
		if err := (&Loop{}).startPlanStepForTool(context.Background(), "s", "bash_run"); err != nil {
			t.Fatalf("unwired loop must be a no-op, got %v", err)
		}
	})

	t.Run("disabled via env, starter not consulted", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_AUTOSTART", "off")
		st := &recordingStarter{err: errors.New("must not be called")}
		if err := newLoop(t, st).startPlanStepForTool(context.Background(), "s", "bash_run"); err != nil {
			t.Fatalf("disabled must not error, got %v", err)
		}
		if st.calls() != 0 {
			t.Fatalf("starter consulted while disabled (calls=%d)", st.calls())
		}
	})

	t.Run("synthetic sub-agent session is skipped", func(t *testing.T) {
		t.Setenv("INFINITY_PLAN_AUTOSTART", "")
		st := &recordingStarter{err: errors.New("must not be called")}
		l := newLoop(t, st)
		for _, sid := range []string{delegateSessionIDPrefix + "x", backgroundSessionIDPrefix + "x", ""} {
			if err := l.startPlanStepForTool(context.Background(), sid, "bash_run"); err != nil {
				t.Fatalf("session %q must be a no-op, got %v", sid, err)
			}
		}
		if st.calls() != 0 {
			t.Fatalf("starter consulted for a synthetic session (calls=%d)", st.calls())
		}
	})
}

// TestIsPlanTrackedWorkTool pins the classifier. The names are taken from the
// live registry, so a rename that silently drops a tool out of the work set
// breaks here rather than in production.
func TestIsPlanTrackedWorkTool(t *testing.T) {
	work := []string{
		"code_agent", "background_build", "code_exec",
		"fs_save", "fs_edit", "artifact_save",
		"bash_run",
		"git_stage", "git_commit", "git_push",
		"project_create", "project_clone",
		"document_create", "media_job",
		"workflow_run", "cron_run_now",
		"phone_call", "purchase_execute", "browser_act",
		"delegate", "delegate_parallel", "agent_team_start",
		// The Mac coding bridge, in both the registry's capitalisation and the
		// lowercase spelling our own recipes use.
		"claude_code__Edit", "claude_code__Write", "claude_code__Bash",
		"claude_code__edit", "claude_code__bash", "claude_code__NotebookEdit",
		// Composio's dynamically-named mutating verbs.
		"composio__GMAIL_SEND_EMAIL", "composio__GITHUB_CREATE_ISSUE",
		"composio__SLACK_DELETE_MESSAGE",
	}
	for _, name := range work {
		if !isPlanTrackedWorkTool(name) {
			t.Errorf("%s does consequential work and must start the step", name)
		}
	}

	notWork := []string{
		// Plan management — must never fire on its own bookkeeping.
		"plan_create", "plan_approve", "plan_update", "plan_verify", "plan_get",
		"plan_list", "plan_revise", "plan_cancel", "todo_write",
		// Looking, not doing.
		"fs_read", "fs_ls", "git_status", "git_diff", "web_search", "http_fetch",
		"artifact_get", "artifact_list", "task_list", "workflow_status",
		"preview_status", "purchase_status", "skills_list", "cron_list",
		"deploy_status", "deploy_status_refresh", "read_email",
		"browser_observe", "browser_extract", "browser_navigate", "browser_open",
		"claude_code__Read", "claude_code__Grep", "claude_code__Glob", "claude_code__LS",
		"composio__GMAIL_FETCH_EMAILS", "composio__GITHUB_LIST_REPOS",
		// Memory and context.
		"remember", "recall", "forget", "compact_context",
		"load_tools", "unload_tools", "tool_search",
		// Deliberate exclusions, each a judgement call worth pinning:
		// skills_invoke returns a recipe rather than a result, and the real
		// work lands on the tools above a moment later; git_pull and
		// project_open change local state without producing any of the step's
		// output.
		"skills_invoke", "git_pull", "project_open",
		// Empty / unknown.
		"", "   ", "some_unregistered_tool",
	}
	for _, name := range notWork {
		if isPlanTrackedWorkTool(name) {
			t.Errorf("%q must NOT start a plan step", name)
		}
	}
}
