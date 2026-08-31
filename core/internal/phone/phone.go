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
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/contacts"
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
	PublicBaseURL string // INFINITY_PUBLIC_URL - Core's public origin, for Twilio status callbacks
}

// Manager owns the substrate: webhook handling, call accept, transcript
// monitoring, and outcome delivery (surface + push).
type Manager struct {
	pool       *pgxpool.Pool
	cfg        Config
	surface    *surface.Store
	push       *push.Sender
	httpClient *http.Client
	// book is the phone book (mem_contacts). It turns "call Ariana" into a
	// number on the way out, and a ringing number into "Ariana, his wife" on
	// the way in. Nil = no book; the number-only paths still work.
	book *contacts.Store
	// secrets is the encrypted store the vault.* keys moved into. Nil falls
	// back to infinity_meta, which is what keeps a not-yet-migrated
	// deployment working rather than silently losing the card mid-errand.
	secrets SecretReader
	// lookup answers the live call agent's find_contact function: the phone
	// book first, then the web for a business he has never called. Wired in
	// serve.go (it needs the tool registry); nil = the agent is told the book
	// is unavailable rather than being left to invent a number.
	lookup ContactLookup
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
	// errand executes a passphrase-VERIFIED boss's spoken instruction as a full
	// agent turn (the "call Jarvis on the drive home, it's done when you arrive"
	// loop) and hands back what Jarvis finally said, which becomes the report.
	// See errand.go: the REPORT is a mechanic, not a prompt sentence.
	errand ErrandRunner
	// followupCreator persists a promised action from a finished call as a
	// task/follow-up (Rule #1b: Go always persists what the summarizer flags).
	followupCreator func(ctx context.Context, title, body string)

	// capture records a finished call into the boss's memory (see memory.go).
	// Without it, an entire channel bypasses the memory-first invariant and the
	// Jarvis he chats to never learns that his calls happened at all.
	capture CaptureFunc

	// liveMu guards liveCalls: the sockets of calls in flight, keyed by brief
	// id. Twilio's answering-machine detection lands on a webhook, in a
	// different goroutine from the monitor, and has to be able to speak into the
	// call that is HAPPENING RIGHT NOW ("a machine answered, deliver the
	// message"). Registered when the monitor attaches, dropped when it exits.
	liveMu    sync.Mutex
	liveCalls map[string]*safeConn
}

// registerLive binds a live call's socket to its brief, so out-of-band signals
// (answering-machine detection) can reach the call while it is still up.
func (m *Manager) registerLive(briefID string, sc *safeConn) {
	if m == nil || briefID == "" || sc == nil {
		return
	}
	m.liveMu.Lock()
	if m.liveCalls == nil {
		m.liveCalls = map[string]*safeConn{}
	}
	m.liveCalls[briefID] = sc
	m.liveMu.Unlock()
}

func (m *Manager) unregisterLive(briefID string) {
	if m == nil || briefID == "" {
		return
	}
	m.liveMu.Lock()
	delete(m.liveCalls, briefID)
	m.liveMu.Unlock()
}

func (m *Manager) liveCall(briefID string) *safeConn {
	if m == nil || briefID == "" {
		return nil
	}
	m.liveMu.Lock()
	defer m.liveMu.Unlock()
	return m.liveCalls[briefID]
}

// SetFollowupCreator late-binds the follow-through seam (serve.go).
func (m *Manager) SetFollowupCreator(fn func(ctx context.Context, title, body string)) {
	if m != nil {
		m.followupCreator = fn
	}
}

