// Package voyager implements Infinity's self-evolving skill loop.
//
// Three coordinated subsystems:
//
//  1. SessionEnd extractor — when a session ends, score recent observations
//     against a heuristic (≥3 distinct tools, no fatal errors, ≥30s elapsed).
//     If it passes, Haiku drafts a SKILL.md candidate from the transcript and
//     a row lands in mem_skill_proposals.
//
//  2. Real-time discovery — every PostToolUse, the manager appends the tool
//     to a per-session window. When the same N-tuple of consecutive tools
//     appears across multiple sessions within a sliding window, that's a
//     pattern worth crystallizing — the agent is doing the same dance often
//     enough that a one-shot skill would be cheaper.
//
//  3. Verifier — for each candidate, Haiku generates synthetic test cases.
//     Instruction-only skills (no impl) auto-promote because there's nothing
//     executable to verify. Implementation-bearing skills sit as candidates
//     until a human (or future automated runner) confirms.
//
// The whole loop is opt-in via INFINITY_VOYAGER=true. With it off the package
// loads but the hooks no-op so the agent loop is unaffected.
package voyager

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/skills"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Manager is the single entry point for the auto-skill loop. Construct one in
// serve.go, register hooks against it, and mount its HTTP routes.
type Manager struct {
	pool       *pgxpool.Pool
	llm        *llm.Anthropic
	skillsReg  *skills.Registry
	skillsRoot string
	enabled    bool

	// Discovery state — per-session sliding windows of recent tool names plus
	// a global counter of repeated triplets across sessions.
	mu              sync.Mutex
	sessionWindows  map[string][]toolEvent
	tripletCounters map[string]*tripletCounter
}

type toolEvent struct {
	name string
	at   time.Time
}

type tripletCounter struct {
	tools   [3]string
	hits    int
	first   time.Time
	lastHit time.Time
	// sessions that contributed (so we don't propose the same triplet twice
	// from a single noisy session)
	sessions map[string]struct{}
}

// Config wires the Manager. LLM is required for extraction/verification;
// without it the manager falls back to discovery-only and skips Haiku passes.
type Config struct {
	Pool        *pgxpool.Pool
	LLM         *llm.Anthropic
	Skills      *skills.Registry
	SkillsRoot  string
}

func New(cfg Config) *Manager {
	enabled := envTrue("INFINITY_VOYAGER")
	root := strings.TrimSpace(cfg.SkillsRoot)
	if root == "" {
		root = "./skills"
	}
	return &Manager{
		pool:            cfg.Pool,
		llm:             cfg.LLM,
		skillsReg:       cfg.Skills,
		skillsRoot:      root,
		enabled:         enabled,
		sessionWindows:  make(map[string][]toolEvent),
		tripletCounters: make(map[string]*tripletCounter),
	}
}

// Enabled reports whether the loop is live. False means hooks no-op.
func (m *Manager) Enabled() bool {
	return m != nil && m.enabled && m.pool != nil
}

// Status is what serve.go prints at boot.
func (m *Manager) Status() string {
	if m == nil {
		return "off"
	}
	if !m.enabled {
		return "off (set INFINITY_VOYAGER=true to enable)"
	}
	parts := []string{"on"}
	if m.llm == nil {
		parts = append(parts, "discovery-only (no LLM)")
	} else {
		parts = append(parts, "extractor+verifier")
	}
	return strings.Join(parts, " · ")
}

// ProposalDTO is the wire shape returned by the API.
type ProposalDTO struct {
	ID            string    `json:"id"`
	Name          string    `json:"name"`
	Description   string    `json:"description"`
	Reasoning     string    `json:"reasoning"`
	SkillMD       string    `json:"skill_md"`
	RiskLevel     string    `json:"risk_level"`
	TestPassRate  float64   `json:"test_pass_rate"`
	Status        string    `json:"status"` // candidate | promoted | rejected
	ParentSkill   string    `json:"parent_skill,omitempty"`
	ParentVersion string    `json:"parent_version,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	DecidedAt     *time.Time `json:"decided_at,omitempty"`
}

// ListProposals returns proposals filtered by status. Empty status = all.
func (m *Manager) ListProposals(ctx context.Context, status string, limit int) ([]ProposalDTO, error) {
	if m == nil || m.pool == nil {
		return []ProposalDTO{}, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	q := `
		SELECT id::text, name, description, reasoning, skill_md, risk_level,
		       test_pass_rate, status,
		       COALESCE(parent_skill, ''), COALESCE(parent_version, ''),
		       created_at, decided_at
		FROM mem_skill_proposals
	`
	args := []any{}
	if status != "" {
		q += ` WHERE status = $1`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT $` + itoa(len(args)+1)
	args = append(args, limit)

	rows, err := m.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ProposalDTO{}
	for rows.Next() {
		var p ProposalDTO
		var decided *time.Time
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Reasoning, &p.SkillMD, &p.RiskLevel,
			&p.TestPassRate, &p.Status, &p.ParentSkill, &p.ParentVersion, &p.CreatedAt, &decided); err != nil {
			return nil, err
		}
		p.DecidedAt = decided
		out = append(out, p)
	}
	return out, rows.Err()
}

