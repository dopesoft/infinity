package agent

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/hooks"
)

// selfPromptHook is the hook name such a turn is filed under.
const selfPromptHook = string(hooks.AgentSelfPrompt)

// A SELF-PROMPT is a turn Infinity started on its own, addressed to Jarvis,
// carrying facts a machine gathered: the finish poller waking him about a
// coding job that stopped without finishing, and anything later that needs the
// same shape.
//
// It exists because the alternative was writing those briefs into the boss's
// transcript as UserPromptSubmit, which is how his job-hunt chat filled with
// machine notes in his own message bubble, in literal asterisks (a typed
// message is not rendered as markdown, and rightly so). What he is meant to
// see is JARVIS'S REPLY - "your build stalled, I'm picking it back up" - in
// Jarvis's voice. The prompt behind it is plumbing.
//
// A marker on the context rather than a parameter on Run, because Run has
// eleven callers and only one of them is machine-authored; a new argument every
// other caller passes false to is a footgun waiting for the twelfth.
type selfPromptKey struct{}

// WithSelfPrompt marks this turn as one Infinity started for itself.
func WithSelfPrompt(ctx context.Context) context.Context {
	return context.WithValue(ctx, selfPromptKey{}, true)
}

// IsSelfPrompt reports whether the turn's opening message was machine-authored.
func IsSelfPrompt(ctx context.Context) bool {
	v, _ := ctx.Value(selfPromptKey{}).(bool)
	return v
}
