package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MarkSuperseded sets the old memory's status to 'superseded', records the
// supersedes relation, and propagates a stale flag to 1-hop graph neighbors.
// Cascading staleness mirrors the spec: a future read should re-verify
// neighbors before trusting them.
func MarkSuperseded(ctx context.Context, pool *pgxpool.Pool, oldID, newID string) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	if _, err := tx.Exec(ctx,
		`UPDATE mem_memories SET status = 'superseded', superseded_by = $1, updated_at = NOW() WHERE id = $2`,
		newID, oldID); err != nil {
		return fmt.Errorf("mark superseded: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO mem_relations (source_id, target_id, relation_type, confidence)
		VALUES ($1, $2, 'supersedes', 1.0)
	`, newID, oldID); err != nil {
		return fmt.Errorf("relation supersedes: %w", err)
	}

	// 1-hop neighbors: any graph node that connects to a graph node mentioning
	// the superseded memory's source observations gets stale_flag=true.
	if _, err := tx.Exec(ctx, `
		UPDATE mem_graph_nodes
		SET stale_flag = TRUE
		WHERE id IN (
			SELECT DISTINCT e.target_id FROM mem_graph_edges e
			JOIN mem_graph_node_observations gno ON gno.node_id = e.source_id
			JOIN mem_memory_sources ms ON ms.observation_id = gno.observation_id
			WHERE ms.memory_id = $1
			UNION
			SELECT DISTINCT e.source_id FROM mem_graph_edges e
			JOIN mem_graph_node_observations gno ON gno.node_id = e.target_id
			JOIN mem_memory_sources ms ON ms.observation_id = gno.observation_id
			WHERE ms.memory_id = $1
		)
	`, oldID); err != nil {
		return fmt.Errorf("propagate stale flag: %w", err)
	}

	return tx.Commit(ctx)
}
