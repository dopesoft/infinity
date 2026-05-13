// Package voice integrates OpenAI's Realtime API for low-latency, full-duplex
// speech. Audio never touches our server — the browser does WebRTC directly
// to OpenAI, authenticated by a short-lived `client_secret` that this package
// mints from the boss's OPENAI_API_KEY. Tool calls round-trip through Core
// over the existing WebSocket so voice has the same tool surface (and the
// same Trust gate) as text mode.
//
// Sequence:
//
//	1. Browser taps mic → POST /api/voice/session { session_id }.
//	2. Core builds Session config (voice, instructions = soul + memory + skills
//	   + British-RP accent line, tools = full registry schemas) and POSTs it
//	   to /v1/realtime/client_secrets with OPENAI_API_KEY.
//	3. Core returns the ephemeral key to the browser.
//	4. Browser does the WebRTC SDP exchange directly with api.openai.com
//	   using that ephemeral key. From then on, audio flows P2P-style.
//	5. Tool calls fire on the data channel; the browser forwards them to
//	   POST /api/voice/tool which runs the same gate + registry as the text
//	   loop, then submits the result back as a function_call_output item.
//	6. Each finalized user/assistant utterance posts to /api/voice/turn
//	   which fires UserPromptSubmit + TaskCompleted hooks. Memory capture
//	   + Sessions tab work identically to text mode.
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

	"github.com/dopesoft/infinity/core/internal/llm"
)

const (
	// defaultRealtimeModel is the model used unless the boss overrides via
	// INFINITY_VOICE_MODEL. The boss explicitly asked for gpt-realtime-1.5;
	// keep it as the documented default so a stale env var doesn't silently
	// downgrade them.
	defaultRealtimeModel = "gpt-realtime-1.5"
	defaultVoice         = "ash"

	// britishAccentLine is appended to the realtime instructions so the
	// Ash voice renders in the accent the boss wants. Ash adapts to
	// accent prompts well; this single line is enough.
	britishAccentLine = "Speak with a refined British (Received Pronunciation) accent — measured, articulate, slightly aristocratic. Stay warm and unhurried."

	// realtimeClientSecretsURL is the OpenAI endpoint that mints the
	// browser's short-lived authentication key. The full API key never
	// leaves the server; the ephemeral key carries the session config
	// baked in.
	realtimeClientSecretsURL = "https://api.openai.com/v1/realtime/client_secrets"

	// realtimeSDPURL is the GA WebRTC entrypoint. The browser POSTs its
	// SDP offer here with the ephemeral key in Authorization; OpenAI
	// returns the SDP answer. The model id is baked into the ephemeral
	// at mint time, so no query params. (The beta surface used
	// /v1/realtime?model=… — do not revive that.)
	realtimeSDPURL = "https://api.openai.com/v1/realtime/calls"
)

// SessionRequest captures everything Core needs to know to mint a key for
// the boss's current chat session.
type SessionRequest struct {
	SessionID string
	// SystemPrompt is the soul + memory + skills + accounts overlay the
	// agent loop would otherwise compose for the next turn. Voice gets
	// the same context so retrievals work; we just append the British-RP
	// instruction line so the model speaks in the accent the boss
	// asked for.
	SystemPrompt string
	// Tools is the registry's full schema list. Voice mode runs without
	// the lazy active-set machinery — at realtime cadence the model
	// would never have time to load-tools and re-call. Ship everything;
	// the schema cost is paid once at session start, not per turn.
	Tools []llm.ToolDef
}

// SessionResponse is the JSON shape the browser receives. It's
// deliberately minimal — everything the WebRTC handshake needs, and
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
// Errors are surfaced verbatim — the calling HTTP handler logs and
// returns 502 so the Studio UI can show a real cause instead of a
// generic "voice failed" toast.
func (m *Minter) Mint(ctx context.Context, req SessionRequest) (*SessionResponse, error) {
	if m == nil {
		return nil, fmt.Errorf("voice: minter not configured (OPENAI_API_KEY unset)")
	}

	instructions := strings.TrimSpace(req.SystemPrompt)
	if instructions != "" {
		instructions += "\n\n"
	}
	instructions += britishAccentLine

	// Newer Realtime API (gpt-realtime family) nests audio config under
	// `audio.input` and `audio.output`. The old flat keys
	// (input_audio_transcription, turn_detection, voice at top level)
	// return 400 "unknown_parameter". Mirror the supported shape so the
	// mint succeeds and the browser can still drive barge-in + captions.
	// We deliberately don't send `output_modalities` here. The same revision
	// that nests audio config under `audio.*` also rejects the legacy
	// top-level modalities key on some surfaces. Realtime defaults to audio
	// output anyway when an audio.output block is present.
	session := map[string]any{
		"type":         "realtime",
		"model":        m.model,
		"instructions": instructions,
		"tool_choice":  "auto",
		"audio": map[string]any{
			"input": map[string]any{
				// Server-side VAD drives barge-in: the browser pauses
				// the audio element on
				// `input_audio_buffer.speech_started` and the model
				// truncates its own response. `create_response: true`
				// makes the model speak back automatically after the
				// user stops, so we don't have to issue manual
				// response.create from the client.
				"turn_detection": map[string]any{
					"type":                "server_vad",
					"create_response":     true,
					"threshold":           0.5,
					"prefix_padding_ms":   300,
					"silence_duration_ms": 500,
				},
				// Captions: ask for live transcription so Studio can
				// render the rolling caption strip.
				"transcription": map[string]any{
					"model": "whisper-1",
				},
			},
			"output": map[string]any{
				"voice": m.voice,
			},
		},
	}

	if len(req.Tools) > 0 {
		session["tools"] = toRealtimeTools(req.Tools)
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

// toRealtimeTools maps llm.ToolDef → Realtime's "function" tool shape.
// Realtime expects a flat structure (no nested function:{} wrapper that
// the Chat Completions API uses).
func toRealtimeTools(defs []llm.ToolDef) []map[string]any {
	out := make([]map[string]any, 0, len(defs))
	for _, d := range defs {
		schema := d.Schema
		if schema == nil {
			schema = map[string]any{"type": "object"}
		}
		out = append(out, map[string]any{
			"type":        "function",
			"name":        d.Name,
			"description": d.Description,
			"parameters":  schema,
		})
	}
	return out
}

// truncateForErr keeps error messages under control — OpenAI sometimes
// returns multi-kilobyte bodies on rate limits.
func truncateForErr(s string) string {
	const max = 400
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