// LiveEvent is one streaming update from a call in flight.
type LiveEvent struct {
	CallID    string `json:"call_id"`
	Direction string `json:"direction"` // inbound | outbound
	Number    string `json:"number"`    // callee (outbound) or caller id (inbound)
	Name      string `json:"name"`      // who that is, per the phone book ("Ariana"), when we know
	Speaker   string `json:"speaker"`   // Jarvis | Caller | Callee ("" on the done event)
	Text      string `json:"text"`      // transcript line ("" on the done event)
	Done      bool   `json:"done"`      // true exactly once, when the call ends
	Summary   string `json:"summary"`   // outcome digest, only on the done event
	Status    string `json:"status"`    // terminal disposition: no-answer|busy|failed|canceled|completed ("" mid-call)
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
// Sensitive call material lives sealed in the vault (set once via Studio
// Settings → Vault), NEVER in agent-readable state or the memory graph:
//   vault.payment_card     - LEGACY. Cards now live in the sealed wallet and
//                            the phone reads the same list, so there is one
//                            place to manage a card rather than two. This key
//                            is the fallback for a boss who has not added a
//                            wallet card yet.
//   vault.phone_passphrase - the spoken phrase that verifies the boss on
//                            INBOUND calls; checked in Go (monitor), never
//                            given to the call agent, scrubbed from stored
//                            transcripts.

const (
	vaultCardKey       = "vault.payment_card"
	vaultPassphraseKey = "vault.phone_passphrase"
	vaultIdentityKey   = "vault.identity"
	vaultBossCellKey   = "vault.boss_cell"
)

// SecretReader is the sealed store the vault keys moved into. Late-bound from
// serve.go so this package keeps no build-time dependency on the vault.
type SecretReader interface {
	Secret(ctx context.Context, key string) (string, error)
	// Releasable returns the boss's personal details that he has marked as
	// things Jarvis may hand over. Anything switched off is never loaded, so
	// the call brief cannot contain it to begin with.
	Releasable(ctx context.Context) (map[string]string, error)
	// Detail reads one detail regardless of its switch, for the spoken
	// password: a phrase Jarvis compares against a caller, never one he says.
	Detail(ctx context.Context, key string) (string, error)
	// PhoneCard resolves the card a call brief should read out. It prefers
	// the boss's wallet, so there is ONE list of cards rather than one for
	// buying and a separate one for calling.
	PhoneCard(ctx context.Context) (string, error)
}

// SetSecretReader points the vault lookups at the encrypted store.
func (m *Manager) SetSecretReader(r SecretReader) {
	if m != nil {
		m.secrets = r
	}
}

// metaValue is the ONE read path for every vault.* key, which is why moving
// these secrets out of plaintext was a change to this function rather than a
// change to every caller.
//
// The sealed store is tried first and infinity_meta remains the fallback, so a
// deployment where the key is not set yet keeps working exactly as before
// instead of silently losing the boss's card mid-errand. Once migrated the
// meta row holds a sentinel, which reads as absent.
func (m *Manager) metaValue(ctx context.Context, key string) (string, error) {
	if m == nil || m.pool == nil {
		return "", fmt.Errorf("phone: no database pool")
	}
	if m.secrets != nil {
		if v, err := m.secrets.Secret(ctx, key); err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v), nil
		}
	}
	var v string
	err := m.pool.QueryRow(ctx, `SELECT value FROM infinity_meta WHERE key = $1`, key).Scan(&v)
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(v)
	if v == "moved:vault" {
		return "", fmt.Errorf("phone: %s lives in the encrypted vault and I cannot open it (INFINITY_VAULT_KEY is not set on core)", key)
	}
	return v, nil
}

