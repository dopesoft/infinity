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
	"sync"
	"sync/atomic"
	"time"

	"github.com/dopesoft/infinity/core/internal/contacts"
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

	// greetingGrace: how long to wait, after attaching, to see whether the
	// session is already greeting the caller on its own before nudging it to.
	// Short enough that a silent line still gets a prompt hello.
	greetingGrace = 900 * time.Millisecond

	// callerWindowSize: how many of the caller's recent utterances are joined
	// when looking for the passphrase. Enough to survive a phrase chopped in
	// half by barge-in, short enough that stray words never assemble into a
	// false match.
	callerWindowSize = 3

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
		"(send a form, call back, a deadline), append a separate line starting " +
		"exactly with 'FOLLOWUP:' and a short description. Otherwise do not add that line. " +
		// A message someone asked Jarvis to PASS ON is not a call summary. It is
		// the boss's mail. He gets their words, and he gets his assistant's read
		// on them, the way a real one would say "she sounded rattled" on the way
		// through the door.
		"If the caller left a MESSAGE for the boss, or asked you to tell him something, " +
		"append a separate line starting exactly with 'MESSAGE:' followed by the message ITSELF, " +
		"in full, in their own words, as close to verbatim as the transcript allows and losing no " +
		"detail they asked to be passed on. Clean up only the noise of speech (ums, false starts, " +
		"stutters, transcription garble); never shorten, summarize, or improve what they meant to say. " +
		"Then append a separate line starting exactly with 'READ:' giving YOUR account of the call as " +
		"his assistant: first what it was about and what they want, in a sentence, then how they " +
		"seemed, their mood and manner (warm, delighted, rattled, curt, upset, in a hurry), and " +
		"anything you noticed between the lines that he would want to know. Two or three sentences, " +
		"first person, candid and specific, the way you would tell him on the way through the door. " +
		"Then append a separate line starting exactly with 'URGENCY:' and one word, low, normal, or " +
		"high. Use high only when they need him soon or something is genuinely wrong. " +
		"Omit all three lines if no message was left. " +
		// Whoever calls should be remembered, the way an assistant remembers.
		"If the caller gave a name AND a number and is worth remembering, append a separate " +
		"line starting exactly with 'CONTACT:' followed by their name, then a | character, " +
		"then their number. Otherwise do not add that line."
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

	// Who this is, in words. The live view said "Incoming call" even when the
	// phone book knew perfectly well it was Ariana.
	who := ""
	if brief != nil && brief.Name != "" {
		who = brief.Name
	} else if direction == "inbound" {
		if c, err := m.book.ByNumber(ctx, number); err == nil && c != nil {
			who = c.Name
		}
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
	sc := &safeConn{c: conn}
	// Reachable from outside while the call is up: Twilio's machine detection
	// lands on a webhook and needs to speak into THIS call.
	m.registerLive(briefID, sc)
	defer m.unregisterLive(briefID)
	infoLog.Printf("phone: monitoring %s call %s", direction, callID)

	// Make Jarvis speak FIRST, but only ONCE.
	//
	// A realtime session normally waits for the caller to speak, so without a
	// nudge the line opens on silence. But the session sometimes starts its own
	// greeting the moment the call is accepted, and an unconditional
	// response.create on top of that produced the double hello the boss heard:
	// "Good afternoon." followed by "Good afternoon, Mr. Kai's office, this is
	// Jarvis." Two greetings, talking over each other.
	//
	// So the nudge waits a beat and only fires if he has NOT already started
	// talking. greetingKicked is read by the loop below, which cancels the
	// pending nudge the instant a response of any kind appears.
	var responseSeen atomic.Bool
	go func() {
		time.Sleep(greetingGrace)
		if responseSeen.Load() {
			return // he is already speaking; a second nudge is a second greeting
		}
		if err := sc.WriteJSON(map[string]any{"type": "response.create"}); err != nil {
			// Non-fatal: the call still works, just greeting-less. Log loud so
			// a silent pickup is diagnosable rather than mysterious.
			log.Printf("phone: initial greeting response.create for call %s failed: %v", callID, err)
		}
	}()

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
		log.Printf("phone: call %s: no passphrase stored (vault: phone passphrase); the boss CANNOT be verified on this call", callID)
	}
	bossVerified := false
	verifyAttempted := false // someone reached for the phrase and missed
	challenged := false      // Jarvis asked for it, i.e. someone commanded this line
	var bossAsk []string

	// The caller's recent utterances, and where they sit in lines[].
	//
	// Barge-in fragments a spoken phrase across several transcript items: on
	// Jul 13 the boss cut Jarvis off to give the passphrase and it arrived as
	// two separate lines. A per-line check can never match either half, so the
	// passphrase is matched across this rolling window. Interrupting is normal
	// on a phone call; it must not cost him his identity.
	var callerWindow []string
	var callerWindowAt []int

	var lines []string
	hangupFired := false
	patchFired := false
	pendingHangup := false // the agent asked to hang up while the human was still talking
	// One invocation is announced on several events, so a function is answered
	// once per call_id and never twice.
	answered := map[string]bool{}
	// The last thing each side said, for the hangup guard. An agent that drops
	// the line mid-sentence is the rudest failure this system has.
	var lastHumanText, lastAgentText string
	var lastHumanAt time.Time
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

		// He has started speaking (any response event at all), so the pending
		// greeting nudge must stand down or he says hello twice.
		if bytes.Contains(raw, []byte(`"type":"response.`)) {
			responseSeen.Store(true)
		}

		// A realtime error event means OUR session config or an event we
		// injected was rejected. Never swallow it: a call that quietly
		// misbehaves is the exact failure this whole file is guarding against.
		if bytes.Contains(raw, []byte(`"type":"error"`)) {
			log.Printf("phone: call %s: realtime error event: %s", callID, clip(string(raw), 500))
			continue
		}

		// The call agent asked to hang up. Honoured only when the call is
		// genuinely over (see hangupAllowed): this model reaches for
		// hangup_call the moment it thinks it has what it needs, and it has
		// already cut the boss off mid-instruction. If he is still talking,
		// the request is held, he is told to stay on the line, and it is
		// re-evaluated as the conversation moves.
		if !hangupFired && calledFunction(raw, "hangup_call") {
			if hangupAllowed(lastHumanText, lastAgentText, lastHumanAt, time.Now()) {
				hangupFired = true
				go m.dropLine(callID)
			} else if !pendingHangup {
				pendingHangup = true
				log.Printf("phone: call %s: held a hangup, the caller was still mid-conversation", callID)
				m.injectNote(ctx, sc, callID, "stay_on_line")
			}
			continue
		}

		// The call agent is looking someone up mid-call, so he can confirm the
		// right person or place with the boss before agreeing to the errand.
		// Answered off the read loop: the phone book (and the web, for somewhere
		// he has never called), handed back for him to speak.
		if id, query, ok := functionCallArg(raw, "find_contact", "query"); ok {
			if !answered[id] {
				answered[id] = true
				go m.answerContactLookup(sc, callID, id, query)
			}
			continue
		}

		// The call agent asked to hand the caller to the boss. Blind
		// transfer via OpenAI refer: the AI leg drops, the caller is
		// connected to Mr. Kai directly.
		if !patchFired && calledFunction(raw, "patch_in_boss") {
			patchFired = true
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
		windowVerified := false
		if direction == "inbound" && speaker != "Jarvis" {
			// Not in this line alone? Try it against the caller's recent
			// utterances joined together, which is how an interrupted
			// passphrase ("blue" ... "falcon") gets recognized at all.
			if !verified && !bossVerified {
				joined := strings.Join(append(append([]string{}, callerWindow...), text), " ")
				if _, jv, ja := matchPassphrase(joined, passphrase); jv {
					verified, windowVerified = true, true
				} else if ja {
					attempted = true
				}
			}
			switch {
			case verified && !bossVerified:
				bossVerified = true
				infoLog.Printf("phone: inbound call %s verified as the boss (passphrase)", callID)
				if windowVerified {
					// The phrase was split across lines, so scrub the pieces
					// out of the ones already stored, then out of this one.
					for _, at := range callerWindowAt {
						lines[at] = humanLabel + ": " + redactFragments(strings.TrimPrefix(lines[at], humanLabel+": "), passphrase)
					}
					redacted = redactFragments(text, passphrase)
				}
				// Tell the LIVE agent he is now talking to the boss and that
				// his asks WILL be carried out. Without this he knows only the
				// two functions in his hand, so he tells the boss "I'm unable
				// to make calls" — a lie about the system he lives in.
				m.injectNote(ctx, sc, callID, "verified")
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

		// Track who said what last, for the hangup guard, and keep the caller
		// window fresh for cross-line passphrase matching.
		if speaker == "Jarvis" {
			lastAgentText = text
		} else {
			lastHumanText, lastHumanAt = text, time.Now()
			if !bossVerified {
				callerWindow = append(callerWindow, text)
				callerWindowAt = append(callerWindowAt, len(lines)-1)
				if len(callerWindow) > callerWindowSize {
					callerWindow = callerWindow[1:]
					callerWindowAt = callerWindowAt[1:]
				}
			}
		}

		// A hangup we held back: now that the conversation has moved on, is the
		// call actually over? This is what lets a real goodbye still end the
		// call promptly while a mid-sentence hangup stays blocked.
		if pendingHangup && !hangupFired && hangupAllowed(lastHumanText, lastAgentText, lastHumanAt, time.Now()) {
			hangupFired = true
			go m.dropLine(callID)
		}

		// Stream the line to Studio's live call view (Phone card modal).
		m.emitLive(LiveEvent{
			CallID: callID, Direction: direction, Number: number, Name: who,
			Speaker: speaker, Text: text,
		})
	}

	// The drive-home loop: a verified boss's spoken asks execute as a full agent
	// turn the moment the call ends, and the result is REPORTED to him whatever
	// happens (errand.go). He never has to go looking for work he commissioned.
	if bossVerified && len(bossAsk) > 0 {
		infoLog.Printf("phone: executing verified boss ask from call %s (%d lines)", callID, len(bossAsk))
		go m.runErrand(strings.Join(bossAsk, "\n"))
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

// safeConn serializes writes to the realtime socket. The read loop injects
// notes, and the contact lookup answers from its own goroutine (a web search
// must not stall the transcript); gorilla permits exactly one concurrent writer,
// so every write goes through here.
type safeConn struct {
	mu sync.Mutex
	c  *websocket.Conn
}

func (s *safeConn) WriteJSON(v any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.c.WriteJSON(v)
}

// answerContactLookup resolves a name the caller said and hands the answer back
// to the live call agent, who reads it out and confirms it with the boss before
// committing to anything ("Ariana, on 929-310-0906?" / "Goodfellas Pizza, the one
// on Preston Road?").
//
// Runs off the read loop because a web search takes seconds and the transcript
// must keep flowing. Fails ALOUD into the call: if the book and the web both come
// up empty, the agent is told so plainly, and says so, rather than agreeing to an
// errand he has no number for.
func (m *Manager) answerContactLookup(sc *safeConn, callID, fnCallID, query string) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()

	var result string
	switch {
	case m.lookup != nil:
		out, err := m.lookup(ctx, query)
		if err != nil {
			log.Printf("phone: call %s: contact lookup for %q failed: %v", callID, query, err)
			result = "The lookup failed, so I could not check. Ask him for the number."
		} else {
			result = out
		}
	case m.book != nil:
		found, err := m.book.Resolve(ctx, query)
		if err != nil {
			log.Printf("phone: call %s: phone-book lookup for %q failed: %v", callID, query, err)
			result = "The phone book could not be read. Ask him for the number."
		} else {
			result = contacts.Describe(found)
		}
	default:
		result = "The phone book is unavailable on this call. Ask him for the number."
	}
	infoLog.Printf("phone: call %s: looked up %q for the call agent", callID, query)

	// Hand the result back against the function's call_id, then let him speak:
	// unlike the persona notes, this one NEEDS a response, because the boss is
	// sitting in silence waiting to hear whether we have his wife's number.
	if err := sc.WriteJSON(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type":    "function_call_output",
			"call_id": fnCallID,
			"output":  result,
		},
	}); err != nil {
		log.Printf("phone: call %s: returning the contact lookup failed: %v", callID, err)
		return
	}
	if err := sc.WriteJSON(map[string]any{"type": "response.create"}); err != nil {
		log.Printf("phone: call %s: prompting the reply after a contact lookup failed: %v", callID, err)
	}
}

