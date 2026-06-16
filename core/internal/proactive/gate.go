package proactive

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
	"github.com/dopesoft/infinity/core/internal/tools"
)

// infoLog writes to stdout so Railway tags these lines severity=info
// instead of the severity=error it stamps on stderr (where stdlib log
// writes by default). Used across the proactive gates + trust queue for
// success/progress lines (allowed, queued, approved); genuine failures
// stay on the default log.Printf (stderr).
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// defaultApprovalTTL bounds how long an idle session approval lasts.
// We use a sliding window - every gate fire renews the expiry - so as
// long as the boss is actively working the approval never lapses. The
// TTL only matters when the session goes quiet (e.g. boss walks away for
// hours). Override with INFINITY_CLAUDE_CODE_APPROVAL_TTL ("8h", "1d", etc.).
const defaultApprovalTTL = 8 * time.Hour

func loadApprovalTTL() time.Duration {
	raw := strings.TrimSpace(os.Getenv("INFINITY_CLAUDE_CODE_APPROVAL_TTL"))
	if raw == "" {
		return defaultApprovalTTL
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		return defaultApprovalTTL
	}
	return d
}

// ClaudeCodeGate is the agent.ToolGate implementation that routes high-risk
// claude_code__* tool calls through the Trust queue.
//
// Policy (overridable via env):
//
//   - claude_code__write, claude_code__edit                      → allow (source
//     edits are git-reversible; gating them is what made walk-away coding
//     impossible — the whole point of this gate is to NOT do that)
//   - claude_code__bash                                          → CONTENT-AWARE:
//   - read-only git (status/diff/log/…)        → allow
//   - any non-filesystem-destructive command   → allow (go build, npm test,
//     git commit, mkdir, …)
//   - filesystem-destructive command           → queue (rm/rmdir/shred/dd/
//     mkfs/truncate/find -delete/
//     mv …/dev/null, …)
//   - claude_code__read, claude_code__ls, claude_code__grep,
//     claude_code__glob                                          → allow
//   - everything else                                            → allow
//
// The boss explicitly scoped "deletes and shit" to *filesystem deletes*. Git
// history/remote rewrites, DB drops, and deploys are deliberately NOT gated by
// this path (git is handled by the boss; deploys are blocked elsewhere). Widen
// the destructive set via env if that changes.
//
// Override:
//
//	$INFINITY_CLAUDE_CODE_AUTOAPPROVE        comma list of tool suffixes that
//	                                         always allow (e.g. "bash" for full
//	                                         trust on the box)
//	$INFINITY_CLAUDE_CODE_BLOCK              comma list of tool suffixes subject
//	                                         to gating (default = "bash"; add
//	                                         "write,edit" to gate those again)
//	$INFINITY_CLAUDE_CODE_BASH_GATE_ALL=true restore legacy behavior: gate EVERY
//	                                         bash (except read-only git), not
//	                                         just destructive ones
//	$INFINITY_CLAUDE_CODE_BASH_DESTRUCTIVE   comma list of extra substrings that
//	                                         mark a bash command destructive
type ClaudeCodeGate struct {
	trust           *TrustStore
	autoAllow       map[string]struct{}
	alwaysGate      map[string]struct{}
	gitReadOK       map[string]struct{} // git subcommands that bypass bash-gating
	bashDestructive map[string]struct{} // extra substrings that mark bash destructive
	gateAllBash     bool                // legacy: gate every non-readonly-git bash
	ttl             time.Duration
}

func NewClaudeCodeGate(trust *TrustStore) *ClaudeCodeGate {
	gitRead := parseToolSet(envOr("INFINITY_CANVAS_GIT_READ_AUTOAPPROVE",
		"status,diff,log,show,branch,remote,fetch,rev-parse,ls-files,ls-tree,blame,config"))
	return &ClaudeCodeGate{
		trust:           trust,
		autoAllow:       parseToolSet(os.Getenv("INFINITY_CLAUDE_CODE_AUTOAPPROVE")),
		alwaysGate:      parseToolSet(envOr("INFINITY_CLAUDE_CODE_BLOCK", "bash")),
		gitReadOK:       gitRead,
		bashDestructive: parseToolSet(os.Getenv("INFINITY_CLAUDE_CODE_BASH_DESTRUCTIVE")),
		gateAllBash:     strings.EqualFold(strings.TrimSpace(os.Getenv("INFINITY_CLAUDE_CODE_BASH_GATE_ALL")), "true"),
		ttl:             loadApprovalTTL(),
	}
}

