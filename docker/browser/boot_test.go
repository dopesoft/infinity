package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// The outage this guards: the boot path ran the FIRST chromedp.Run on
// `context.WithTimeout(ctx, 60s)` with a `defer bootCancel()`. chromedp binds
// the Chromium process (exec.CommandContext) and the CDP connection to the
// context of that first Run, so the deferred cancel killed the browser the
// instant newSession returned. The session id stayed in the manager holding a
// slot against maxSessions while every verb answered "context canceled".
//
// The invariant these tests encode: capping how long boot may take must never
// cancel the context the browser's lifetime hangs off.

func TestAwaitWithDeadlineLeavesContextLiveOnSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := awaitWithDeadline(ctx, time.Second, func() error { return nil }); err != nil {
		t.Fatalf("awaitWithDeadline: %v", err)
	}
	if err := ctx.Err(); err != nil {
		t.Fatalf("context was cancelled by a successful boot (%v) — the browser would die with it", err)
	}
}

func TestAwaitWithDeadlineLeavesContextLiveOnTimeout(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := awaitWithDeadline(ctx, 20*time.Millisecond, func() error {
		time.Sleep(2 * time.Second)
		return nil
	})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout error", err)
	}
	// The caller owns teardown on timeout. The deadline itself must not decide
	// to destroy the session context.
	if err := ctx.Err(); err != nil {
		t.Fatalf("the deadline cancelled the context it was timing (%v)", err)
	}
}

func TestAwaitWithDeadlinePropagatesTheRealError(t *testing.T) {
	boom := errors.New("boot chromium: no usable sandbox")
	err := awaitWithDeadline(context.Background(), time.Second, func() error { return boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the underlying failure %v", err, boom)
	}
}

func TestAwaitWithDeadlineHonoursCallerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := awaitWithDeadline(ctx, 5*time.Second, func() error {
		time.Sleep(2 * time.Second)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
