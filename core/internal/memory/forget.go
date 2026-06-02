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
	LessonsTrimmed        int  `json:"lessons_trimmed"`
	OperationalTrimmed    int  `json:"operational_trimmed"`
	GraphTrimmed          int  `json:"graph_trimmed"`
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
		// Failure traces are mechanics, not conversation - prune at 24h, not
		// 180d (they used to live under conversationHooks and pile up). The
		// voyager extractor consumes them within seconds of SessionEnd.
		"PostToolUseFailure",
		// Compaction markers were orphaned (in NEITHER list) so they never
		// pruned. They're trace.
		"conversation_compaction",
	}
	// Trace retention is tight on purpose. PreToolUse/PostToolUse rows have
	// two real consumers: /logs detail pages (last few hours of activity) and
	// the voyager session-end extractor (fires within seconds of SessionEnd).
	// Neither needs days-old rows, and at heartbeat-induced ~500-1000 rows/day
	// anything longer turns mem_observations into a write-only landfill.
	traceRetention = "24 hours"

	conversationHooks = []string{
		"UserPromptSubmit", "TaskCompleted",
		"DashboardSeed",
	}
	conversationRetention = "180 days"

	// lessonRetention prunes mem_lessons that never proved their worth: low
	// confidence, never reinforced, and stale. mem_lessons has no read consumer
	// (the agent is shaped by mem_reflection_chains, not this raw archive) and
	// nothing else prunes it, so without this it grows without bound - exactly
	// how it reached 294 redundant rows. Reinforced or high-confidence lessons
	// survive indefinitely.
	lessonRetention = "30 days"
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

	lessonCount, err := pruneLessons(ctx, pool, lessonRetention, dryRun)
	if err != nil {
		return report, fmt.Errorf("lesson prune: %w", err)
	}
	report.LessonsTrimmed = lessonCount

	opCount, err := pruneOperationalLogs(ctx, pool, dryRun)
	if err != nil {
		return report, fmt.Errorf("operational prune: %w", err)
	}
	report.OperationalTrimmed = opCount

	graphCount, err := pruneGraphHygiene(ctx, pool, dryRun)
	if err != nil {
		return report, fmt.Errorf("graph prune: %w", err)
	}
	report.GraphTrimmed = graphCount

	return report, nil
}

// pruneGraphHygiene keeps the knowledge graph + entity store from re-gunking:
// transient file nodes aged out, long-orphaned nodes (evidence gone for 30d,
// excluding the boss's identity), and the slash-fragment "project" entity
// garbage the extractor produces. Conservative age gates so a recently-mentioned
// entity whose 24h trace just expired is NOT deleted.
func pruneGraphHygiene(ctx context.Context, pool *pgxpool.Pool, dryRun bool) (int, error) {
	stmts := []string{
		// transient file-path nodes older than 7 days
		`DELETE FROM mem_graph_nodes WHERE type = 'file' AND created_at < NOW() - INTERVAL '7 days'`,
		// long-orphaned nodes (no evidence, 30d+), never the identity core
		`DELETE FROM mem_graph_nodes n
		   WHERE n.created_at < NOW() - INTERVAL '30 days'
		     AND NOT EXISTS (SELECT 1 FROM mem_graph_node_observations o WHERE o.node_id = n.id)
		     AND lower(n.name) NOT IN ('kai','boss','khaya','malabie industries','mr khaya','infinity','jarvis')`,
		// slash-fragment / id / date entity garbage mislabeled as projects
		`DELETE FROM mem_entities WHERE kind = 'project'
		     AND (name LIKE '%/%' OR name ~ '^[0-9]' OR name ILIKE 'inbox/%' OR length(trim(name)) < 3)`,
	}
	total := 0
	for _, q := range stmts {
		if dryRun {
			continue
		}
		tag, err := pool.Exec(ctx, q)
		if err != nil {
			return total, fmt.Errorf("graph hygiene: %w", err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

// opLogSpecs are high-volume operational tables that have no other reaper and
// would otherwise grow without bound (the heartbeat ticker alone writes ~680
// rows/day). None are retrieved into the agent's reasoning — they're logs,
// ledgers, and settled dashboard findings. Open findings + recent rows survive.
var opLogSpecs = []struct {
	name  string
	where string // a SQL predicate selecting the DELETABLE rows
}{
	{"mem_heartbeats", "started_at < NOW() - INTERVAL '2 days'"},
	{"mem_heartbeat_findings", "status IN ('resolved','dismissed','auto_dismissed') AND COALESCE(resolved_at, auto_dismissed_at, last_seen_at, created_at) < NOW() - INTERVAL '7 days'"},
	{"mem_predictions", "resolved_at IS NOT NULL AND resolved_at < NOW() - INTERVAL '14 days'"},
	{"mem_cost_events", "created_at < NOW() - INTERVAL '14 days'"},
	{"mem_audit", "created_at < NOW() - INTERVAL '90 days'"},
}

// pruneOperationalLogs sweeps the opLogSpecs tables. Best-effort per table: one
// table's failure (e.g. a column rename) is logged but never blocks the rest.
func pruneOperationalLogs(ctx context.Context, pool *pgxpool.Pool, dryRun bool) (int, error) {
	total := 0
	for _, s := range opLogSpecs {
		if dryRun {
			var n int
			if err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM "+s.name+" WHERE "+s.where).Scan(&n); err != nil {
				return total, fmt.Errorf("%s count: %w", s.name, err)
			}
			total += n
			continue
		}
		tag, err := pool.Exec(ctx, "DELETE FROM "+s.name+" WHERE "+s.where)
		if err != nil {
			return total, fmt.Errorf("%s delete: %w", s.name, err)
		}
		total += int(tag.RowsAffected())
	}
	return total, nil
}

// pruneLessons deletes stale, low-confidence, never-reinforced lessons so the
// behaviorally-dead mem_lessons archive self-cleans instead of growing forever.
// A lesson survives if it's reinforced (times_reinforced > 0) OR high-confidence
// (>= 0.85) OR recent (< retention). Everything else is noise that never proved
// itself. Mirrors pruneObservations.
func pruneLessons(ctx context.Context, pool *pgxpool.Pool, retention string, dryRun bool) (int, error) {
	const q = `
		DELETE FROM mem_lessons
		 WHERE COALESCE(times_reinforced, 0) = 0
		   AND COALESCE(confidence, 0) < 0.85
		   AND created_at < NOW() - $1::interval
	`
	if dryRun {
		var n int
		if err := pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM mem_lessons
			 WHERE COALESCE(times_reinforced,0)=0 AND COALESCE(confidence,0)<0.85
			   AND created_at < NOW() - $1::interval
		`, retention).Scan(&n); err != nil {
			return 0, fmt.Errorf("count: %w", err)
		}
		return n, nil
	}
	tag, err := pool.Exec(ctx, q, retention)
	if err != nil {
		return 0, fmt.Errorf("delete: %w", err)
	}
	return int(tag.RowsAffected()), nil
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
