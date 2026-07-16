package proactive

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// TrustExecutor runs the actions the boss approved AFTER the gate that
// queued them gave up waiting.
//
// The gap it closes. Every gate (github, composio, claude_code, bridge,
// browser, phone, ward) follows the same shape: intercept a risky call,
// Queue a contract carrying {"tool", "input", "session_id", "project"} in
// action_spec, then BLOCK in WaitForDecision for its WaitTimeout - 15
// minutes in GitHubGate. Approve inside that window and the gate wakes,
// calls ConsumeApprovedForTool, and the tool runs in-session. Approve
// outside it and nothing runs the action, ever: the gate has already
// returned, the session is dead, and both HasRecentApprovalForTool and
// ConsumeApprovedForTool match on action_spec->>'session_id', so no later
// session can adopt the orphan. The row sits at status='approved'
// permanently while the boss believes he approved the thing.
//
// That is not a corner case, it is the main case for anything the boss
// reviews on his own schedule rather than the agent's. A drafted blog post
// queued by a 6am cron and read at noon is six hours outside every gate's
// window. Without this loop, "I'll read it and approve when I get a
// chance" cannot work at all.
//
// Deliberately generic (Rule #1). It reads only the two fields every gate
// already writes - the tool name and its input map - looks the tool up in
// the same registry the agent loop uses, and executes it. There is no
// per-tool branch, no per-source branch, and no knowledge of what any tool
// does. Add a gate tomorrow and it inherits deferred approval for free.
//
// A note on coherence, since it is surprising the first time. The agent
// was told "timed out waiting for approval" when the gate gave up, and may
// have said so to the boss. When the boss approves hours later the action
// fires anyway - because his approval IS the intent, and executing it is
// what he asked for. The audit trail stays honest about which path ran it:
// executed_at is set only here, never by the in-session gate.
type TrustExecutor struct {
	store    *TrustStore
	tools    ToolLookup
	grace    time.Duration
	interval time.Duration
	timeout  time.Duration
}

// ToolLookup is the narrow slice of *tools.Registry the executor needs.
// Kept as an interface so the executor can be exercised without standing up
// the full registry, and so the dependency is visibly one method wide.
type ToolLookup interface {
	Get(name string) (tools.Tool, bool)
}

const (
	// defaultExecutorGrace MUST stay strictly greater than the longest gate
	// WaitTimeout (currently 15 minutes, GitHubGate). See claim() - this is
	// the whole double-execution defense, not a tuning knob.
	defaultExecutorGrace = 20 * time.Minute
	// How often to sweep. The work is a single indexed query that returns
	// nothing almost every time, so this is cheap; a minute of extra latency
	// on an action the boss already waited hours to approve is irrelevant.
	defaultExecutorInterval = 60 * time.Second
	// Per-action ceiling, so one hung tool cannot stall the sweep forever.
	defaultExecutorTimeout = 2 * time.Minute
	// Bounded batch: a backlog drains over several ticks rather than firing
	// an unbounded burst of side effects in one go.
	executorBatchSize = 10
	// Written at claim time and cleared on success. If the process dies
	// mid-execution the row keeps this text, so a crashed run reads as
	// "unknown", never as a silent success.
	executorClaimedNote = "claimed by TrustExecutor; result not recorded (interrupted)"
)

func NewTrustExecutor(store *TrustStore, reg ToolLookup) *TrustExecutor {
	return &TrustExecutor{
		store:    store,
		tools:    reg,
		grace:    loadExecutorGrace(),
		interval: defaultExecutorInterval,
		timeout:  defaultExecutorTimeout,
	}
}

// loadExecutorGrace mirrors loadApprovalTTL. Overridable because the gate
// timeouts are themselves env-tunable in places, and the invariant
// (grace > longest WaitTimeout) has to be able to follow them.
func loadExecutorGrace() time.Duration {
	raw := strings.TrimSpace(os.Getenv("INFINITY_TRUST_EXECUTOR_GRACE"))
	if raw == "" {
		return defaultExecutorGrace
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultExecutorGrace
	}
	return d
}

// Start runs the sweep until ctx is cancelled. Non-blocking.
func (e *TrustExecutor) Start(ctx context.Context) {
	if e == nil || e.store == nil || e.store.pool == nil || e.tools == nil {
		log.Printf("TrustExecutor: not started (store or registry unwired) - " +
			"approvals that land after a gate times out will NOT run")
		return
	}
	infoLog.Printf("TrustExecutor: started (grace=%s interval=%s)", e.grace, e.interval)
	go func() {
		tick := time.NewTicker(e.interval)
		defer tick.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				if n, err := e.RunOnce(ctx); err != nil {
					log.Printf("TrustExecutor: sweep err=%v", err)
				} else if n > 0 {
					infoLog.Printf("TrustExecutor: executed %d deferred approval(s)", n)
				}
			}
		}
	}()
}

type claimedContract struct {
	id    string
	tool  string
	input map[string]any
}

