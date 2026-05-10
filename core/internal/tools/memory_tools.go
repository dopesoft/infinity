// Native tools that let the agent (or user) interact with the memory
// subsystem directly: remember a fact, recall by query, forget by id.
//
// These tools only register if a memory.Store is provided. The serve command
// wires them in when DATABASE_URL is set.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/dopesoft/infinity/core/internal/memory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// RegisterMemoryTools wires remember / recall / forget native tools into the
// registry. No-op if pool is nil.
func RegisterMemoryTools(r *Registry, pool *pgxpool.Pool, embedder embed.Embedder, searcher *memory.Searcher) {
	if r == nil || pool == nil {
		return
	}
	r.Register(&rememberTool{pool: pool, embedder: embedder})
	r.Register(&recallTool{searcher: searcher})
	r.Register(&forgetTool{pool: pool})
}

// ----- remember --------------------------------------------------------------

type rememberTool struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
}

func (t *rememberTool) Name() string        { return "remember" }
func (t *rememberTool) Description() string { return "Save a fact, decision, or preference to long-term memory. Returns the new memory id." }
func (t *rememberTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"title":      map[string]any{"type": "string", "description": "Short title (≤80 chars)."},
			"content":    map[string]any{"type": "string", "description": "Full content of the memory."},
			"tier":       map[string]any{"type": "string", "enum": []string{"working", "episodic", "semantic", "procedural"}, "default": "semantic"},
			"importance": map[string]any{"type": "integer", "minimum": 1, "maximum": 10, "default": 6},
			"project":    map[string]any{"type": "string"},
		},
		"required": []string{"content"},
	}
}

func (t *rememberTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	content, _ := input["content"].(string)
	if strings.TrimSpace(content) == "" {
		return "", errors.New("content is required")
	}
	title, _ := input["title"].(string)
	if title == "" {
		title = firstLine(content, 80)
	}
	tier, _ := input["tier"].(string)
	if tier == "" {
		tier = "semantic"
	}
	importance := 6
	if n, ok := input["importance"].(float64); ok {
		importance = int(n)
	}
	project, _ := input["project"].(string)

	var emb any
	if t.embedder != nil {
		if v, err := t.embedder.Embed(ctx, title+"\n"+content); err == nil {
			emb = pgvector.NewVector(v)
		}
	}

	id := uuid.NewString()
	_, err := t.pool.Exec(ctx, `
		INSERT INTO mem_memories
			(id, title, content, tier, version, status, strength, importance, embedding, fts_doc, project, created_at, updated_at, last_accessed_at)
		VALUES
			($1, $2, $3, $4, 1, 'active', 1.0, $5, $6,
			 to_tsvector('english', COALESCE($2, '') || ' ' || COALESCE($3, '')),
			 $7, NOW(), NOW(), NOW())
	`, id, title, content, tier, importance, emb, nullable(project))
	if err != nil {
		return "", fmt.Errorf("insert memory: %w", err)
	}

	resp := map[string]any{"id": id, "tier": tier, "importance": importance}
	out, _ := json.Marshal(resp)
	return string(out), nil
}

// ----- recall ----------------------------------------------------------------

type recallTool struct {
	searcher *memory.Searcher
}

func (t *recallTool) Name() string        { return "recall" }
func (t *recallTool) Description() string { return "Search memory via triple-stream retrieval (BM25 + vector + graph) with RRF fusion. Use to ground answers in prior context." }
func (t *recallTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "Natural-language search query."},
			"limit": map[string]any{"type": "integer", "default": 8, "minimum": 1, "maximum": 25},
		},
		"required": []string{"query"},
	}
}

func (t *recallTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	if t.searcher == nil {
		return "", errors.New("memory subsystem unavailable")
	}
	q, _ := input["query"].(string)
	if strings.TrimSpace(q) == "" {
		return "", errors.New("query is required")
	}
	limit := 8
	if n, ok := input["limit"].(float64); ok {
		limit = int(n)
	}
	results, err := t.searcher.Search(ctx, q, memory.SearchOpts{Limit: limit, IncludeBreakdown: true})
	if err != nil {
		return "", err
	}
	out, _ := json.MarshalIndent(results, "", "  ")
	return string(out), nil
}

// ----- forget ----------------------------------------------------------------

type forgetTool struct {
	pool *pgxpool.Pool
}

func (t *forgetTool) Name() string        { return "forget" }
func (t *forgetTool) Description() string { return "Archive a memory by id. Use status='deleted' to hard-delete (rare, audited)." }
func (t *forgetTool) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":   map[string]any{"type": "string", "description": "Memory uuid to forget."},
			"hard": map[string]any{"type": "boolean", "default": false, "description": "If true, delete the row entirely."},
		},
		"required": []string{"id"},
	}
}

func (t *forgetTool) Execute(ctx context.Context, input map[string]any) (string, error) {
	id, _ := input["id"].(string)
	if id == "" {
		return "", errors.New("id is required")
	}
	hard, _ := input["hard"].(bool)
	if hard {
		_, err := t.pool.Exec(ctx, `DELETE FROM mem_memories WHERE id = $1`, id)
		if err != nil {
			return "", err
		}
		return `{"deleted":true}`, nil
	}
	tag, err := t.pool.Exec(ctx, `UPDATE mem_memories SET status = 'archived', updated_at = NOW() WHERE id = $1`, id)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"archived":true,"rows":%d}`, tag.RowsAffected()), nil
}

// helpers
func firstLine(s string, max int) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > max {
		s = s[:max] + "…"
	}
	return s
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
