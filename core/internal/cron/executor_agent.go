package cron

import (
	"context"
	"errors"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/google/uuid"
)

// AgentExecutor wraps *agent.Loop to satisfy the cron.Executor interface.
//
//   • system_event:        runs against a fresh main-session id (the loop's
//                          GetOrCreateSession will create one if missing)
//   • isolated_agent_turn: spawns a brand-new session UUID per fire so the
//                          isolated turn writes its findings to memory
//                          without touching any live session
//
// Both cases drain the run-event channel and discard streaming output —
// cron jobs are background work; the agent loop's hooks pipeline still
// captures observations into mem_observations.
type AgentExecutor struct {
	Loop *agent.Loop
}

func NewAgentExecutor(l *agent.Loop) *AgentExecutor { return &AgentExecutor{Loop: l} }

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

	out := make(chan agent.RunEvent, 64)
	go func() {
		for range out {
			// drain
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// nil steer channel: cron-driven turns aren't user-steerable.
	if err := e.Loop.Run(ctx, sessionID, j.Target, nil, out); err != nil {
		return fmt.Errorf("cron run failed: %w", err)
	}
	return nil
}
