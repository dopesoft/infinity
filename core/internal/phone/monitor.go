// monitor.go — the transcript observer for a live SIP call.
//
// Once a call is accepted, Core attaches to the same realtime session over
// WSS (?call_id=) and collects transcript events until the socket closes
// (the call ended). The outcome then lands on the boss's dashboard as a
// generic surface item (surface="calls") and — when it matters — a push.
// The whole call is tracked as a mem_runs row so Studio shows a live,
// navigation-proof "on a call" spinner (Server-tracked progress rule).
package phone

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/gorilla/websocket"
)

const (
	// maxCallDuration caps the monitor so a stuck socket can't leak a
	// goroutine forever. Longer than any plausible call.
	maxCallDuration = 2 * time.Hour

	// maxTranscriptChars clips the surface body (full transcripts of long
	// calls would bloat the dashboard payload).
	maxTranscriptChars = 8000

	// pushInboundMinDuration: inbound calls shorter than this were
	// wrong-numbers / instant hangups — card only, no push.
	pushInboundMinDuration = 20 * time.Second
)

// Monitor attaches to the live call, collects the transcript, and delivers
// the outcome when the call ends. Blocking — callers run it in a goroutine.
// brief is nil for inbound calls.
func (m *Manager) Monitor(callID, direction string, brief *Brief) {
	ctx, cancel := context.WithTimeout(context.Background(), maxCallDuration)
	defer cancel()

	label := direction + " call"
	if brief != nil && brief.To != "" {
		label = direction + " call to " + brief.To
	}
	// runs.Track books the mem_runs row (status=running → ok/error) so the
	// call shows as live work in Studio and a monitor failure is classified
	// red, not silently dropped.
	_ = runs.Track(ctx, runs.KindPhoneCall, callID, label, runs.SourceAgent, func(ctx context.Context) error {
		return m.monitorOnce(ctx, callID, direction, brief)
	})
}

// transcriptEvent is the lenient shape of any realtime event that carries a
// finished transcript segment. Both event families we care about —
// response.output_audio_transcript.done (Jarvis) and
// conversation.item.input_audio_transcription.completed (the human) — put
// the text in a top-level "transcript" string, so one shape covers both and
// tolerates future variants.
type transcriptEvent struct {
	Type       string `json:"type"`
	Transcript string `json:"transcript"`
}

func (m *Manager) monitorOnce(ctx context.Context, callID, direction string, brief *Brief) error {
	start := time.Now()

	header := http.Header{}
	header.Set("Authorization", "Bearer "+m.cfg.OpenAIKey)
	url := realtimeMonitorURL + "?call_id=" + callID
	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, url, header)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
			_ = resp.Body.Close()
		}
		// Fail loud: without the monitor there is no transcript and no
		// outcome — the call happened but Jarvis would report nothing.
		return fmt.Errorf("phone: monitor dial for call %s failed (HTTP %d): %w", callID, status, err)
	}
	defer conn.Close()
	infoLog.Printf("phone: monitoring %s call %s", direction, callID)

	// Cap the whole read loop at the ctx deadline (maxCallDuration) so a
	// half-dead socket can't leak this goroutine forever. NOTE: gorilla
	// treats any ReadMessage error — including a deadline timeout — as
	// permanent, so the loop must break on the first error rather than
	// retry reads.
	if d, ok := ctx.Deadline(); ok {
		_ = conn.SetReadDeadline(d)
	}

	// Human side label: on inbound the human is the caller; on outbound
	// it's the callee Jarvis dialed.
	humanLabel := "Caller"
	if direction == "outbound" {
		humanLabel = "Callee"
	}

	var lines []string
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			// Normal end-of-call: the server closes the socket when the
			// call hangs up. A deadline hit means the maxCallDuration cap
			// fired — log it, then deliver whatever transcript we have.
			if ctx.Err() != nil || time.Since(start) >= maxCallDuration-time.Minute {
				log.Printf("phone: monitor for call %s stopped at the %s cap: %v", callID, maxCallDuration, err)
			}
			break
		}

		var ev transcriptEvent
		if json.Unmarshal(raw, &ev) != nil || strings.TrimSpace(ev.Transcript) == "" {
			continue
		}
		speaker := humanLabel
		if strings.Contains(ev.Type, "output_audio_transcript") || strings.HasPrefix(ev.Type, "response.") {
			speaker = "Jarvis"
		}
		lines = append(lines, speaker+": "+strings.TrimSpace(ev.Transcript))
	}

	m.deliverOutcome(callID, direction, brief, lines, time.Since(start))
	return nil
}

// deliverOutcome writes the ONE durable record of the call — a generic
// surface item on the "calls" surface — and pushes the boss's phone for
// outbound calls or substantive inbound ones. Uses a fresh ctx: the call
// ctx may already be expired and the outcome must land regardless.
func (m *Manager) deliverOutcome(callID, direction string, brief *Brief, lines []string, dur time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	transcript := strings.Join(lines, "\n")
	if transcript == "" {
		transcript = "(no speech transcribed)"
	}

	title := capitalize(direction) + " call"
	if brief != nil && brief.To != "" {
		title += " to " + brief.To
	}

	if m.surface != nil {
		importance := 70
		if _, err := m.surface.Upsert(ctx, &surface.Item{
			Surface:    "calls",
			Kind:       "call",
			Source:     "phone",
			ExternalID: callID,
			Title:      title,
			Subtitle:   fmt.Sprintf("%s · %d exchanges", dur.Round(time.Second), len(lines)),
			Body:       clip(transcript, maxTranscriptChars),
			Importance: &importance,
		}); err != nil {
			log.Printf("phone: surface call outcome %s: %v", callID, err)
		}
	} else {
		log.Printf("phone: no surface store; transcript for call %s dropped", callID)
	}

	// Push: outbound always (the boss commissioned it and wants the
	// result); inbound only when the call had substance.
	if m.push != nil && (direction == "outbound" || dur >= pushInboundMinDuration) {
		body := "The call ended."
		if len(lines) > 0 {
			body = clip(lines[len(lines)-1], 120)
		}
		m.push.Notify(ctx, push.Notification{
			Title: "Call finished",
			Body:  body,
			Kind:  "phone_call",
			URL:   "/",
			Tag:   "phone-" + callID,
		})
	}
	infoLog.Printf("phone: %s call %s ended after %s (%d transcript lines)", direction, callID, dur.Round(time.Second), len(lines))
}

// capitalize upper-cases the first ASCII letter ("inbound" → "Inbound").
// strings.Title is deprecated and overkill for a single known word.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