// Decide promotes or rejects a proposal. Promotion writes the skill to disk
// and reloads the registry so the agent sees it immediately.
func (m *Manager) Decide(ctx context.Context, id, decision string) error {
	if m == nil || m.pool == nil {
		return errors.New("voyager: no database pool")
	}
	if decision != "promoted" && decision != "rejected" {
		return errors.New("voyager: decision must be 'promoted' or 'rejected'")
	}

	if decision == "promoted" {
		var name, skillMD, description, riskLevel string
		err := m.pool.QueryRow(ctx, `
			SELECT name,
			       COALESCE(skill_md, ''),
			       COALESCE(description, ''),
			       COALESCE(risk_level, 'low')
			  FROM mem_skill_proposals WHERE id = $1
		`, id).Scan(&name, &skillMD, &description, &riskLevel)
		if err != nil {
			return err
		}
		// Persist to Postgres FIRST so the skill survives any container
		// restart (Railway redeploys wipe the ephemeral filesystem).
		// Disk write is then a derivative — used by the loader to
		// populate the in-memory registry. On every boot the
		// MaterializeFromDB hook (see skills.MaterializeActiveSkills)
		// re-syncs disk to match active rows in Postgres, so even if
		// the disk write is lost we recover automatically.
		version := nextVersionStamp()
		if err := m.persistSkillToDB(ctx, name, version, skillMD, description, riskLevel); err != nil {
			return err
		}
		if err := m.writeSkillToDisk(name, skillMD); err != nil {
			return err
		}
		if m.skillsReg != nil {
			_, _ = m.skillsReg.Reload(ctx)
		}
	}

	_, err := m.pool.Exec(ctx, `
		UPDATE mem_skill_proposals
		SET status = $2, decided_at = NOW()
		WHERE id = $1
	`, id, decision)
	return err
}

func (m *Manager) writeSkillToDisk(name, skillMD string) error {
	safe := safeName(name)
	if safe == "" {
		return errors.New("voyager: skill name produced empty filename")
	}
	dir := strings.TrimRight(m.skillsRoot, "/") + "/" + safe
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(dir+"/SKILL.md", []byte(skillMD), 0o644)
}

// persistSkillToDB upserts the auto-evolved skill into the three durable
// tables (mem_skills + mem_skill_versions + mem_skill_active) inside a
// single transaction. This is the deploy-survival guarantee for skills
// generated at runtime — Railway's ephemeral container filesystem can
// disappear on every push, but these Postgres rows persist forever and
// are re-materialized to disk on the next boot.
func (m *Manager) persistSkillToDB(ctx context.Context, name, version, skillMD, description, riskLevel string) error {
	if m == nil || m.pool == nil {
		return errors.New("voyager: no database pool")
	}
	if riskLevel == "" {
		riskLevel = "low"
	}
	tx, err := m.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// 1. Upsert the catalog row. Source = auto_evolved so we can filter
	//    in Studio (e.g. "show me everything Voyager built me").
	if _, err := tx.Exec(ctx, `
		INSERT INTO mem_skills (name, description, risk_level, status, source, last_evolved, updated_at)
		VALUES ($1, $2, $3, 'active', 'auto_evolved', NOW(), NOW())
		ON CONFLICT (name) DO UPDATE
		   SET description  = EXCLUDED.description,
		       risk_level   = EXCLUDED.risk_level,
		       status       = 'active',
		       source       = CASE
		                        WHEN mem_skills.source = 'manual' THEN 'manual'
		                        ELSE 'auto_evolved'
		                      END,
		       last_evolved = NOW(),
		       updated_at   = NOW()
	`, name, description, riskLevel); err != nil {
		return fmt.Errorf("upsert mem_skills: %w", err)
	}

	// 2. Insert the new version row (immutable history — every evolution
	//    leaves a trail).
	if _, err := tx.Exec(ctx, `
		INSERT INTO mem_skill_versions (skill_name, version, skill_md, source, promoted_at)
		VALUES ($1, $2, $3, 'auto_evolved', NOW())
		ON CONFLICT DO NOTHING
	`, name, version, skillMD); err != nil {
		return fmt.Errorf("insert mem_skill_versions: %w", err)
	}

	// 3. Point the active pointer at the new version so MaterializeActiveSkills
	//    knows which body to write to disk on boot.
	if _, err := tx.Exec(ctx, `
		INSERT INTO mem_skill_active (skill_name, active_version, updated_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (skill_name) DO UPDATE
		   SET active_version = EXCLUDED.active_version,
		       updated_at     = NOW()
	`, name, version); err != nil {
		return fmt.Errorf("upsert mem_skill_active: %w", err)
	}

	return tx.Commit(ctx)
}

// nextVersionStamp returns a YYYYMMDDhhmmss-style version label. Lexically
// sortable, human-readable, and unique enough for the auto-evolution path
// where the boss only promotes one variant per (skill, second).
func nextVersionStamp() string {
	return time.Now().UTC().Format("20060102-150405")
}

func safeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('_')
		}
	}
	out := b.String()
	out = strings.Trim(out, "_")
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

func envTrue(key string) bool {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	return v == "1" || v == "true" || v == "yes" || v == "on"
}

func itoa(i int) string {
	// tiny helper to avoid pulling strconv just for query-param indices
	if i == 0 {
		return "0"
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	return string(digits)
}
