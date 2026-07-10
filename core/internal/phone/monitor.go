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
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
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

	// callSummarySystem is the outcome digester's instruction. Same
	// precedent as llm/critic.go: the instruction for an auxiliary LLM
	// task lives beside its seam.
	callSummarySystem = "You summarize finished phone calls for the boss. " +
		"In 1-2 short sentences, state the OUTCOME - what was agreed, arranged, " +
		"confirmed, or learned - keeping every concrete detail: times, prices, " +
		"names, addresses, confirmation numbers. If the goal wasn't achieved, say " +
		"what happened and what's still open. No preamble, no restating the transcript. " +
		"Never use em dashes or en dashes; use commas or colons."
)

// Monitor attaches to the live call, collects + streams the transcript, and
// delivers the outcome when the call ends. Blocking — callers run it in a
// goroutine. brief is nil for inbound calls; callerNumber is the SIP From
// (inbound) and may be empty.
func (m *Manager) Monitor(callID, direction string, brief *Brief, callerNumber string) {
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
		return m.monitorOnce(ctx, callID, direction, brief, callerNumber)
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

func (m *Manager) monitorOnce(ctx context.Context, callID, direction string, brief *Brief, callerNumber string) error {
	start := time.Now()

	// The number Studio's live view shows: who Jarvis dialed (outbound) or
	// who's calling (inbound). Cleaned to the bare number when the SIP From
	// is a full URI.
	number := callerNumber
	if brief != nil && brief.To != "" {
		number = brief.To
	}
	if d := lastDigits(number, 11); d != "" {
		number = "+" + d
	}

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

	// Make Jarvis speak FIRST. A realtime session waits for user audio and
	// never opens its mouth unprompted, so without this the caller hears
	// silence until *they* say hello (and an outbound callee gets dead air
	// after picking up). One response.create on connect kicks the initial
	// greeting; WHAT he says is the persona's judgment (mem_agent_state),
	// not ours — this is purely the "go" signal.
	if err := conn.WriteJSON(map[string]any{"type": "response.create"}); err != nil {
		// Non-fatal: the call still works, just greeting-less. Log loud so
		// a silent pickup is diagnosable rather than mysterious.
		log.Printf("phone: initial greeting response.create for call %s failed: %v", callID, err)
	}

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

	// Boss verification (inbound): the spoken passphrase is checked HERE in
	// code — never handed to the call agent, so it can't be sweet-talked
	// out, and a match from ANY phone verifies the boss (spoofed caller ID
	// is irrelevant; the secret is the identity).
	passphrase := m.phonePassphrase(ctx)
	bossVerified := false
	var bossAsk []string

	var lines []string
	hangupFired := false
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

		// The call agent asked to hang up (goodbyes exchanged). Lenient
		// match on the raw event — function-call output arrives in a few
		// shapes across realtime revisions — then a short grace so the
		// farewell audio finishes playing out before the line drops.
		if !hangupFired && bytes.Contains(raw, []byte(`"hangup_call"`)) &&
			bytes.Contains(raw, []byte(`function_call`)) {
			hangupFired = true
			go func() {
				time.Sleep(2 * time.Second)
				if err := m.hangupCall(context.Background(), callID); err != nil {
					log.Printf("phone: agent-requested hangup for call %s failed: %v", callID, err)
				} else {
					infoLog.Printf("phone: call %s ended by Jarvis (hangup_call)", callID)
				}
			}()
			continue
		}

		var ev transcriptEvent
		if json.Unmarshal(raw, &ev) != nil || strings.TrimSpace(ev.Transcript) == "" {
			continue
		}
		speaker := humanLabel
		if strings.Contains(ev.Type, "output_audio_transcript") || strings.HasPrefix(ev.Type, "response.") {
			speaker = "Jarvis"
		}
		text := strings.TrimSpace(ev.Transcript)

		// Verification + collection happen BEFORE scrubbing (the raw line
		// is what carries the phrase); storage and streaming only ever see
		// the scrubbed form.
		if direction == "inbound" && speaker != "Jarvis" {
			if !bossVerified && passphrase != "" && speechContains(text, passphrase) {
				bossVerified = true
				infoLog.Printf("phone: inbound call %s verified as the boss (passphrase)", callID)
			} else if bossVerified {
				bossAsk = append(bossAsk, text)
			}
		}

		text = scrubSensitive(text, passphrase)
		// Collapse internal whitespace/newlines to single spaces so each
		// stored entry is exactly ONE "Speaker: text" line. Without this, a
		// transcript with embedded newlines (a relayed message with line
		// breaks) splits into continuation lines the renderer re-parses,
		// mis-attributing e.g. "Kai says: ..." as its own speaker.
		text = strings.Join(strings.Fields(text), " ")
		if text == "" {
			continue
		}
		lines = append(lines, speaker+": "+text)
		// Stream the line to Studio's live call view (Phone card modal).
		m.emitLive(LiveEvent{
			CallID: callID, Direction: direction, Number: number,
			Speaker: speaker, Text: text,
		})
	}

	// The drive-home loop: a verified boss's spoken asks execute as a
	// detached agent turn with his full memory the moment the call ends.
	if bossVerified && len(bossAsk) > 0 && m.executeAsk != nil {
		infoLog.Printf("phone: executing verified boss ask from call %s (%d lines)", callID, len(bossAsk))
		go m.executeAsk(strings.Join(bossAsk, "\n"))
	}

	m.deliverOutcome(callID, direction, brief, number, lines, time.Since(start))
	return nil
}

