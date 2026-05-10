package memory

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ForgetReport struct {
	TTLExpired      int `json:"ttl_expired"`
	LowValue        int `json:"low_value_evicted"`
	OverProjectCap  int `json:"over_project_cap"`
	DryRun          bool `json:"dry_run"`
}

const projectCap = 10_000

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
	return report, nil
}
