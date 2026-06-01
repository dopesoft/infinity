package proactive

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// GoalEntityUrgencyChecklist returns a Checklist that joins agent goals
// (mem_agent_goals) to the entities they depend on (mem_entities, via
// entity_id) and surfaces a Finding when a recent observation (last 6h)
// mentions that entity. This is what makes a goal feel "alive" - the
// agent doesn't just remember it has a goal about a person/project, it
// notices when that person/project shows up in the wild.
//
// Compose with DefaultChecklist + AgentGoalChecklist via ComposeChecklists.
// Cheap: a single JOIN + ILIKE limited to 5 findings; runs every heartbeat
// tick without LLM calls.
func GoalEntityUrgencyChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		rows, err := pool.Query(ctx, `
			SELECT g.id::text, e.id::text, g.title, e.kind, e.name,
			       COALESCE(o.raw_text, ''), o.created_at
			  FROM mem_agent_goals g
			  JOIN mem_entities    e ON e.id = g.entity_id
			  JOIN LATERAL (
			       SELECT raw_text, created_at
			         FROM mem_observations
			        WHERE created_at > NOW() - INTERVAL '6 hours'
			          AND raw_text IS NOT NULL
			          AND raw_text ILIKE '%' || e.name || '%'
			        ORDER BY created_at DESC
			        LIMIT 1
			       ) o ON true
			 WHERE g.status IN ('active', 'blocked')
			   AND e.status <> 'archived'
			 ORDER BY o.created_at DESC
			 LIMIT 5
		`)
		if err != nil {
			return nil, nil
		}
		defer rows.Close()

		var out []Finding
		for rows.Next() {
			var (
				goalID, entID, goalTitle, entKind, entName, raw string
			)
			var ignoredCreated interface{}
			if err := rows.Scan(&goalID, &entID, &goalTitle, &entKind, &entName, &raw, &ignoredCreated); err != nil {
				continue
			}
			snippet := raw
			if len(snippet) > 220 {
				snippet = snippet[:220] + "…"
			}
			out = append(out, Finding{
				Kind:      "pattern",
				Title:     fmt.Sprintf("Goal %q has new activity on %s %q", goalTitle, entKind, entName),
				Detail:    snippet,
				SourceTag: "goal_entity:" + goalID + ":" + entID,
			})
		}
		return out, nil
	}
}
