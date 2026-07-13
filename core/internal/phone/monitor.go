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
		"Never use em dashes or en dashes; use commas or colons. " +
		"If the call promised a follow-up action the boss must do or be reminded of " +
		"(send a form, call back, a deadline), append a final separate line starting " +
		"exactly with 'FOLLOWUP:' and a short description. Otherwise do not add that line."
)

// Monitor attaches to the live call, collects + streams the transcript, and
// delivers the outcome when the call ends. Blocking — callers run it in a
// goroutine. brief is nil for inbound calls; callerNumber is the SIP From
// (inbound) and may be empty.
func (m *Manager) Monitor(callID, direction string, brief *Brief, callerNumber, briefID string) {
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
		return m.monitorOnce(ctx, callID, direction, brief, callerNumber, briefID)
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

func (m *Manager) monitorOnce(ctx context.Context, callID, direction string, brief *Brief, callerNumber, briefID string) error {
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
	// is irrelevant; the secret is the identity). Matching tolerates the
	// transcription drift a phone line guarantees (see passphrase.go); it does
	// not tolerate a different phrase.
	passphrase := m.phonePassphrase(ctx)
	if direction == "inbound" && passphrase == "" {
		// Loud, because the failure is invisible otherwise: with no phrase
		// stored NOBODY can verify, so every instruction the boss gives by
		// voice is discarded and the call still looks like it went fine.
		log.Printf("phone: call %s: no passphrase stored (infinity_meta vault.phone_passphrase); the boss CANNOT be verified on this call", callID)
	}
	bossVerified := false
	verifyAttempted := false // someone reached for the phrase and missed
	challenged := false      // Jarvis asked for it, i.e. someone commanded this line
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

		// A realtime error event means OUR session config or an event we
		// injected was rejected. Never swallow it: a call that quietly
		// misbehaves is the exact failure this whole file is guarding against.
		if bytes.Contains(raw, []byte(`"type":"error"`)) {
			log.Printf("phone: call %s: realtime error event: %s", callID, clip(string(raw), 500))
			continue
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

		// The call agent asked to hand the caller to the boss. Blind
		// transfer via OpenAI refer: the AI leg drops, the caller is
		// connected to Mr. Kai directly.
		if bytes.Contains(raw, []byte(`"patch_in_boss"`)) && bytes.Contains(raw, []byte(`function_call`)) {
			go func() {
				if err := m.referToBoss(context.Background(), callID); err != nil {
					log.Printf("phone: patch_in_boss for call %s failed: %v", callID, err)
				} else {
					infoLog.Printf("phone: call %s handed to the boss (patch_in_boss)", callID)
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

		// Verification + ask collection happen on the raw line (it is what
		// carries the phrase); storage and streaming only ever see the
		// redacted form, so the secret never reaches the transcript, the
		// summarizer, a push, or call history.
		redacted, verified, attempted := matchPassphrase(text, passphrase)
		if direction == "inbound" && speaker != "Jarvis" {
			switch {
			case verified && !bossVerified:
				bossVerified = true
				infoLog.Printf("phone: inbound call %s verified as the boss (passphrase)", callID)
				// Tell the LIVE agent he is now talking to the boss and that
				// his asks WILL be carried out when the call ends. Without
				// this he knows only the two functions in his hand, so he
				// tells the boss "I'm unable to make calls" — a lie about the
				// system he lives in, and exactly what happened on Jul 11.
				m.notifyVerified(ctx, conn, callID)
				// An instruction given in the SAME BREATH as the phrase ("It's
				// Glock 17, call my wife") must not be lost to the redaction.
				if ask := spokenAsk(redacted); ask != "" {
					bossAsk = append(bossAsk, ask)
				}
			case bossVerified:
				bossAsk = append(bossAsk, redacted)
			case attempted:
				verifyAttempted = true
			}
		}
		// The persona only reaches for the passphrase when a caller claims to
		// BE the boss or starts giving orders, which makes Jarvis's own word
		// the deterministic marker that this line was commanded.
		if speaker == "Jarvis" && strings.Contains(strings.ToLower(text), "passphrase") {
			challenged = true
		}

		text = cardPattern.ReplaceAllString(redacted, "[card]")
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

	// The honesty guard. Someone commanded this line and was not verified.
	// REFUSING them is correct; refusing them SILENTLY is not: that is how a
	// call where the boss asked for something real gets filed green and he
	// discovers days later that nothing ever happened.
	if direction == "inbound" && !bossVerified && (challenged || verifyAttempted) {
		m.alertUnverifiedCommand(callID, number, passphrase == "", lines)
	}

	m.deliverOutcome(callID, briefID, direction, brief, number, lines, time.Since(start))
	return nil
}

// notifyVerified tells the LIVE call agent, mid-call, that the caller just
// verified as the boss.
//
// The MECHANIC (inject the moment verification lands) is here in code so it
// cannot be forgotten or dropped. The WORDS are data (mem_agent_state
// phone:persona:verified): what Jarvis should DO once he knows is judgment, and
// judgment never belongs in a Go const.
//
// Injected as a system message on the live realtime session, with no
// response.create: he should not interrupt the boss, only know the truth by the
// time he next opens his mouth.
func (m *Manager) notifyVerified(ctx context.Context, conn *websocket.Conn, callID string) {
	note, err := m.loadPersona(ctx, "verified")
	if err != nil {
		// Loud: without the note he keeps insisting he is unable to act.
		log.Printf("phone: call %s: the verified note is missing, so the call agent will not know he can act: %v", callID, err)
		return
	}
	if err := conn.WriteJSON(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message",
			"role": "system",
			"content": []map[string]any{
				{"type": "input_text", "text": note},
			},
		},
	}); err != nil {
		log.Printf("phone: call %s: injecting the verified note failed: %v", callID, err)
	}
}

// alertUnverifiedCommand surfaces a call where someone gave orders and could not
// be verified. Card + push, in the boss's own voice, because the alternative is
// the failure that produced this function: he asked for something on the phone,
// was told "I can't do that", and nothing anywhere said so.
func (m *Manager) alertUnverifiedCommand(callID, number string, noPassphrase bool, lines []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var body string
	if noPassphrase {
		body = "Someone rang your line and gave me instructions as though they were you. There is no passphrase stored at all, so I cannot verify anyone by voice, and nothing asked of me on the phone will be carried out until one is set (Settings, Privacy, Phone vault). If that was you, that is why nothing happened."
	} else {
		body = "Someone rang your line, spoke as though they were you, and gave me instructions. The phrase they gave did not match the one in your vault, so I did not act on any of it. If that was you, say the phrase again and I will carry out what you ask the moment we hang up."
	}
	if number != "" {
		body += "\n\nThey called from " + number + "."
	}
	body += "\n\n---\n\n" + clip(strings.Join(lines, "\n"), maxTranscriptChars)

	if m.surface != nil {
		importance := 90
		if _, err := m.surface.Upsert(ctx, &surface.Item{
			Surface: "calls",
			Kind:    "call",
			Source:  "phone",
			// Distinct from the call's own card: the outcome and the refusal
			// are two different things the boss needs to see.
			ExternalID: callID + ":unverified",
			Title:      "I refused instructions from a caller I could not verify",
			Subtitle:   "The passphrase did not match, so I acted on nothing",
			Body:       body,
			Importance: &importance,
		}); err != nil {
			log.Printf("phone: surface unverified-command alert for call %s: %v", callID, err)
		}
	}
	if m.push != nil {
		m.push.Notify(ctx, push.Notification{
			Title: "Someone gave me orders I could not verify",
			Body:  "The passphrase did not match, so I acted on nothing. Open to see what they asked for.",
			Kind:  "phone_call",
			URL:   "/",
			Tag:   "phone-unverified-" + callID,
		})
	}
	log.Printf("phone: call %s: instructions from an UNVERIFIED caller were refused (no passphrase match)", callID)
}

var cardPattern = regexp.MustCompile(`\b(?:\d[ -]?){13,19}\b`)

// callSurfaceKey is the dedup key for a call's surface card. Outbound calls
// key on the briefID (the ONLY id shared between the OpenAI monitor path and
// the Twilio status-callback path, so "placed -> ringing -> answered+outcome"
// collapse into one evolving card). Inbound has no brief, so it keys on the
// OpenAI call id.
func callSurfaceKey(briefID, callID string) string {
	if briefID != "" {
		return "brief:" + briefID
	}
	return callID
}

// deliverOutcome writes the ONE durable record of the call — a generic
// surface item on the "calls" surface — and pushes the boss's phone for
// outbound calls or substantive inbound ones. Uses a fresh ctx: the call
// ctx may already be expired and the outcome must land regardless.
func (m *Manager) deliverOutcome(callID, briefID, direction string, brief *Brief, number string, lines []string, dur time.Duration) {
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
	// Follow-through: the summarizer appends a "FOLLOWUP:" line when the call
	// promised an action or callback. Go ALWAYS persists it (the only
	// judgment is whether one exists), then strips it from the shown summary.
	if m.followupCreator != nil && summary != "" {
		if idx := strings.Index(summary, "FOLLOWUP:"); idx >= 0 {
			task := strings.TrimSpace(summary[idx+len("FOLLOWUP:"):])
			summary = strings.TrimSpace(strings.TrimRight(summary[:idx], "\n -"))
			if task != "" {
				title := task
				if len(title) > 80 {
					title = title[:80]
				}
				body := "From your " + direction + " call"
				if brief != nil && brief.Name != "" {
					body += " with " + brief.Name
				}
				body += ": " + task
				go m.followupCreator(context.Background(), title, body)
			}
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
			ExternalID: callSurfaceKey(briefID, callID),
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
