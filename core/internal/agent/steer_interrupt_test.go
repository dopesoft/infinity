package agent

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// Why: 2026-08-26 the boss said "don't build, discuss" while a code_agent job
// ran for nine minutes; his message sat in the steer channel unread because the
// loop was blocked inside the tool. A SteerInterruptible tool must stop the
// moment he speaks, and his words must be handed to the model next.

type blockingTool struct {
	name        string
	interruptOK bool
	started     chan struct{}
}

func (b *blockingTool) Name() string           { return b.name }
func (b *blockingTool) Description() string    { return "blocks until cancelled" }
func (b *blockingTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (b *blockingTool) InterruptOnSteer() bool { return b.interruptOK }
func (b *blockingTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	close(b.started)
	select {
	case <-ctx.Done():
		return "stopped: " + ctx.Err().Error(), nil
	case <-time.After(3 * time.Second):
		return "finished on its own", nil
	}
}

// detachableTool stands in for code_agent: its real work runs somewhere else
// (a detached `claude -p` on the Mac), so a non-stop message must DETACH it,
// not cancel it. It records which wire the loop actually pulled.
type detachableTool struct {
	name      string
	started   chan struct{}
	cancelled atomic.Bool
	detached  atomic.Bool
}

func (d *detachableTool) Name() string           { return d.name }
func (d *detachableTool) Description() string    { return "work that outlives the turn" }
func (d *detachableTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (d *detachableTool) InterruptOnSteer() bool { return true }
func (d *detachableTool) DetachOnSteer() bool    { return true }
func (d *detachableTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	close(d.started)
	select {
	case <-tools.DetachRequested(ctx):
		d.detached.Store(true)
		return "STILL RUNNING (not stopped, not failed): Claude Code is Edit core/x.go. Run id run-1.", nil
	case <-ctx.Done():
		d.cancelled.Store(true)
		return "stopped: " + ctx.Err().Error(), nil
	case <-time.After(3 * time.Second):
		return "finished on its own", nil
	}
}

func newLoopWith(t *testing.T, tl ...tools.Tool) *Loop {
	t.Helper()
	reg := tools.NewRegistry()
	for _, x := range tl {
		reg.Register(x)
	}
	return &Loop{tools: reg}
}

func TestExecuteInterruptible_SteerStopsOptedInTool(t *testing.T) {
	bt := &blockingTool{name: "code_agent_fake", interruptOK: true, started: make(chan struct{})}
	loop := newLoopWith(t, bt)
	steer := make(chan Steer, 4)
	go func() {
		<-bt.started
		steer <- Steer{Text: "well i just told u not to build anything"}
		steer <- Steer{Text: "discuss first"}
	}()
	out, err, consumed := loop.executeInterruptible(context.Background(), llm.ToolCall{ID: "1", Name: bt.name}, steer)
	if err != nil {
		t.Fatalf("interrupt must not surface as a tool error: %v", err)
	}
	if !strings.HasPrefix(out, "INTERRUPTED:") || !strings.Contains(out, "stopped: context canceled") {
		t.Fatalf("result must say it was stopped for the boss, got: %q", out)
	}
	if len(consumed) != 2 || consumed[0].Text != "well i just told u not to build anything" {
		t.Fatalf("both queued steers must be handed back in order, got %+v", consumed)
	}
}

// Why: 2026-08-28, the other half of the same bug. "how's it going?" killed a
// 14-minute build, because arrival of a message WAS the kill order. A tool
// whose work outlives the turn must be DETACHED by an ordinary message: still
// alive, boss answered now, result reported back later.
func TestExecuteInterruptible_NonStopSteerDetachesInsteadOfKilling(t *testing.T) {
	dt := &detachableTool{name: "code_agent_fake", started: make(chan struct{})}
	loop := newLoopWith(t, dt)
	steer := make(chan Steer, 2)
	go func() {
		<-dt.started
		steer <- Steer{Text: "how's it going?"}
	}()
	out, err, consumed := loop.executeInterruptible(context.Background(), llm.ToolCall{ID: "1", Name: dt.name}, steer)
	if err != nil {
		t.Fatalf("a detach must not surface as a tool error: %v", err)
	}
	if dt.cancelled.Load() {
		t.Fatal("a question must NEVER cancel the tool — that is the bug this exists to stop")
	}
	if !dt.detached.Load() {
		t.Fatal("the loop must have pulled the detach wire")
	}
	if !strings.HasPrefix(out, "STILL RUNNING:") {
		t.Fatalf("the sentinel must tell the model the job is alive, got: %q", out)
	}
	for _, want := range []string{"DETACHED, not stopped", "Do NOT start it again", "Run id run-1"} {
		if !strings.Contains(out, want) {
			t.Fatalf("result missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "do not restart this call unless he asks") {
		t.Fatal("the old sentinel wording is what left long jobs dead; it must be gone from the detach path")
	}
	if len(consumed) != 1 || consumed[0].Text != "how's it going?" {
		t.Fatalf("the boss must still be answered this turn, got %+v", consumed)
	}
}

// Why: the other side of the same switch — an explicit stop still kills, on
// exactly the same tool, with no per-tool branch in the loop.
func TestExecuteInterruptible_ExplicitStopStillKillsADetachableTool(t *testing.T) {
	dt := &detachableTool{name: "code_agent_fake", started: make(chan struct{})}
	loop := newLoopWith(t, dt)
	steer := make(chan Steer, 2)
	go func() {
		<-dt.started
		steer <- Steer{Text: "stop building"}
	}()
	out, err, consumed := loop.executeInterruptible(context.Background(), llm.ToolCall{ID: "1", Name: dt.name}, steer)
	if err != nil {
		t.Fatalf("a stop must not surface as a tool error: %v", err)
	}
	if !dt.cancelled.Load() || dt.detached.Load() {
		t.Fatalf("an explicit stop must cancel the tool (cancelled=%v detached=%v)", dt.cancelled.Load(), dt.detached.Load())
	}
	if !strings.HasPrefix(out, "INTERRUPTED:") || !strings.Contains(out, "stopped: context canceled") {
		t.Fatalf("a stop keeps the stopped-for-the-boss result: %q", out)
	}
	if len(consumed) != 1 {
		t.Fatalf("the stop message is still handed to the model, got %+v", consumed)
	}
}

// Why: "how's it going" then "actually stop" is a stop. The decision reads
// every message consumed in the interruption, not just the first to arrive.
func TestExecuteInterruptible_StopAnywhereInTheBatchKills(t *testing.T) {
	dt := &detachableTool{name: "code_agent_fake", started: make(chan struct{})}
	loop := newLoopWith(t, dt)
	// Both queued before the call, so the drain provably sees them together.
	steer := make(chan Steer, 4)
	steer <- Steer{Text: "how's it going?"}
	steer <- Steer{Text: "actually cancel it"}
	out, _, consumed := loop.executeInterruptible(context.Background(), llm.ToolCall{ID: "1", Name: dt.name}, steer)
	if len(consumed) != 2 {
		t.Fatalf("both messages must reach the model, got %+v", consumed)
	}
	if dt.detached.Load() || !strings.HasPrefix(out, "INTERRUPTED:") {
		t.Fatalf("a stop later in the batch must still kill: detached=%v out=%q", dt.detached.Load(), out)
	}
}

func TestExecuteInterruptible_LeavesNonOptedToolAlone(t *testing.T) {
	bt := &blockingTool{name: "plain_fake", interruptOK: false, started: make(chan struct{})}
	loop := newLoopWith(t, bt)
	steer := make(chan Steer, 1)
	steer <- Steer{Text: "hi"}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	out, _, consumed := loop.executeInterruptible(ctx, llm.ToolCall{ID: "1", Name: bt.name}, steer)
	if len(consumed) != 0 || len(steer) != 1 {
		t.Fatalf("a tool that did not opt in must not consume the steer; consumed=%d queued=%d", len(consumed), len(steer))
	}
	if !strings.HasPrefix(out, "stopped:") {
		t.Fatalf("expected the ctx timeout to end the fake tool, got %q", out)
	}
}

func TestExecuteInterruptible_AutonomousRunsNeverInterrupt(t *testing.T) {
	bt := &blockingTool{name: "code_agent_fake", interruptOK: true, started: make(chan struct{})}
	loop := newLoopWith(t, bt)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	out, _, consumed := loop.executeInterruptible(ctx, llm.ToolCall{ID: "1", Name: bt.name}, nil)
	if consumed != nil || strings.HasPrefix(out, "INTERRUPTED:") {
		t.Fatalf("nil steer channel (cron / delegate) must run the plain path, got %q", out)
	}
}

// Why: the guard must catch every way the heal pass could rewrite Infinity,
// including the generic fs/bash tools when they point at the checkout, while
// leaving ordinary workspace edits alone.
func TestIsSourceMutation(t *testing.T) {
	yes := []llm.ToolCall{
		{Name: "code_agent", Input: map[string]any{"task": "fix plan resume"}},
		{Name: "claude_code__Edit", Input: map[string]any{"file_path": "/x"}},
		{Name: "git_push"},
		{Name: "fs_edit", Input: map[string]any{"path": "/workspace/infinity/core/internal/tools/plan_tools.go"}},
		{Name: "bash_run", Input: map[string]any{"cmd": "cd ~/Dev/infinity && go build ./..."}},
	}
	for _, tc := range yes {
		if !isSourceMutation(tc) {
			t.Errorf("%s should be a source mutation", tc.Name)
		}
	}
	no := []llm.ToolCall{
		{Name: "plan_update", Input: map[string]any{"step_id": "2"}},
		{Name: "fs_save", Input: map[string]any{"path": "/workspace/uploads/notes.md"}},
		{Name: "bash_run", Input: map[string]any{"cmd": "pdftotext book.pdf -"}},
		{Name: "web_search", Input: map[string]any{"q": "infinity"}},
	}
	for _, tc := range no {
		if isSourceMutation(tc) {
			t.Errorf("%s should NOT be a source mutation", tc.Name)
		}
	}
}

func TestRefuseSelfHealSourceChange_FilesProposal(t *testing.T) {
	loop := &Loop{}
	var gotTask, gotTool string
	loop.AttachCodeProposalFiler(CodeProposalFilerFunc(func(_ context.Context, _, toolName, task string) (string, error) {
		gotTool, gotTask = toolName, task
		return "prop-1", nil
	}))
	msg := loop.refuseSelfHealSourceChange(context.Background(), "sess", llm.ToolCall{Name: "code_agent", Input: map[string]any{"task": "Fix the durable plan resume bug"}})
	if gotTool != "code_agent" || gotTask != "Fix the durable plan resume bug" {
		t.Fatalf("intent must be filed verbatim, got tool=%q task=%q", gotTool, gotTask)
	}
	for _, want := range []string{"BLOCKED (self-heal guard)", "prop-1", "answer the boss"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("refusal missing %q:\n%s", want, msg)
		}
	}
}
