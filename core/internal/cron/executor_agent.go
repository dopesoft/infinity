package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

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
	e.seedSelfImproveApprovals(sessionID, j)

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

// selfImproveJobNames are the cron jobs whose autonomous sessions are allowed
// to push to GitHub + open/merge PRs unattended — but ONLY when the master env
// switch is on. Every other cron stays subject to the normal Trust gates.
func isSelfImproveJob(j Job) bool {
	switch j.Name {
	case "nightly-self-improve", "post-deploy-verify":
		return true
	}
	return false
}

func selfImproveAutonomyEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INFINITY_SELF_IMPROVE_AUTONOMY"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

func selfImproveApprovalTTL() time.Duration {
	if v := strings.TrimSpace(os.Getenv("INFINITY_SELF_IMPROVE_APPROVAL_TTL")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
	}
	return 8 * time.Hour
}

// seedSelfImproveApprovals pre-inserts session-scoped Trust approvals so the
// nightly self-improve / post-deploy-verify sessions can push + open/merge PRs
// without a human awake at 3am. It runs ONLY when INFINITY_SELF_IMPROVE_AUTONOMY
// is on AND the job is one of the self-improve crons. When the env is off it
// seeds nothing, so every outward call routes through the normal Trust queue and
// the loop degrades gracefully to "prepare changes, queue the push for morning".
//
// CRITICAL: the rows MUST use source='claude_code_gate' and carry tool +
// session_id in action_spec, because all three gates (ClaudeCodeGate,
// BridgeGate, GitHubGate) resolve approvals through
// TrustStore.HasRecentApprovalForTool / ConsumeApprovedForTool, which hardcode
// that source. A row with any other source is invisible to the lookup and the
// push still blocks. See core/internal/proactive/trust.go.
func (e *AgentExecutor) seedSelfImproveApprovals(sessionID string, j Job) {
	if e == nil || e.Pool == nil || sessionID == "" {
		return
	}
	if !selfImproveAutonomyEnabled() || !isSelfImproveJob(j) {
		return
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return
	}
	// The outward verbs the self-improve recipe needs. Reads (status/diff/log)
	// and non-destructive bash (build/test/commit) already run unattended via
	// the gates; these are the ones that otherwise queue.
	toolNames := []string{
		"git_push",                    // cloud-bridge push (BridgeGate)
		"github__push_files",          // GitHub MCP push (GitHubGate)
		"github__create_pull_request", // GitHubGate
		"github__merge_pull_request",  // GitHubGate
		"claude_code__bash",           // ClaudeCodeGate (destructive-bash path)
	}
	ttl := selfImproveApprovalTTL()
	for _, tool := range toolNames {
		spec, err := json.Marshal(map[string]any{
			"tool":              tool,
			"session_id":        sessionID,
			"auto_self_improve": true,
			"ttl":               ttl.String(),
		})
		if err != nil {
			continue
		}
		_, _ = e.Pool.Exec(context.Background(), `
			INSERT INTO mem_trust_contracts
				(title, risk_level, source, action_spec, reasoning, preview, status, decided_at)
			VALUES ($1, 'high', 'claude_code_gate', $2::jsonb, $3, $4, 'approved', NOW())
		`,
			"auto self-improve pre-approval: "+tool,
			string(spec),
			"Pre-seeded by INFINITY_SELF_IMPROVE_AUTONOMY for self-improve session "+sessionID+". Session-scoped; expires with the gate TTL.",
			"session-scoped auto-approval for "+tool,
		)
	}
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
