package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Cite returns a JIT provenance chain for a memory: the memory itself + each
// source observation with timestamp/session/excerpt + a confidence score
// derived from cluster cohesion (number of sources + recency).
func Cite(ctx context.Context, pool *pgxpool.Pool, memoryID string) (*ProvenanceChain, error) {
	store := NewStore(pool)
	mem, err := store.GetMemory(ctx, memoryID)
	if err != nil {
		return nil, err
	}
	if mem == nil {
		return nil, fmt.Errorf("memory %s not found", memoryID)
	}

	rows, err := pool.Query(ctx, `
		SELECT o.id, o.session_id, COALESCE(o.raw_text, ''), o.created_at, ms.confidence
		FROM mem_memory_sources ms
		JOIN mem_observations o ON o.id = ms.observation_id
		WHERE ms.memory_id = $1
		ORDER BY o.created_at DESC
	`, memoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := &ProvenanceChain{Memory: *mem}
	var sumConf float64
	for rows.Next() {
		var p Provenance
		var sessionID *string
		if err := rows.Scan(&p.ObservationID, &sessionID, &p.Excerpt, &p.CreatedAt, &p.Confidence); err != nil {
			return nil, err
		}
		if sessionID != nil {
			p.SessionID = *sessionID
		}
		if len(p.Excerpt) > 240 {
			p.Excerpt = p.Excerpt[:240] + "…"
		}
		out.Sources = append(out.Sources, p)
		sumConf += p.Confidence
	}
	if len(out.Sources) > 0 {
		out.Confidence = sumConf / float64(len(out.Sources))
	}
	return out, nil
}
