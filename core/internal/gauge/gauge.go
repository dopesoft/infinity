// Package gauge is up-front EFFORT SIZING of a task.
//
// Before doing the work, it helps to know how much work it deserves. The Gauge
// is a cheap classifier (same shape as intent.Detector) that reads each
// substantive turn and sizes it — glance / standard / deep. It runs ASYNC, off
// the chat hot path (the boss is latency-sensitive), so turn-1 sizing is
// advisory + observable; a session-scoped Provider folds the read back into
// multi-turn work so longer tasks calibrate (deep → "open a Mandate"). The
// Gauge never hard-caps cognition — it's a hint and a signal, not a gate.
package gauge

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Tier is how much cognition a task warrants.
type Tier string

const (
	TierGlance   Tier = "glance"   // a fact, a quick edit — answer and move on
	TierStandard Tier = "standard" // ordinary task — normal care
	TierDeep     Tier = "deep"     // real work — plan, verify, likely open a Mandate
)

func (t Tier) Valid() bool {
	switch t {
	case TierGlance, TierStandard, TierDeep:
		return true
	}
	return false
}

// Read is the classifier's output for one turn.
type Read struct {
	Tier   Tier   `json:"tier"`
	Reason string `json:"reason"`
}

// Detector sizes a turn via a cheap model call (the active brain, empty model
// id → provider default). Fail-open to standard on any error so a classifier
// outage never changes behavior.
type Detector struct {
	provider llm.Provider
}

func NewDetector(provider llm.Provider) *Detector {
	return &Detector{provider: provider}
}

// Classify sizes one message. Never returns an error — degrades to standard.
func (d *Detector) Classify(ctx context.Context, message, recentContext string) Read {
	std := Read{Tier: TierStandard, Reason: "default"}
	if d == nil || d.provider == nil || strings.TrimSpace(message) == "" {
		return std
	}
	prompt := buildPrompt(message, recentContext)
	raw, err := llm.Complete(ctx, d.provider, "", gaugeSystem, prompt)
	if err != nil {
		return Read{Tier: TierStandard, Reason: "classifier error: " + err.Error()}
	}
	r, perr := parse(raw)
	if perr != nil {
		return Read{Tier: TierStandard, Reason: "parse error"}
	}
	return r
}

const gaugeSystem = `You size how much effort a task deserves, before any work is done.

Output strict JSON: { "tier": "glance" | "standard" | "deep", "reason": "one short clause" }

- glance: a single fact, a lookup, a one-line edit, chit-chat. Answer and move on; no plan, no tools needed.
- standard: an ordinary task — a focused edit, a normal question that needs a tool or two, a short multi-step job.
- deep: real work where being wrong is costly or the path is long — multi-step builds, anything the user said to "make sure" of, anything that should be planned and verified before calling it done.

Bias toward standard when unsure. Output ONLY the JSON object.`

func buildPrompt(message, recentContext string) string {
	var b strings.Builder
	if strings.TrimSpace(recentContext) != "" {
		b.WriteString("Recent context:\n")
		b.WriteString(recentContext)
		b.WriteString("\n\n")
	}
	b.WriteString("Task / message to size:\n")
	b.WriteString(message)
	return b.String()
}

func parse(raw string) (Read, error) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)
	if i := strings.IndexByte(raw, '{'); i > 0 {
		raw = raw[i:]
	}
	var r Read
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return Read{}, err
	}
	r.Tier = Tier(strings.ToLower(strings.TrimSpace(string(r.Tier))))
	if !r.Tier.Valid() {
		return Read{}, fmt.Errorf("invalid tier %q", r.Tier)
	}
	return r, nil
}

// Store persists gauge reads (the durable record + the Studio chip source).
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Record writes one read. Best-effort; an observability miss never blocks chat.
func (s *Store) Record(ctx context.Context, sessionID, message string, r Read) {
	if s == nil || s.pool == nil {
		return
	}
	excerpt := strings.Join(strings.Fields(message), " ")
	if len(excerpt) > 200 {
		excerpt = excerpt[:200] + "…"
	}
	var sessionArg any
	if strings.TrimSpace(sessionID) != "" {
		sessionArg = sessionID
	}
	_, _ = s.pool.Exec(ctx, `
		INSERT INTO mem_gauge_reads (session_id, turn_excerpt, tier, reason, created_at)
		VALUES ($1::uuid, $2, $3, $4, $5)
	`, sessionArg, excerpt, string(r.Tier), r.Reason, time.Now().UTC())
}

// Latest returns the most recent read for a session, or (Read{}, false) when
// none. Used by the session-scoped Provider.
func (s *Store) Latest(ctx context.Context, sessionID string) (Read, bool) {
	if s == nil || s.pool == nil || strings.TrimSpace(sessionID) == "" {
		return Read{}, false
	}
	var tier, reason string
	err := s.pool.QueryRow(ctx, `
		SELECT tier, reason FROM mem_gauge_reads
		 WHERE session_id = $1::uuid
		 ORDER BY created_at DESC LIMIT 1
	`, sessionID).Scan(&tier, &reason)
	if err != nil {
		return Read{}, false
	}
	return Read{Tier: Tier(tier), Reason: reason}, true
}
