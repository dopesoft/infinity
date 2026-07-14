// memory.go — calls enter the brain.
//
// Until now, everything a call learned died inside the phone silo. The transcript
// became a card. The number got a rolling note (phone:history:<digits>) that only
// the CALL agent ever read, and only when that same number came up again. The
// phone book learned a name. And that was all.
//
// mem_observations never saw any of it, which means: no memories, no provenance,
// no A-MEM links, no world-model entities, no RRF retrieval. So the Jarvis the
// boss talks to in chat had NO IDEA his calls had happened. Ask him "what did my
// sister say?" and he would have nothing, while the answer sat in a surface card
// three feet away. The whole memory-first invariant was being bypassed by an
// entire channel.
//
// This is the seam that fixes it. Every finished call is captured as an
// observation through the SAME pipeline as everything else, so it compresses into
// memories with provenance, links to neighbours, feeds entity extraction (people,
// their attributes, their birthdays), and is retrievable in chat. One seam; every
// downstream loop gets calls for free, which is exactly the point of a substrate.
package phone

import (
	"context"
	"log"
	"strings"
	"time"

	"github.com/google/uuid"
)

// CaptureFunc records a finished call into memory. serve.go wires it to the hook
// pipeline (StripSecrets → embed → observation → compression), so this package
// never imports the memory stack.
type CaptureFunc func(ctx context.Context, sessionID, text string, payload map[string]any)

// SetCapture late-binds the memory seam (serve.go).
func (m *Manager) SetCapture(fn CaptureFunc) {
	if m != nil {
		m.capture = fn
	}
}

// rememberCall writes the call into the boss's memory.
//
// The text is written as a NARRATIVE, not a dump: who it was, which way it went,
// what came of it, and what was said. That matters because the downstream
// extractors read this text to learn people and facts, and "Phone call with
// Phumi. Kai asked me to wish her a happy birthday" teaches the world model a
// person AND a date, where a bare transcript teaches it nothing.
func (m *Manager) rememberCall(ctx context.Context, callID, direction, number, who string, brief *Brief, summary string, lines []string, dur time.Duration) {
	if m == nil || m.capture == nil || len(lines) == 0 {
		return
	}

	// Observations need a session to hang from, and a call IS a conversation, so
	// it gets one. This also means the call is a real, openable session rather
	// than a card with a transcript glued to it.
	sessionID := uuid.NewString()
	if m.pool != nil {
		if _, err := m.pool.Exec(ctx, `
			INSERT INTO mem_sessions (id, kind, origin_ref, started_at, ended_at)
			VALUES ($1::uuid, 'user', $2::jsonb, NOW() - ($3::text)::interval, NOW())
			ON CONFLICT (id) DO NOTHING
		`, sessionID, `{"kind":"phone_call"}`, dur.String()); err != nil {
			log.Printf("phone: opening a session for the call memory failed: %v", err)
			return
		}
	}

	name := who
	if name == "" && brief != nil && brief.Name != "" {
		name = brief.Name
	}
	if name == "" {
		name = number
	}

	var b strings.Builder
	if direction == "outbound" {
		b.WriteString("Jarvis called " + name + " on the boss's behalf")
	} else {
		b.WriteString(name + " called the boss's line and spoke to Jarvis")
	}
	if number != "" && name != number {
		b.WriteString(" (" + number + ")")
	}
	b.WriteString(", on " + time.Now().UTC().Format("January 2 2006") + ".\n")
	if brief != nil && brief.Goal != "" {
		b.WriteString("\nWhy the boss asked for the call: " + clip(brief.Goal, 600) + "\n")
	}
	if summary != "" {
		b.WriteString("\nWhat came of it: " + summary + "\n")
	}
	b.WriteString("\nWhat was said:\n" + clip(strings.Join(lines, "\n"), maxTranscriptChars))

	m.capture(ctx, sessionID, b.String(), map[string]any{
		"kind":      "phone_call",
		"direction": direction,
		"number":    number,
		"name":      name,
		"call_id":   callID,
	})
	infoLog.Printf("phone: call %s remembered (session %s)", callID, sessionID)
}
