package proactive

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/dopesoft/infinity/core/internal/surface"
)

// SubstrateSurfaceChecklist mirrors the noteworthy state of the assembly
// substrate onto the generic dashboard surface - so the agent's goals, its
// broken extensions, and its failing capabilities show up on the dashboard
// with ZERO bespoke Studio code.
//
// This is Rule #1 applied to the substrate itself: the dashboard's generic
// SurfaceCard already renders whatever `surface` key gets written, so a
// substrate phase doesn't need its own page - it just writes through the
// surface contract. Each heartbeat tick this reconciles two surfaces:
//
//	surface='agenda' - Jarvis's OWN active / blocked goals (mem_agent_goals).
//	                   Distinct from the boss's Pursuits card (mem_pursuits) -
//	                   this is what the agent is working toward, not the
//	                   boss's habits.
//	surface='health' - extensions that failed to activate, plus any
//	                   skill/workflow/tool whose last 3 runs all failed
//
// Reconcile = items for state that's no longer noteworthy (a goal finished,
// an extension fixed) are dismissed, so the cards stay current.
func SubstrateSurfaceChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		store := surface.NewStore(pool, nil)
		surfaceAgentGoals(ctx, pool, store)
		surfaceSubstrateHealth(ctx, pool, store)
		// The surface items ARE the output - no heartbeat Findings needed
		// (AgentGoalChecklist already emits the needs-attention nudges).
		return nil, nil
	}
}

// surfaceAgentGoals upserts one card per active/blocked goal onto the
// 'agenda' surface (Jarvis's agenda - not the boss's Pursuits), then
// dismisses cards for goals that are no longer active.
func surfaceAgentGoals(ctx context.Context, pool *pgxpool.Pool, store *surface.Store) {
	rows, err := pool.Query(ctx, `
		SELECT id::text, title, description, status, priority, blocker
		  FROM mem_agent_goals
		 WHERE status IN ('active', 'blocked')
		 ORDER BY created_at DESC
		 LIMIT 30
	`)
	if err != nil {
		return // migration 020 may not be applied - degrade quietly
	}
	defer rows.Close()

	desired := map[string]bool{}
	for rows.Next() {
		var id, title, desc, status, priority, blocker string
		if err := rows.Scan(&id, &title, &desc, &status, &priority, &blocker); err != nil {
			continue
		}
		extID := "goal-" + id
		desired[extID] = true

		imp := 55
		switch {
		case status == "blocked":
			imp = 80
		case priority == "high":
			imp = 70
		case priority == "low":
			imp = 45
		}
		body := desc
		if status == "blocked" && blocker != "" {
			body = "Blocked: " + blocker
		}
		_, _ = store.Upsert(ctx, &surface.Item{
			Surface:    "agenda",
			Kind:       "goal",
			Source:     "agent-goals",
			ExternalID: extID,
			Title:      title,
			Subtitle:   status + " · " + priority,
			Body:       body,
			Importance: &imp,
			Metadata:   map[string]any{"goal_id": id, "status": status, "priority": priority},
		})
	}
	reconcileSurface(ctx, store, "agenda", "agent-goals", desired)
}

// surfaceSubstrateHealth upserts a card for anything in the substrate that
// is broken - an extension that failed to activate, or a capability whose
// last three runs all failed - onto the 'health' surface.
func surfaceSubstrateHealth(ctx context.Context, pool *pgxpool.Pool, store *surface.Store) {
	desired := map[string]bool{}

	// Extensions that failed to activate.
	if rows, err := pool.Query(ctx, `
		SELECT name, kind, last_error
		  FROM mem_extensions
		 WHERE enabled = TRUE AND status = 'error'
	`); err == nil {
		for rows.Next() {
			var name, kind, lastErr string
			if rows.Scan(&name, &kind, &lastErr) != nil {
				continue
			}
			extID := "ext-" + name
			desired[extID] = true
			imp := 75
			_, _ = store.Upsert(ctx, &surface.Item{
				Surface:          "health",
				Kind:             "alert",
				Source:           "substrate-health",
				ExternalID:       extID,
				Title:            fmt.Sprintf("Extension %q failed to activate", name),
				Subtitle:         kind + " extension",
				Body:             lastErr,
				Importance:       &imp,
				ImportanceReason: "A registered capability is broken",
			})
		}
		rows.Close()
	}

	// Skills / workflows / tools whose last 3 recorded outcomes all failed.
	if rows, err := pool.Query(ctx, `
		SELECT subject_kind, subject_name FROM (
		  SELECT subject_kind, subject_name,
		         array_agg(outcome ORDER BY created_at DESC) AS recent
		  FROM (
		    SELECT subject_kind, subject_name, outcome, created_at,
		           row_number() OVER (
		             PARTITION BY subject_kind, subject_name
		             ORDER BY created_at DESC
		           ) AS rn
		      FROM mem_evals
		  ) ranked
		  WHERE rn <= 3
		  GROUP BY subject_kind, subject_name
		) agg
		WHERE recent = ARRAY['failure','failure','failure']::text[]
	`); err == nil {
		for rows.Next() {
			var kind, name string
			if rows.Scan(&kind, &name) != nil {
				continue
			}
			extID := "eval-" + kind + "-" + name
			desired[extID] = true
			imp := 70
			_, _ = store.Upsert(ctx, &surface.Item{
				Surface:          "health",
				Kind:             "alert",
				Source:           "substrate-health",
				ExternalID:       extID,
				Title:            fmt.Sprintf("%s %q is failing", kind, name),
				Subtitle:         "last 3 runs failed",
				Importance:       &imp,
				ImportanceReason: "Regressed - the last 3 recorded outcomes were all failures",
			})
		}
		rows.Close()
	}

	reconcileSurface(ctx, store, "health", "substrate-health", desired)
}

// reconcileSurface dismisses open items on a surface - written by `source`
// - whose external_id is no longer in the desired set. This is what keeps
// the cards current: state that resolved drops off the dashboard.
func reconcileSurface(ctx context.Context, store *surface.Store, surfaceKey, source string, desired map[string]bool) {
	items, err := store.ListBySurface(ctx, surfaceKey, 200)
	if err != nil {
		return
	}
	dismissed := surface.StatusDismissed
	for _, it := range items {
		if it.Source != source || it.ExternalID == "" || desired[it.ExternalID] {
			continue
		}
		_ = store.Update(ctx, it.ID, surface.Patch{Status: &dismissed})
	}
}

var _ = time.Now
