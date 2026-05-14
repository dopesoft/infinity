package proactive

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentGoalChecklist returns a Checklist that scans the agent's own goals
// (mem_agent_goals, Phase 5) and resurfaces the ones that need attention —
// goals that are blocked, due soon, or stalled (no progress recorded in a
// while). This is the autonomous-pursuit loop: a goal the agent set and
// then forgot about gets pulled back into view on every heartbeat tick, so
// the agent re-plans or closes it instead of letting it rot.
//
// Compose with DefaultChecklist + CuriosityChecklist via ComposeChecklists.
func AgentGoalChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		rows, err := pool.Query(ctx, `
			SELECT title, status, blocker, due_at
			  FROM mem_agent_goals
			 WHERE status IN ('active', 'blocked')
			   AND (
			        status = 'blocked'
			     OR (due_at IS NOT NULL AND due_at <= NOW() + INTERVAL '48 hours')
			     OR last_progress_at <= NOW() - INTERVAL '3 days'
			   )
			 ORDER BY
			   CASE status WHEN 'blocked' THEN 0 ELSE 1 END,
			   CASE priority WHEN 'high' THEN 0 WHEN 'med' THEN 1 ELSE 2 END,
			   due_at NULLS LAST
			 LIMIT 5
		`)
		if err != nil {
			// Degrade quietly — migration 020 may not be applied yet.
			return nil, nil
		}
		defer rows.Close()

		var out []Finding
		for rows.Next() {
			var (
				title, status, blocker string
				dueAt                  *time.Time
			)
			if err := rows.Scan(&title, &status, &blocker, &dueAt); err != nil {
				continue
			}
			detail := "No progress recorded in a while — revisit or close it."
			switch {
			case status == "blocked":
				detail = "Blocked: " + blocker
			case dueAt != nil && dueAt.Before(time.Now().Add(48*time.Hour)):
				detail = fmt.Sprintf("Due %s — pick this back up.", dueAt.Format("Mon Jan 2"))
			}
			out = append(out, Finding{
				Kind:   "pattern",
				Title:  "Goal needs attention: " + title,
				Detail: detail,
			})
		}
		return out, nil
	}
}
