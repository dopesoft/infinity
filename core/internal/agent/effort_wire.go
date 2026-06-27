package agent

import (
	"context"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// EffortRequest is the thin set of per-turn facts the Loop knows and hands to
// the effort resolver. The resolver (wired in serve.go) enriches it with the
// signals that need external stores (gauge, loop-gate call rate, capability)
// and runs the deterministic effort.Router. Keeping this struct minimal keeps
// the hot path free of store dependencies.
type EffortRequest struct {
	SessionID string
	Model     string // the resolved active model id (read-only; never changed)
	Project   string // non-empty -> a coding/project session
	Pinned    string // boss override from the WS frame: "" | "auto" | a level
}

type effortPinCtxType struct{}

// WithEffortPin stamps the boss's per-turn effort override (from the Composer
// ThinkingChip) onto the context so resolveEffort can honor it. "auto"/"" mean
// "let C decide". Set by the WS turn handler (batch 6).
func WithEffortPin(ctx context.Context, pin string) context.Context {
	if pin == "" {
		return ctx
	}
	return context.WithValue(ctx, effortPinCtxType{}, pin)
}

// EffortPinFromContext returns the boss's per-turn effort pin, or "".
func EffortPinFromContext(ctx context.Context) string {
	if v, ok := ctx.Value(effortPinCtxType{}).(string); ok {
		return v
	}
	return ""
}

// SetEffortFn installs the per-turn effort resolver (steal C). Nil disables
// per-turn effort entirely (every turn keeps the model default). Mirrors
// SetActiveModelFn; safe to call after construction.
func (l *Loop) SetEffortFn(fn func(ctx context.Context, req EffortRequest) (llm.Effort, string)) {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	l.effortFn = fn
}

// SetVerifyDirective installs the Lever-3 adversarial-verify recipe text (steal
// C). DATA, not a Go const (Rule #1b) — serve.go reads it from infinity_meta
// (seeded by migration) so it's versioned + editable. Empty disables the pass.
func (l *Loop) SetVerifyDirective(text string) {
	l.providerMu.Lock()
	defer l.providerMu.Unlock()
	l.verifyDirective = strings.TrimSpace(text)
}

// verifyDirectiveText reads the current verify directive under providerMu, or "".
func (l *Loop) verifyDirectiveText() string {
	if l == nil {
		return ""
	}
	l.providerMu.RLock()
	defer l.providerMu.RUnlock()
	return l.verifyDirective
}

// resolveEffort reads the effort resolver under providerMu and returns the
// per-turn level + source. Fails open: nil resolver or a panic yields ("",
// "") so the turn behaves exactly as it did before C existed.
func (l *Loop) resolveEffort(ctx context.Context, req EffortRequest) (level llm.Effort, source string) {
	if l == nil {
		return "", ""
	}
	l.providerMu.RLock()
	fn := l.effortFn
	l.providerMu.RUnlock()
	if fn == nil {
		return "", ""
	}
	defer func() {
		// Never let a resolver panic tank the turn; fall back to omit.
		if r := recover(); r != nil {
			level, source = "", "error"
		}
	}()
	return fn(ctx, req)
}
