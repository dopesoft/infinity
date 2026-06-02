package main

import (
	"context"
	"strings"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// runVerifyTurn executes one ephemeral skill-verification turn over agent.Loop
// and returns the assistant's accumulated final text. It's the adapter behind
// voyager.Manager.SetVerifyHarness — kept here (not in voyager) so the voyager
// package doesn't import agent. Empty model ⇒ the loop's active brain. The
// caller (the harness) owns the session id and the post-run cleanup.
func runVerifyTurn(ctx context.Context, loop *agent.Loop, sessionID, prompt string) (string, error) {
	if loop == nil {
		return "", nil
	}
	out := make(chan agent.RunEvent, 64)
	var runErr error
	done := make(chan struct{})
	go func() {
		runErr = loop.Run(ctx, sessionID, prompt, "", nil, out)
		close(out)
		close(done)
	}()

	var sb strings.Builder
	for ev := range out {
		if ev.Kind == agent.EventDelta {
			sb.WriteString(ev.TextDelta)
		}
	}
	<-done
	if runErr != nil {
		return "", runErr
	}
	return strings.TrimSpace(sb.String()), nil
}
