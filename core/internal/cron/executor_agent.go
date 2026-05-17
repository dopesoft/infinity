package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// AgentExecutor wraps *agent.Loop to satisfy the cron.Executor interface.
//
//   - system_event:        runs against a fresh main-session id (the loop's
//     GetOrCreateSession will create one if missing)
//   - isolated_agent_turn: spawns a brand-new session UUID per fire so the
//     isolated turn writes its findings to memory
//     without touching any live session
//
// Both cases drain the run-event channel and discard streaming output -
// cron jobs are background work; the agent loop's hooks pipeline still
// captures observations into mem_observations.
//
// Model selection is the loop's responsibility: passing "" delegates to
// Loop.Run's central resolver (SetActiveModelFn in serve.go), which
// picks up the boss's Studio selection. The cron executor never speaks
// to the settings store directly - single source of truth lives on the
// loop so cron, workflow executor, delegate, and ws all honor the
// active model with one wire.
type AgentExecutor struct {
	Loop *agent.Loop
	Pool *pgxpool.Pool
}

func NewAgentExecutor(l *agent.Loop, pool ...*pgxpool.Pool) *AgentExecutor {
	var p *pgxpool.Pool
	if len(pool) > 0 {
		p = pool[0]
	}
	return &AgentExecutor{Loop: l, Pool: p}
}

func (e *AgentExecutor) ExecuteJob(j Job) error {
	if e == nil || e.Loop == nil {
		return errors.New("no agent loop wired into cron executor")
	}
	if j.Target == "" {
		return errors.New("cron target prompt empty")
	}

	sessionID := j.Name + "-system"
	if j.JobKind == JobIsolatedAgentTurn {
		sessionID = uuid.NewString()
	}
	e.markCronSession(sessionID, j)

	out := make(chan agent.RunEvent, 64)
	go func() {
		for range out {
			// drain
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// nil steer channel: cron-driven turns aren't user-steerable.
	// Empty model string: Loop.Run resolves to the boss's active
	// selection via its activeModelFn, falling through to the provider
	// boot default only when nothing is set.
	if err := e.Loop.Run(ctx, sessionID, j.Target, "", nil, out); err != nil {
		return fmt.Errorf("cron run failed: %w", err)
	}
	e.markCronSession(sessionID, j)
	return nil
}

func (e *AgentExecutor) markCronSession(sessionID string, j Job) {
	if e == nil || e.Pool == nil || sessionID == "" {
		return
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return
	}
	origin, _ := json.Marshal(map[string]any{
		"cron_id":   j.ID,
		"cron_name": j.Name,
		"job_kind":  j.JobKind,
	})
	_, _ = e.Pool.Exec(context.Background(), `
		INSERT INTO mem_sessions (id, kind, origin_ref, started_at)
		VALUES ($1::uuid, 'cron', $2::jsonb, NOW())
		ON CONFLICT (id) DO UPDATE
		   SET kind       = 'cron',
		       origin_ref = CASE
		           WHEN mem_sessions.origin_ref = '{}'::jsonb THEN EXCLUDED.origin_ref
		           ELSE mem_sessions.origin_ref
		       END
	`, sessionID, string(origin))
}