// speechContains reports whether spoken text contains the phrase, tolerant
// of transcription formatting only (case, spacing, punctuation): both sides
// are lowercased and stripped to letters+digits before the substring check.
// "Blue Falcon 22!" matches "blue falcon 22" — pick a passphrase whose
// words transcribe unambiguously.
func speechContains(text, phrase string) bool {
	n := func(s string) string {
		var b []rune
		for _, r := range strings.ToLower(s) {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b = append(b, r)
			}
		}
		return string(b)
	}
	p := n(phrase)
	return p != "" && strings.Contains(n(text), p)
}

// scrubSensitive removes call-borne secrets from a transcript line before it
// is stored, streamed, or summarized: the boss's passphrase and anything
// card-number-shaped (13-19 digits with optional spaces/dashes).
func scrubSensitive(text, passphrase string) string {
	if passphrase != "" {
		text = replaceFold(text, passphrase, "[passphrase]")
	}
	return cardPattern.ReplaceAllString(text, "[card]")
}

var cardPattern = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)

// replaceFold is a case-insensitive strings.ReplaceAll.
func replaceFold(s, old, repl string) string {
	if old == "" {
		return s
	}
	lower, lowerOld := strings.ToLower(s), strings.ToLower(old)
	var b strings.Builder
	for {
		i := strings.Index(lower, lowerOld)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i])
		b.WriteString(repl)
		s, lower = s[i+len(old):], lower[i+len(old):]
	}
}