func (m *Manager) vaultPaymentCard(ctx context.Context) (string, error) {
	// The wallet first, then the legacy sealed blob, then plaintext meta for
	// a deployment with no vault key. Same card either way from his side.
	var card string
	if m.secrets != nil {
		if v, err := m.secrets.PhoneCard(ctx); err == nil {
			card = strings.TrimSpace(v)
		}
	}
	if card == "" {
		v, err := m.metaValue(ctx, vaultCardKey)
		if err != nil {
			return "", fmt.Errorf("no card is saved - the boss adds one in Settings, Vault, Cards. Place the call without payment (arrange pay-on-arrival) or tell him it's missing")
		}
		card = v
	}
	if card == "" {
		return "", fmt.Errorf("no card is saved - the boss adds one in Settings, Vault, Cards. Place the call without payment (arrange pay-on-arrival) or tell him it's missing")
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

// vaultIdentity formats the boss's stored identity details for a call that
// needs them (verifying with a bank/utility). Never in the agent's context,
// only released server-side into the brief, same guarantee as the card.
func (m *Manager) vaultIdentity(ctx context.Context) (string, error) {
	// The per-detail switches in Settings, Vault, Personal info decide what
	// goes into a brief. This is the enforcement point, and it is a filter on
	// what gets LOADED rather than an instruction to the call agent, so a
	// withheld detail is not something it can be talked into saying.
	if m.secrets != nil {
		if rel, err := m.secrets.Releasable(ctx); err == nil {
			var parts []string
			if v := rel["dob"]; v != "" {
				parts = append(parts, "date of birth "+v)
			}
			if v := rel["account_number"]; v != "" {
				parts = append(parts, "account number "+v)
			}
			if v := rel["ssn_last4"]; v != "" {
				parts = append(parts, "last four "+v)
			}
			name := strings.TrimSpace(rel["given_name"] + " " + rel["family_name"])
			if name != "" {
				parts = append(parts, "full name "+name)
			}
			if v := rel["email"]; v != "" {
				parts = append(parts, "email "+v)
			}
			addr := joinAddress(rel, "ship_")
			if addr == "" {
				addr = joinAddress(rel, "bill_")
			}
			if addr != "" {
				parts = append(parts, "address "+addr)
			}
			if v := rel["bill_postal"]; v != "" && addr == "" {
				parts = append(parts, "billing zip "+v)
			}
			if len(parts) > 0 {
				return strings.Join(parts, ", "), nil
			}
		}
	}
	raw, err := m.metaValue(ctx, vaultIdentityKey)
	if err != nil || raw == "" {
		return "", fmt.Errorf("no personal details are saved - the boss adds them in Settings, Vault, Personal info. Proceed without them or tell him they're missing")
	}
	var id struct {
		DOB     string `json:"dob"`
		Account string `json:"account"`
		Last4   string `json:"last4"`
		Zip     string `json:"zip"`
	}
	if json.Unmarshal([]byte(raw), &id) == nil && (id.DOB != "" || id.Account != "" || id.Last4 != "" || id.Zip != "") {
		var parts []string
		if id.DOB != "" {
			parts = append(parts, "date of birth "+id.DOB)
		}
		if id.Account != "" {
			parts = append(parts, "account number "+id.Account)
		}
		if id.Last4 != "" {
			parts = append(parts, "last four "+id.Last4)
		}
		if id.Zip != "" {
			parts = append(parts, "billing zip "+id.Zip)
		}
		return strings.Join(parts, ", "), nil
	}
	return raw, nil
}

// vaultBossCell returns the boss's cell (E.164) for patch-in / callback, or "".
// joinAddress renders one address from the detail map, or "" when he has not
// given one or has switched it off.
func joinAddress(rel map[string]string, prefix string) string {
	var parts []string
	for _, f := range []string{"line1", "line2", "city", "state", "postal", "country"} {
		if v := strings.TrimSpace(rel[prefix+f]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, ", ")
}

func (m *Manager) vaultBossCell(ctx context.Context) string {
	// His phone number is a detail he maintains in one place now. The old meta
	// key stays as the fallback so patch-in never stops working mid-migration.
	if m.secrets != nil {
		if rel, err := m.secrets.Releasable(ctx); err == nil {
			if v := strings.TrimSpace(rel["phone"]); v != "" {
				return v
			}
		}
	}
	v, err := m.metaValue(ctx, vaultBossCellKey)
	if err != nil {
		return ""
	}
	return v
}

// referToBoss hands the live call to the boss's cell (OpenAI blind transfer):
// the AI leg drops and the caller is connected to Mr. Kai directly. Used by
// the patch_in_boss function tool.
func (m *Manager) referToBoss(ctx context.Context, callID string) error {
	cell := m.vaultBossCell(ctx)
	if cell == "" {
		return fmt.Errorf("no cell number is saved for him (Settings, Vault, Personal info)")
	}
	body, _ := json.Marshal(map[string]any{"target_uri": "tel:" + cell})
	return m.callControl(ctx, callID, "refer", body)
}

// phonePassphrase returns the boss-verification phrase, or "" when unset.
// phonePassphrase reads the phrase that verifies the BOSS. It deliberately
// does NOT go through Releasable: this phrase is an input Jarvis checks, never
// an output he offers, and the catalog marks it unreleasable for that reason.
func (m *Manager) phonePassphrase(ctx context.Context) string {
	if m.secrets != nil {
		if v, err := m.secrets.Detail(ctx, "passphrase"); err == nil && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	v, err := m.metaValue(ctx, vaultPassphraseKey)
	if err != nil {
		return ""
	}
	return v
}

// ContactLookup resolves a name the caller SAID ("Ariana", "Goodfellas Pizza")
// into something the agent can read back to him: a phone-book hit, or, for a
// place he has never called, what the web says. Returns plain prose because the
// call agent speaks it aloud.
type ContactLookup func(ctx context.Context, query string) (string, error)

// SetContacts gives the manager the phone book.
func (m *Manager) SetContacts(s *contacts.Store) {
	if m != nil {
		m.book = s
	}
}

// SetContactLookup late-binds the live find_contact resolver (serve.go).
func (m *Manager) SetContactLookup(fn ContactLookup) {
	if m != nil {
		m.lookup = fn
	}
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
		PublicBaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("INFINITY_PUBLIC_URL")), "/"),
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

// loadBriefRaw returns the stored brief JSON string (for retry re-scheduling).
func (m *Manager) loadBriefRaw(ctx context.Context, id string) (string, error) {
	if m.pool == nil {
		return "", fmt.Errorf("phone: no database pool")
	}
	var raw string
	err := m.pool.QueryRow(ctx, `SELECT value::text FROM mem_agent_state WHERE key = $1`, briefKeyPrefix+id).Scan(&raw)
	return raw, err
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
		return "", fmt.Errorf("phone: persona %q missing (run migrations 172 and 175): %w", direction, err)
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
	// Two sources, BOTH read, never one instead of the other.
	//
	// The phone book says WHO is ringing (anyone the boss saved or called is a
	// known person, not a stranger to be screened), and phone:known_numbers
	// carries the boss's own standing instructions for how to treat particular
	// callers (his own entry is what tells Jarvis to ask him for the passphrase).
	// Returning early on the first hit would have silently dropped the second,
	// which for his own number means dropping the passphrase ritual entirely.
	var parts []string
	if m.book != nil {
		if c, err := m.book.ByNumber(ctx, callerID); err == nil && c != nil {
			who := c.Name
			if c.Kind == "org" {
				who += " (a business the boss deals with)"
			}
			if c.Location != "" {
				who += ", " + c.Location
			}
			if c.Note != "" {
				who += ". " + c.Note
			}
			parts = append(parts, strings.TrimSpace(who))
		}
	}
	var raw string
	err := m.pool.QueryRow(ctx, `
		SELECT value::text FROM mem_agent_state WHERE key = 'phone:known_numbers'
	`).Scan(&raw)
	if err != nil {
		return strings.Join(parts, "\n\n") // no list yet: the book's word is all we have
	}
	known := map[string]string{}
	if err := json.Unmarshal([]byte(raw), &known); err != nil {
		log.Printf("phone: phone:known_numbers is not a JSON object: %v", err)
		return strings.Join(parts, "\n\n")
	}
	for number, note := range known {
		if lastDigits(number, 10) == digits {
			parts = append(parts, strings.TrimSpace(note))
			break
		}
	}
	return strings.Join(parts, "\n\n")
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
		"tools": []map[string]any{
			{
				"type":        "function",
				"name":        "hangup_call",
				"description": "End the phone call. Call this after goodbyes are exchanged or when the conversation is clearly over.",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
			{
				"type":        "function",
				"name":        "patch_in_boss",
				"description": "Connect the caller directly to Mr. Kai on his own phone when they genuinely need to speak with him personally. This hands the call to him and ends your part. Use sparingly, only when relaying a message will not do.",
				"parameters":  map[string]any{"type": "object", "properties": map[string]any{}},
			},
			// find_contact is what lets Jarvis confirm WHO he is about to call
			// while the boss is still on the line: "Ariana, on 929-310-0906?" or
			// "Goodfellas Pizza, the one on Preston Road?". It reads the phone
			// book, and searches the web for a place the boss has never called.
			// Without it he would agree to an errand and only discover after
			// hanging up that he has no number, which is exactly the kind of
			// dead end this system is not allowed to have.
			{
				"type": "function",
				"name": "find_contact",
				"description": "Look up someone the caller named, to confirm you have the right person or place BEFORE agreeing to the errand. " +
					"Use it whenever Mr. Kai names anyone to contact. It searches his phone book first, then the web for a business he has not called before. " +
					"If it comes back with more than one match, ask him which he means (\"the one on Preston Road?\"). Whoever you agree on is saved to his phone book automatically.",
				"parameters": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The name he said: \"Ariana\", \"my wife\", \"Goodfellas Pizza\".",
						},
					},
					"required": []any{"query"},
				},
			},
		},
		"audio": map[string]any{
			// Input transcription is OFF by default on realtime sessions —
			// without asking for it the caller's words never appear in the
			// transcript (only Jarvis's own lines, which are free). This is
			// what makes the call log a real two-sided record.
			//
			// language is NOT optional in practice. Without it the transcriber
			// detects the language per utterance, and a two-word reply over an
			// 8kHz phone codec gives it almost nothing to go on: on Jul 13 the
			// boss said "blue falcon" in English and it came back as
			// "ブルーファルコン" — the right sounds, transcribed into the wrong
			// alphabet. The passphrase check saw non-Latin text, matched
			// nothing, and refused him on his own line.
			"input": map[string]any{
				"transcription": map[string]any{
					"model":    "gpt-4o-mini-transcribe",
					"language": "en",
				},
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
