// Self-improve tools — the two generic building blocks the nightly
// self-improvement skills (`nightly-self-improve`, `post-deploy-verify`) need
// to close their loop. Per Rule #1 the cognition lives in the SKILL.md bodies;
// these are dumb, generic verbs:
//
//   code_proposal_decide - mark a mem_code_proposals row applied/rejected/approved
//   deploy_status        - read "is the running binary behind main?" (running
//                          SHA vs main HEAD), so the verify skill knows whether
//                          its push landed or bricked
//
// Both are wired in serve.go via RegisterSelfImproveTools. Narrow interfaces +
// an injected function keep this package free of import cycles (tools must not
// import voyager or server).

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CodeProposalDecider is the slice of *voyager.Manager the code_proposal_decide
// tool needs. The concrete *voyager.Manager satisfies it directly.
type CodeProposalDecider interface {
	DecideCodeProposal(ctx context.Context, id, decision, note string) error
}

// selfImproveCrons are the cron names the loop is composed of. Pausing both is
// the conversational kill switch; resuming re-arms it.
var selfImproveCrons = []string{"nightly-self-improve", "post-deploy-verify"}

// RegisterSelfImproveTools wires the self-improve verbs. No-op for any nil
// dependency so chat-only / no-DB deployments don't break registration.
//
// sched + pool power the self_improve_control tool (pause/resume/status) so the
// boss can drive the loop by voice/chat exactly like the Cron tab does.
func RegisterSelfImproveTools(r *Registry, decider CodeProposalDecider, deployFn func() any, pool *pgxpool.Pool, sched CronScheduler) {
	if r == nil {
		return
	}
	if decider != nil {
		r.Register(&codeProposalDecide{decider: decider, deployFn: deployFn})
	}
	if deployFn != nil {
		r.Register(&deployStatusTool{fn: deployFn})
		r.Register(&deployStatusRefreshTool{fn: deployFn})
	}
	if pool != nil && sched != nil {
		r.Register(&selfImproveControl{pool: pool, sched: sched})
	}
}

// ── self_improve_control ──────────────────────────────────────────────────

type selfImproveControl struct {
	pool  *pgxpool.Pool
	sched CronScheduler
}

func (t *selfImproveControl) Name() string { return "self_improve_control" }
func (t *selfImproveControl) Description() string {
	return "Control your own autonomous nightly self-improvement loop by voice/chat — the same thing the Cron tab does. 'pause' is the kill switch: it disables both self-improve crons (nightly-self-improve + post-deploy-verify) so nothing edits/pushes code. 'resume' re-enables them. 'status' reports whether the crons are enabled and whether push-autonomy + skill auto-promote are armed (env). Use when the boss says 'pause/stop self-improving', 'resume self-improvement', or 'are you allowed to edit your own code right now?'."
}
func (t *selfImproveControl) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"pause", "resume", "status"},
				"description": "pause = disable both self-improve crons (kill switch); resume = enable them; status = report current posture.",
			},
		},
		"required": []string{"action"},
	}
}
func (t *selfImproveControl) Execute(ctx context.Context, in map[string]any) (string, error) {
	action := strString(in, "action")
	switch action {
	case "pause", "resume":
		enabled := action == "resume"
		if _, err := t.pool.Exec(ctx,
			`UPDATE mem_crons SET enabled = $1 WHERE name = ANY($2)`,
			enabled, selfImproveCrons); err != nil {
			return "", err
		}
		if err := t.sched.Reload(ctx); err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"ok":true,"action":%q,"crons_enabled":%v,"crons":["nightly-self-improve","post-deploy-verify"]}`, action, enabled), nil
	case "status":
		var nightly, verify bool
		_ = t.pool.QueryRow(ctx, `SELECT enabled FROM mem_crons WHERE name = 'nightly-self-improve'`).Scan(&nightly)
		_ = t.pool.QueryRow(ctx, `SELECT enabled FROM mem_crons WHERE name = 'post-deploy-verify'`).Scan(&verify)
		b, _ := json.Marshal(map[string]any{
			"nightly_self_improve_enabled": nightly,
			"post_deploy_verify_enabled":   verify,
			"push_autonomy_armed":          envOn("INFINITY_SELF_IMPROVE_AUTONOMY"),
			"skill_autopromote_armed":      envOn("INFINITY_VOYAGER_AUTOPROMOTE"),
			"note":                         "Loop only acts when the crons are enabled AND push_autonomy_armed is true (else it prepares changes and queues the push). Env changes require a core restart to take effect.",
		})
		return string(b), nil
	default:
		return "", errors.New("action must be pause, resume, or status")
	}
}

func envOn(key string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(key))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// ── code_proposal_decide ──────────────────────────────────────────────────

type codeProposalDecide struct {
	decider  CodeProposalDecider
	deployFn func() any // injected from serve.go; nil in tests that don't need the gate
}

func (t *codeProposalDecide) Name() string { return "code_proposal_decide" }
func (t *codeProposalDecide) Description() string {
	return "Record the outcome of a code proposal (mem_code_proposals row). Use after you've actually applied an approved change ('applied'), decided to take it on ('approved'), or given up on it ('rejected'). Prefix the note with [auto] when you (the autonomous self-improve loop) applied it unattended, so the Lab can show it landed without the boss. This only records status — it does not itself edit any file."
}
func (t *codeProposalDecide) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"id":       map[string]any{"type": "string", "description": "UUID of the mem_code_proposals row."},
			"decision": map[string]any{"type": "string", "enum": []string{"approved", "rejected", "applied"}, "description": "applied = you made + verified the change; rejected = not doing it (say why in note); approved = intent to do it later."},
			"note":     map[string]any{"type": "string", "description": "Why. For autonomous applies/reverts, prefix with [auto] (e.g. '[auto] applied: build + tests green, pushed', '[auto] reverted: crashed on boot')."},
		},
		"required": []string{"id", "decision"},
	}
}
func (t *codeProposalDecide) Execute(ctx context.Context, in map[string]any) (string, error) {
	id := strString(in, "id")
	decision := strString(in, "decision")
	note := strString(in, "note")
	if id == "" || decision == "" {
		return "", errors.New("id and decision are required")
	}
	switch decision {
	case "approved", "rejected", "applied":
	default:
		return "", fmt.Errorf("decision must be approved, rejected, or applied (got %q)", decision)
	}
	// Gate: "applied" means "deployed and verified" — the deploy MUST have caught
	// up before we stamp that. Checked only in autonomous turns (nightly crons,
	// heartbeat, delegates) because interactive boss turns explicitly decide.
	// This is the mechanical enforcement of Rule #1b: the skill prose says "check
	// deploy_status first" but prose can be dropped; the tool refuses to lie.
	if decision == "applied" && IsAutonomous(ctx) && t.deployFn != nil {
		if err := checkDeployedBeforeApplied(t.deployFn()); err != nil {
			return "", err
		}
	}
	if err := t.decider.DecideCodeProposal(ctx, id, decision, note); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"ok":true,"id":%q,"decision":%q}`, id, decision), nil
}

