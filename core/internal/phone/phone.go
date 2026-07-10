// Package phone is the PHONE substrate: a real phone number (Twilio SIP
// trunk) fronting OpenAI Realtime's native SIP integration, so Jarvis can
// answer the boss's line and place calls on his behalf.
//
//	Inbound:  caller → Twilio trunk → sip:$PROJECT@sip.api.openai.com →
//	          OpenAI fires `realtime.call.incoming` at /webhooks/openai-realtime →
//	          Core accepts with per-call instructions → monitor goroutine
//	          collects the transcript → surface item + push when it ends.
//	Outbound: the `phone_call` tool (Trust-gated via proactive.PhoneGate)
//	          stores a brief in mem_agent_state, then asks Twilio's REST API
//	          to dial the callee and bridge to the OpenAI SIP URI with an
//	          X-Jarvis-Brief header, so the same webhook correlates the leg
//	          back to its brief and builds outbound instructions from it.
//
// Per Rule #1b the personas (the judgment: how to screen, what never to
// share, how to stick to a brief) live in mem_agent_state rows seeded by
// migration 172 — NOT in Go consts. Go only assembles persona + brief facts.
package phone

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/push"
	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog writes to stdout so Railway's log shipper tags these lines
// severity=info. Failures keep using stdlib log (stderr). See CLAUDE.md
// "Logging — severity must match reality".
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

const (
	// defaultModel is the realtime model that runs the call unless
	// INFINITY_PHONE_MODEL overrides it.
	defaultModel = "gpt-realtime-2.1-mini"

	// defaultVoice is the call voice unless INFINITY_PHONE_VOICE overrides.
	defaultVoice = "ash"

	// realtimeCallsURL is the base for accept/reject/hangup on a SIP call.
	realtimeCallsURL = "https://api.openai.com/v1/realtime/calls"

	// realtimeMonitorURL is the WSS endpoint the monitor attaches to
	// (standard realtime events, ?call_id= selects the live call).
	realtimeMonitorURL = "wss://api.openai.com/v1/realtime"

	// briefKeyPrefix namespaces call briefs inside mem_agent_state
	// (generic keyed-state contract from migration 167 — no new table).
	briefKeyPrefix = "phone:brief:"

	// personaKeyPrefix namespaces the base personas seeded by migration 172.
	personaKeyPrefix = "phone:persona:"
)

// Config carries every env the substrate reads. Missing values degrade
// loudly, never silently: inbound 503s without the webhook secret, and
// phone_call errors naming the exact missing envs.
type Config struct {
	OpenAIKey     string // OPENAI_API_KEY - accept/monitor auth
	WebhookSecret string // OPENAI_WEBHOOK_SECRET - Standard Webhooks signing secret (whsec_...)
	ProjectID     string // OPENAI_REALTIME_PROJECT_ID - the proj_... in the SIP URI
	TwilioSID     string // TWILIO_ACCOUNT_SID
	TwilioToken   string // TWILIO_AUTH_TOKEN
	TwilioNumber  string // TWILIO_PHONE_NUMBER - the From for outbound legs
	Voice         string // INFINITY_PHONE_VOICE (default "ash")
	Model         string // INFINITY_PHONE_MODEL (default "gpt-realtime-2.1-mini")
}

// Manager owns the substrate: webhook handling, call accept, transcript
// monitoring, and outcome delivery (surface + push).
type Manager struct {
	pool       *pgxpool.Pool
	cfg        Config
	surface    *surface.Store
	push       *push.Sender
	httpClient *http.Client
	// summarize distills a finished call's transcript into the 1-2 sentence
	// outcome the boss actually wants ("pizza ready in 20 min, 2:50pm").
	// Late-bound in serve.go to the active-model drafter; nil = raw
	// transcript only (fail-open, the record still lands).
	summarize Summarizer
	// liveNotify streams call progress to Studio (the Phone card's live
	// indicator + transcript modal): one event per transcript line, one
	// final done event carrying the outcome summary. Late-bound in
	// serve.go to the WS broadcaster; nil = no live view, the durable
	// surface item still lands.
	liveNotify func(ev LiveEvent)
	// executeAsk runs a passphrase-VERIFIED inbound caller's request as a
	// detached agent turn (the "call Jarvis on the drive home, it's done
	// when you arrive" loop). Wired in serve.go to the main loop — the
	// executor has the boss's full memory; the phone agent never needed it.
	executeAsk func(transcript string)
}