// dropLine ends the call after a short grace, so the farewell audio finishes
// playing out before the line goes dead.
func (m *Manager) dropLine(callID string) {
	time.Sleep(2 * time.Second)
	if err := m.hangupCall(context.Background(), callID); err != nil {
		log.Printf("phone: agent-requested hangup for call %s failed: %v", callID, err)
		return
	}
	infoLog.Printf("phone: call %s ended by Jarvis (hangup_call)", callID)
}

// injectNote speaks to the LIVE call agent mid-call, in system voice: that the
// caller just verified as the boss ("verified"), or that he must not hang up
// while someone is still talking to him ("stay_on_line").
//
// The MECHANIC (inject at the moment it matters) is here in code so it cannot be
// forgotten or dropped. The WORDS are data (mem_agent_state phone:persona:*):
// what Jarvis should DO once he knows is judgment, and judgment never belongs in
// a Go const.
//
// Sent with no response.create: he should not interrupt the boss, only know the
// truth by the time he next opens his mouth.
func (m *Manager) injectNote(ctx context.Context, sc *safeConn, callID, key string) {
	note, err := m.loadPersona(ctx, key)
	if err != nil {
		// Loud: without the note he keeps insisting he is unable to act, or
		// keeps trying to hang up on whoever is talking to him.
		log.Printf("phone: call %s: the %q note is missing, so the call agent will not know how to behave: %v", callID, key, err)
		return
	}
	if err := sc.WriteJSON(map[string]any{
		"type": "conversation.item.create",
		"item": map[string]any{
			"type": "message",
			"role": "system",
			"content": []map[string]any{
				{"type": "input_text", "text": note},
			},
		},
	}); err != nil {
		log.Printf("phone: call %s: injecting the %q note failed: %v", callID, key, err)
	}
}

