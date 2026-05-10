package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ConsolidateReport struct {
	Decayed      int          `json:"decayed"`
	HotReset     int          `json:"hot_reset"`
	ClustersFound int         `json:"clusters_found"`
	Forget       ForgetReport `json:"forget"`
}

// ConsolidateNightly runs the 4-tier consolidation pass per the spec:
//  1. Decay all memory strengths by 0.95
//  2. Reset hot memories (last_accessed_at within 7 days) to 1.0
//  3. Cluster episodic memories with cosine similarity > 0.85; clusters of
//     ≥3 are merged into semantic memories. (LLM-based merge is a follow-up
//     pass — for now, identification only.)
//  4. Run auto-forget.
func ConsolidateNightly(ctx context.Context, pool *pgxpool.Pool) (ConsolidateReport, error) {
	report := ConsolidateReport{}

	// 1. Decay
	dec, err := pool.Exec(ctx, `UPDATE mem_memories SET strength = strength * 0.95 WHERE status = 'active'`)
	if err != nil {
		return report, fmt.Errorf("decay: %w", err)
	}
	report.Decayed = int(dec.RowsAffected())

	// 2. Reset hot
	hot, err := pool.Exec(ctx, `
		UPDATE mem_memories SET strength = 1.0
		WHERE status = 'active' AND last_accessed_at > NOW() - INTERVAL '7 days'
	`)
	if err != nil {
		return report, fmt.Errorf("hot reset: %w", err)
	}
	report.HotReset = int(hot.RowsAffected())

	// 3. Identify clusters (cosine > 0.85, size ≥ 3 in episodic tier).
	//    Real merge requires an LLM call — that lives in a separate worker.
	const clusterQuery = `
		WITH pairs AS (
			SELECT a.id AS a_id, b.id AS b_id, 1 - (a.embedding <=> b.embedding) AS sim
			FROM mem_memories a
			JOIN mem_memories b
			  ON a.id < b.id AND a.tier = 'episodic' AND b.tier = 'episodic'
			 AND a.status = 'active' AND b.status = 'active'
			 AND a.embedding IS NOT NULL AND b.embedding IS NOT NULL
			WHERE 1 - (a.embedding <=> b.embedding) > 0.85
			LIMIT 5000
		)
		SELECT a_id, COUNT(*) FROM pairs
		GROUP BY a_id
		HAVING COUNT(*) >= 2
	`
	rows, err := pool.Query(ctx, clusterQuery)
	if err != nil {
		// Cluster query is best-effort; missing pgvector index is not fatal.
		report.ClustersFound = 0
	} else {
		defer rows.Close()
		count := 0
		for rows.Next() {
			count++
		}
		report.ClustersFound = count
	}

	// 4. Forget
	freport, err := RunAutoForget(ctx, pool, false)
	if err != nil {
		return report, fmt.Errorf("auto-forget: %w", err)
	}
	report.Forget = freport
	return report, nil
}