// isReadOnlyGit reports whether a bash invocation is a safe, read-only git
// query (`git status`, `git diff`, `git log`…). Canvas's IDE-style left pane
// polls these constantly; gating every call would drown the boss in Trust
// prompts for actions that can't change disk or remote state.
//
// Rules to qualify:
//   - input has a "command" string starting with "git "
//   - the subcommand is in gitReadOK
//   - no shell metacharacters that could append a write (`;`, `&&`, `||`,
//     `|`, backticks, `$(`). The check is conservative; in particular it
//     rejects any redirection (`>`, `<`) and any newline.
func (g *ClaudeCodeGate) isReadOnlyGit(input map[string]any) bool {
	if g == nil || len(g.gitReadOK) == 0 {
		return false
	}
	raw, _ := input["command"].(string)
	cmd := strings.TrimSpace(raw)
	if cmd == "" {
		return false
	}
	for _, bad := range []string{";", "&&", "||", "|", "`", "$(", ">", "<", "\n", "\r"} {
		if strings.Contains(cmd, bad) {
			return false
		}
	}
	fields := strings.Fields(cmd)
	if len(fields) < 2 || fields[0] != "git" {
		return false
	}
	sub := strings.ToLower(fields[1])
	_, ok := g.gitReadOK[sub]
	return ok
}

// destructiveBashCmds are leading executables that delete or destroy
// filesystem state. Matched against the executable token of every
// sub-command (split across shell separators), with path prefixes and
// common wrappers (sudo/time/nice/xargs/…) stripped first. Scoped to
// FILESYSTEM destruction per the boss's choice — git/DB/deploy verbs are
// intentionally absent here.
var destructiveBashCmds = map[string]struct{}{
	"rm": {}, "rmdir": {}, "unlink": {}, "shred": {},
	"dd": {}, "mkfs": {}, "truncate": {}, "wipefs": {}, "blkdiscard": {},
}

// bashWrappers are command prefixes that delegate to the next token, so the
// real executable sits one (or more) tokens deeper: `sudo rm`, `xargs rm`,
// `time rm`, `env FOO=1 rm`. We skip past these to find what actually runs.
var bashWrappers = map[string]struct{}{
	"sudo": {}, "doas": {}, "time": {}, "nice": {}, "nohup": {},
	"env": {}, "command": {}, "xargs": {}, "stdbuf": {}, "ionice": {},
}