// callerLabel is who to put on an inbound call's card: her NAME if she is in the
// phone book, otherwise the bare number, which still beats an anonymous "Inbound
// call". Empty only when we have neither.
func (m *Manager) callerLabel(ctx context.Context, number string) string {
	if number == "" {
		return ""
	}
	if m.book != nil {
		if c, err := m.book.ByNumber(ctx, number); err == nil && c != nil && c.Name != "" {
			return c.Name
		}
	}
	return number
}

// deliverMessage hands the boss a message somebody asked Jarvis to pass on.
//
// It lands on surface="messages", NOT "calls", and that is the whole point: the
// dashboard sends every calls item to the Phone card and filters that key out of
// "Surfaced by Jarvis", so a message on the call card is a message he never
// reads. This one arrives in his inbox, in Jarvis's own voice, carrying their
// actual words, and buzzes his phone.
func (m *Manager) deliverMessage(ctx context.Context, callID, direction, number string, brief *Brief, d callDigest, saidVerbatim []string) {
	from := "someone"
	switch {
	case brief != nil && brief.Name != "":
		from = brief.Name
	default:
		// Her name, not her digits: "Message for you from Ariana" is mail,
		// "Message for you from +19293100906" is a log line.
		if who := m.callerLabel(ctx, number); who != "" {
			from = who
		}
	}

	title := "Message from " + from
	if isUrgent(d.Urgency) {
		title = "Urgent message from " + from
	}

	// Body is the fallback for any renderer that does not know what a message is.
	// The real payload is the metadata below, so the UI can lay this out properly
	// instead of scraping prose.
	var b strings.Builder
	if d.Read != "" {
		b.WriteString(d.Read + "\n\n")
	}
	b.WriteString(d.Message)
	if number != "" {
		b.WriteString("\n\nThey were on " + number + ".")
	}

	if m.surface != nil {
		// His mail outranks an FYI and sits under a live decision. Urgency is the
		// model's read; what urgency MEANS is Go's call, so it can never be
		// argued into or out of reaching him.
		importance := importanceFor(d.Urgency)
		if _, err := m.surface.Upsert(ctx, &surface.Item{
			Surface:    "messages",
			Kind:       "message",
			Source:     "phone",
			ExternalID: callID + ":message",
			Title:      title,
			Subtitle:   clip(d.Message, 90),
			Body:       b.String(),
			Importance: &importance,
			// The row's subtext is Jarvis's own account of the call, so the boss
			// knows what it is and how they sounded WITHOUT opening anything.
			ImportanceReason: d.Read,
			// Structured, so the UI renders a message as a message: who it is
			// from, what they said, how they seemed, how urgent it is.
			Metadata: map[string]any{
				"from":    from,
				"number":  number,
				"message": d.Message,
				"read":    d.Read,
				"urgency": strings.TrimSpace(d.Urgency),
				"call_id": callID,
				// Their unedited words, for when he wants to hear it exactly as
				// they said it. Kept out of the body: he reads the message, and
				// opens the raw only if he wants to.
				"verbatim": saidVerbatim,
			},
			Actions: []surface.Action{
				{
					ID:     "call_back",
					Label:  "Call back",
					Intent: "Call " + from + " back on " + number + " about the message they left. Ask what they need and handle it.",
					Style:  "primary",
				},
			},
		}); err != nil {
			log.Printf("phone: surfacing the message from call %s failed: %v", callID, err)
		}
	}
	if m.push != nil {
		m.push.Notify(ctx, push.Notification{
			Title: title,
			Body:  clip(d.Message, 200),
			Kind:  "phone_call",
			URL:   "/",
			Tag:   "phone-msg-" + callID,
		})
	}
	infoLog.Printf("phone: call %s left a message for the boss (from %s)", callID, from)
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
		body = "Someone rang your line and gave me instructions as though they were you. There is no passphrase stored at all, so I cannot verify anyone by voice, and nothing asked of me on the phone will be carried out until one is set (Settings, Vault, Personal info). If that was you, that is why nothing happened."
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

	// Name the card. An outbound call knows who it dialed from the brief; an
	// inbound one used to say nothing but "Inbound call", even when the phone
	// book knew exactly who was ringing. If we know her, say her name.
	title := capitalize(direction) + " call"
	switch {
	case brief != nil && brief.Name != "":
		title += " to " + brief.Name
	case brief != nil && brief.To != "":
		title += " to " + brief.To
	case direction == "inbound":
		if who := m.callerLabel(ctx, number); who != "" {
			title += " from " + who
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
	// One parse, every part acted on. The model only judges; Go always delivers.
	digest := parseDigest(summary)
	summary = digest.Outcome

	// The boss's MAIL. It cannot ride the call card: the dashboard routes every
	// surface='calls' item to the Phone card and filters that key OUT of the
	// inbox, so a message left there is a message he never sees. Ariana called
	// back with something to tell him and it died inside a transcript.
	//
	// The caller's OWN words go with it, lifted straight from the transcript
	// rather than trusted to a paraphrase: he asked not to lose any detail, and
	// the exact words are sitting right here.
	if digest.Message != "" {
		human := "Caller"
		if direction == "outbound" {
			human = "Callee"
		}
		m.deliverMessage(ctx, callID, direction, number, brief, digest, speakerLines(lines, human))
	}

	// Whoever calls gets remembered, the way an assistant remembers. Only
	// outbound calls used to write to the phone book, so a caller could give her
	// full name and number and be a stranger again next time.
	if digest.Contact != "" && m.book != nil {
		if name, num := parseContact(digest.Contact); name != "" && num != "" {
			if err := m.book.Upsert(ctx, contacts.Contact{
				Name: name, Number: num, Source: "call",
				Note: "Rang the boss's line on " + time.Now().UTC().Format("Jan 2 2006") + ".",
			}); err != nil {
				// Loud: a book that quietly stops learning sends us back to digits.
				log.Printf("phone: call %s: saving %s (%s) to the phone book failed: %v", callID, name, num, err)
			} else {
				infoLog.Printf("phone: call %s: saved %s to the phone book", callID, name)
			}
		}
	}

	// Follow-through: an action the call left the boss owing.
	if m.followupCreator != nil && digest.Followup != "" {
		title := digest.Followup
		if len(title) > 80 {
			title = title[:80]
		}
		body := "From your " + direction + " call"
		if brief != nil && brief.Name != "" {
			body += " with " + brief.Name
		}
		body += ": " + digest.Followup
		go m.followupCreator(context.Background(), title, body)
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
		// When the call rang, when it hung up, how long it ran — as STRUCTURED
		// metadata, not just the "4m36s" prefix glued into the subtitle string.
		// The viewer renders these above the transcript, and a display string is
		// not a contract: recovering the duration for the calls logged before
		// this took a regex over the subtitle (migration 187), which is exactly
		// the tax for storing data as prose. deliverOutcome runs the instant the
		// call ends, so now IS the hang-up; the start is that minus its length.
		endedAt := time.Now().UTC()
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
			Body: body,
			Metadata: map[string]any{
				"started_at":  endedAt.Add(-dur).Format(time.RFC3339),
				"ended_at":    endedAt.Format(time.RFC3339),
				"duration_ms": dur.Milliseconds(),
			},
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
	// Into the brain. Everything above is a RECORD of the call; this is what makes
	// the call part of what Jarvis KNOWS: retrievable in chat, compressed into
	// memories with provenance, and read by the entity extractor that learns who
	// these people are and what matters to them.
	m.rememberCall(ctx, callID, direction, number, m.callerLabel(ctx, number), brief, summary, lines, dur)

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
