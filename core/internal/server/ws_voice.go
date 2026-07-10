package server

import (
	"context"
	"encoding/base64"
	"hash/fnv"
	"log"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dopesoft/infinity/core/internal/voice"
)

// speakPump turns a voice turn's streamed text into spoken audio. It owns a
// SentenceChunker and a single serialized synthesis goroutine: as Loop.Run
// emits text deltas, complete sentences are queued and synthesized one at a
// time (preserving spoken order) and shipped to the browser as `voice_audio`
// frames. This is the "mouth" - the brain is the same Loop.Run text uses.
//
// Narration mechanic (Infinity Rule #1b): the voice overlay ASKS the model to
// narrate before tool calls, but a model can drop that instruction. So the
// pump GUARANTEES no dead air: on a tool call with no recent speech, it speaks
// a short synthesized status line. Capability never depends on the prose.
type speakPump struct {
	speaker   *voice.Speaker
	send      func(wsServerEvent)
	sessionID string
	ctx       context.Context

	chunker voice.SentenceChunker
	q       chan speakClip
	drained chan struct{}
	narrate bool

	// mu guards seq/lastAt/closed and the channel send. Two producers exist:
	// the turn's event goroutine (onDelta/onToolCall/finish) and the
	// turn-start ack timer - without the lock the timer could race finish()'s
	// close(q) and panic on a closed-channel send.
	mu     sync.Mutex
	seq    int
	lastAt time.Time
	closed bool

	// squelched is the server half of barge-in. The client cuts local
	// playback the instant VAD fires, but Core would otherwise keep
	// synthesizing and shipping the REST of the interrupted reply - which is
	// exactly the "kept jabbing on" failure. Squelch() flips this on (clips
	// are dropped at both enqueue and synthesis time; captions unaffected);
	// EventSteered - the boss's interjection being absorbed by the loop -
	// flips it off so the answer to the steer is spoken. epoch invalidates
	// clips already sitting in the channel when the squelch landed.
	squelched atomic.Bool
	epoch     atomic.Int64
}

type speakClip struct {
	seq   int
	text  string
	epoch int64
}

// narrationGap is how long the model can be silent during work before the
// pump speaks a filler status. Tuned so a model that DOES narrate ("let me
// check…") suppresses the filler, but a silent tool call never leaves dead air.
const narrationGap = 1200 * time.Millisecond

// turnStartAckDelay is how long the brain may stay silent at the START of a
// voice turn before the pump speaks a one-word ack. This is the perceived-
// latency killer: the boss hears Jarvis engage within ~a second (the ack clip
// is prewarmed - zero TTS round-trip) while the model is still producing its
// first sentence. A brain that answers faster than this never triggers it.
const turnStartAckDelay = 900 * time.Millisecond

// turnStartAcks are the spoken acks. Short, dry, Jarvis. Rotated per turn so
// back-to-back slow turns don't repeat the same word.
var turnStartAcks = []string{
	"Mm, one moment.",
	"Right.",
	"One sec, boss.",
}

// ackLine picks a turn-start ack, varying by session and wall clock.
func ackLine(sessionID string) string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(sessionID))
	idx := (int(h.Sum32()) + int(time.Now().Unix()/7)) % len(turnStartAcks)
	if idx < 0 {
		idx = -idx
	}
	return turnStartAcks[idx]
}

// newSpeakPump returns a pump for a voice turn, or nil when this isn't a voice
// turn or no Speaker is configured (voice degrades to captions-only). When
// non-nil it has already started its synthesis goroutine.
func (s *Server) newSpeakPump(ctx context.Context, sessionID string, voiceTurn bool, send func(wsServerEvent)) *speakPump {
	if !voiceTurn || s.speaker == nil {
		return nil
	}
	p := &speakPump{
		speaker:   s.speaker,
		send:      send,
		sessionID: sessionID,
		ctx:       ctx,
		q:         make(chan speakClip, 64),
		drained:   make(chan struct{}),
		narrate:   voiceToolNarrationEnabled(),
	}
	go p.run()
	// No-dead-air at turn START: if the brain hasn't produced a speakable
	// sentence by the deadline, speak a prewarmed one-word ack. Firing after
	// the pump closed/squelched is a guarded no-op.
	if p.narrate {
		time.AfterFunc(turnStartAckDelay, p.maybeTurnStartAck)
	}
	return p
}

// maybeTurnStartAck speaks a short ack iff nothing has been queued for speech
// yet. Runs on the timer goroutine - all state behind mu / atomics.
func (p *speakPump) maybeTurnStartAck() {
	if p == nil || p.ctx.Err() != nil || p.squelched.Load() {
		return
	}
	p.mu.Lock()
	spoke := !p.lastAt.IsZero()
	p.mu.Unlock()
	if spoke {
		return
	}
	p.enqueue(ackLine(p.sessionID))
}

// onDelta feeds streamed assistant text; any newly-complete sentences are
// queued for speech.
func (p *speakPump) onDelta(delta string) {
	if p == nil {
		return
	}
	for _, sentence := range p.chunker.Push(delta) {
		p.enqueue(sentence)
	}
}

