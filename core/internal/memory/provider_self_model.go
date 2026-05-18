package memory

import (
	"context"
	"fmt"
	"math"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// SelfModelProvider implements agent.MemoryProvider and surfaces a
// <self_model_alert> block when ANY agent metric has drifted >2σ off its
// 14-day baseline. Silent on normal days - the prompt stays clean and the
// agent only notices itself when there's something to notice.
//
// Reads from mem_agent_metrics; populated by RollupAgentMetrics during
// nightly consolidation.
type SelfModelProvider struct {
	pool *pgxpool.Pool
}

func NewSelfModelProvider(pool *pgxpool.Pool) *SelfModelProvider {
	return &SelfModelProvider{pool: pool}
}

func (p *SelfModelProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT metric_name, dimension, value, baseline_14d, sigma_14d, sample_size
		  FROM mem_agent_metrics
		 WHERE day = (
		         SELECT MAX(day) FROM mem_agent_metrics
		        )
		   AND baseline_14d IS NOT NULL
		   AND sigma_14d   IS NOT NULL
		   AND sigma_14d > 0
		   AND ABS(value - baseline_14d) > 2 * sigma_14d
		 ORDER BY ABS(value - baseline_14d) / NULLIF(sigma_14d, 0) DESC
		 LIMIT 5
	`)
	if err != nil {
		return "", nil
	}
	defer rows.Close()

	var b strings.Builder
	count := 0
	for rows.Next() {
		var (
			metric, dimension          string
			value, baseline, sigma     float64
			samples                    int
		)
		if err := rows.Scan(&metric, &dimension, &value, &baseline, &sigma, &samples); err != nil {
			continue
		}
		if count == 0 {
			b.WriteString("<self_model_alert>\n")
			b.WriteString("Today's behavior is unusual versus the 14-day baseline. Take this into account before doing more of the same.\n")
		}
		direction := "above"
		if value < baseline {
			direction = "below"
		}
		zscore := math.Abs(value-baseline) / sigma
		label := metric
		if dimension != "" {
			label = fmt.Sprintf("%s/%s", metric, dimension)
		}
		fmt.Fprintf(&b, "- %s: %.3f (%.1fσ %s baseline %.3f ±%.3f, n=%d)\n",
			label, value, zscore, direction, baseline, sigma, samples)
		count++
	}
	if count == 0 {
		return "", nil
	}
	b.WriteString("</self_model_alert>")
	return b.String(), nil
}