// deliverOutcome writes the ONE durable record of the call — a generic
// surface item on the "calls" surface — and pushes the boss's phone for
// outbound calls or substantive inbound ones. Uses a fresh ctx: the call
// ctx may already be expired and the outcome must land regardless.
func (m *Manager) deliverOutcome(callID, direction string, brief *Brief, number string, lines []string, dur time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	transcript := strings.Join(lines, "\n")
	if transcript == "" {
		transcript = "(no speech transcribed)"
	}

	title := capitalize(direction) + " call"
	if brief != nil {
		switch {
		case brief.Name != "":
			title += " to " + brief.Name
		case brief.To != "":
			title += " to " + brief.To
		}
	}

	// Distill the OUTCOME — "your pizza will be ready in 20 minutes, at
	// 2:50pm" — not just a raw transcript. This is the report-back the boss
	// commissioned the call for: it leads the card as a summary block and
	// becomes the push body. Fail-open: no summarizer / an LLM error just
	// means the raw transcript ships alone.
	summary := ""
	if m.summarize != nil && len(lines) > 0 {
		prompt := capitalize(direction) + " call.\n"
		if brief != nil {
			prompt += "The goal of the call was: " + brief.Goal + "\n"
		}
		prompt += "\nTranscript:\n" + clip(transcript, maxTranscriptChars)
		if out, err := m.summarize(ctx, callSummarySystem, prompt); err != nil {
			log.Printf("phone: outcome summary for call %s failed (shipping raw transcript): %v", callID, err)
		} else if out = strings.TrimSpace(out); out != "" {
			summary = out
		}
	}
	body := clip(transcript, maxTranscriptChars)
	if summary != "" {
		body = "**Outcome:** " + summary + "\n\n---\n\n" + clip(transcript, maxTranscriptChars-len(summary)-20)
	}

	// Number-keyed call memory: when this number calls back ("about the
	// pizza order you just placed…"), the next call agent gets this context
	// injected instead of answering with amnesia. Best-effort.
	if digits := lastDigits(number, 10); digits != "" && m.pool != nil {
		recall := capitalize(direction) + " call " + time.Now().UTC().Format("Jan 2 2006")
		if brief != nil && brief.Name != "" {
			recall = brief.Name + ": " + recall
		}
		if brief != nil {
			recall += ". Goal: " + clip(brief.Goal, 200)
		}
		if summary != "" {
			recall += ". Outcome: " + clip(summary, 300)
		}
		// Rolling history, newest first, clipped - a RELATIONSHIP, not a
		// cache: the pizza place from last month still rings a bell. Main
		// Jarvis (and the boss) can enrich the same cell via state_set
		// ("their usual is pepperoni + extra cheese").
		if _, err := m.pool.Exec(ctx, `
			INSERT INTO mem_agent_state (key, value, note, updated_at)
			VALUES ($1, to_jsonb($2::text), 'phone: call history with this number', NOW())
			ON CONFLICT (key) DO UPDATE
			SET value = to_jsonb(left($2 || ' | Previously: ' || (mem_agent_state.value #>> '{}'), 1500)),
			    updated_at = NOW()
		`, "phone:history:"+digits, recall); err != nil {
			log.Printf("phone: store call recall for %s: %v", number, err)
		}
		if brief != nil && brief.Kind != "" {
			_, _ = m.pool.Exec(ctx, `
				INSERT INTO mem_agent_state (key, value, note, updated_at)
				VALUES ($1, to_jsonb($2::text), 'phone: contact kind', NOW())
				ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW()
			`, "phone:kind:"+digits, brief.Kind)
		}
	}

	// Close the live view: the modal swaps its "live" state for the
	// outcome summary the moment this lands.
	m.emitLive(LiveEvent{
		CallID: callID, Direction: direction, Number: number,
		Done: true, Summary: summary,
	})

	if m.surface != nil {
		importance := 70
		if _, err := m.surface.Upsert(ctx, &surface.Item{
			Surface:    "calls",
			Kind:       "call",
			Source:     "phone",
			ExternalID: callID,
			Title:      title,
			Subtitle: func() string {
				if brief != nil && brief.Topic != "" {
					return fmt.Sprintf("%s · %s", dur.Round(time.Second), brief.Topic)
				}
				if summary != "" {
					return fmt.Sprintf("%s · %s", dur.Round(time.Second), clip(summary, 90))
				}
				return fmt.Sprintf("%s · %d exchanges", dur.Round(time.Second), len(lines))
			}(),
			Body:       body,
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
		pushBody := "The call ended."
		if summary != "" {
			pushBody = clip(summary, 200)
		} else if len(lines) > 0 {
			pushBody = clip(lines[len(lines)-1], 120)
		}
		pushTitle := "Call finished"
		if summary != "" && direction == "outbound" {
			pushTitle = "Done: " + title
		}
		m.push.Notify(ctx, push.Notification{
			Title: pushTitle,
			Body:  pushBody,
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
