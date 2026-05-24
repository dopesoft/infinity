package memory

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
)

// RollupAgentMetrics computes today's row of mem_agent_metrics for each
// metric the agent tracks about itself, and recomputes the 14-day trailing
// baseline + sigma. Registered as a ConsolidateNightly hook so it runs once
// per night alongside reflection, compression, and consolidation.
//
// The metrics are intentionally lightweight aggregate queries - no Haiku
// calls, no clustering, no judgment. The judgment happens at READ time in
// SelfModelProvider, which only surfaces an alert when today's value is
// more than 2σ off baseline.
func RollupAgentMetrics(ctx context.Context, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	log := slog.Default()
	rollups := []struct {
		name string
		sql  string
	}{
		// avg_quality_score - the critic's self-score across today's sessions.
		{"avg_quality_score", `
			INSERT INTO mem_agent_metrics (day, metric_name, dimension, value, sample_size)
			SELECT CURRENT_DATE,
			       'avg_quality_score',
			       '',
			       COALESCE(AVG(quality_score), 0),
			       COUNT(*)
			  FROM mem_reflections
			 WHERE created_at >= CURRENT_DATE
			   AND created_at < CURRENT_DATE + INTERVAL '1 day'
			HAVING COUNT(*) > 0
			ON CONFLICT (day, metric_name, dimension) DO UPDATE SET
			  value = EXCLUDED.value,
			  sample_size = EXCLUDED.sample_size,
			  updated_at = NOW()
		`},
		// cost_usd_total - total spend across all categories today.
		{"cost_usd_total", `
			INSERT INTO mem_agent_metrics (day, metric_name, dimension, value, sample_size)
			SELECT CURRENT_DATE,
			       'cost_usd_total',
			       '',
			       COALESCE(SUM(cost_usd)::float8, 0),
			       COUNT(*)
			  FROM mem_cost_events
			 WHERE created_at >= CURRENT_DATE
			   AND created_at < CURRENT_DATE + INTERVAL '1 day'
			HAVING COUNT(*) > 0
			ON CONFLICT (day, metric_name, dimension) DO UPDATE SET
			  value = EXCLUDED.value,
			  sample_size = EXCLUDED.sample_size,
			  updated_at = NOW()
		`},
		// avg_surprise - how often the agent was wrong about tool outcomes.
		{"avg_surprise", `
			INSERT INTO mem_agent_metrics (day, metric_name, dimension, value, sample_size)
			SELECT CURRENT_DATE,
			       'avg_surprise',
			       '',
			       COALESCE(AVG(surprise_score), 0),
			       COUNT(*)
			  FROM mem_predictions
			 WHERE surprise_score IS NOT NULL
			   AND created_at >= CURRENT_DATE
			   AND created_at < CURRENT_DATE + INTERVAL '1 day'
			HAVING COUNT(*) > 0
			ON CONFLICT (day, metric_name, dimension) DO UPDATE SET
			  value = EXCLUDED.value,
			  sample_size = EXCLUDED.sample_size,
			  updated_at = NOW()
		`},
		// skill_success_rate per skill - one row per skill that ran today.
		{"skill_success_rate", `
			INSERT INTO mem_agent_metrics (day, metric_name, dimension, value, sample_size)
			SELECT CURRENT_DATE,
			       'skill_success_rate',
			       skill_name,
			       AVG(CASE WHEN success THEN 1.0 ELSE 0.0 END)::float8,
			       COUNT(*)
			  FROM mem_skill_runs
			 WHERE started_at >= CURRENT_DATE
			   AND started_at < CURRENT_DATE + INTERVAL '1 day'
			 GROUP BY skill_name
			HAVING COUNT(*) > 0
			ON CONFLICT (day, metric_name, dimension) DO UPDATE SET
			  value = EXCLUDED.value,
			  sample_size = EXCLUDED.sample_size,
			  updated_at = NOW()
		`},
		// tool_error_rate per tool - fraction of predictions that didn't match.
		{"tool_error_rate", `
			INSERT INTO mem_agent_metrics (day, metric_name, dimension, value, sample_size)
			SELECT CURRENT_DATE,
			       'tool_error_rate',
			       tool_name,
			       AVG(CASE WHEN matched = FALSE THEN 1.0 ELSE 0.0 END)::float8,
			       COUNT(*)
			  FROM mem_predictions
			 WHERE matched IS NOT NULL
			   AND created_at >= CURRENT_DATE
			   AND created_at < CURRENT_DATE + INTERVAL '1 day'
			 GROUP BY tool_name
			HAVING COUNT(*) > 0
			ON CONFLICT (day, metric_name, dimension) DO UPDATE SET
			  value = EXCLUDED.value,
			  sample_size = EXCLUDED.sample_size,
			  updated_at = NOW()
		`},
	}
	for _, r := range rollups {
		if _, err := pool.Exec(ctx, r.sql); err != nil {
			log.Warn("agent metrics rollup failed", "metric", r.name, "err", err)
		}
	}
	// Recompute the 14-day trailing baseline + sigma for every row written in
	// the last 30 days. One SQL pass; cheap relative to nightly reflection.
	if _, err := pool.Exec(ctx, `
		WITH stats AS (
			SELECT m.id,
			       AVG(prev.value) AS baseline,
			       STDDEV_SAMP(prev.value) AS sigma
			  FROM mem_agent_metrics m
			  LEFT JOIN mem_agent_metrics prev
			    ON prev.metric_name = m.metric_name
			   AND prev.dimension   = m.dimension
			   AND prev.day BETWEEN m.day - INTERVAL '14 days' AND m.day - INTERVAL '1 day'
			 WHERE m.day >= CURRENT_DATE - INTERVAL '30 days'
			 GROUP BY m.id
			HAVING COUNT(prev.value) >= 7
		)
		UPDATE mem_agent_metrics m
		   SET baseline_14d = stats.baseline,
		       sigma_14d    = stats.sigma,
		       updated_at   = NOW()
		  FROM stats
		 WHERE m.id = stats.id
	`); err != nil {
		log.Warn("agent metrics baseline recompute failed", "err", err)
	}
}
