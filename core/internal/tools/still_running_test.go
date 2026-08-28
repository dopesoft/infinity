package tools

import (
	"errors"
	"fmt"
	"testing"
	"time"
)

// Why this matters: "the worker outlived the wait window" and "the build broke"
// are opposite facts, and for months they were the same fact to everything above
// this package. background_build returned the still-running sentinel, serve.go
// asked isRecoverableErr (a SUBSTRING scan for "timeout"/"eof"/…), the sentinel's
// wording matched none of them, and the boss got a red ❌ step plus a "Build
// failed" push for code that had already landed on disk.
//
// So the detector must be TYPE-based and wrapping-safe, and it must not fire on
// wording. These tests pin exactly that.
func TestIsStillRunning_MatchesTheSentinelEvenWrapped(t *testing.T) {
	sentinel := &stillRunningError{jobID: "run-1", repo: "/Users/kai/Dev/infinity", elapsed: 14 * time.Minute}

	if !IsStillRunning(sentinel) {
		t.Fatal("the still-running sentinel must be recognised")
	}
	// Errors get wrapped as they travel up the runner → background agent chain.
	wrapped := fmt.Errorf("background build: %w", sentinel)
	if !IsStillRunning(wrapped) {
		t.Fatal("a WRAPPED still-running sentinel must still be recognised - errors.As, not ==")
	}
	if !IsStillRunning(fmt.Errorf("outer: %w", wrapped)) {
		t.Fatal("double-wrapped must still be recognised")
	}
}

func TestIsStillRunning_DoesNotMatchOnWording(t *testing.T) {
	// A plain error whose TEXT is the sentinel's text. If detection were
	// substring-based (the bug), this would pass as "still running" - and worse,
	// a real failure that happened to mention "still working" would silently
	// stop failing. Type-based detection makes both impossible.
	sentinel := &stillRunningError{jobID: "run-1", repo: "/repo", elapsed: time.Minute}
	lookalike := errors.New(sentinel.Error())
	if IsStillRunning(lookalike) {
		t.Fatal("wording must never decide this - only the type may")
	}
	if IsStillRunning(errors.New("go build failed: undefined: foo")) {
		t.Fatal("a genuine build failure must NOT read as still-running")
	}
	if IsStillRunning(nil) {
		t.Fatal("nil is not still-running")
	}
}

func TestStillRunningMessage_EmptyForRealErrors(t *testing.T) {
	sentinel := &stillRunningError{jobID: "run-9", repo: "/Users/kai/Dev/infinity", elapsed: 20 * time.Minute}
	msg := StillRunningMessage(sentinel)
	if msg == "" {
		t.Fatal("the sentinel must yield its boss-facing 'still working' line")
	}
	if msg == sentinel.Error() {
		t.Fatal("the inline message is the friendly form, not the raw error")
	}
	// "" is the caller's "this was a real error, handle it normally" branch, so
	// a genuine failure must never produce a message here.
	if StillRunningMessage(errors.New("tests failed")) != "" {
		t.Fatal("a real error must produce no still-working message")
	}
	if StillRunningMessage(nil) != "" {
		t.Fatal("nil must produce no still-working message")
	}
}