// splitShellSegments breaks a command line into the individual commands a
// shell would run, splitting on the operators that chain or pipe commands
// (`;`, `&&`, `||`, `|`, newlines) and on subshell boundaries (`$(`, backtick,
// braces) so a destructive call hidden inside `foo && rm -rf x` or `$(rm x)`
// is examined on its own.
func splitShellSegments(cmd string) []string {
	repl := strings.NewReplacer(
		"&&", "\x00", "||", "\x00", "|", "\x00", ";", "\x00",
		"\n", "\x00", "\r", "\x00", "$(", "\x00", ")", "\x00",
		"`", "\x00", "{", "\x00", "}", "\x00",
	)
	parts := strings.Split(repl.Replace(cmd), "\x00")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// isDestructiveBash reports whether a claude_code__bash invocation runs a
// filesystem-destructive command. Conservative-but-targeted: it inspects the
// leading executable of every chained sub-command (after stripping path
// prefixes, leading `VAR=val` assignments, and wrapper commands) plus a few
// argument-shape patterns (`find … -delete`, `find … -exec rm`, `mv …
// /dev/null`). Note: `>`/`>>` redirects are treated as NON-destructive — they
// overwrite-in-place like an edit and are git-reversible, and gating every
// redirect would reintroduce exactly the friction this gate exists to remove.
func (g *ClaudeCodeGate) isDestructiveBash(input map[string]any) bool {
	raw, _ := input["command"].(string)
	return isDestructiveCommand(raw, g.bashDestructive)
}

// isForcePush reports whether a bash command force-pushes git history — a
// rewrite of a remote ref. Catches `git push --force` / `-f` /
// `--force-with-lease`, and a `+` refspec (`git push origin +main`). Walks
// shell segments so a force-push hidden behind && / | / $() is still caught,
// and tolerates global git flags (`git -C path push --force`). Used to block
// force-push in autonomous runs (see Authorize).
func isForcePush(raw string) bool {
	low := strings.ToLower(strings.TrimSpace(raw))
	if low == "" {
		return false
	}
	for _, seg := range splitShellSegments(low) {
		toks := strings.Fields(seg)
		gitIdx := -1
		for i, t := range toks {
			if t == "git" {
				gitIdx = i
				break
			}
		}
		if gitIdx < 0 {
			continue
		}
		pushIdx := -1
		for i := gitIdx + 1; i < len(toks); i++ {
			if toks[i] == "push" {
				pushIdx = i
				break
			}
		}
		if pushIdx < 0 {
			continue
		}
		for _, t := range toks[pushIdx+1:] {
			if t == "-f" || t == "--force" || strings.HasPrefix(t, "--force-with-lease") ||
				(strings.HasPrefix(t, "+") && len(t) > 1) {
				return true
			}
		}
	}
	return false
}

// isDestructiveCommand is the shared filesystem-destruction classifier used by
// both ClaudeCodeGate (claude_code__bash, input key "command") and BridgeGate
// (bash_run, input key "cmd"). `extra` is an optional set of additional
// destructive substrings. Keeping one classifier means a delete is recognised
// identically no matter which coding surface served the call.
func isDestructiveCommand(raw string, extra map[string]struct{}) bool {
	cmd := strings.TrimSpace(raw)
	if cmd == "" {
		return false
	}
	low := strings.ToLower(cmd)

	for _, seg := range splitShellSegments(low) {
		// Env-extended substring patterns: any match anywhere in the segment.
		for p := range extra {
			if strings.Contains(seg, p) {
				return true
			}
		}

		toks := strings.Fields(seg)
		// Skip leading `VAR=val` assignments and wrapper commands to reach the
		// executable that actually runs.
		i := 0
		for i < len(toks) {
			t := toks[i]
			if eq := strings.IndexByte(t, '='); eq > 0 && !strings.HasPrefix(t, "-") && !strings.ContainsAny(t[:eq], "/") {
				i++ // VAR=val
				continue
			}
			exe := t
			if idx := strings.LastIndexByte(exe, '/'); idx >= 0 {
				exe = exe[idx+1:] // /usr/bin/rm -> rm
			}
			if _, wrap := bashWrappers[exe]; wrap {
				i++ // sudo/xargs/time/… — look one deeper
				continue
			}
			// `exe` is the real command for this segment.
			if _, bad := destructiveBashCmds[exe]; bad {
				return true
			}
			// mkfs comes in per-filesystem variants: mkfs.ext4, mkfs.xfs, …
			if strings.HasPrefix(exe, "mkfs") {
				return true
			}
			if exe == "find" && (strings.Contains(seg, "-delete") || strings.Contains(seg, "-exec rm")) {
				return true
			}
			if exe == "mv" && strings.Contains(seg, "/dev/null") {
				return true
			}
			break
		}
	}
	return false
}

func parseToolSet(raw string) map[string]struct{} {
	out := make(map[string]struct{})
	for _, p := range strings.Split(raw, ",") {
		p = strings.TrimSpace(strings.ToLower(p))
		if p != "" {
			out[p] = struct{}{}
		}
	}
	return out
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// Authorize implements agent.ToolGate. Only acts on claude_code__* tool
// names; everything else passes through.
func (g *ClaudeCodeGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || !agent.IsClaudeCodeTool(toolName) {
		return agent.GateDecision{Allow: true}
	}
	suffix := strings.TrimPrefix(toolName, "claude_code__")
	suffix = strings.ToLower(suffix)

	// Interactive sign-in must NEVER run on the Mac via claude_code — it pops a
	// browser on the boss's computer and authenticates the wrong machine (he may
	// be on his phone with no computer at all). Checked BEFORE autoAllow so even
	// full-box trust ($INFINITY_CLAUDE_CODE_AUTOAPPROVE=bash) can't let a login
	// slip onto the Mac. Generic, any CLI. Mirrors the bash_run guard so both
	// shell paths funnel sign-in through extension_activate → browser_open (the
	// self-contained cloud-browser flow). Rule #1b: mechanic in code, not prose.
	if suffix == "bash" {
		if cmd, _ := input["command"].(string); tools.IsInteractiveLoginCmd(cmd) {
			return agent.GateDecision{
				Allow: false,
				Reason: "Interactive sign-in (`auth login` / `login`) must not run on the Mac via claude_code — " +
					"it opens a browser on the boss's computer and auths the wrong machine (he may be on his phone). " +
					"Sign in self-contained instead: call extension_activate \"<name>\" (it runs the sign-in on your " +
					"cloud workspace and returns auth_url), then browser_open that auth_url so the page is live in the " +
					"boss's Preview pane and he approves it there. Never sign in via claude_code or bash.",
			}
		}
	}

	if _, ok := g.autoAllow[suffix]; ok {
		return agent.GateDecision{Allow: true}
	}
	if _, ok := g.alwaysGate[suffix]; !ok {
		// Not in the block list and not explicitly auto-allowed: default allow.
		return agent.GateDecision{Allow: true}
	}

	// claude_code__bash is content-aware. The boss wants to walk away while
	// Jarvis writes code, so the default is to ALLOW bash and only stop for
	// filesystem-destructive commands.
	//
	//   • read-only git (status/diff/log/…)  → allow (IDE-style polling)
	//   • non-destructive command            → allow (go build, npm test,
	//                                           git commit, mkdir, …)
	//   • filesystem-destructive command     → fall through to the queue
	//
	// $INFINITY_CLAUDE_CODE_BASH_GATE_ALL=true restores the legacy behavior of
	// gating every non-readonly-git bash.
	if suffix == "bash" {
		// Force-push is blocked in AUTONOMOUS runs (self-improve / deploy crons,
		// heartbeat, delegated sub-agents). Rewriting shared history unattended
		// is exactly what the "never force-push" line in those skills guarded —
		// and per Rule #1b that mechanic must be enforced HERE, not trusted to
		// prose the LLM can drop. Interactive turns (the boss driving the chat)
		// are untouched: a human force-push is the boss's call.
		if tools.IsAutonomous(ctx) {
			if cmd, _ := input["command"].(string); isForcePush(cmd) {
				infoLog.Printf("ClaudeCodeGate: blocked autonomous force-push: %s", strings.TrimSpace(cmd))
				return agent.GateDecision{
					Allow:  false,
					Reason: "Force-push is not allowed during autonomous runs — rewriting shared git history unattended is blocked. If this is genuinely required, do it from an interactive session where you're driving.",
				}
			}
		}
		if g.isReadOnlyGit(input) {
			return agent.GateDecision{Allow: true}
		}
		if !g.gateAllBash && !g.isDestructiveBash(input) {
			return agent.GateDecision{Allow: true}
		}
	}

	// All approval state lives in mem_trust_contracts - no in-memory cache
	// to lose on a Railway redeploy. Two checks against the durable store:
	//
	//   1. HasRecentApprovalForTool - has the boss ever approved THIS
	//      (session, tool) in the last TTL window? If yes → allow. This
	//      handles both first-call-after-approval AND every follow-up
	//      call in the same session for the duration of the window.
	//
	//   2. ConsumeApprovedForTool - fold the first 'approved' row over
	//      to 'consumed' for audit clarity. Subsequent calls hit (1)
	//      because 'consumed' is also acceptable evidence of approval.
	//
	// Deploy semantics: the rows survive in Postgres, so after a deploy
	// the gate happily allows tools the boss previously approved without
	// re-prompting. Old in-memory map is gone.
	if g.trust != nil {
		hasApproval, err := g.trust.HasRecentApprovalForTool(ctx, sessionID, toolName, g.ttl)
		if err != nil {
			log.Printf("ClaudeCodeGate: approval lookup error: %v", err)
			// Fall through to queueing - fail closed.
		} else if hasApproval {
			// Best-effort fold approved→consumed so the audit view shows
			// when the approval was first acted on. Idempotent: if the
			// row is already 'consumed' this no-ops.
			_, _ = g.trust.ConsumeApprovedForTool(ctx, sessionID, toolName)
			infoLog.Printf("ClaudeCodeGate: %s allowed via prior approval (durable, %s window)",
				toolName, g.ttl)
			return agent.GateDecision{Allow: true}
		}
	}

	// No standing approval. Queue a fresh contract and block the loop
	// on its resolution. The inline Approve/Deny buttons in Studio's
	// tool card POST to /api/trust-contracts/:id/decide →
	// WaitForDecision returns → the agent runs the same tool call
	// inline. No tab switch, no "retry" message.

	// Queue for approval.
	if g.trust == nil {
		log.Printf("ClaudeCodeGate: trust store nil, refusing %s", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store not configured; refusing to run claude_code__" + suffix + " unattended",
		}
	}

	reasoning := "Claude Code requested a gated action on the home Mac."
	title := fmt.Sprintf("Run %s on home Mac", toolName)
	if suffix == "bash" && !g.gateAllBash {
		reasoning = "Claude Code wants to run a filesystem-destructive shell " +
			"command (delete/overwrite-of-disk-state) on the home Mac. " +
			"Everything else in this coding session runs unattended — this " +
			"one stopped because it can destroy files."
		title = "Destructive command on home Mac"
	} else {
		reasoning = "Claude Code requested a gated action (" + suffix + ") on the " +
			"home Mac. Gated because INFINITY_CLAUDE_CODE_BLOCK includes this verb."
	}

	preview := buildPreview(toolName, input)
	id, err := g.trust.Queue(ctx, &TrustContract{
		Title:     title,
		RiskLevel: "high",
		Source:    "claude_code_gate",
		ActionSpec: map[string]any{
			"tool":       toolName,
			"input":      input,
			"session_id": sessionID,
			"project":    project,
		},
		Reasoning: reasoning,
		Preview:   preview,
	})
	if err != nil {
		log.Printf("ClaudeCodeGate: %s queue err=%v", toolName, err)
		return agent.GateDecision{
			Allow:  false,
			Reason: "could not queue trust contract: " + err.Error(),
		}
	}
	if id == "" {
		// Queue swallows pool=nil with a silent ("", nil). That used to leave
		// the model telling the user "queued in the Trust tab" while nothing
		// actually landed. Fail loud instead - the agent's tool output and
		// the boss's logs both surface the real story.
		log.Printf("ClaudeCodeGate: %s queue returned empty id (pool unwired?)", toolName)
		return agent.GateDecision{
			Allow:  false,
			Reason: "trust store unavailable; row was NOT persisted - do not tell the boss it was queued",
		}
	}
	infoLog.Printf("ClaudeCodeGate: %s queued as contract=%s (loop will wait)", toolName, id)
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     15 * time.Minute,
		Preview:         preview,
	}
}

// WaitForDecision implements agent.ToolGate. Polls the contract's status
// every second until it leaves "pending", or the timeout / ctx fires.
// Returns (approved, reason). On approval we also seed the session-wide
// approval cache so follow-up calls in the same turn don't bounce.
func (g *ClaudeCodeGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
	if g == nil || g.trust == nil || contractID == "" {
		return false, "trust store not configured"
	}
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-waitCtx.Done():
			// Returns context.DeadlineExceeded on timeout, context.Canceled
			// when the parent (turn) ctx fires.
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return false, "timed out waiting for approval (" + timeout.String() + ")"
			}
			return false, "session ended before approval"
		case <-tick.C:
			status, sessionID, toolName, err := g.trust.LookupForGate(waitCtx, contractID)
			if err != nil {
				continue // transient db error, keep polling
			}
			switch status {
			case "approved":
				infoLog.Printf("ClaudeCodeGate: contract %s approved", contractID)
				// Flip the row to 'consumed' for audit. The gate's
				// HasRecentApprovalForTool will continue to see this
				// row as evidence of approval for the whole TTL
				// window, so further calls in the same session
				// auto-allow without re-queueing.
				_, _ = g.trust.ConsumeApprovedForTool(waitCtx, sessionID, toolName)
				return true, ""
			case "denied":
				return false, "denied by the boss"
			case "snoozed":
				return false, "snoozed by the boss (treat as deny for this run)"
			case "consumed":
				// Already used by another flow - accept it.
				return true, ""
			default:
				// "pending" - keep waiting.
				continue
			}
		}
	}
}

func buildPreview(toolName string, input map[string]any) string {
	b, err := json.MarshalIndent(map[string]any{
		"tool":  toolName,
		"input": input,
	}, "", "  ")
	if err != nil {
		return toolName
	}
	if len(b) > 4096 {
		b = append(b[:4093], []byte("...")...)
	}
	return string(b)
}
