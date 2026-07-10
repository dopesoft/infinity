package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Speaker turns Jarvis's TEXT response (produced by the real agent loop) into
// spoken audio via OpenAI's text-to-speech endpoint. This is the "mouth" half
// of voice parity: cognition runs through the same Loop.Run as text, and its
// streamed words are synthesized here so voice speaks the EXACT brain output
// (no second model paraphrasing it).
//
// Synthesis is per-sentence on purpose. The agent loop streams text deltas;
// the ws voice pump batches them into sentences and calls Synthesize once per
// sentence, emitting each clip as it's ready. That gives incremental, low
// latency playback without HTTP chunk-streaming on either side, and a clean
// barge-in unit (stop after the current clip).
const (
	// defaultTTSModel is gpt-4o-mini-tts - the model that accepts an
	// `instructions` field for delivery/accent control. Override via
	// INFINITY_VOICE_TTS_MODEL.
	defaultTTSModel = "gpt-4o-mini-tts"

	// ttsAudioFormat is what /v1/audio/speech returns. mp3 decodes cleanly
	// via the browser's AudioContext.decodeAudioData and stays small over
	// the WebSocket. The matching MIME is audioMIME.
	ttsAudioFormat = "mp3"
	audioMIME      = "audio/mpeg"

	// speechURL is the OpenAI text-to-speech endpoint.
	speechURL = "https://api.openai.com/v1/audio/speech"

	// ttsDeliveryInstructions is the spoken-delivery guidance handed to the
	// TTS model. The BRITISH accent itself comes from the `fable` voice
	// (defaultVoice) - a base voice's accent dominates and an instructions
	// string can't reliably override an American voice, which is why this
	// line alone never produced a British accent on the old `ash` voice.
	// With `fable` doing the accent, this now just reinforces the RP
	// delivery and sets the dry, restrained Jarvis tone. Delivery belongs
	// with the TTS, not the brain (it used to be a line in the realtime
	// model's frozen prompt, britishAccentLine).
	//
	// Pacing: "brisk" on purpose. The earlier "measured pacing" wording
	// produced a delivery the boss flagged as "hella slow"; brisk + the
	// `speed` request param below are the two levers that fixed it.
	ttsDeliveryInstructions = "Speak in a clearly British Received Pronunciation accent - non-rhotic vowels, crisp consonants, warm Jarvis-style restraint. Brisk, energetic conversational pace - never slow, never drawn out, no long pauses. Refined and dry, never American. Sound like a real person talking, not a narrator reading."

	// defaultTTSSpeed nudges playback above 1.0 because the boss found the
	// default delivery too slow. /v1/audio/speech accepts 0.25-4.0 (verified
	// against OpenAI's API spec). Override with INFINITY_VOICE_TTS_SPEED.
	defaultTTSSpeed = 1.15
)

// Speaker holds the OpenAI key + TTS config. Stateless; safe to share. Build
// once at boot via NewSpeaker. nil when OPENAI_API_KEY is unset so the server
// degrades gracefully (voice turns fall back to captions-only).
type Speaker struct {
	apiKey       string
	model        string
	voice        string
	instructions string
	speed        float64
	httpClient   *http.Client
}