// SetExecuteAsk late-binds the verified-boss execution seam (serve.go).
func (m *Manager) SetExecuteAsk(fn func(transcript string)) {
	if m != nil {
		m.executeAsk = fn
	}
}

// LiveEvent is one streaming update from a call in flight.
type LiveEvent struct {
	CallID    string `json:"call_id"`
	Direction string `json:"direction"`      // inbound | outbound
	Number    string `json:"number"`         // callee (outbound) or caller id (inbound)
	Speaker   string `json:"speaker"`        // Jarvis | Caller | Callee ("" on the done event)
	Text      string `json:"text"`           // transcript line ("" on the done event)
	Done      bool   `json:"done"`           // true exactly once, when the call ends
	Summary   string `json:"summary"`        // outcome digest, only on the done event
}

// SetLiveNotify late-binds the live-call streamer (serve.go).
func (m *Manager) SetLiveNotify(fn func(ev LiveEvent)) {
	if m != nil {
		m.liveNotify = fn
	}
}

func (m *Manager) emitLive(ev LiveEvent) {
	if m != nil && m.liveNotify != nil {
		m.liveNotify(ev)
	}
}

// ── vault ────────────────────────────────────────────────────────────────
// Sensitive call material lives in infinity_meta (set once via Studio
// Settings → Privacy), NEVER in agent-readable state or the memory graph:
//   vault.payment_card     - card details the phone tool attaches to a brief
//                            server-side when include_payment=true; the main
//                            agent's context never contains the number.
//   vault.phone_passphrase - the spoken phrase that verifies the boss on
//                            INBOUND calls; checked in Go (monitor), never
//                            given to the call agent, scrubbed from stored
//                            transcripts.

const (
	vaultCardKey       = "vault.payment_card"
	vaultPassphraseKey = "vault.phone_passphrase"
)

