package agent

import "context"

// Voice mode is a PER-TURN flag, not a session property: a session is shared
// between text and voice (same session id), so marking the Session would leak
// voice behavior into a later text turn. We carry it on the turn context
// instead - the ws voice pump wraps the turn ctx with WithVoiceMode, and the
// loop reads it once while assembling the system prompt.
//
// The overlay below is JUDGMENT only (per Infinity Rule #1b): how to TALK when
// speaking out loud. Everything load-bearing - who the boss is, memory, tools,
// the gate, the model - comes from the identical Loop.Run path text uses, so
// dropping this overlay degrades delivery, never capability. The no-dead-air
// narration during tool calls is additionally guaranteed in code (the ws pump),
// not left to this prose.

type voiceModeKey struct{}

// WithVoiceMode marks a turn context as voice-originated.
func WithVoiceMode(ctx context.Context) context.Context {
	return context.WithValue(ctx, voiceModeKey{}, true)
}

// VoiceModeFromContext reports whether this turn is being spoken out loud.
func VoiceModeFromContext(ctx context.Context) bool {
	v, _ := ctx.Value(voiceModeKey{}).(bool)
	return v
}

// voiceModeSystemOverlay is prepended to the system prompt on voice turns. Kept
// short on purpose - it's a delivery nudge, not new capability.
const voiceModeSystemOverlay = `<voice_mode>
You are speaking OUT LOUD to the boss right now. Same Jarvis as text - same memory, same tools, same judgment - just delivered as speech.
- Keep replies short and conversational: spoken sentences, not essays. No markdown, no bullet lists, no code blocks read aloud.
- Narrate your work as you do it. Before a tool call, say in one short line what you're about to do ("Right, checking your inbox now"); after it, say what you found. This keeps the conversation alive while tools run.
- Don't read URLs, ids, or raw data aloud - summarize them in plain speech.
</voice_mode>`