// NewSpeaker constructs a Speaker from env. Returns nil when OPENAI_API_KEY is
// unset. Shares INFINITY_VOICE_NAME with the realtime Minter so the spoken
// voice is consistent whichever path produced it.
func NewSpeaker() *Speaker {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil
	}
	model := strings.TrimSpace(os.Getenv("INFINITY_VOICE_TTS_MODEL"))
	if model == "" {
		model = defaultTTSModel
	}
	voice := strings.TrimSpace(os.Getenv("INFINITY_VOICE_NAME"))
	if voice == "" {
		voice = defaultVoice
	}
	speed := defaultTTSSpeed
	if v := strings.TrimSpace(os.Getenv("INFINITY_VOICE_TTS_SPEED")); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil && f >= 0.25 && f <= 4.0 {
			speed = f
		}
	}
	return &Speaker{
		apiKey:       key,
		model:        model,
		voice:        voice,
		instructions: ttsDeliveryInstructions,
		speed:        speed,
		// TTS for a single sentence is fast; keep a generous ceiling so a
		// slow network doesn't truncate a clip mid-word.
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// Synthesize speaks one chunk of text. Returns the raw audio bytes and the
// MIME type. Errors are returned verbatim so the caller can log + skip the
// clip (a failed clip drops to captions-only for that sentence rather than
// killing the whole turn).
func (sp *Speaker) Synthesize(ctx context.Context, text string) ([]byte, string, error) {
	if sp == nil {
		return nil, "", fmt.Errorf("voice: speaker not configured (OPENAI_API_KEY unset)")
	}
	// Mechanic, not prose (Rule #1b): the voice overlay ASKS the brain for
	// no markdown, but a model drops instructions - so the mouth guarantees
	// it. Strip markup here, at the single synthesis chokepoint, so
	// "**Aldeez**" is spoken as "Aldeez" no matter what the brain emitted.
	text = strings.TrimSpace(StripSpokenMarkup(text))
	if text == "" {
		return nil, "", nil
	}

	body, err := json.Marshal(map[string]any{
		"model":           sp.model,
		"voice":           sp.voice,
		"input":           text,
		"instructions":    sp.instructions,
		"speed":           sp.speed,
		"response_format": ttsAudioFormat,
	})
	if err != nil {
		return nil, "", fmt.Errorf("marshal tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, speechURL, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("build tts request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+sp.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := sp.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("call tts: %w", err)
	}
	defer resp.Body.Close()

	audio, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", fmt.Errorf("read tts body: %w", err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, "", fmt.Errorf("tts %d: %s", resp.StatusCode, truncateForErr(string(audio)))
	}
	return audio, audioMIME, nil
}

// spokenMarkupPatterns are the markdown constructs that must never reach the
// TTS model - they get read aloud as symbols ("asterisk asterisk Aldeez") or
// warp the delivery. Ordered: links first (their text survives, the URL
// doesn't), then emphasis/code wrappers, then structural line prefixes.
var (
	reMarkdownLink   = regexp.MustCompile(`\[([^\]]*)\]\([^)]*\)`)
	reMarkdownEmph   = regexp.MustCompile("(\\*\\*|__|\\*|_|`{1,3}|~~)")
	reMarkdownPrefix = regexp.MustCompile(`(?m)^\s{0,3}(#{1,6}\s+|>\s?|[-*+]\s+|\d+\.\s+)`)
)

// StripSpokenMarkup converts markdown-ish brain output into plain speakable
// text. Deterministic and conservative: it only removes markup characters,
// never words. Exported for the ws voice pump's tests.
func StripSpokenMarkup(s string) string {
	s = reMarkdownLink.ReplaceAllString(s, "$1")
	s = reMarkdownPrefix.ReplaceAllString(s, "")
	s = reMarkdownEmph.ReplaceAllString(s, "")
	return s
}

// SentenceChunker accumulates streamed text deltas and emits complete
// sentences for synthesis. It exists so we speak as soon as the first
// sentence is whole instead of waiting for the entire response - the thing
// that keeps perceived latency low on conversational turns.
//
// Not safe for concurrent use; the ws voice pump owns one per turn and feeds
// it deltas in order.
type SentenceChunker struct {
	buf strings.Builder
}

// sentenceEnders are the characters we treat as a hard end-of-utterance for
// speaking. A clip is emitted when we cross one of these AND the accumulated
// text is past a small floor (so we don't synthesize "Mr." as its own clip).
const sentenceMinChars = 12

// Push adds a delta and returns any newly-complete sentences ready to speak.
// Partial trailing text stays buffered until the next Push or Flush.
func (c *SentenceChunker) Push(delta string) []string {
	if delta == "" {
		return nil
	}
	c.buf.WriteString(delta)
	return c.drain(false)
}

// Flush returns whatever remains (the final partial sentence) at turn end.
// Call once after the loop completes so the last clause is spoken.
func (c *SentenceChunker) Flush() []string {
	return c.drain(true)
}

func (c *SentenceChunker) drain(final bool) []string {
	var out []string
	for {
		s := c.buf.String()
		idx := acceptableBreak(s)
		if idx < 0 {
			break
		}
		sentence := strings.TrimSpace(s[:idx+1])
		rest := s[idx+1:]
		c.buf.Reset()
		c.buf.WriteString(rest)
		if sentence != "" {
			out = append(out, sentence)
		}
	}
	if final {
		tail := strings.TrimSpace(c.buf.String())
		c.buf.Reset()
		if tail != "" {
			out = append(out, tail)
		}
	}
	return out
}

// acceptableBreak returns the index of the first sentence terminator (. ! ? or
// newline) such that the sentence ending there is at least sentenceMinChars
// long, or -1 if none qualifies yet. The length floor skips abbreviations and
// decimals ("Mr.", "3.5") so they aren't spoken as their own clip - the scan
// continues to the NEXT terminator instead of stalling on the first one.
func acceptableBreak(s string) int {
	for i, r := range s {
		switch r {
		case '.', '!', '?', '\n':
			if len([]rune(strings.TrimSpace(s[:i+1]))) >= sentenceMinChars {
				return i
			}
		}
	}
	return -1
}
