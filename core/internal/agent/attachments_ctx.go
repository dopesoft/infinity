package agent

import (
	"context"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Per-turn attachments ride the context, mirroring WithVoiceMode /
// WithEffortPin: every caller of Loop.Run keeps its signature, and only the
// live-chat WS path (the one place files can arrive) sets the value.

type attachmentsCtxKey struct{}

// WithAttachments stamps the files attached to this turn's opening user
// message onto ctx. Run appends them to the user llm.Message so the provider
// ships them as native blocks, and persists their metadata on the
// UserPromptSubmit hook so the transcript and a post-restart hydrate can
// find them again.
func WithAttachments(ctx context.Context, atts []llm.Attachment) context.Context {
	if len(atts) == 0 {
		return ctx
	}
	return context.WithValue(ctx, attachmentsCtxKey{}, atts)
}

// AttachmentsFromContext returns the turn's attachments, or nil.
func AttachmentsFromContext(ctx context.Context) []llm.Attachment {
	if ctx == nil {
		return nil
	}
	atts, _ := ctx.Value(attachmentsCtxKey{}).([]llm.Attachment)
	return atts
}

// Steer is one mid-turn user input drained by the loop between iterations.
// A steer can carry files exactly like an opening message: the boss drops a
// screenshot while Jarvis is mid-task and it must land in the same turn.
type Steer struct {
	Text        string
	Attachments []llm.Attachment
}
