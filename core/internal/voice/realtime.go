// Package voice integrates OpenAI's Realtime API as the EARS of voice mode:
// low-latency mic capture + whisper transcription + VAD barge-in, nothing
// more. The realtime model is deliberately demoted - it does NOT think, hold
// the conversation, or run tools. Cognition runs through the SAME agent loop
// (Loop.Run) that text uses, so voice Jarvis IS text Jarvis - same memory,
// skills, tools, gate, and Studio-selected brain. The brain's text reply is
// spoken back via the Speaker (tts.go), the MOUTH.
//
// Why demote? A separate realtime model meant a different brain with a frozen,
// trimmed snapshot of context - it didn't know the boss, couldn't make skills
// on the fly, couldn't block for Trust. Routing every utterance through
// Loop.Run erases all of that divergence in one move.
//
// Sequence:
//
//  1. Browser taps mic → POST /api/voice/session { session_id }.
//  2. Core mints an ephemeral realtime key configured for INPUT ONLY:
//     whisper transcription + server-VAD, output_modalities=["text"], no
//     tools, no auto-response. The model never speaks.
//  3. Browser does the WebRTC SDP exchange directly with api.openai.com.
//  4. On a finalized user transcript the browser sends a `message` frame
//     (voice:true) over the existing chat WebSocket. Core runs Loop.Run -
//     the identical text brain - and streams the reply's text back as
//     captions AND synthesized speech (`voice_audio` frames via the Speaker).
//  5. Memory capture, mem_turns, hooks, Trust gating all happen inside
//     Loop.Run exactly as in text mode. No bespoke voice tool/turn handlers.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// defaultRealtimeModel is the model used unless the boss overrides via
	// INFINITY_VOICE_MODEL. The boss explicitly asked for gpt-realtime-1.5;
	// keep it as the documented default so a stale env var doesn't silently
	// downgrade them.
	defaultRealtimeModel = "gpt-realtime-1.5"
	defaultVoice         = "ash"

	// voiceInputModeInstructions is all the demoted realtime model needs. It
	// never generates the reply (the agent loop does) and never speaks (the
	// Speaker does) - this just keeps the session well-formed and tells the
	// model to stay quiet so a stray response can't talk over Jarvis. The
	// boss identity, tools, and dispatch discipline that used to be crammed
	// here now live where they belong: in the agent loop's memory + tools.
	voiceInputModeInstructions = "You are a passive transcription endpoint. Do not respond, do not speak, do not call tools. Another system handles all replies. Remain silent."

	// realtimeClientSecretsURL is the OpenAI endpoint that mints the
	// browser's short-lived authentication key. The full API key never
	// leaves the server; the ephemeral key carries the session config
	// baked in.
	realtimeClientSecretsURL = "https://api.openai.com/v1/realtime/client_secrets"

	// realtimeSDPURL is the GA WebRTC entrypoint. The browser POSTs its
	// SDP offer here with the ephemeral key in Authorization; OpenAI
	// returns the SDP answer. The model id is baked into the ephemeral
	// at mint time, so no query params. (The beta surface used
	// /v1/realtime?model=… - do not revive that.)
	realtimeSDPURL = "https://api.openai.com/v1/realtime/calls"
)

// SessionRequest is what Core needs to mint an input-only realtime key. The
// realtime model is demoted to ears, so there's no system prompt and no tools
// to carry - the agent loop owns all of that. Just the session id, so voice
// and text share memory + provenance.
type SessionRequest struct {
	SessionID string
}

// SessionResponse is the JSON shape the browser receives. It's
// deliberately minimal - everything the WebRTC handshake needs, and
// nothing else.
type SessionResponse struct {
	ClientSecret string `json:"client_secret"`
	ExpiresAt    int64  `json:"expires_at"`
	Model        string `json:"model"`
	Voice        string `json:"voice"`
	SDPURL       string `json:"sdp_url"`
}

// Minter holds the OpenAI API key + HTTP client. Stateless; safe to
// share. Build once at server boot.
type Minter struct {
	apiKey     string
	model      string
	voice      string
	httpClient *http.Client
}

