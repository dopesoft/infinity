package main

import (
	"context"
	"testing"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// stubProvider is a named brain that never runs.
type stubProvider struct{ name string }

func (s stubProvider) Name() string  { return s.name }
func (s stubProvider) Model() string { return "m" }
func (s stubProvider) Stream(context.Context, string, string, []llm.Message, []llm.ToolDef, chan<- llm.StreamEvent) (llm.Response, error) {
	return llm.Response{}, nil
}

// Background cognition must not run on Claude Max. Every call through this
// adapter is bookkeeping - naming a session, compressing observations - and on
// that brain each one launches a CLI process and spends the weekly limit.
// Nothing would error if this regressed; the boss would just watch his
// allowance drain and his memory pipeline fall behind.
func TestAuxWorkAvoidsTheHarnessBrain(t *testing.T) {
	cheap := stubProvider{name: "deepseek"}
	d := &activeModelProvider{fallback: cheap}

	got, model := d.avoidHarnessBrain(stubProvider{name: llm.ProviderClaudeMax}, "claude-opus-5")
	if got.Name() != "deepseek" {
		t.Fatalf("bookkeeping was left on the harness brain: %s", got.Name())
	}
	// The Claude model id must go with the vendor, or it is forced onto a
	// brain that has never heard of it and every call fails.
	if model != "" {
		t.Errorf("a Claude model id followed the substitution: %q", model)
	}
}

// Any other brain passes through untouched, model and all.
func TestAuxWorkLeavesOtherBrainsAlone(t *testing.T) {
	d := &activeModelProvider{fallback: stubProvider{name: "deepseek"}}
	got, model := d.avoidHarnessBrain(stubProvider{name: "openai_oauth"}, "gpt-5.6")
	if got.Name() != "openai_oauth" || model != "gpt-5.6" {
		t.Fatalf("an unrelated brain was rewritten: %s / %q", got.Name(), model)
	}
}

// With Claude Max as the ONLY brain, slow bookkeeping beats none: memories
// that never form are worse than memories that form late.
func TestAuxWorkFallsBackRatherThanGoingSilent(t *testing.T) {
	d := &activeModelProvider{}
	got, _ := d.avoidHarnessBrain(stubProvider{name: llm.ProviderClaudeMax}, "claude-opus-5")
	if got == nil || got.Name() != llm.ProviderClaudeMax {
		t.Fatal("with no alternative, bookkeeping must still run rather than stop")
	}
}
