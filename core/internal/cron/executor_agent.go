package cron

import (
	"context"
	"errors"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/settings"
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
// Both cases drain the run-event channel and discard streaming output.
// Cron jobs are background work, but they still need the same model routing
// truth as live chat. That means we pass the persisted selected model through
// when one exists, instead of silently falling back to the provider default.
// Falling back was what routed background work onto gpt-5-codex and broke
// ChatGPT-account OAuth runs.
type AgentExecutor struct {
	Loop     *agent.Loop
	Settings *settings.Store
}

func NewAgentExecutor(l *agent.Loop, s *settings.Store) *AgentExecutor {
	return &AgentExecutor{Loop: l, Settings: s}
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

	out := make(chan agent.RunEvent, 64)
	go func() {
		for range out {
			// drain
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := ""
	if e.Settings != nil {
		model = e.Settings.GetModel(ctx)
	}

	// nil steer channel: cron-driven turns aren't user-steerable.
	// Model routing: use the persisted selected model when the boss picked
	// one, otherwise let the loop fall back to the active provider default.
	if err := e.Loop.Run(ctx, sessionID, j.Target, model, nil, out); err != nil {
		return fmt.Errorf("cron run failed: %w", err)
	}
	return nil
}
