package llm

import (
	"context"
	"strings"
)

// Em-dash and en-dash hard ban.
//
// The boss has a hard, explicit rule against the em-dash (U+2014) and
// en-dash (U+2013) characters anywhere in the app. The soul prompt
// tells the model not to produce them. This file is the belt: every
// LLM Stream() call is wrapped so any em/en-dash that slips through
// gets substituted before it reaches:
//
//   * the WebSocket client (chat bubbles)
//   * mem_observations / mem_turns (persisted history)
//   * helper-LLM callers (summarizer, critic, code-proposal generator,
//     session namer, compaction summary, curiosity question generator)
//
// Substitute on the way out, never produce. Same rule the soul prompt
// states. This is a runtime safety net for the case where a future
// model regresses or a prompt-injection slips through.
//
// Replacement choices:
//   U+2014 (-) em dash  -> "-" (single ASCII hyphen)
//   U+2013 (-) en dash  -> "-"
//
// Both are visually compatible with the surrounding prose. A
// non-breaking minor info loss (dashes as sentence separators read as
// hyphenated phrases) is the explicit tradeoff the boss has chosen.

func StripDashes(s string) string {
	if s == "" {
		return s
	}
	if !strings.ContainsAny(s, "—–") {
		return s
	}
	r := strings.NewReplacer(
		"—", "-", // em dash
		"–", "-", // en dash
	)
	return r.Replace(s)
}

// WrapNoDashes returns a Provider that delegates to p but scrubs em/
// en-dashes from every text delta in the stream AND from the final
// Response.Text. Tool-call input/output is left untouched - those are
// structured JSON the model decided to send and substituting could
// corrupt code paths or commit messages that legitimately need the
// dash characters (the soul-prompt rule already steers them away).
//
// Apply once at provider-registry construction so every code path
// that reaches a Provider gets the sanitizer for free. Idempotent:
// wrapping an already-wrapped provider is harmless because the second
// pass finds no dashes to replace.
func WrapNoDashes(p Provider) Provider {
	if p == nil {
		return nil
	}
	return &noDashesProvider{inner: p}
}

type noDashesProvider struct{ inner Provider }

func (n *noDashesProvider) Name() string  { return n.inner.Name() }
func (n *noDashesProvider) Model() string { return n.inner.Model() }

// Unwrap exposes the inner concrete provider so callers that need to
// type-assert (e.g. cmd/infinity/serve.go's `provider.(*llm.Anthropic)`
// check to wire the compressor) can do so even when the provider is
// wrapped. Use llm.Unwrap as the entry point - it walks through every
// wrapper that participates in the convention.
func (n *noDashesProvider) Unwrap() Provider { return n.inner }

// Unwrap walks p down to its innermost concrete Provider by repeatedly
// calling Unwrap() on any wrapper that exposes one. Callers that need
// to type-assert against a concrete provider type should pass it
// through this helper first so future wrappers (sanitizers, metrics,
// retry, etc.) don't break the assertion.
func Unwrap(p Provider) Provider {
	for {
		u, ok := p.(interface{ Unwrap() Provider })
		if !ok {
			return p
		}
		inner := u.Unwrap()
		if inner == nil || inner == p {
			return p
		}
		p = inner
	}
}

func (n *noDashesProvider) Stream(
	ctx context.Context,
	model, system string,
	messages []Message,
	tools []ToolDef,
	out chan<- StreamEvent,
) (Response, error) {
	// Filter every event through a goroutine that owns the consumer
	// channel. The inner provider writes to mid; we forward to out
	// after substituting em/en-dashes in any text payload.
	if out == nil {
		// No streaming consumer; just call through and scrub the
		// final Response.Text in-place below.
		resp, err := n.inner.Stream(ctx, model, system, messages, tools, nil)
		resp.Text = StripDashes(resp.Text)
		return resp, err
	}
	mid := make(chan StreamEvent, 32)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range mid {
			ev.TextDelta = StripDashes(ev.TextDelta)
			ev.ThinkingDelta = StripDashes(ev.ThinkingDelta)
			out <- ev
		}
	}()
	resp, err := n.inner.Stream(ctx, model, system, messages, tools, mid)
	close(mid)
	<-done
	resp.Text = StripDashes(resp.Text)
	return resp, err
}
