package main

import (
	"testing"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// The chain this pins:
//
//	tools.stillRunningError  →  tools.IsStillRunning (errors.As)
//	                         →  agent.classifyBackgroundRun
//	                         →  BackgroundResult.StillRunning
//	                         →  classifyBackgroundResult   ← here
//	                         →  NEVER SettlePlanForSession(StepFailed)
//
// Before this, "still working" arrived as a non-empty r.Err and was run past
// isRecoverableErr, which matches SUBSTRINGS ("timeout", "eof", "connection
// reset"). The sentinel's wording is none of those, so it fell straight through
// to the failed settle and a "Build failed" push — for a job that was still
// writing code.

func TestClassifyBackgroundResult_StillWorkingNeverSettlesFailed(t *testing.T) {
	r := agent.BackgroundResult{
		Task:         "finish the pursuit feature",
		Summary:      "⏳ Claude Code is still working after 20m0s (run run-1).",
		StillRunning: true,
	}
	got := classifyBackgroundResult(r)
	if got == bgSettleFailed {
		t.Fatal("a still-working build must NEVER settle its plan step failed - that is the red ❌ for work that landed on disk")
	}
	if got == bgSettleRetry {
		t.Fatal("re-dispatching a job that is still running would spend the boss's plan twice on the same work")
	}
	if got != bgSettleStillWorking {
		t.Fatalf("want bgSettleStillWorking, got %v", got)
	}
	if r.Err != "" {
		t.Fatal("a still-working result must carry no error, so every consumer that branches on 'did it error?' sees 'no'")
	}
}

// Defence in depth: even if some future path sets BOTH the flag and an error
// string, the flag wins. "Still running" is a fact about the worker; an error
// string is at best a description, and descriptions are what got this wrong.
func TestClassifyBackgroundResult_StillWorkingWinsOverAnyErrorText(t *testing.T) {
	r := agent.BackgroundResult{
		StillRunning: true,
		Err:          "code_agent: Claude Code is still working after 14m0s; it was not stopped",
	}
	if got := classifyBackgroundResult(r); got != bgSettleStillWorking {
		t.Fatalf("the typed flag must decide, never the wording: got %v", got)
	}
}

// The line we must not cross: a real failure still goes red, still surfaces,
// still feeds the self-improve backlog.
func TestClassifyBackgroundResult_GenuineFailureStillFails(t *testing.T) {
	r := agent.BackgroundResult{Err: "go build failed: undefined: foo"}
	if got := classifyBackgroundResult(r); got != bgSettleFailed {
		t.Fatalf("a real build failure must still settle failed: got %v", got)
	}
}

func TestClassifyBackgroundResult_SuccessAndRetryUnchanged(t *testing.T) {
	if got := classifyBackgroundResult(agent.BackgroundResult{Summary: "done"}); got != bgSettleDone {
		t.Fatalf("a clean finish must settle done: got %v", got)
	}
	// A transient bridge/timeout failure keeps its one-shot auto-recovery.
	if got := classifyBackgroundResult(agent.BackgroundResult{Err: "post to bridge: connection refused"}); got != bgSettleRetry {
		t.Fatalf("a transient failure must still get its single retry: got %v", got)
	}
}

// isRecoverableErr is exactly the substring scan that caused the bug. It must
// still not recognise the still-running wording — which is precisely why
// detection had to move off wording and onto the type.
func TestIsRecoverableErr_CannotSeeStillRunning(t *testing.T) {
	if isRecoverableErr("code_agent: Claude Code is still working after 14m0s (run x); it was not stopped") {
		t.Fatal("unexpected: if this ever starts matching, the still-working path would silently become a RETRY (double-spending the plan) instead of being handled by StillRunning")
	}
}
