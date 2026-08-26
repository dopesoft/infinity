package agent

import (
	"context"
	"strings"
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
