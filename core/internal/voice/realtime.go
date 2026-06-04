// Package voice integrates OpenAI's Realtime API for low-latency, full-duplex
// speech. Audio never touches our server - the browser does WebRTC directly
// to OpenAI, authenticated by a short-lived `client_secret` that this package
// mints from the boss's OPENAI_API_KEY. Tool calls round-trip through Core
// over the existing WebSocket so voice has the same tool surface (and the
// same Trust gate) as text mode.
//
// Sequence:
//
//  1. Browser taps mic → POST /api/voice/session { session_id }.
//  2. Core builds Session config (voice, instructions = soul + memory + skills
//     + British-RP accent line, tools = full registry schemas) and POSTs it
//     to /v1/realtime/client_secrets with OPENAI_API_KEY.
//  3. Core returns the ephemeral key to the browser.
//  4. Browser does the WebRTC SDP exchange directly with api.openai.com
//     using that ephemeral key. From then on, audio flows P2P-style.
//  5. Tool calls fire on the data channel; the browser forwards them to
//     POST /api/voice/tool which runs the same gate + registry as the text
//     loop, then submits the result back as a function_call_output item.
//  6. Each finalized user/assistant utterance posts to /api/voice/turn
//     which fires UserPromptSubmit + TaskCompleted hooks. Memory capture
//     + Sessions tab work identically to text mode.
package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
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

	// The provider rejects oversized instructions. Voice must stay far
	// below that hard ceiling because the browser's realtime session is
	// rebuilt from a huge text-mode prompt (memory + skills + dormant tool
	// catalog + connected-account overlay). Keep a conservative headroom so
	// small prompt growth doesn't push us over again.
	realtimeInstructionHardLimit = 16384
	voiceInstructionTarget       = 12000

	// voiceIdentityAccessBlock is deliberately separate from the source
	// prompt so budget trimming can never drop it. It corrects a voice
	// failure mode where the realtime model acted like an external helper
	// and told Kai to ask a developer instead of treating Infinity code
	// issues as work Jarvis can inspect and fix through tools.
	voiceIdentityAccessBlock = "VOICE MODE IDENTITY AND ACCESS - highest priority: Your name is Jarvis. Your boss is Kai. Infinity is the platform and codebase you run inside. You are Kai's developer and operator for Infinity. For fixable product issues in Infinity code, never deflect with 'ask the developers', 'contact support', or 'I cannot modify the system'. You can inspect, change, test, commit, deploy, or kick off code work through the available tools. If work is heavy or multi-step, use background_build with a complete task brief, then tell Kai you kicked it off. If a request is outside Infinity or needs unavailable credentials, say the real blocker plainly."

	// britishAccentLine is appended to the realtime instructions so the
	// voice renderer has explicit delivery guidance, not just persona.
	britishAccentLine = "Voice delivery: use a clearly British Received Pronunciation accent. Prefer non-rhotic British vowels, crisp consonants, measured pacing, and warm Jarvis-style restraint. Do not use American pronunciation. Do not use em dash or en dash characters in spoken transcripts."

	// voiceDispatchLine keeps the EXPENSIVE realtime model out of heavy
	// engineering work. Voice (this realtime session) is metered per audio
	// token; the boss's settings model runs on a flat subscription. So any
	// real build should be handed to `background_build`, which runs the
	// full coding agent on the settings model in the background and pings
	// the boss when done - rather than the realtime model orchestrating a
	// build turn-by-turn (every tool result re-billed through realtime).
	voiceDispatchLine = "VOICE MODE - cost discipline: you are the realtime voice model and you are metered. Voice is for light, conversational work: questions, calendar, email, quick lookups, status, and KICKING THINGS OFF. For any heavy or multi-step engineering work - writing or editing code, building a feature, refactoring, a build/test cycle, or anything that would take many tool steps - DO NOT do it yourself turn-by-turn. Call the `background_build` tool once with a complete, self-contained task description. It runs the full coding agent on the boss's main (subscription) model in the background, returns immediately, and notifies the boss when it's done so he can hang up. After calling it, briefly confirm out loud that you've kicked it off and will report back. Only perform small, quick tool actions inline."

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
	// Tools is the per-session active-set's schema list - same lazy-load
	// discipline as text mode. The handler in voice_api.go resolves
	// `sess.Active.Names()` and passes the matching ToolDefs here. The
	// dormant long tail lives in the system-prompt catalog block; when
	// the model calls tool_search → load_tools mid-conversation, the
	// browser mirrors the updated active set into the live realtime
	// session via session.update (no re-mint required).
	Tools []llm.ToolDef
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

	instructions, err := buildVoiceInstructions(req.SystemPrompt)
	if err != nil {
		return nil, err
	}

	// Newer Realtime API (gpt-realtime family) nests audio config under
	// `audio.input` and `audio.output`. The old flat keys
	// (input_audio_transcription, turn_detection, voice at top level)
	// return 400 "unknown_parameter". Mirror the supported shape so the
	// mint succeeds and the browser can still drive barge-in + captions.
	// Per the GA docs: `output_modalities` defaults to ["audio"] for a
	// realtime session, but make it explicit. Some surfaces have been
	// observed routing to text-only when this is omitted alongside a
	// configured `audio.output.voice` - better to be explicit than to
	// rely on a default that's been shifting between revisions.
	session := map[string]any{
		"type":              "realtime",
		"model":             m.model,
		"instructions":      instructions,
		"tool_choice":       "auto",
		"output_modalities": []string{"audio"},
		"audio": map[string]any{
			"input": map[string]any{
				// Noise reduction runs before VAD/turn detection. Near-field
				// matches the browser mic use case and reduces false
				// speech_started events from speaker echo.
				"noise_reduction": map[string]any{
					"type": "near_field",
				},
				// Server-side VAD drives barge-in. `create_response: true`
				// makes the model speak back automatically after the user
				// stops, and `interrupt_response: true` makes a real user
				// barge-in cancel the current response at the provider.
				// Studio commits transcripts from output transcript events,
				// not from speech_started, so a false VAD start does not
				// split Jarvis into phantom bubbles.
				"turn_detection": map[string]any{
					"type":                "server_vad",
					"create_response":     true,
					"interrupt_response":  true,
					"threshold":           0.65,
					"prefix_padding_ms":   300,
					"silence_duration_ms": 700,
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

func buildVoiceInstructions(systemPrompt string) (string, error) {
	base := []string{compactVoiceCoreInstructions(), voiceIdentityAccessBlock, britishAccentLine, voiceDispatchLine}
	available := voiceInstructionTarget - estimateTokens(strings.Join(base, "\n\n"))
	if available < 0 {
		available = 0
	}

	sections := splitPromptSections(systemPrompt)
	prefix := compactVoiceRelevantSections(sections, available)

	parts := make([]string, 0, 1+len(base))
	if prefix != "" {
		parts = append(parts, prefix)
	}
	parts = append(parts, base...)
	instructions := strings.TrimSpace(strings.Join(parts, "\n\n"))
	if estimateTokens(instructions) > realtimeInstructionHardLimit {
		return "", fmt.Errorf("voice instructions still exceed provider limit after compaction: est=%d limit=%d", estimateTokens(instructions), realtimeInstructionHardLimit)
	}
	return instructions, nil
}

func compactVoiceCoreInstructions() string {
	return strings.TrimSpace(`You are Jarvis in Infinity voice mode.

Address the boss as "boss". Be concise, useful, and conversational. Prefer short spoken sentences and natural punctuation. Do not use em dash or en dash characters.

Use tools when they move the work forward. Do not claim you did something you did not actually do. If a tool result matters, state the outcome plainly.`)
}

func compactVoiceRelevantSections(sections []string, budget int) string {
	if budget <= 0 || len(sections) == 0 {
		return ""
	}
	type scoredSection struct {
		text     string
		estimate int
		score    int
		idx      int
	}
	picked := make([]scoredSection, 0, len(sections))
	for i, section := range sections {
		trimmed := strings.TrimSpace(section)
		if trimmed == "" {
			continue
		}
		est := estimateTokens(trimmed)
		if est == 0 {
			continue
		}
		picked = append(picked, scoredSection{text: trimmed, estimate: est, score: scoreVoiceSection(trimmed), idx: i})
	}
	if len(picked) == 0 {
		return ""
	}
	sort.SliceStable(picked, func(i, j int) bool {
		if picked[i].score == picked[j].score {
			return picked[i].idx < picked[j].idx
		}
		return picked[i].score > picked[j].score
	})

	chosen := make([]scoredSection, 0, len(picked))
	used := 0
	for _, section := range picked {
		if used >= budget {
			break
		}
		if used > 0 && used+section.estimate > budget {
			continue
		}
		if used == 0 && section.estimate > budget {
			section.text = truncateToTokenBudget(section.text, budget)
			section.estimate = estimateTokens(section.text)
			if section.text == "" {
				continue
			}
		}
		chosen = append(chosen, section)
		used += section.estimate
	}
	if len(chosen) == 0 {
		return ""
	}
	sort.SliceStable(chosen, func(i, j int) bool { return chosen[i].idx < chosen[j].idx })
	parts := make([]string, 0, len(chosen))
	for _, section := range chosen {
		parts = append(parts, section.text)
	}
	return strings.Join(parts, "\n\n")
}

func splitPromptSections(prompt string) []string {
	normalized := strings.ReplaceAll(prompt, "\r\n", "\n")
	return strings.Split(normalized, "\n\n")
}

func scoreVoiceSection(section string) int {
	lower := strings.ToLower(section)
	score := 1
	switch {
	case strings.Contains(lower, "you are jarvis"):
		score += 120
	case strings.Contains(lower, "about the boss") || strings.Contains(lower, "boss"):
		score += 70
	case strings.Contains(lower, "procedural skills") || strings.Contains(lower, "approved finding fallback"):
		score += 55
	case strings.Contains(lower, "relevant memory") || strings.Contains(lower, "memory"):
		score += 45
	case strings.Contains(lower, "current_time") || strings.Contains(lower, "timezone"):
		score += 35
	case strings.Contains(lower, "active_project") || strings.Contains(lower, "bridge"):
		score += 25
	case strings.Contains(lower, "tool_catalog") || strings.Contains(lower, "connected_accounts"):
		score += 5
	}
	if strings.Contains(lower, "voice") {
		score += 15
	}
	if strings.Contains(lower, "important") || strings.Contains(lower, "must") {
		score += 8
	}
	return score
}

func truncateToTokenBudget(text string, budget int) string {
	if budget <= 0 {
		return ""
	}
	trimmed := strings.TrimSpace(text)
	if estimateTokens(trimmed) <= budget {
		return trimmed
	}
	maxChars := budget * 4
	if maxChars <= 1 {
		return ""
	}
	if maxChars > len(trimmed) {
		maxChars = len(trimmed)
	}
	candidate := strings.TrimSpace(trimmed[:maxChars])
	if lastBreak := strings.LastIndexAny(candidate, " \n\t.,;:)"); lastBreak > maxChars/2 {
		candidate = strings.TrimSpace(candidate[:lastBreak])
	}
	if candidate == "" {
		return ""
	}
	if estimateTokens(candidate) > budget {
		for estimateTokens(candidate) > budget && len(candidate) > 1 {
			candidate = strings.TrimSpace(candidate[:len(candidate)-1])
		}
	}
	return candidate
}

func estimateTokens(s string) int {
	return (len(s) + 3) / 4
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
