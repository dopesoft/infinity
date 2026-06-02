package main

import (
	"context"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/settings"
)

// activeModelProvider is the single adapter that makes every auxiliary LLM task
// (skill drafting + verification, memory compression, reflection, prediction,
// intent detection, email triage, skill merge/optimize) run on the boss's
// CURRENTLY SET model — the same Studio Settings selection (provider + model)
// that drives chat — instead of a vendor pinned at boot. It replaces the old
// per-feature `provider.(*llm.Anthropic)` gates that silently no-op'd whenever
// the brain wasn't Anthropic (the "why is my memory/intent/triage dead on
// ChatGPT" class of bug).
//
// It satisfies BOTH shapes the subsystems depend on:
//   - llm.Provider (Name/Model/Stream) — for intent + triage, which stream.
//   - the Draft(...) signature — for prediction recorder, merge evaluator,
//     skill_optimize, and the drafter-backed summarizer/critic.
//
// The active selection is resolved PER CALL, so a Settings hot-swap is honored
// immediately, and the boss's set model always wins over any stale per-call
// model hint a caller passes.
type activeModelProvider struct {
	registry *llm.Registry   // multi-vendor registry (anthropic / openai / google / oauth)
	settings *settings.Store // active provider + model overrides
	fallback llm.Provider    // boot provider, used before a selection exists or if the registry lacks the active vendor
}

// resolve returns the provider + model id for the boss's current selection,
// falling back to the boot provider (and its default model) when unset.
func (d *activeModelProvider) resolve(ctx context.Context) (llm.Provider, string) {
	var p llm.Provider = d.fallback
	model := ""
	if d.settings != nil {
		model = d.settings.GetModel(ctx)
		if name := d.settings.GetProvider(ctx); name != "" && d.registry != nil {
			if got, ok := d.registry.Get(name); ok {
				p = got
			}
		}
	}
	return p, model
}

// --- llm.Provider ---

func (d *activeModelProvider) Name() string {
	p, _ := d.resolve(context.Background())
	if p == nil {
		return "active"
	}
	return p.Name()
}

func (d *activeModelProvider) Model() string {
	p, model := d.resolve(context.Background())
	if model != "" {
		return model
	}
	if p != nil {
		return p.Model()
	}
	return ""
}

// Stream forces the boss's active model (ignoring the caller's model arg — that
// arg is usually a stale env default like a pinned Haiku id that would break
// under a different vendor). This is the whole point: auxiliary cognition runs
// on the same brain as chat.
func (d *activeModelProvider) Stream(ctx context.Context, _ string, system string, messages []llm.Message, tools []llm.ToolDef, out chan<- llm.StreamEvent) (llm.Response, error) {
	p, model := d.resolve(ctx)
	if p == nil {
		// Degenerate case (no boot provider AND no active selection) — in
		// production the fallback is always the boot provider, so this never
		// fires. Close the consumer channel (if any) so a caller ranging over
		// it doesn't hang, then surface the error.
		if out != nil {
			close(out)
		}
		return llm.Response{}, llm.ErrNotImplemented
	}
	// Transparent passthrough: the resolved provider owns `out` exactly as it
	// would if the caller had held it directly, so the channel contract is
	// unchanged.
	return p.Stream(ctx, model, system, messages, tools, out)
}

// --- llm.Drafter (one-shot) ---

// Draft ignores the caller's model hint + maxTokens and runs a one-shot
// completion against the active provider + model via llm.Complete.
func (d *activeModelProvider) Draft(ctx context.Context, _ string, system, userPrompt string, _ int64) (string, error) {
	p, model := d.resolve(ctx)
	return llm.Complete(ctx, p, model, system, strings.TrimSpace(userPrompt))
}
