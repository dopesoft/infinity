package cron

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/httpx"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/plan"
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

func (e *AgentExecutor) ExecuteJob(j Job) (summary RunSummary, err error) {
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
	e.tagCronRunSession(j.ID, sessionID)
	e.seedSelfImproveApprovals(sessionID, j)

	// Bind the job's name to this session so any plan the agent builds inside
	// the turn inherits it as the card headline (cron, card, and plan all read
	// the same name) instead of the model inventing its own title. Mechanic, not
	// prose — see tools.RegisterJobForSession.
	tools.RegisterJobForSession(sessionID, j.Name)
	defer tools.UnregisterJobForSession(sessionID)

	// Lock the turn to the job's tool_scope allowlist (if any). A cron like
	// inbox-triage carries {"tool_scope":{"allow":[...]}} in TargetConfig; this
	// makes every off-task tool physically invisible for the session so the
	// model can't wander into delegate / skill_optimize / recall / system_map and
	// quit with the work undone (Rule #1b: mechanic, not droppable prose). See
	// tools.RegisterToolScopeForSession + the scope visibility func in serve.go.
	if allow := toolScopeAllow(j.TargetConfig); len(allow) > 0 {
		tools.RegisterToolScopeForSession(sessionID, allow)
		defer tools.UnregisterToolScopeForSession(sessionID)
	}

	// Close this turn's plan bookkeeping the moment it ends. When the agent
	// finishes (even by giving up via TaskCompleted), its plan.step runs would
	// otherwise stay 'running' and the plan 'active' forever — a phantom
	// "Running" card on the Agent Work board (the 6.4-hour zombie the boss saw).
	// The reaper ticker is the safety net; this makes the close immediate.
	//
	// The settled count is stamped into the summary the SCHEDULER classifies
	// from: finalize runs before the caller sees the return values (deferred
	// func over named returns), but classifyOutcome runs after — so without
	// this stamp the plan looks complete by the time it's read and a run the
	// agent abandoned mid-plan would read "done" instead of "stopped early"
	// (the boss saw exactly that on the broken self-improve nights).
	defer func() {
		if n := e.finalizeSession(sessionID); n > 0 {
			if summary.Meta == nil {
				summary.Meta = map[string]any{}
			}
			summary.Meta["abandoned_steps"] = n
		}
	}()

	out := make(chan agent.RunEvent, 64)
	go func() {
		for range out {
			// drain
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Tag outbound HTTP from this turn with its session so a hard failure
	// (401/5xx) on any tool/connector call is attributed to this run and vetoes a
	// false-green outcome (the universal "Jarvis always sees his errors" guard).
	ctx = httpx.WithSession(ctx, sessionID)
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
		// A run that produced no turns did nothing — but it still happened, so
		// don't return a blank: stamp the counts so the classifier reads it as
		// "nothing needed" and the inbox/floor can speak for it. The empty
		// Summary is filled by the run-close floor (runs.Finish).
		return RunSummary{Meta: map[string]any{
			"session_id": sessionID,
			"turns":      0,
			"tool_calls": 0,
			"failures":   0,
		}}
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

// toolScopeAllow extracts the optional tool_scope.allow allowlist from a job's
// TargetConfig. Returns nil when the job carries no scope (the common case:
// full toolset). This is what lets a cron lock its turn to a specific toolset —
// e.g. inbox-triage to mail+surface tools only — as pure data on the job, with
// no per-job branch anywhere in the loop.
func toolScopeAllow(cfg json.RawMessage) []string {
	if len(cfg) == 0 {
		return nil
	}
	var parsed struct {
		ToolScope struct {
			Allow []string `json:"allow"`
		} `json:"tool_scope"`
	}
	if err := json.Unmarshal(cfg, &parsed); err != nil {
		return nil
	}
	return parsed.ToolScope.Allow
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

// finalizeSession closes the turn's plan/run bookkeeping right after Loop.Run
// returns, so a finished (or given-up) cron turn never leaves a phantom
// "Running" card on the Agent Work board. Returns the number of steps it had
// to force-settle — >0 means the agent walked away from an unfinished plan,
// which the outcome classifier must read as "stopped early", never "done".
// Best-effort + nil-safe; non-UUID (system_event) sessions have no UUID-keyed
// plans and are skipped.
func (e *AgentExecutor) finalizeSession(sessionID string) int {
	if e == nil || e.Pool == nil || sessionID == "" {
		return 0
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	n, err := plan.NewStore(e.Pool).FinalizeSession(ctx, sessionID)
	if err != nil {
		// Real failure → stderr (severity:error). Success is silent; the plan
		// reconcile ticker logs aggregate counts.
		log.Printf("cron finalize session %s: %v", sessionID, err)
	}
	return n
}

// tagCronRunSession stamps the freshly-minted session id onto the cron's
// in-flight mem_runs row immediately. The dashboard folds a cron's run card
// INTO the plan the turn creates by matching session id — but the scheduler
// only writes meta.session_id AFTER the turn finishes (its post-run SetMeta),
// so during the run the fold key is missing and the board shows TWO "Inbox
// triage" cards for one fire (the run card + the plan card). Stamping it at the
// start closes that gap. Best-effort + nil-safe; the overlap guard guarantees
// at most one running row per cron, so "most recent running" is unambiguous.
func (e *AgentExecutor) tagCronRunSession(cronID, sessionID string) {
	if e == nil || e.Pool == nil || cronID == "" || sessionID == "" {
		return
	}
	if _, err := uuid.Parse(sessionID); err != nil {
		return
	}
	_, _ = e.Pool.Exec(context.Background(), `
		UPDATE mem_runs
		   SET meta = COALESCE(meta, '{}'::jsonb) || jsonb_build_object('session_id', $2::text)
		 WHERE id = (
		     SELECT id FROM mem_runs
		      WHERE kind = 'cron' AND target_id = $1 AND status = 'running'
		      ORDER BY started_at DESC
		      LIMIT 1
		 )
	`, cronID, sessionID)
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
