package llm

import (
	"context"
	"errors"
	"strings"
)

// Drafter is the minimal one-shot completion capability. *Anthropic satisfies
// it directly (its Draft method); the production wiring passes an adapter that
// routes through the boss's active provider+model so summarization, reflection,
// prediction, and skill drafting all run on the same brain as chat instead of a
// pinned vendor. Defined here so subsystems depend on the interface, not a
// concrete provider.
type Drafter interface {
	Draft(ctx context.Context, model, system, userPrompt string, maxTokens int64) (string, error)
}

// Complete runs a single one-shot turn against ANY Provider and returns the
// assistant's full text. It is the cross-vendor equivalent of Anthropic's
// concrete Draft helper: subsystems that need one-shot output (Voyager skill
// drafting + verification, etc.) call this so they follow the boss's ACTIVE
// model — Anthropic, OpenAI, or Google — instead of being pinned to one
// vendor's concrete client. No tools are offered (drafting never calls tools);
// an empty model falls back to the provider's default.
//
// Channel ownership mirrors the agent loop (loop.go): the goroutine that runs
// Stream closes the event channel after Stream returns, so there is no
// double-close. The caller drains text deltas as a fallback for providers that
// don't populate Response.Text.
func Complete(ctx context.Context, p Provider, model, system, userPrompt string) (string, error) {
	if p == nil {
		return "", errors.New("llm.Complete: nil provider")
	}
	out := make(chan StreamEvent, 64)
	var resp Response
	var streamErr error
	done := make(chan struct{})
	go func() {
		defer close(done)
		resp, streamErr = p.Stream(ctx, model, system,
			[]Message{{Role: RoleUser, Content: userPrompt}}, nil, out)
		close(out)
	}()

	var b strings.Builder
	for ev := range out {
		if ev.Kind == StreamText {
			b.WriteString(ev.TextDelta)
		}
	}
	<-done

	if streamErr != nil {
		return "", streamErr
	}
	if t := strings.TrimSpace(resp.Text); t != "" {
		return resp.Text, nil
	}
	return b.String(), nil
}
