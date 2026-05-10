package memory

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

const (
	streamLimit       = 50
	rrfK              = 60.0
	maxPerSessionDiv  = 3
	defaultLimit      = 10
)

type Searcher struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
}

func NewSearcher(pool *pgxpool.Pool, embedder embed.Embedder) *Searcher {
	if embedder == nil {
		embedder = embed.NewStub()
	}
	return &Searcher{pool: pool, embedder: embedder}
}

// Search runs the three streams (BM25 / vector / graph) in parallel,
// fuses with RRF k=60, then session-diversifies to at most 3 hits per session.
func (s *Searcher) Search(ctx context.Context, query string, opts SearchOpts) ([]SearchResult, error) {
	if s.pool == nil {
		return nil, fmt.Errorf("memory.Search: no database pool configured")
	}
	limit := opts.limit()
	if limit <= 0 {
		limit = defaultLimit
	}

	type streamResult struct {
		name  string
		items []ScoredItem
		err   error
	}

	var wg sync.WaitGroup
	results := make(chan streamResult, 3)

	wg.Add(3)
	go func() {
		defer wg.Done()
		items, err := s.bm25Stream(ctx, query, opts)
		results <- streamResult{"bm25", items, err}
	}()
	go func() {
		defer wg.Done()
		items, err := s.vectorStream(ctx, query, opts)
		results <- streamResult{"vector", items, err}
	}()
	go func() {
		defer wg.Done()
		items, err := s.graphStream(ctx, query, opts)
		results <- streamResult{"graph", items, err}
	}()

	wg.Wait()
	close(results)

	streams := make(map[string][]ScoredItem, 3)
	for r := range results {
		if r.err != nil {
			// Don't fail the whole search on one stream's error — just log via context.
			continue
		}
		streams[r.name] = r.items
	}

	fused := RRF([][]ScoredItem{streams["bm25"], streams["vector"], streams["graph"]}, rrfK)
	if len(fused) == 0 {
		return []SearchResult{}, nil
	}

	ids := make([]string, 0, len(fused))
	for _, f := range fused {
		ids = append(ids, f.ID)
	}
	hydrated, err := s.hydrate(ctx, ids)
	if err != nil {
		return nil, err
	}

	// Stamp scores + streams onto hydrated results in fused order
	out := make([]SearchResult, 0, len(fused))
	for _, f := range fused {
		row, ok := hydrated[f.ID]
		if !ok {
			continue
		}
		row.Score = f.Score
		row.Streams = whichStreams(f.ID, streams)
		out = append(out, row)
	}

	out = DiversifyBySession(out, maxPerSessionDiv)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func whichStreams(id string, streams map[string][]ScoredItem) []string {
	out := make([]string, 0, 3)
	for name, items := range streams {
		for _, it := range items {
			if it.ID == id {
				out = append(out, name)
				break
			}
		}
	}
	return out
}

func (s *Searcher) bm25Stream(ctx context.Context, query string, opts SearchOpts) ([]ScoredItem, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM mem_observations
		WHERE fts_doc @@ websearch_to_tsquery('english', $1)
		  AND ($2::interval IS NULL OR created_at > NOW() - $2::interval)
		ORDER BY ts_rank_cd(fts_doc, websearch_to_tsquery('english', $1)) DESC
		LIMIT $3
	`, query, intervalArg(opts.TimeWindow), streamLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScoredItem, 0, streamLimit)
	rank := 1
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, ScoredItem{ID: id, Rank: rank})
		rank++
	}
	return out, rows.Err()
}

func (s *Searcher) vectorStream(ctx context.Context, query string, opts SearchOpts) ([]ScoredItem, error) {
	emb, err := s.embedder.Embed(ctx, query)
	if err != nil {
		return nil, nil // graceful degradation: empty stream
	}

	rows, err := s.pool.Query(ctx, `
		SELECT id
		FROM mem_observations
		WHERE embedding IS NOT NULL
		  AND ($2::interval IS NULL OR created_at > NOW() - $2::interval)
		ORDER BY embedding <=> $1
		LIMIT $3
	`, pgvector.NewVector(emb), intervalArg(opts.TimeWindow), streamLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScoredItem, 0, streamLimit)
	rank := 1
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, ScoredItem{ID: id, Rank: rank})
		rank++
	}
	return out, rows.Err()
}

// graphStream finds graph nodes whose names appear in the query (cheap; the
// full LLM-based entity extraction described in the spec lives in
// hooks.compress and pre-attaches observations to nodes via
// mem_graph_node_observations). BFS up to 2 hops.
func (s *Searcher) graphStream(ctx context.Context, query string, opts SearchOpts) ([]ScoredItem, error) {
	rows, err := s.pool.Query(ctx, `
		WITH matched AS (
			SELECT id FROM mem_graph_nodes
			WHERE name ILIKE '%' || $1 || '%'
			   OR $1 ILIKE '%' || name || '%'
			LIMIT 20
		),
		two_hop AS (
			SELECT m.id FROM matched m
			UNION
			SELECT e.target_id AS id FROM mem_graph_edges e JOIN matched m ON e.source_id = m.id
			UNION
			SELECT e.source_id AS id FROM mem_graph_edges e JOIN matched m ON e.target_id = m.id
		)
		SELECT o.id
		FROM mem_graph_node_observations gno
		JOIN two_hop t ON gno.node_id = t.id
		JOIN mem_observations o ON o.id = gno.observation_id
		WHERE ($2::interval IS NULL OR o.created_at > NOW() - $2::interval)
		LIMIT $3
	`, query, intervalArg(opts.TimeWindow), streamLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]ScoredItem, 0, streamLimit)
	rank := 1
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, ScoredItem{ID: id, Rank: rank})
		rank++
	}
	return out, rows.Err()
}

func (s *Searcher) hydrate(ctx context.Context, ids []string) (map[string]SearchResult, error) {
	out := make(map[string]SearchResult, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, hook_name, COALESCE(raw_text, ''), created_at
		FROM mem_observations
		WHERE id = ANY($1)
	`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r SearchResult
		var sessionID *string
		if err := rows.Scan(&r.ObservationID, &sessionID, &r.HookName, &r.RawText, &r.CreatedAt); err != nil {
			return nil, err
		}
		if sessionID != nil {
			r.SessionID = *sessionID
		}
		out[r.ObservationID] = r
	}
	return out, rows.Err()
}

