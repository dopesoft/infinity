package proactive

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultChecklist returns a checklist function suitable for the v1 heartbeat.
// It performs the cheap, deterministic checks that don't require an LLM:
//
//   • outcomes overdue (mem_outcomes.follow_up_at < NOW())
//   • patterns recently observed (mem_patterns.status = 'open')
//   • skills with falling success rates
//
// Phase 6 will layer in LLM-driven items (curiosity, surprise, security scan).
func DefaultChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, h *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		var findings []Finding

		// Overdue outcomes
		rows, err := pool.Query(ctx, `
			SELECT id::text, decision_text, follow_up_at
			  FROM mem_outcomes
			 WHERE status = 'pending'
			   AND follow_up_at IS NOT NULL
			   AND follow_up_at <= NOW()
			 ORDER BY follow_up_at ASC
			 LIMIT 10
		`)
		if err == nil {
			for rows.Next() {
				var id, txt string
				var due time.Time
				if err := rows.Scan(&id, &txt, &due); err == nil {
					findings = append(findings, Finding{
						Kind:   "outcome",
						Title:  "Decision follow-up overdue",
						Detail: fmt.Sprintf("%s — %s", txt, due.Format(time.RFC3339)),
					})
				}
			}
			rows.Close()
		}

		// Open patterns (Phase 6 promotes; Phase 5 just surfaces)
		rows, err = pool.Query(ctx, `
			SELECT id::text, description, occurrences
			  FROM mem_patterns
			 WHERE status = 'open'
			 ORDER BY occurrences DESC, last_seen_at DESC
			 LIMIT 5
		`)
		if err == nil {
			for rows.Next() {
				var id, desc string
				var n int
				if err := rows.Scan(&id, &desc, &n); err == nil {
					findings = append(findings, Finding{
						Kind:   "pattern",
						Title:  fmt.Sprintf("Repeated request seen %d×", n),
						Detail: desc,
					})
				}
			}
			rows.Close()
		}

		// Skill error rates
		rows, err = pool.Query(ctx, `
			SELECT skill_name,
			       COUNT(*) FILTER (WHERE NOT success) AS fails,
			       COUNT(*) AS total
			  FROM mem_skill_runs
			 WHERE started_at > NOW() - INTERVAL '24 hours'
			 GROUP BY skill_name
			HAVING COUNT(*) FILTER (WHERE NOT success) >= 2
			   AND COUNT(*) FILTER (WHERE NOT success) * 2 > COUNT(*)
		`)
		if err == nil {
			for rows.Next() {
				var name string
				var fails, total int
				if err := rows.Scan(&name, &fails, &total); err == nil {
					findings = append(findings, Finding{
						Kind:   "self_heal",
						Title:  fmt.Sprintf("Skill %s failing", name),
						Detail: fmt.Sprintf("%d failures of %d runs in last 24h", fails, total),
					})
				}
			}
			rows.Close()
		}

		return findings, nil
	}
}