// RunOnce claims and executes one batch. Exported so a test (or a future
// admin endpoint) can drive a sweep deterministically instead of waiting on
// the ticker.
func (e *TrustExecutor) RunOnce(ctx context.Context) (int, error) {
	claimed, err := e.claim(ctx)
	if err != nil || len(claimed) == 0 {
		return 0, err
	}
	for _, c := range claimed {
		e.execute(ctx, c)
	}
	return len(claimed), nil
}

// claim atomically takes ownership of the approved-but-never-run contracts
// and returns them. Ownership is the status flip itself: a row leaves
// 'approved' in the same statement that hands it to us, so two executors
// (or a restart mid-sweep) cannot both run the same action.
//
// THE RACE, and why `grace` is load-bearing. A gate sitting in
// WaitForDecision returns true on status 'approved' AND on 'consumed' (see
// GitHubGate.WaitForDecision). So if this query consumed a row that a live
// gate were still polling, the gate's very next tick would read 'consumed',
// return true, and run the tool a SECOND time - a double publish, a double
// send, a double charge. Nothing in the row says "a gate is still waiting
// on me", so the defense is time: a gate waits at most WaitTimeout from
// created_at, and we refuse to look at anything younger than grace, which
// is strictly longer. Past that point no gate can still be polling, so the
// claim is unopposed. Shortening grace below the longest gate timeout
// reintroduces double execution.
//
// The two action_spec guards are what keep this to real, replayable gate
// actions:
//   - a non-empty 'tool' is the registry key we need to run anything;
//   - 'input' must be a JSON object, which is the tool's argument map.
//
// The 'input' guard is also what excludes TrustStore.PreApproveTools rows.
// Those are inserted directly at status='approved' to record "the boss
// tapped this action in Studio, that tap is the approval", and carry only
// {"tool","session_id","preapproved"} - no input, because there is no
// captured call to replay. Executing one would invent an action the boss
// never queued. They have no 'input' key, so jsonb_typeof(...) returns SQL
// NULL, the predicate is not true, and they are never claimed.
func (e *TrustExecutor) claim(ctx context.Context) ([]claimedContract, error) {
	rows, err := e.store.pool.Query(ctx, `
		UPDATE mem_trust_contracts
		   SET status = 'consumed',
		       executed_at = NOW(),
		       execution_error = $3
		 WHERE id IN (
		     SELECT id
		       FROM mem_trust_contracts
		      WHERE status = 'approved'
		        AND COALESCE(action_spec->>'tool', '') <> ''
		        AND jsonb_typeof(action_spec->'input') = 'object'
		        AND created_at < NOW() - $1::interval
		      ORDER BY created_at
		      LIMIT $2
		      FOR UPDATE SKIP LOCKED
		 )
		RETURNING id::text, action_spec->>'tool', action_spec->'input'
	`, e.grace.String(), executorBatchSize, executorClaimedNote)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]claimedContract, 0, executorBatchSize)
	for rows.Next() {
		var (
			id, tool string
			rawInput []byte
		)
		if err := rows.Scan(&id, &tool, &rawInput); err != nil {
			return nil, err
		}
		input := map[string]any{}
		if err := json.Unmarshal(rawInput, &input); err != nil {
			// Claimed but undecodable: the row is already flipped, so record
			// the failure rather than dropping it on the floor silently.
			e.record(ctx, id, "action_spec.input is not a decodable object: "+err.Error())
			continue
		}
		out = append(out, claimedContract{id: id, tool: tool, input: input})
	}
	return out, rows.Err()
}

// execute runs one claimed action and records the outcome. Every exit path
// writes execution_error exactly once, so no row is left holding the
// interrupted-claim placeholder after a completed attempt.
func (e *TrustExecutor) execute(ctx context.Context, c claimedContract) {
	tool, ok := e.tools.Get(c.tool)
	if !ok {
		// The tool vanished between queue and approval (MCP server down, verb
		// renamed, extension unloaded). Nothing to run.
		log.Printf("TrustExecutor: contract=%s tool=%s not registered", c.id, c.tool)
		e.record(ctx, c.id, "tool not registered at execution time: "+c.tool)
		return
	}

	runCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()

	if _, err := tool.Execute(runCtx, c.input); err != nil {
		log.Printf("TrustExecutor: contract=%s tool=%s exec err=%v", c.id, c.tool, err)
		e.record(ctx, c.id, err.Error())
		return
	}
	infoLog.Printf("TrustExecutor: contract=%s ran %s (deferred approval)", c.id, c.tool)
	e.record(ctx, c.id, "")
}

// record writes the terminal outcome. Uses the parent ctx, not the
// per-action one, so an execution that failed by timing out can still
// persist why it failed.
func (e *TrustExecutor) record(ctx context.Context, id, execErr string) {
	if _, err := e.store.pool.Exec(ctx, `
		UPDATE mem_trust_contracts
		   SET execution_error = $2
		 WHERE id = $1::uuid
	`, id, execErr); err != nil {
		log.Printf("TrustExecutor: contract=%s record err=%v", id, err)
	}
}