func intervalArg(d time.Duration) any {
	if d <= 0 {
		return nil
	}
	return d
}

// BuildSystemPrefix implements agent.MemoryProvider, so memory plugs into the
// agent loop without coupling.
//
// Composes two layers:
//  1. The boss-profile primer — semantic-tier memories under project='_self'.
//     Always prepended so generic queries (where retrieval finds nothing) still
//     have identity context: who the user is, persistent preferences, projects
//     in flight.
//  2. Query-relevant retrieval — the standard triple-stream RRF search.
func (s *Searcher) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	var b stringBuilder

	if profile, _ := s.fetchBossProfile(ctx); profile != "" {
		b.WriteString("About the boss (always-on context):\n")
		b.WriteString(profile)
		b.WriteString("\n")
	}

	results, err := s.Search(ctx, query, SearchOpts{Limit: 10})
	if err != nil {
		// Even if the search fails, return any profile we already wrote so
		// the agent doesn't lose identity context to a transient pgvector hiccup.
		if got := b.String(); got != "" {
			return got, nil
		}
		return "", err
	}
	if len(results) > 0 {
		b.WriteString("Relevant memory (cite when used):\n")
		for i, r := range results {
			fmt.Fprintf(&b, "  [%d] (%s · %s) %s\n",
				i+1, r.HookName, r.CreatedAt.Format("2006-01-02"), trim(r.RawText, 200))
		}
	}
	return b.String(), nil
}

// fetchBossProfile pulls the always-on identity primer. Lives in mem_memories
// at tier='semantic' AND project='_self'. Cap at 8 rows / ~1.5k chars so we
// don't balloon the system prompt — if you need more, write fewer/denser facts.
func (s *Searcher) fetchBossProfile(ctx context.Context) (string, error) {
	if s.pool == nil {
		return "", nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(title, ''), COALESCE(content, '')
		FROM mem_memories
		WHERE tier = 'semantic'
		  AND status = 'active'
		  AND project = '_self'
		ORDER BY importance DESC, updated_at DESC
		LIMIT 8
	`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var out stringBuilder
	totalLen := 0
	for rows.Next() {
		var title, content string
		if err := rows.Scan(&title, &content); err != nil {
			return "", err
		}
		line := strings.TrimSpace(content)
		if line == "" {
			continue
		}
		if totalLen+len(line) > 1500 {
			break
		}
		fmt.Fprintf(&out, "  - %s\n", line)
		totalLen += len(line)
	}
	return out.String(), rows.Err()
}

// stringBuilder mirrors strings.Builder but adds Fprintf via fmt's Write.
type stringBuilder struct {
	buf []byte
}

func (s *stringBuilder) Write(p []byte) (int, error) { s.buf = append(s.buf, p...); return len(p), nil }
func (s *stringBuilder) WriteString(p string) (int, error) {
	s.buf = append(s.buf, p...)
	return len(p), nil
}
func (s *stringBuilder) String() string { return string(s.buf) }

func trim(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

var _ = pgx.ErrNoRows // keep import in case future ext.
