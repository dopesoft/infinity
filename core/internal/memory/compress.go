package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Compressor takes a raw observation and uses an LLM to extract structured
// facts, then writes a mem_memories row in the episodic tier with provenance
// linking back to the source observation. Entities mentioned become graph
// nodes/edges. This is the bridge between raw capture and semantic memory.
//
// Disabled by default. Enable with INFINITY_AUTO_COMPRESS=true. Each call
// costs one Claude Haiku turn — keep this off during high-volume capture
// (filesystem ops, big tool outputs) unless you've budgeted for it.
type Compressor struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
	llm      Summarizer
	auditor  *Auditor
}

// Summarizer is the minimal LLM dependency the compressor needs.
// Implementations live in the llm package; declared here to avoid an import
// cycle.
type Summarizer interface {
	SummarizeObservation(ctx context.Context, hookName, rawText string) (CompressedFacts, error)
}

// CompressedFacts is the structured shape we expect the LLM to return.
type CompressedFacts struct {
	Type     string   `json:"type"`     // decision | fact | error | preference | event
	Title    string   `json:"title"`    // ≤80 chars
	Summary  string   `json:"summary"`  // 1-3 sentences
	Concepts []string `json:"concepts"` // free-text concepts
	Entities []Entity `json:"entities"` // typed entities
	Files    []string `json:"files"`
}

type Entity struct {
	Type string `json:"type"` // person, project, file, concept, decision, error, skill
	Name string `json:"name"`
}

func NewCompressor(pool *pgxpool.Pool, embedder embed.Embedder, llm Summarizer) *Compressor {
	if embedder == nil {
		embedder = embed.NewStub()
	}
	return &Compressor{
		pool:     pool,
		embedder: embedder,
		llm:      llm,
		auditor:  NewAuditor(pool),
	}
}

// Compress promotes a single observation into the episodic tier.
// Idempotent: re-running for an already-compressed observation is a no-op.
func (c *Compressor) Compress(ctx context.Context, observationID, project string) error {
	if c == nil || c.pool == nil || c.llm == nil {
		return errors.New("compressor not configured")
	}

	var hookName, rawText string
	err := c.pool.QueryRow(ctx, `
		SELECT hook_name, COALESCE(raw_text, '')
		FROM mem_observations
		WHERE id = $1
	`, observationID).Scan(&hookName, &rawText)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("observation %s not found", observationID)
	}
	if err != nil {
		return err
	}
	if strings.TrimSpace(rawText) == "" {
		return nil
	}

	// Idempotency: skip if a memory already cites this observation.
	var alreadyCompressed bool
	if err := c.pool.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM mem_memory_sources WHERE observation_id = $1)`,
		observationID,
	).Scan(&alreadyCompressed); err != nil {
		return err
	}
	if alreadyCompressed {
		return nil
	}

	facts, err := c.llm.SummarizeObservation(ctx, hookName, rawText)
	if err != nil {
		return fmt.Errorf("summarize: %w", err)
	}
	if strings.TrimSpace(facts.Summary) == "" {
		return nil // model declined to summarize — likely empty / boilerplate input
	}

	memEmb, err := c.embedder.Embed(ctx, facts.Title+"\n"+facts.Summary)
	if err != nil {
		memEmb = nil
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	memID := uuid.NewString()
	importance := importanceForFactType(facts.Type)
	var memEmbArg any
	if memEmb != nil {
		memEmbArg = pgvector.NewVector(memEmb)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO mem_memories
			(id, title, content, tier, version, status, strength, importance, embedding, fts_doc, project, created_at, updated_at, last_accessed_at)
		VALUES
			($1, $2, $3, 'episodic', 1, 'active', 1.0, $4, $5,
			 to_tsvector('english', COALESCE($2, '') || ' ' || COALESCE($3, '')),
			 $6, NOW(), NOW(), NOW())
	`, memID, truncate80(facts.Title), facts.Summary, importance, memEmbArg, nullable(project))
	if err != nil {
		return fmt.Errorf("insert memory: %w", err)
	}

	_, err = tx.Exec(ctx, `
		INSERT INTO mem_memory_sources (memory_id, observation_id, confidence)
		VALUES ($1, $2, $3)
		ON CONFLICT DO NOTHING
	`, memID, observationID, 1.0)
	if err != nil {
		return fmt.Errorf("link source: %w", err)
	}

	for _, ent := range facts.Entities {
		entType := strings.ToLower(strings.TrimSpace(ent.Type))
		entName := strings.TrimSpace(ent.Name)
		if entType == "" || entName == "" {
			continue
		}
		var nodeID string
		if err := tx.QueryRow(ctx, `
			INSERT INTO mem_graph_nodes (type, name)
			VALUES ($1, $2)
			ON CONFLICT (type, name) DO UPDATE SET stale_flag = FALSE
			RETURNING id
		`, entType, entName).Scan(&nodeID); err != nil {
			continue
		}
		_, _ = tx.Exec(ctx, `
			INSERT INTO mem_graph_node_observations (node_id, observation_id)
			VALUES ($1, $2)
			ON CONFLICT DO NOTHING
		`, nodeID, observationID)
	}

	if err := tx.Commit(ctx); err != nil {
		return err
	}

	diff := map[string]any{
		"memory_id":      memID,
		"observation_id": observationID,
		"type":           facts.Type,
		"entities":       len(facts.Entities),
	}
	_ = c.auditor.Log(ctx, "create", "mem_memories", memID, "compressor", diff)
	return nil
}

func importanceForFactType(t string) int {
	switch strings.ToLower(t) {
	case "decision":
		return 8
	case "error":
		return 7
	case "preference":
		return 6
	case "fact":
		return 5
	case "event":
		return 4
	default:
		return 5
	}
}

func truncate80(s string) string {
	s = strings.TrimSpace(s)
	if len(s) <= 80 {
		return s
	}
	return s[:80] + "…"
}

// CompressNewObservations runs Compress over every observation that doesn't
// yet have a memory record. Used by `infinity consolidate --compress`.
func (c *Compressor) CompressNewObservations(ctx context.Context, batchSize int) (int, error) {
	if batchSize <= 0 {
		batchSize = 50
	}
	rows, err := c.pool.Query(ctx, `
		SELECT o.id
		FROM mem_observations o
		LEFT JOIN mem_memory_sources ms ON ms.observation_id = o.id
		WHERE ms.observation_id IS NULL
		  AND o.created_at > NOW() - INTERVAL '24 hours'
		  AND COALESCE(LENGTH(o.raw_text), 0) > 0
		ORDER BY o.created_at DESC
		LIMIT $1
	`, batchSize)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	ids := make([]string, 0, batchSize)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	processed := 0
	for _, id := range ids {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		err := c.Compress(ctx, id, "")
		cancel()
		if err != nil {
			fmt.Printf("compress %s: %v\n", id, err)
			continue
		}
		processed++
	}
	return processed, nil
}

// MarshalFacts is exposed for tests/debugging.
func MarshalFacts(f CompressedFacts) string {
	b, _ := json.Marshal(f)
	return string(b)
}
