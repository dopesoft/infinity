package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ConsolidateReport summarizes what a sleep-time pass did. Each field is a
// count of rows touched by that operation - handy for observability + the
// Studio Memory tab's "last consolidation" line.
type ConsolidateReport struct {
	Decayed             int          `json:"decayed"`
	HotReset            int          `json:"hot_reset"`
	ClustersFound       int          `json:"clusters_found"`
	ContradictionsFound int          `json:"contradictions_found"`
	AssociativePruned   int          `json:"associative_pruned"`
	WeakAssocPurged     int          `json:"weak_associative_purged"`
	ProceduralReweighted int         `json:"procedural_reweighted"`
	Forget              ForgetReport `json:"forget"`
}

// ConsolidateNightly runs the sleep-time pass.
//
// Sleep-time consolidation is a distinct compute regime from online
// compression (LightMem, arXiv 2510.18866). Where the compressor turns one
// observation into one memory, this pass operates on the GRAPH: decays
// strength, resets hot memories, identifies clusters, surfaces
// contradictions, prunes redundant associative edges, re-weights procedural
// importance from skill success rates, then runs auto-forget.
//
// The order matters:
//   1. Decay every active memory's strength by 0.95 (default forgetting).
//   2. Reset hot memories (recently accessed) to strength 1.0.
//   3. Identify episodic clusters (cosine >0.85, size ≥3) - flag for merge.
//   4. Find contradicting active memories - when two semantic memories with
//      a 'contradicts' edge are both active, mark the older one stale.
//   5. Prune redundant associative edges (>10 outgoing from any node - keep
//      top-10 by confidence).
//   6. Drop weak associative edges (confidence < 0.40 after recompute).
//   7. Re-weight procedural skills by recent success rate.
//   8. Run auto-forget.
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

	// 3. Identify clusters
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
		report.ClustersFound = 0
	} else {
		count := 0
		for rows.Next() {
			count++
		}
		rows.Close()
		report.ClustersFound = count
	}

	// 4. Surface contradictions. When two active semantic memories carry a
	// 'contradicts' edge, mark the OLDER one superseded so retrieval doesn't
	// keep returning both halves of a now-resolved disagreement.
	contradictRows, err := pool.Query(ctx, `
		SELECT source_id::text, target_id::text
		  FROM mem_relations
		 WHERE relation_type = 'contradicts'
		 ORDER BY created_at DESC
		 LIMIT 200
	`)
	if err == nil {
		for contradictRows.Next() {
			var srcID, tgtID string
			if err := contradictRows.Scan(&srcID, &tgtID); err != nil {
				continue
			}
			// Both must be active and non-superseded for resolution to fire.
			var loserID string
			if err := pool.QueryRow(ctx, `
				SELECT loser::text FROM (
					SELECT CASE WHEN m1.created_at < m2.created_at THEN m1.id ELSE m2.id END AS loser,
					       m1.status AS s1, m2.status AS s2
					  FROM mem_memories m1, mem_memories m2
					 WHERE m1.id = $1::uuid AND m2.id = $2::uuid
				) x WHERE s1 = 'active' AND s2 = 'active'
			`, srcID, tgtID).Scan(&loserID); err == nil && loserID != "" {
				_, _ = pool.Exec(ctx, `
					UPDATE mem_memories
					   SET status = 'superseded',
					       superseded_by = CASE WHEN id::text = $2 THEN $1::uuid ELSE $2::uuid END,
					       updated_at = NOW()
					 WHERE id::text = $3
				`, srcID, tgtID, loserID)
				report.ContradictionsFound++
			}
		}
		contradictRows.Close()
	}

	// 5. Prune redundant associative edges - keep at most 10 outgoing per
	// source node, drop the rest by lowest confidence.
	pruned, err := pool.Exec(ctx, `
		DELETE FROM mem_relations
		 WHERE id IN (
		   SELECT id FROM (
			   SELECT id,
			          ROW_NUMBER() OVER (
			              PARTITION BY source_id, relation_type
			              ORDER BY confidence DESC, created_at DESC
			          ) AS rn
			     FROM mem_relations
			    WHERE relation_type = 'associative'
		   ) ranked
		   WHERE rn > 10
		 )
	`)
	if err == nil {
		report.AssociativePruned = int(pruned.RowsAffected())
	}

	// 6. Drop weak associative edges. The A-MEM threshold at write was 0.65;
	// after decay-style aging we drop anything ≤ 0.40.
	purged, err := pool.Exec(ctx, `
		DELETE FROM mem_relations
		 WHERE relation_type = 'associative' AND confidence < 0.40
	`)
	if err == nil {
		report.WeakAssocPurged = int(purged.RowsAffected())
	}

	// 7. Re-weight procedural memories by recent skill success rate.
	// A skill running well bumps its procedural strength to 1.0; a skill
	// running badly drags strength below decay. The agent's TopK retrieval
	// then naturally prefers high-success procedural skills.
	reweighted, err := pool.Exec(ctx, `
		WITH rates AS (
			SELECT skill_name,
			       AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END) AS rate,
			       COUNT(*) AS n
			  FROM mem_skill_runs
			 WHERE started_at > NOW() - INTERVAL '7 days'
			 GROUP BY skill_name
			HAVING COUNT(*) >= 3
		)
		UPDATE mem_memories m
		   SET strength = LEAST(1.0, GREATEST(0.1, r.rate))
		  FROM rates r
		 WHERE m.tier = 'procedural'
		   AND m.status = 'active'
		   AND m.title = 'skill:' || r.skill_name
	`)
	if err == nil {
		report.ProceduralReweighted = int(reweighted.RowsAffected())
	}

	// 8. Forget
	freport, err := RunAutoForget(ctx, pool, false)
	if err != nil {
		return report, fmt.Errorf("auto-forget: %w", err)
	}
	report.Forget = freport
	return report, nil
}
