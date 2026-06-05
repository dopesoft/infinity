package server

import (
	"context"
	"encoding/base64"
	"log"
	"os"
	"strings"
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
	seq     int
	lastAt  time.Time
	narrate bool
}

type speakClip struct {
	seq  int
	text string
}

// narrationGap is how long the model can be silent during work before the
// pump speaks a filler status. Tuned so a model that DOES narrate ("let me
// check…") suppresses the filler, but a silent tool call never leaves dead air.
const narrationGap = 1200 * time.Millisecond

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
	return p
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
	if !p.lastAt.IsZero() && time.Since(p.lastAt) < narrationGap {
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
	close(p.q)
	<-p.drained
}

func (p *speakPump) enqueue(text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	p.seq++
	p.lastAt = time.Now()
	select {
	case p.q <- speakClip{seq: p.seq, text: text}:
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
			continue // barge-in/interrupt: skip remaining clips
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
