package worldmodel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GoalsProvider implements agent.MemoryProvider so the agent's own active and
// blocked goals appear in the system prompt on every turn. Without this, the
// agent reasons without knowing what it's supposed to be doing across
// sessions: goals live in mem_agent_goals and surface to the heartbeat
// dashboard, but never enter the model's mind during inference.
//
// One thin slice: top-5 by status (blocked first), priority, due_at. Each row
// is rendered as a single line plus the next un-done plan step so the agent
// can pick up where it left off without scanning the full plan history.
type GoalsProvider struct {
	pool *pgxpool.Pool
}

func NewGoalsProvider(pool *pgxpool.Pool) *GoalsProvider {
	return &GoalsProvider{pool: pool}
}

// BuildSystemPrefix renders the <agent_goals> block. Returns "" when there
// are no live goals so the system prompt stays clean.
func (p *GoalsProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id::text, title, status, priority, blocker, plan, progress,
		       due_at, last_progress_at
		  FROM mem_agent_goals
		 WHERE status IN ('active', 'blocked')
		 ORDER BY
		   CASE status WHEN 'blocked' THEN 0 ELSE 1 END,
		   CASE priority WHEN 'high' THEN 0 WHEN 'med' THEN 1 ELSE 2 END,
		   due_at NULLS LAST
		 LIMIT 5
	`)
	if err != nil {
		return "", nil
	}
	defer rows.Close()

	var b strings.Builder
	b.WriteString("<agent_goals>\n")
	b.WriteString("Your own durable goals (mem_agent_goals). Log progress with goal_update; mark done/blocked as work changes.\n")
	count := 0
	for rows.Next() {
		var (
			id, title, status, priority, blocker, progress string
			planRaw                                        []byte
			dueAt                                          *time.Time
			lastProgressAt                                 time.Time
		)
		if err := rows.Scan(&id, &title, &status, &priority, &blocker, &planRaw, &progress, &dueAt, &lastProgressAt); err != nil {
			continue
		}
		count++
		fmt.Fprintf(&b, "- [%s · %s] %s (id=%s)\n", status, priority, title, id)
		if blocker != "" {
			fmt.Fprintf(&b, "    blocker: %s\n", trimGoalLine(blocker, 200))
		}
		if dueAt != nil {
			fmt.Fprintf(&b, "    due: %s\n", dueAt.Format("Mon Jan 2"))
		}
		if step := nextOpenStep(planRaw); step != "" {
			fmt.Fprintf(&b, "    next step: %s\n", trimGoalLine(step, 200))
		}
		if progress != "" {
			fmt.Fprintf(&b, "    last note: %s\n", trimGoalLine(lastProgressLine(progress), 200))
		}
		fmt.Fprintf(&b, "    last touched: %s\n", lastProgressAt.Format("2006-01-02 15:04"))
	}
	if count == 0 {
		return "", nil
	}
	b.WriteString("</agent_goals>")
	return b.String(), nil
}

// nextOpenStep walks the plan JSON (a list of {step, done}) and returns the
// first step where done=false. Empty when the plan is empty or fully done.
func nextOpenStep(planRaw []byte) string {
	if len(planRaw) == 0 {
		return ""
	}
	var plan []PlanItem
	if json.Unmarshal(planRaw, &plan) != nil {
		return ""
	}
	for _, item := range plan {
		if !item.Done && strings.TrimSpace(item.Step) != "" {
			return item.Step
		}
	}
	return ""
}

// lastProgressLine returns the final line of the running narrative. progress
// in mem_agent_goals is newline-appended, so the last line is the most recent
// note - the only one worth showing in the system prompt to keep it tight.
func lastProgressLine(progress string) string {
	progress = strings.TrimRight(progress, "\n")
	if idx := strings.LastIndex(progress, "\n"); idx >= 0 {
		return strings.TrimSpace(progress[idx+1:])
	}
	return strings.TrimSpace(progress)
}

func trimGoalLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
