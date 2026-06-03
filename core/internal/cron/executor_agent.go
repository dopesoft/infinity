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
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/tools"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// CronReauthParker parks a credential-failed cron turn so the existing reauth
// poller can resume it once the boss reconnects the model. Satisfied by
// *reauth.Store; kept as a local interface so cron doesn't import reauth.
type CronReauthParker interface {
	Park(ctx context.Context, sessionID, provider, model, userText, reason string) error
}

// CronBossNotifier surfaces a message to the boss (push + any live chat).
// Satisfied by the same *watchNotifier the reauth poller uses.
type CronBossNotifier interface {
	Notify(sessionID, text string)
}

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
	Loop   *agent.Loop
	Pool   *pgxpool.Pool
	reauth CronReauthParker
	notify CronBossNotifier
}

func NewAgentExecutor(l *agent.Loop, pool ...*pgxpool.Pool) *AgentExecutor {
	var p *pgxpool.Pool
	if len(pool) > 0 {
		p = pool[0]
	}
	return &AgentExecutor{Loop: l, Pool: p}
}

// SetReauthHandler wires park-and-resume for credential failures hit inside a
// scheduled run. Called from serve.go after the push sender + reauth store
// exist (AgentExecutor is constructed earlier, before either is ready).
func (e *AgentExecutor) SetReauthHandler(p CronReauthParker, n CronBossNotifier) {
	if e == nil {
		return
	}
	e.reauth = p
	e.notify = n
}

func (e *AgentExecutor) ExecuteJob(j Job) (RunSummary, error) {
	if e == nil || e.Loop == nil {
		return RunSummary{}, errors.New("no agent loop wired into cron executor")
	}
	if j.Target == "" {
		return RunSummary{}, errors.New("cron target prompt empty")
	}

	sessionID := j.Name + "-system"
	if j.JobKind == JobIsolatedAgentTurn {
		sessionID = uuid.NewString()
	}
	e.markCronSession(sessionID, j)
	e.seedSelfImproveApprovals(sessionID, j)

	// Bind the job's name to this session so any plan the agent builds inside
	// the turn inherits it as the card headline (cron, card, and plan all read
	// the same name) instead of the model inventing its own title. Mechanic, not
	// prose — see tools.RegisterJobForSession.
	tools.RegisterJobForSession(sessionID, j.Name)
	defer tools.UnregisterJobForSession(sessionID)

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
		// Credential failure (revoked/expired model token) is NOT a job
		// failure — it's a "the boss needs to reconnect" event. Park the turn
		// so the reauth poller resumes it the moment the model is healthy
		// again, and notify the boss instead of screaming a raw 401 into the
		// run log every fire. This is the safety net at the cron boundary: it
		// catches auth errors no matter where in the loop they surfaced
		// (token mint, stream, or tool), which the loop's own stream-scoped
		// parker can miss.
		if e.reauth != nil && llm.IsAuthFailure(err.Error()) {
			if perr := e.reauth.Park(context.Background(), sessionID, "", "", j.Target, err.Error()); perr == nil {
				if e.notify != nil {
					e.notify.Notify(sessionID, fmt.Sprintf(
						"⚠️ Your model needs reconnecting — the scheduled \"%s\" run is paused. Reconnect it in Settings and it'll finish automatically.",
						j.Name))
				}
				e.markCronSession(sessionID, j)
				return RunSummary{Summary: "Paused — your model needs reconnecting. This run will finish automatically once it's healthy again."}, nil
			}
			// Park failed (no DB / store nil) — fall through to hard error.
		}
		// Even on a hard failure, salvage what the agent did before it broke so
		// the run detail explains the failure in context, not just the raw error.
		return e.summarizeRun(sessionID), fmt.Errorf("cron run failed: %w", err)
	}
	e.markCronSession(sessionID, j)
	return e.summarizeRun(sessionID), nil
}

// summarizeRun reads the turns the agent just wrote for this cron session and
// distills them into a boss-facing "what I did / how it went / outcome"
// narrative for the mem_runs row. The richest source is the agent's own final
// turn (its closing summary / assistant text) — that's the agent narrating the
// run in its own words; we prepend a one-line stat header (turns · tool calls ·
// any failures) so the card reads at a glance. Best-effort and nil-safe: if the
// session isn't a UUID (legacy system_event sessions) or has no turns yet, we
// return the zero RunSummary and the run still closes as it did before.
func (e *AgentExecutor) summarizeRun(sessionID string) RunSummary {
	if e == nil || e.Pool == nil || sessionID == "" {
		return RunSummary{}
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		// system_event sessions use a non-UUID id ("<name>-system") and don't
		// write UUID-keyed turns; nothing to read.
		return RunSummary{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	rows, err := e.Pool.Query(ctx, `
		SELECT COALESCE(assistant_text,''), COALESCE(summary,''),
		       COALESCE(tool_call_count,0), COALESCE(status,''), COALESCE(error,'')
		  FROM mem_turns
		 WHERE session_id = $1::uuid
		 ORDER BY started_at ASC
	`, sessionID)
	if err != nil {
		return RunSummary{}
	}
	defer rows.Close()

	var (
		turns       int
		toolCalls   int
		failures    int
		lastSummary string
		lastText    string
	)
	for rows.Next() {
		var assistant, summary, status, turnErr string
		var tc int
		if err := rows.Scan(&assistant, &summary, &tc, &status, &turnErr); err != nil {
			return RunSummary{}
		}
		turns++
		toolCalls += tc
		if status == "errored" || strings.TrimSpace(turnErr) != "" {
			failures++
		}
		if s := strings.TrimSpace(summary); s != "" {
			lastSummary = s
		}
		if t := strings.TrimSpace(assistant); t != "" {
			lastText = t
		}
	}
	if turns == 0 {
		return RunSummary{}
	}

	// Prefer the agent's FULL closing message (its actual report of what it
	// did) so the OPENED detail shows everything — fall back to the short turn
	// summary only when there's no assistant text. This fixes the boss seeing a
	// clipped "summary of a summary": result_summary used to be the ~140-char
	// turn summary, so the Agent Work modal cut off mid-sentence. The kanban
	// CARD still CSS-clamps this to 2 lines for a clean preview; the detail
	// renders it in full. Cap generously so a real report isn't choked, while
	// still bounding a runaway closing message.
	narrative := lastText
	if narrative == "" {
		narrative = lastSummary
	}
	narrative = clampNarrative(narrative, 4000)

	header := fmt.Sprintf("%d turn%s · %d tool call%s",
		turns, plural(turns), toolCalls, plural(toolCalls))
	if failures > 0 {
		header += fmt.Sprintf(" · %d failed", failures)
	}
	summary := header
	if narrative != "" {
		summary += "\n" + narrative
	}
	return RunSummary{
		Summary: summary,
		Meta: map[string]any{
			"session_id": sessionID,
			"turns":      turns,
			"tool_calls": toolCalls,
			"failures":   failures,
		},
	}
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// clampNarrative trims an agent narrative to max runes on a word boundary so a
// runaway closing message can't bloat the run row or the kanban card.
func clampNarrative(s string, max int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	cut := string(r[:max])
	if i := strings.LastIndexAny(cut, " \n"); i > max/2 {
		cut = cut[:i]
	}
	return strings.TrimSpace(cut) + "…"
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