// onToolCall is the no-dead-air guarantee: if the model went quiet before
// calling a tool, speak a short status so the boss hears work happening.
func (p *speakPump) onToolCall(name string) {
	if p == nil || !p.narrate {
		return
	}
	p.mu.Lock()
	last := p.lastAt
	p.mu.Unlock()
	if !last.IsZero() && time.Since(last) < narrationGap {
		return // the model already narrated; don't talk over it
	}
	p.enqueue(voiceToolNarration(name))
}

// finish flushes the trailing partial sentence, closes the queue, and waits
// for synthesis to drain so the final clip ships before the turn goroutine
// returns (which is when the turn ctx is cancelled).
func (p *speakPump) finish() {
	if p == nil {
		return
	}
	for _, sentence := range p.chunker.Flush() {
		p.enqueue(sentence)
	}
	// closed-before-close ordering: once the flag is set no producer (event
	// goroutine or ack timer) can reach the channel send, so close is safe.
	p.mu.Lock()
	p.closed = true
	p.mu.Unlock()
	close(p.q)
	<-p.drained
}

// Squelch drops everything queued for speech and suppresses new clips until
// Unsquelch. Called (via the turns registry) when the browser reports a
// barge-in. Captions are untouched - only the mouth goes quiet. Safe from any
// goroutine.
func (p *speakPump) Squelch() {
	if p == nil {
		return
	}
	p.epoch.Add(1) // invalidate clips already in the channel
	p.squelched.Store(true)
}

// Unsquelch re-enables speech. Called on EventSteered - the loop has absorbed
// the boss's interjection, so what streams next is the answer to it.
func (p *speakPump) Unsquelch() {
	if p == nil {
		return
	}
	p.squelched.Store(false)
}

func (p *speakPump) enqueue(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	if p.squelched.Load() {
		return // barge-in: the boss cut this reply off; caption-only from here
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return // turn already finished (e.g. ack timer fired late)
	}
	p.seq++
	p.lastAt = time.Now()
	select {
	case p.q <- speakClip{seq: p.seq, text: text, epoch: p.epoch.Load()}:
	default:
		// Queue full (model dumping text far faster than TTS can speak it).
		// Dropping a clip is better than blocking the turn's event loop; the
		// caption still shows the full text. Rare in practice.
		log.Printf("voice: speak queue full, dropped clip seq=%d", p.seq)
	}
}

// run is the serialized synthesis loop. One clip at a time keeps spoken order
// correct. On a cancelled context (interrupt/barge-in) it drains without
// synthesizing so finish() returns promptly.
func (p *speakPump) run() {
	defer close(p.drained)
	for clip := range p.q {
		if p.ctx.Err() != nil {
			continue // interrupt/timeout: skip remaining clips
		}
		if clip.epoch != p.epoch.Load() {
			continue // enqueued before a barge-in: stale, never speak it
		}
		audio, mime, err := p.speaker.Synthesize(p.ctx, clip.text)
		if err != nil {
			if p.ctx.Err() == nil {
				// Real failure (not a cancel). Stderr so Railway tags it
				// error; the sentence still showed as a caption.
				log.Printf("voice: tts synth failed seq=%d: %v", clip.seq, err)
			}
			continue
		}
		if len(audio) == 0 {
			continue
		}
		p.send(wsServerEvent{
			Type:      "voice_audio",
			SessionID: p.sessionID,
			Audio: &wsVoiceAudio{
				Seq:      clip.seq,
				MimeType: mime,
				Data:     base64.StdEncoding.EncodeToString(audio),
			},
		})
	}
}

// voiceToolNarration is the spoken filler when the model calls a tool without
// narrating first. Short, natural, first-person - never robotic categories.
// Generic default covers any tool we don't have a line for.
func voiceToolNarration(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.HasPrefix(n, "claude_code") || strings.Contains(n, "code") || strings.Contains(n, "build"):
		return "Right, let me get into that."
	case strings.Contains(n, "gmail") || strings.Contains(n, "mail") || strings.Contains(n, "inbox"):
		return "Checking your inbox now."
	case strings.Contains(n, "calendar") || strings.Contains(n, "event"):
		return "Let me look at your calendar."
	case strings.Contains(n, "memory") || strings.Contains(n, "recall") || strings.Contains(n, "remember"):
		return "One moment, checking what I remember."
	case strings.Contains(n, "search") || strings.Contains(n, "web") || strings.Contains(n, "fetch") || strings.Contains(n, "browse"):
		return "Looking that up now."
	case strings.Contains(n, "skill"):
		return "One moment, working through that."
	case strings.Contains(n, "delegate") || strings.Contains(n, "background") || strings.Contains(n, "task"):
		return "Kicking that off for you."
	default:
		return "On it, give me a moment."
	}
}

// voiceToolNarrationEnabled lets the no-dead-air filler be switched off via env
// for anyone who finds it chatty. Default on.
func voiceToolNarrationEnabled() bool {
	return !strings.EqualFold(strings.TrimSpace(os.Getenv("INFINITY_VOICE_NARRATION")), "false")
}

// voicePrewarmLines is every fixed short line the pump can speak - fed to
// Speaker.Prewarm at boot so their clips are cached before first use. The
// narration lines are enumerated through voiceToolNarration itself (one
// representative key per branch) so this list can never drift from the switch.
func voicePrewarmLines() []string {
	lines := append([]string{}, turnStartAcks...)
	for _, key := range []string{"code", "mail", "calendar", "memory", "search", "skill", "delegate", ""} {
		lines = append(lines, voiceToolNarration(key))
	}
	return lines
}
