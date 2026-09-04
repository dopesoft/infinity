package server

import (
	"context"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// longTool stands in for code_agent: it works until its context is cancelled,
// or returns its "still working" receipt the moment it is told to detach.
type longTool struct {
	detachable bool
	started    chan struct{}
	detached   chan struct{}
	cancelled  chan struct{}
}

func (t *longTool) Name() string           { return "long_tool" }
func (t *longTool) Description() string    { return "test" }
func (t *longTool) Schema() map[string]any { return map[string]any{"type": "object"} }
func (t *longTool) DetachOnSteer() bool    { return t.detachable }
func (t *longTool) InterruptOnSteer() bool { return true }
func (t *longTool) Execute(ctx context.Context, _ map[string]any) (string, error) {
	close(t.started)
	select {
	case <-tools.DetachRequested(ctx):
		close(t.detached)
		return "still working: run abc keeps going", nil
	case <-ctx.Done():
		close(t.cancelled)
		return "", ctx.Err()
	}
}

func newLongTool(detachable bool) *longTool {
	return &longTool{detachable: detachable, started: make(chan struct{}), detached: make(chan struct{}), cancelled: make(chan struct{})}
}

func wait(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for the tool to be %s", what)
	}
}

// THE 60-SECOND KILL. Claude Code aborting its MCP request cancelled the tool's
// context, which code_agent read as the Stop button. The request going away is
// a detach, never a kill.
func TestMCPExec_ARequestGoingAwayDetachesInsteadOfKilling(t *testing.T) {
	tool := newLongTool(true)
	reg := tools.NewRegistry()
	reg.Register(tool)
	srv := &Server{}

	reqCtx, abort := context.WithCancel(tools.WithSessionID(context.Background(), "chat"))
	done := make(chan string, 1)
	go func() {
		out, _ := srv.executeMCPTool(reqCtx, reg, llm.ToolCall{Name: "long_tool"})
		done <- out
	}()
	wait(t, tool.started, "started")
	abort() // Claude's timer fired / the process went away
	wait(t, tool.detached, "detached")
	select {
	case <-tool.cancelled:
		t.Fatal("the tool's own context was cancelled by the caller leaving - that is the kill this exists to prevent")
	default:
	}
	select {
	case out := <-done:
		if out == "" {
			t.Fatal("the interim receipt must come back")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("executeMCPTool must return once the tool hands back its receipt")
	}
}

// Stop means stop: the boss's interrupt is the one thing that cancels a tool
// the brain started through MCP.
func TestMCPExec_TheBossesStopCancelsTheExecution(t *testing.T) {
	tool := newLongTool(true)
	reg := tools.NewRegistry()
	reg.Register(tool)
	srv := &Server{turns: map[string]*turnState{}}

	reqCtx := tools.WithSessionID(context.Background(), "chat")
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = srv.executeMCPTool(reqCtx, reg, llm.ToolCall{Name: "long_tool"})
	}()
	wait(t, tool.started, "started")
	if n := srv.cancelMCPExecs("chat"); n != 1 {
		t.Fatalf("one execution registered for the session, cancelled %d", n)
	}
	wait(t, tool.cancelled, "cancelled")
	<-done
	if n := srv.cancelMCPExecs("chat"); n != 0 {
		t.Fatalf("a finished execution must be untracked, found %d", n)
	}
}

// A tool that cannot detach simply runs to its own end when the caller leaves;
// its result is written to nobody, which is harmless, and it is never killed.
func TestMCPExec_ANonDetachableToolIsLeftToFinish(t *testing.T) {
	tool := newLongTool(false)
	reg := tools.NewRegistry()
	reg.Register(tool)
	srv := &Server{}
	reqCtx, abort := context.WithCancel(tools.WithSessionID(context.Background(), "chat"))
	go func() { _, _ = srv.executeMCPTool(reqCtx, reg, llm.ToolCall{Name: "long_tool"}) }()
	wait(t, tool.started, "started")
	abort()
	// The detach signal still fires (the tool may or may not read it); what
	// must NOT happen is a cancel.
	wait(t, tool.detached, "detached")
	select {
	case <-tool.cancelled:
		t.Fatal("a non-detachable tool must not be killed by its caller leaving")
	default:
	}
}
