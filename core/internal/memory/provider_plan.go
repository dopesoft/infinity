package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PlanProvider implements agent.MemoryProvider, injecting the session's active
// plan (mem_plans / mem_plan_steps) into every turn.
//
// This is what closes the planning loop and makes long tasks DURABLE: after
// compaction, a restart, or a fresh session, the agent re-reads exactly where
// it left off - the ordered steps, their status, and an explicit "next step"
// pointer - instead of starting over. It complements the ephemeral todo_write
// dock (which dies with the background run) with a steerable plan the boss
// watches on the dashboard.
//
// Stays silent (returns "") when the session has no active plan, per the
// provider contract that empty substrate must not bloat the system prompt.
type PlanProvider struct {
	pool *pgxpool.Pool
}

func NewPlanProvider(pool *pgxpool.Pool) *PlanProvider {
	return &PlanProvider{pool: pool}
}

func (p *PlanProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.pool == nil || sessionID == "" {
		return "", nil
	}

	var (
		planID string
		title  string
		goal   string
		status string
	)
	err := p.pool.QueryRow(ctx, `
		SELECT id::text, title, goal, status
		  FROM mem_plans
		 WHERE session_id = $1::uuid AND status IN ('active','paused')
		 ORDER BY updated_at DESC LIMIT 1
	`, sessionID).Scan(&planID, &title, &goal, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", nil
	}

	rows, err := p.pool.Query(ctx, `
		SELECT idx, title, status, is_checkpoint, verify_required, result_summary
		  FROM mem_plan_steps WHERE plan_id = $1::uuid ORDER BY idx ASC
	`, planID)
	if err != nil {
		return "", nil
	}
	defer rows.Close()

	type stepRow struct {
		idx                    int
		title, status, note    string
		checkpoint, mustVerify bool
	}
	var steps []stepRow
	for rows.Next() {
		var s stepRow
		if err := rows.Scan(&s.idx, &s.title, &s.status, &s.checkpoint, &s.mustVerify, &s.note); err != nil {
			continue
		}
		steps = append(steps, s)
	}
	if len(steps) == 0 {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("<active_plan>\n")
	b.WriteString("Your durable plan for this task (survives across turns/restarts). Keep it current with plan_update as you work, and plan_verify before marking a step done. ")
	b.WriteString("If you've drifted from it, get back on it or revise it.\n")
	fmt.Fprintf(&b, "Plan: %s", title)
	if strings.TrimSpace(goal) != "" {
		fmt.Fprintf(&b, " — %s", goal)
	}
	b.WriteString("\n")

	nextStep := ""
	for _, s := range steps {
		marker := stepMarker(s.status)
		tags := ""
		if s.checkpoint {
			tags += " [checkpoint]"
		}
		if s.mustVerify {
			tags += " [verify]"
		}
		line := fmt.Sprintf("%s %d. %s%s", marker, s.idx+1, trimLessonLine(s.title, 160), tags)
		if strings.TrimSpace(s.note) != "" {
			line += fmt.Sprintf(" — %s", trimLessonLine(s.note, 120))
		}
		b.WriteString(line + "\n")
		if nextStep == "" && (s.status == "pending" || s.status == "in_progress" || s.status == "blocked") {
			nextStep = fmt.Sprintf("%d. %s", s.idx+1, s.title)
			if s.status == "blocked" {
				nextStep += " (blocked — replan it)"
			}
		}
	}
	if nextStep != "" {
		fmt.Fprintf(&b, "Next: %s\n", nextStep)
	}
	b.WriteString("</active_plan>")
	return b.String(), nil
}

func stepMarker(status string) string {
	switch status {
	case "done":
		return "[x]"
	case "in_progress":
		return "[~]"
	case "blocked":
		return "[!]"
	case "failed":
		return "[✗]"
	case "skipped":
		return "[-]"
	default:
		return "[ ]"
	}
}
