package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ForgetReport struct {
	TTLExpired            int  `json:"ttl_expired"`
	LowValue              int  `json:"low_value_evicted"`
	OverProjectCap        int  `json:"over_project_cap"`
	ObsTraceTrimmed       int  `json:"obs_trace_trimmed"`
	ObsConversationTrimmed int `json:"obs_conversation_trimmed"`
	DryRun                bool `json:"dry_run"`
}

const projectCap = 10_000

// Observation retention windows. Tool-trace audit rows are cheap individually
// but multiply to hundreds per day under heartbeat + voyager; conversation
// rows are load-bearing for /sessions/<id>/messages so they keep a much longer
// horizon. Compressed observations (cited by mem_memory_sources) are always
// preserved to keep the provenance chain intact, regardless of hook.
var (
	traceHooks = []string{
		"PreToolUse", "PostToolUse", "ToolGated",
		"SessionStart", "SessionEnd", "Stop",
		"Notification", "PreCompact",
		"SubagentStart", "SubagentStop",
	}
	// Trace retention is tight on purpose. PreToolUse/PostToolUse rows have
	// two real consumers: /logs detail pages (last few hours of activity) and
	// the voyager session-end extractor (fires within seconds of SessionEnd).
	// Neither needs days-old rows, and at heartbeat-induced ~500-1000 rows/day
	// anything longer turns mem_observations into a write-only landfill.
	traceRetention = "24 hours"

	conversationHooks = []string{
		"UserPromptSubmit", "TaskCompleted",
		"DashboardSeed", "PostToolUseFailure",
	}
	conversationRetention = "180 days"
)

// RunAutoForget applies the four mechanisms from the spec:
//  1. TTL expiry (forget_after past)
//  2. Low-value eviction (age > 90d AND importance < 3)
//  3. Per-project cap (evict lowest-importance until <= 10000)
//  4. Contradiction detection lives in consolidate.go (Jaccard > 0.9 → demote)
//
// Pass dryRun=true to preview without deleting.
func RunAutoForget(ctx context.Context, pool *pgxpool.Pool, dryRun bool) (ForgetReport, error) {
	report := ForgetReport{DryRun: dryRun}

	// 1. TTL
	{
		const sel = `SELECT COUNT(*) FROM mem_memories WHERE forget_after IS NOT NULL AND forget_after < NOW()`
		var n int
		if err := pool.QueryRow(ctx, sel).Scan(&n); err != nil {
			return report, fmt.Errorf("ttl count: %w", err)
		}
		report.TTLExpired = n
		if !dryRun && n > 0 {
			if _, err := pool.Exec(ctx, `DELETE FROM mem_memories WHERE forget_after IS NOT NULL AND forget_after < NOW()`); err != nil {
				return report, fmt.Errorf("ttl delete: %w", err)
			}
		}
	}

	// 2. Low value
	{
		const sel = `SELECT COUNT(*) FROM mem_memories WHERE created_at < NOW() - INTERVAL '90 days' AND importance < 3 AND status = 'active'`
		var n int
		if err := pool.QueryRow(ctx, sel).Scan(&n); err != nil {
			return report, fmt.Errorf("low-value count: %w", err)
		}
		report.LowValue = n
		if !dryRun && n > 0 {
			if _, err := pool.Exec(ctx, `
				UPDATE mem_memories
				SET status = 'archived', updated_at = NOW()
				WHERE created_at < NOW() - INTERVAL '90 days'
				  AND importance < 3
				  AND status = 'active'
			`); err != nil {
				return report, fmt.Errorf("low-value archive: %w", err)
			}
		}
	}

	// 3. Per-project cap
	rows, err := pool.Query(ctx, `
		SELECT project, COUNT(*)
		FROM mem_memories
		WHERE status = 'active' AND project IS NOT NULL AND project != ''
		GROUP BY project
		HAVING COUNT(*) > $1
	`, projectCap)
	if err != nil {
		return report, fmt.Errorf("per-project query: %w", err)
	}
	defer rows.Close()
	type overflow struct {
		project string
		count   int
	}
	var overs []overflow
	for rows.Next() {
		var o overflow
		if err := rows.Scan(&o.project, &o.count); err != nil {
			return report, err
		}
		overs = append(overs, o)
	}
	rows.Close()

	for _, o := range overs {
		excess := o.count - projectCap
		report.OverProjectCap += excess
		if dryRun {
			continue
		}
		if _, err := pool.Exec(ctx, `
			UPDATE mem_memories
			SET status = 'archived', updated_at = NOW()
			WHERE id IN (
				SELECT id FROM mem_memories
				WHERE project = $1 AND status = 'active'
				ORDER BY importance ASC, last_accessed_at ASC
				LIMIT $2
			)
		`, o.project, excess); err != nil {
			return report, fmt.Errorf("project cap evict: %w", err)
		}
	}

	// 4. Observation prune. Trace audit + conversation rows past retention,
	//    excluding anything already cited by mem_memory_sources (provenance
	//    must survive). CASCADE on mem_graph_node_observations + mem_memory_sources
	//    handles the FK cleanup; the graph node row itself survives, just
	//    loses one piece of evidence.
	traceCount, err := pruneObservations(ctx, pool, traceHooks, traceRetention, dryRun)
	if err != nil {
		return report, fmt.Errorf("trace prune: %w", err)
	}
	report.ObsTraceTrimmed = traceCount

	convoCount, err := pruneObservations(ctx, pool, conversationHooks, conversationRetention, dryRun)
	if err != nil {
		return report, fmt.Errorf("conversation prune: %w", err)
	}
	report.ObsConversationTrimmed = convoCount

	return report, nil
}

func pruneObservations(ctx context.Context, pool *pgxpool.Pool, hooks []string, retention string, dryRun bool) (int, error) {
	const countQ = `
		SELECT COUNT(*) FROM mem_observations o
		 WHERE o.hook_name = ANY($1)
		   AND o.created_at < NOW() - $2::interval
		   AND NOT EXISTS (
		     SELECT 1 FROM mem_memory_sources s WHERE s.observation_id = o.id
		   )
	`
	var n int
	if err := pool.QueryRow(ctx, countQ, hooks, retention).Scan(&n); err != nil {
		return 0, fmt.Errorf("count: %w", err)
	}
	if dryRun || n == 0 {
		return n, nil
	}
	const delQ = `
		DELETE FROM mem_observations o
		 WHERE o.hook_name = ANY($1)
		   AND o.created_at < NOW() - $2::interval
		   AND NOT EXISTS (
		     SELECT 1 FROM mem_memory_sources s WHERE s.observation_id = o.id
		   )
	`
	if _, err := pool.Exec(ctx, delQ, hooks, retention); err != nil {
		return 0, fmt.Errorf("delete: %w", err)
	}
	return n, nil
}