// New constructs a Minter from env. Returns nil when OPENAI_API_KEY is
// unset so the server can degrade gracefully (voice endpoints will 503
// instead of crashing on startup).
func New() *Minter {
	key := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if key == "" {
		return nil
	}
	model := strings.TrimSpace(os.Getenv("INFINITY_VOICE_MODEL"))
	if model == "" {
		model = defaultRealtimeModel
	}
	voice := strings.TrimSpace(os.Getenv("INFINITY_VOICE_NAME"))
	if voice == "" {
		voice = defaultVoice
	}
	return &Minter{
		apiKey:     key,
		model:      model,
		voice:      voice,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

func (m *Minter) Model() string { return m.model }
func (m *Minter) Voice() string { return m.voice }

// Mint POSTs the session config to OpenAI and returns the ephemeral
// client_secret. The browser uses it as the bearer for SDP exchange.
//
// Errors are surfaced verbatim - the calling HTTP handler logs and
// returns 502 so the Studio UI can show a real cause instead of a
// generic "voice failed" toast.
func (m *Minter) Mint(ctx context.Context, req SessionRequest) (*SessionResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("voice: minter not configured (OPENAI_API_KEY unset)")
	}

	// Input-only realtime config. The model is demoted to a transcriber:
	//   - output_modalities ["text"]: never produces audio. The Speaker
	//     (tts.go) voices the agent loop's reply instead.
	//   - no tools: tools run through Loop.Run, not the realtime model.
	//   - create_response false: server-VAD detects speech for transcription
	//     but never makes the model answer on its own. The browser drives
	//     the conversation by sending finalized transcripts to the agent loop
	//     over the chat WebSocket - it no longer sends response.create at all.
	//   - whisper transcription + near-field noise reduction stay: that's the
	//     whole point of keeping the realtime session (low-latency STT + VAD).
	session := map[string]any{
		"type":              "realtime",
		"model":             m.model,
		"instructions":      voiceInputModeInstructions,
		"output_modalities": []string{"text"},
		"audio": map[string]any{
			"input": map[string]any{
				"noise_reduction": map[string]any{
					"type": "near_field",
				},
				"turn_detection": map[string]any{
					"type":                "server_vad",
					"create_response":     false,
					"interrupt_response":  false,
					"threshold":           0.65,
					"prefix_padding_ms":   300,
					"silence_duration_ms": 700,
				},
				"transcription": map[string]any{
					"model": "whisper-1",
				},
			},
		},
	}

	body, err := json.Marshal(map[string]any{"session": session})
	if err != nil {
		return nil, fmt.Errorf("marshal session: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, realtimeClientSecretsURL, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build mint request: %w", err)
	}
	httpReq.Header.Set("Authorization", "Bearer "+m.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	// Do NOT send `OpenAI-Beta: realtime=v1` here. The GA Realtime
	// surface mints "GA client secrets" and rejects them in any
	// subsequent request that carries the beta header
	// (api_version_mismatch). Mint and SDP exchange both stay GA.

	resp, err := m.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("call client_secrets: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("client_secrets %d: %s", resp.StatusCode, truncateForErr(string(raw)))
	}

	// The minted payload comes back in one of two shapes depending on
	// API version. Newer surfaces flatten { value, expires_at } at the
	// top level; older ones nest under `client_secret`. Try both.
	var flat struct {
		Value     string `json:"value"`
		ExpiresAt int64  `json:"expires_at"`
	}
	var nested struct {
		ClientSecret struct {
			Value     string `json:"value"`
			ExpiresAt int64  `json:"expires_at"`
		} `json:"client_secret"`
	}
	_ = json.Unmarshal(raw, &flat)
	_ = json.Unmarshal(raw, &nested)

	value := flat.Value
	expires := flat.ExpiresAt
	if value == "" {
		value = nested.ClientSecret.Value
		expires = nested.ClientSecret.ExpiresAt
	}
	if value == "" {
		return nil, fmt.Errorf("client_secrets returned empty secret: %s", truncateForErr(string(raw)))
	}

	return &SessionResponse{
		ClientSecret: value,
		ExpiresAt:    expires,
		Model:        m.model,
		Voice:        m.voice,
		SDPURL:       realtimeSDPURL,
	}, nil
}

// truncateForErr keeps error messages under control - OpenAI sometimes
// returns multi-kilobyte bodies on rate limits.
func truncateForErr(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