func (m *Manager) metaValue(ctx context.Context, key string) (string, error) {
	if m == nil || m.pool == nil {
		return "", fmt.Errorf("phone: no database pool")
	}
	var v string
	err := m.pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = $1`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(v), nil
}

func (m *Manager) vaultPaymentCard(ctx context.Context) (string, error) {
	card, err := m.metaValue(ctx, vaultCardKey)
	if err != nil || card == "" {
		return "", fmt.Errorf("no payment card is stored - the boss adds one in Settings → Privacy → Phone vault. Place the call without payment (arrange pay-on-arrival) or tell him it's missing")
	}
	// Structured form (Settings stores JSON fields); legacy free-text
	// values pass through untouched.
	var c struct {
		Name   string `json:"name"`
		Number string `json:"number"`
		Exp    string `json:"exp"`
		CVC    string `json:"cvc"`
		Zip    string `json:"zip"`
	}
	if json.Unmarshal([]byte(card), &c) == nil && c.Number != "" {
		out := "Card number " + c.Number
		if c.Exp != "" {
			out += ", expiry " + c.Exp
		}
		if c.CVC != "" {
			out += ", security code " + c.CVC
		}
		if c.Name != "" {
			out += ", name on card " + c.Name
		}
		if c.Zip != "" {
			out += ", billing zip " + c.Zip
		}
		return out, nil
	}
	return card, nil
}

// phonePassphrase returns the boss-verification phrase, or "" when unset.
func (m *Manager) phonePassphrase(ctx context.Context) string {
	v, err := m.metaValue(ctx, vaultPassphraseKey)
	if err != nil {
		return ""
	}
	return v
}

// Summarizer is the LLM seam for post-call outcome digests.
type Summarizer func(ctx context.Context, system, prompt string) (string, error)

// SetSummarizer late-binds the outcome digester (serve.go, once the
// active-model drafter exists).
func (m *Manager) SetSummarizer(fn Summarizer) {
	if m != nil {
		m.summarize = fn
	}
}

// NewManager reads the phone envs and binds the delivery channels. Always
// returns a Manager (nil pool/surface/push are tolerated per-path); use
// Enabled() / MissingOutboundEnvs() to know what actually works.
func NewManager(pool *pgxpool.Pool, surf *surface.Store, sender *push.Sender) *Manager {
	cfg := Config{
		OpenAIKey:     strings.TrimSpace(os.Getenv("OPENAI_API_KEY")),
		WebhookSecret: strings.TrimSpace(os.Getenv("OPENAI_WEBHOOK_SECRET")),
		ProjectID:     strings.TrimSpace(os.Getenv("OPENAI_REALTIME_PROJECT_ID")),
		TwilioSID:     strings.TrimSpace(os.Getenv("TWILIO_ACCOUNT_SID")),
		TwilioToken:   strings.TrimSpace(os.Getenv("TWILIO_AUTH_TOKEN")),
		TwilioNumber:  strings.TrimSpace(os.Getenv("TWILIO_PHONE_NUMBER")),
		Voice:         strings.TrimSpace(os.Getenv("INFINITY_PHONE_VOICE")),
		Model:         strings.TrimSpace(os.Getenv("INFINITY_PHONE_MODEL")),
	}
	if cfg.Voice == "" {
		cfg.Voice = defaultVoice
	}
	if cfg.Model == "" {
		cfg.Model = defaultModel
	}
	return &Manager{
		pool:       pool,
		cfg:        cfg,
		surface:    surf,
		push:       sender,
		httpClient: &http.Client{Timeout: 15 * time.Second},
	}
}

// Enabled reports whether the inbound path can work at all: the webhook
// secret is the minimum (without it every delivery is unverifiable and the
// handler 503s), plus the API key to accept calls.
func (m *Manager) Enabled() bool {
	return m != nil && m.cfg.WebhookSecret != "" && m.cfg.OpenAIKey != ""
}

// MissingOutboundEnvs lists the env vars still needed before phone_call can
// place a call. Empty slice = outbound is fully configured. The tool errors
// with this exact list so the boss knows what to set (fail loud, never a
// silent no-op success).
func (m *Manager) MissingOutboundEnvs() []string {
	var missing []string
	if m == nil {
		return []string{"OPENAI_API_KEY", "OPENAI_REALTIME_PROJECT_ID", "TWILIO_ACCOUNT_SID", "TWILIO_AUTH_TOKEN", "TWILIO_PHONE_NUMBER"}
	}
	if m.cfg.OpenAIKey == "" {
		missing = append(missing, "OPENAI_API_KEY")
	}
	if m.cfg.ProjectID == "" {
		missing = append(missing, "OPENAI_REALTIME_PROJECT_ID")
	}
	if m.cfg.TwilioSID == "" {
		missing = append(missing, "TWILIO_ACCOUNT_SID")
	}
	if m.cfg.TwilioToken == "" {
		missing = append(missing, "TWILIO_AUTH_TOKEN")
	}
	if m.cfg.TwilioNumber == "" {
		missing = append(missing, "TWILIO_PHONE_NUMBER")
	}
	return missing
}

// Brief is the producer-style briefing the agent writes before an outbound
// call: the exact objective, the facts the call agent may share, and what
// it must not commit to. Stored under mem_agent_state key
// "phone:brief:<uuid>" so the incoming-call webhook (which sees only SIP
// headers) can rehydrate it by id.
type Brief struct {
	// Topic is the 3-5 word "point of the call" ("pizza pickup order") -
	// the dashboard card's subtitle.
	Topic string `json:"topic,omitempty"`
	// Name is who/what is being called ("Goodfellas Pizza") - flows into
	// the call history, card titles, and future calls with this number.
	Name string `json:"name,omitempty"`
	// Kind is "person" or "org", set when Jarvis dials, so the contact
	// book shows the right icon.
	Kind        string `json:"kind,omitempty"`
	To          string `json:"to"`
	Goal        string `json:"goal"`
	Constraints string `json:"constraints,omitempty"`
	CreatedAt   string `json:"created_at"`
}

// storeBrief persists a brief under its keyed-state cell.
func (m *Manager) storeBrief(ctx context.Context, id string, b *Brief) error {
	if m.pool == nil {
		return fmt.Errorf("phone: no database pool; cannot store call brief")
	}
	raw, err := json.Marshal(b)
	if err != nil {
		return fmt.Errorf("phone: marshal brief: %w", err)
	}
	_, err = m.pool.Exec(ctx, `
		INSERT INTO mem_agent_state (key, value, note, updated_at)
		VALUES ($1, $2::jsonb, $3, NOW())
		ON CONFLICT (key) DO UPDATE
		SET value = EXCLUDED.value, note = EXCLUDED.note, updated_at = NOW()
	`, briefKeyPrefix+id, string(raw), "outbound call brief for "+b.To)
	return err
}

// loadBrief rehydrates the brief the X-Jarvis-Brief SIP header points at.
func (m *Manager) loadBrief(ctx context.Context, id string) (*Brief, error) {
	if m.pool == nil {
		return nil, fmt.Errorf("phone: no database pool; cannot load call brief")
	}
	var raw string
	err := m.pool.QueryRow(ctx, `
		SELECT value::text FROM mem_agent_state WHERE key = $1
	`, briefKeyPrefix+id).Scan(&raw)
	if err != nil {
		return nil, fmt.Errorf("phone: load brief %q: %w", id, err)
	}
	var b Brief
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return nil, fmt.Errorf("phone: brief %q is not valid JSON: %w", id, err)
	}
	return &b, nil
}

// loadPersona reads the base persona for a call direction ("inbound" |
// "outbound") from mem_agent_state. The value is a JSON string seeded by
// migration 172 — the judgment lives in data, never in a Go const, so the
// boss (and Voyager) can evolve how Jarvis handles his line without a
// deploy. A missing row is a hard error: Go must not invent a persona.
func (m *Manager) loadPersona(ctx context.Context, direction string) (string, error) {
	if m.pool == nil {
		return "", fmt.Errorf("phone: no database pool; cannot load persona")
	}
	var raw string
	err := m.pool.QueryRow(ctx, `
		SELECT value::text FROM mem_agent_state WHERE key = $1
	`, personaKeyPrefix+direction).Scan(&raw)
	if err != nil {
		return "", fmt.Errorf("phone: persona %q missing (run migration 172): %w", direction, err)
	}
	var persona string
	if err := json.Unmarshal([]byte(raw), &persona); err != nil {
		return "", fmt.Errorf("phone: persona %q is not a JSON string: %w", direction, err)
	}
	if strings.TrimSpace(persona) == "" {
		return "", fmt.Errorf("phone: persona %q is empty (reseed migration 172)", direction)
	}
	return persona, nil
}

// buildInstructions assembles the per-call system instructions: the stored
// persona plus deterministic facts (the brief for outbound, the caller id
// for inbound, and the caller's recognized identity when their number is in
// phone:known_numbers). Assembly only — zero judgment text originates here;
// even the "how to treat this caller" line is the VALUE stored against
// their number.
func buildInstructions(persona string, brief *Brief, callerID, callerNote string) string {
	var sb strings.Builder
	sb.WriteString(strings.TrimSpace(persona))
	if brief != nil {
		sb.WriteString("\n\n## The brief for this call\n")
		sb.WriteString("- Calling: " + brief.To + "\n")
		sb.WriteString("- Goal: " + brief.Goal + "\n")
		if strings.TrimSpace(brief.Constraints) != "" {
			sb.WriteString("- Constraints: " + brief.Constraints + "\n")
		}
	}
	if callerID != "" {
		sb.WriteString("\n\nCaller ID: " + callerID)
	}
	if callerNote != "" {
		sb.WriteString("\n\n## You KNOW this caller\n" + callerNote)
	}
	return sb.String()
}

// lookupCaller matches the SIP From header against the boss-editable
// phone:known_numbers map in mem_agent_state (JSON object: E.164-ish number
// → a note describing who it is and how to treat them). Matching is on the
// last 10 digits so "sip:+16095550123@x", "+1 (609) 555-0123", and
// "6095550123" all hit the same entry. Empty note = unknown caller (the
// screening persona handles them).
func (m *Manager) lookupCaller(ctx context.Context, callerID string) string {
	digits := lastDigits(callerID, 10)
	if m.pool == nil || digits == "" {
		return ""
	}
	var raw string
	err := m.pool.QueryRow(ctx, `
		SELECT value::text FROM mem_agent_state WHERE key = 'phone:known_numbers'
	`).Scan(&raw)
	if err != nil {
		return "" // no list yet - every caller is a stranger
	}
	known := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &known); err != nil {
		log.Printf("phone: phone:known_numbers is not a JSON object: %v", err)
		return ""
	}
	for number, note := range known {
		if lastDigits(number, 10) == digits {
			return strings.TrimSpace(note)
		}
	}
	return ""
}

// lastDigits extracts the trailing n digits of any phone-ish string.
func lastDigits(s string, n int) string {
	var b []rune
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b = append(b, r)
		}
	}
	if len(b) < n {
		if len(b) == 0 {
			return ""
		}
		return string(b)
	}
	return string(b[len(b)-n:])
}

// acceptCall POSTs the accept endpoint with the realtime session config so
// OpenAI answers the SIP leg and runs the call on our instructions. Voice
// rides the GA session shape (audio.output.voice).
func (m *Manager) acceptCall(ctx context.Context, callID, instructions string) error {
	body, err := json.Marshal(map[string]any{
		"type":         "realtime",
		"model":        m.cfg.Model,
		"instructions": instructions,
		// hangup_call gives the call agent a way to END the call - without
		// it he waits politely forever after goodbyes and the other side
		// has to hang up on him. WHEN to use it is persona judgment (data);
		// the monitor watches for the function call and drops the line.
		"tools": []map[string]any{{
			"type":        "function",
			"name":        "hangup_call",
			"description": "End the phone call. Call this after goodbyes are exchanged or when the conversation is clearly over.",
			"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
		}},
		"audio": map[string]any{
			// Input transcription is OFF by default on realtime sessions —
			// without asking for it the caller's words never appear in the
			// transcript (only Jarvis's own lines, which are free). This is
			// what makes the call log a real two-sided record.
			"input": map[string]any{
				"transcription": map[string]any{"model": "gpt-4o-mini-transcribe"},
			},
			"output": map[string]any{"voice": m.cfg.Voice},
		},
	})
	if err != nil {
		return fmt.Errorf("phone: marshal accept: %w", err)
	}
	return m.callControl(ctx, callID, "accept", body)
}

// rejectCall declines an incoming call — used when the substrate cannot
// run it honestly (e.g. persona rows missing). Better a fast busy signal
// than a call answered by an unbriefed model.
func (m *Manager) rejectCall(ctx context.Context, callID string) error {
	return m.callControl(ctx, callID, "reject", nil)
}

// hangupCall ends a live call — the agent side of "bye". Triggered by the
// call agent's hangup_call function call (see acceptCall tools).
func (m *Manager) hangupCall(ctx context.Context, callID string) error {
	return m.callControl(ctx, callID, "hangup", nil)
}

// callControl is the shared POST for accept/reject/hangup.
func (m *Manager) callControl(ctx context.Context, callID, verb string, body []byte) error {
	url := realtimeCallsURL + "/" + callID + "/" + verb
	var rdr io.Reader
	if len(body) > 0 {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, rdr)
	if err != nil {
		return fmt.Errorf("phone: build %s request: %w", verb, err)
	}
	req.Header.Set("Authorization", "Bearer "+m.cfg.OpenAIKey)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("phone: %s call %s: %w", verb, callID, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("phone: %s call %s: HTTP %d: %s", verb, callID, resp.StatusCode, clip(string(raw), 400))
	}
	return nil
}

// clip truncates s to max bytes with an ellipsis, for error messages and
// surface bodies.
func clip(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