// checkDeployedBeforeApplied rejects the "applied" stamp when the cached
// deploy snapshot shows the running binary has not yet caught up to the latest
// pushed SHA. Returns nil when (a) the snapshot is absent/uninitialised,
// (b) both SHAs are empty (local dev with no Railway env), or (c) the SHAs
// match (deploy landed). Returns a descriptive error only when LatestSHA is
// set, RunningSHA is set, and they differ — i.e. a real push hasn't deployed.
func checkDeployedBeforeApplied(snapshot any) error {
	if snapshot == nil {
		return nil
	}
	b, err := json.Marshal(snapshot)
	if err != nil {
		return nil // marshal failure → don't block
	}
	var ds struct {
		RunningSHA string `json:"running_sha"`
		LatestSHA  string `json:"latest_sha"`
	}
	if err := json.Unmarshal(b, &ds); err != nil {
		return nil
	}
	if ds.LatestSHA == "" || ds.RunningSHA == "" {
		return nil // uninitialised (local dev, no Railway env)
	}
	if ds.RunningSHA == ds.LatestSHA {
		return nil // deploy has landed
	}
	short := func(s string) string {
		if len(s) >= 8 {
			return s[:8]
		}
		return s
	}
	return fmt.Errorf(
		"cannot mark proposal as applied: deploy has not caught up "+
			"(running=%s, latest=%s) — wait for the build to land or use 'approved' "+
			"to record commit intent without confirming the deploy",
		short(ds.RunningSHA), short(ds.LatestSHA),
	)
}

// ── deploy_status ─────────────────────────────────────────────────────────

type deployStatusTool struct{ fn func() any }

type deployStatusRefreshTool struct{ fn func() any }

func (t *deployStatusTool) Name() string { return "deploy_status" }
func (t *deployStatusTool) Description() string {
	return "Report whether the running binary is behind GitHub main: running_sha vs latest_sha, behind (bool), commits_behind, repo, branch, checked_at. Use to confirm a push actually deployed (running_sha catches up to latest_sha) or to detect a stuck/bricked deploy (still behind long after the push)."
}
func (t *deployStatusTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *deployStatusTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	b, err := json.Marshal(t.fn())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

func (t *deployStatusRefreshTool) Name() string { return "deploy_status_refresh" }
func (t *deployStatusRefreshTool) Description() string {
	return "Force a fresh GitHub-vs-running deploy status check, then return the same snapshot as deploy_status. Use this after a push and settle delay when you need a deterministic post-deploy verification instead of the passive poll cache."
}
func (t *deployStatusRefreshTool) Schema() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}
func (t *deployStatusRefreshTool) Execute(_ context.Context, _ map[string]any) (string, error) {
	b, err := json.Marshal(t.fn())
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}
