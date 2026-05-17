// Package memory - skill_drafts.go: the A-MEM bridge for in-flight
// skill proposals (Opt 7).
//
// Why: today a skill_optimize call produces a mem_skill_proposals row.
// That's a typed table; nothing embeds it. The agent can't RRF-recall
// "I already drafted a refinement to gmail_triage last week that
// added retries" while reasoning - the draft is invisible to memory
// retrieval until promoted.
//
// The fix: every UpsertCandidate (fresh or merged) writes a twin
// mem_memories row tier='working' with the draft body, embedded, and
// auto-linked to its nearest neighbours via AutoLinkMemory. The twin
// gets refreshed on every merge so the latest body is what the agent
// retrieves. Promoted skills already get a procedural row via
// onSkillPromoted - this is the EARLIER hook so drafts also live in
// the graph.

package memory

import (
	"context"
	"errors"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"github.com/dopesoft/infinity/core/internal/embed"
)

// SkillDraftMemoryWriter satisfies proposals.DraftMemoryWriter.
// Constructed once at boot from the same pool + embedder the rest of
// the memory package shares.
type SkillDraftMemoryWriter struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
	logger   *slog.Logger
}

func NewSkillDraftMemoryWriter(pool *pgxpool.Pool, embedder embed.Embedder, logger *slog.Logger) *SkillDraftMemoryWriter {
	if logger == nil {
		logger = slog.Default()
	}
	return &SkillDraftMemoryWriter{pool: pool, embedder: embedder, logger: logger}
}

// WriteSkillDraftMemory upserts the twin memory row for a skill draft.
// Idempotent on title: "skill_draft:<parent_skill>" so successive
// merges UPDATE in place instead of producing N stale memory rows.
// Auto-link runs after the upsert so the draft gets associative edges
// to whatever is currently semantically near it (other related skill
// drafts, the parent skill's procedural memory, prior reflections
// about the same flow).
func (w *SkillDraftMemoryWriter) WriteSkillDraftMemory(ctx context.Context, proposalID, parentSkill, name, body string) (string, error) {
	if w == nil || w.pool == nil {
		return "", nil
	}
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}

	title := "skill_draft:" + strings.TrimSpace(parentSkill)
	if parentSkill == "" {
		// Brand-new skill proposal - use the proposed name so brand-new
		// drafts also land in the graph and don't collide on title.
		title = "skill_draft:new:" + strings.TrimSpace(name)
	}

	var emb []float32
	if w.embedder != nil {
		e, err := w.embedder.Embed(ctx, title+"\n"+body)
		if err == nil {
			emb = e
		}
	}
	var embArg any
	if emb != nil {
		embArg = pgvector.NewVector(emb)
	}

	var existingID string
	err := w.pool.QueryRow(ctx, `
		SELECT id::text FROM mem_memories
		 WHERE tier = 'working' AND status = 'active' AND title = $1
		 LIMIT 1
	`, title).Scan(&existingID)
	if err == nil && existingID != "" {
		_, err = w.pool.Exec(ctx, `
			UPDATE mem_memories
			   SET content          = $2,
			       embedding        = $3,
			       fts_doc          = to_tsvector('english', COALESCE($1, '') || ' ' || COALESCE($2, '')),
			       strength         = 1.0,
			       updated_at       = NOW(),
			       last_accessed_at = NOW()
			 WHERE id = $4::uuid
		`, title, body, embArg, existingID)
		if err != nil {
			return "", err
		}
		// Refresh associative edges - the body changed so its neighbours
		// might have too.
		if len(emb) > 0 {
			_ = AutoLinkMemory(ctx, w.pool, existingID, emb)
		}
		return existingID, nil
	}

	if w.embedder == nil && emb == nil {
		// Without an embedder we still want the row but skip
		// auto-link. The agent can full-text retrieve via fts_doc.
	}
	if w.embedder == nil {
		// Allow boot without embedder; just skip embedding/link below.
	}
	if title == "" {
		return "", errors.New("skill_drafts: empty title")
	}

	id := uuid.NewString()
	_, err = w.pool.Exec(ctx, `
		INSERT INTO mem_memories
		  (id, title, content, tier, version, status, strength, importance, embedding, fts_doc,
		   created_at, updated_at, last_accessed_at)
		VALUES
		  ($1::uuid, $2, $3, 'working', 1, 'active', 1.0, 6, $4,
		   to_tsvector('english', COALESCE($2, '') || ' ' || COALESCE($3, '')),
		   NOW(), NOW(), NOW())
	`, id, title, body, embArg)
	if err != nil {
		return "", err
	}
	if len(emb) > 0 {
		_ = AutoLinkMemory(ctx, w.pool, id, emb)
	}
	return id, nil
}
