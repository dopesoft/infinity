// code_agent.go - the Mac-bridge coding muscle.
//
// THE POINT (read CLAUDE.md "Coding via Claude Code"): on the Mac bridge
// the chat model is an ORCHESTRATOR, not the code author. The actual
// coding cognition is delegated to `claude -p` - the real Claude Code
// agent running under the boss's Anthropic Max subscription on his Mac.
// That's the only path where the subscription the boss pays for does the
// thinking; using claude_code__Edit/Write directly makes the *chat* model
// (ChatGPT OAuth) author every byte and bills against its quota.
//
// Mechanism: a long coding run can exceed the Mac bridge /bash 600s wall
// cap, and the bridge HTTP client caps at 60s. So we don't hold one
// request open - we LAUNCH `claude -p` detached (nohup + &, which the
// bridge's exec.CommandContext can't reap once reparented) and POLL a
// status file with cheap sub-second /bash calls until it finishes. The
// whole run is booked as a mem_runs row so Studio shows a spinner that
// survives navigation/refresh/device (CLAUDE.md "Server-tracked progress").
//
// Autonomy: the nested Claude runs with --permission-mode bypassPermissions
// (it just works) EXCEPT deletes. A PreToolUse hook - injected via inline
// --settings and mirroring proactive.IsDestructiveBash - blocks any
// filesystem-destructive Bash command (rm -rf, shred, truncate -s0, git
// reset --hard, find -delete, …). When Claude hits one it's reported back,
// the chat model surfaces it, and the boss approves that single command
// through the existing gated bash path. "Let it run, only approve deletes."
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/errs"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/runs"
)

// deleteGateHookCmd is the PreToolUse hook body that runs on the Mac
// inside the nested `claude -p`. It reads the hook JSON on stdin and, if
// the payload contains a filesystem-destructive command, blocks it
// (exit 2 feeds stderr back to Claude so it stops and reports instead of
// retrying). The ERE MIRRORS proactive.IsDestructiveBash in
// core/internal/proactive/gate.go - keep the two in sync.
// The trailing class on -delete includes a double-quote because the hook
// greps the raw JSON payload, where the command is `..."command":"find …
// -delete"}` — so -delete is followed by `"`, not a space/EOL. Verified
// 2026-05-31: without the `"` the find-delete case slips the gate.
const deleteGateHookCmd = `in=$(cat); if printf '%s' "$in" | grep -qE 'rm[[:space:]]+-[a-zA-Z]*[rf]|rmdir|shred|mkfs|dd[[:space:]]+if=|truncate[[:space:]]+-s[[:space:]]*0|git[[:space:]]+clean[[:space:]]+-[a-zA-Z]*f|git[[:space:]]+reset[[:space:]]+--hard|>[[:space:]]*/dev/sd|[[:space:]]-delete([[:space:]"\\]|$)|-exec[[:space:]]+rm'; then echo "INFINITY_DELETE_BLOCKED: a destructive/delete command was blocked - it needs the boss's approval. Do NOT retry it; describe exactly what you wanted to delete in your final summary so the boss can approve that one command." >&2; exit 2; fi`

// codeAgentSettings is what we hand `claude -p --settings`. It MERGES
// with the boss's normal Claude config (--settings is additive), so all
// it carries is the delete-gate hook. PreToolUse hooks fire even under
// bypassPermissions.
func codeAgentSettingsJSON() string {
	settings := map[string]any{
		"hooks": map[string]any{
			"PreToolUse": []map[string]any{
				{
					"matcher": "Bash",
					"hooks": []map[string]any{
						{"type": "command", "command": deleteGateHookCmd},
					},
				},
			},
		},
	}
	b, _ := json.Marshal(settings)
	return string(b)
}

const (
	codeAgentMaxWait  = 20 * time.Minute // inline ceiling; longer jobs → background_build
	codeAgentPollEach = 15 * time.Second
	codeAgentTmpDir   = "/tmp/inf-code"
)

// RegisterCodeAgentTool wires the code_agent tool. router/prefs resolve
// the active bridge (it only runs on Mac); tracker books the mem_runs row.
func RegisterCodeAgentTool(r *Registry, router *bridge.Router, prefs PreferenceFetcher, tracker *runs.Tracker) {
	r.Register(&codeAgent{router: router, prefs: prefs, tracker: tracker})
}

type codeAgent struct {
	router  *bridge.Router
	prefs   PreferenceFetcher
	tracker *runs.Tracker
}

func (t *codeAgent) Name() string   { return "code_agent" }
func (t *codeAgent) ReadOnly() bool { return false }
func (t *codeAgent) Description() string {
	return "Delegate a coding task to Claude Code on the boss's Mac (runs under his Anthropic Max " +
		"subscription, NOT the chat model's quota). Give it a complete, self-contained brief - it " +
		"reads the repo, writes/edits the code, runs builds/tests, and returns a summary of what it " +
		"changed. This is the RIGHT way to write code on the Mac bridge: you orchestrate, Claude Code " +
		"does the implementation. It runs freely; only destructive deletes are blocked and surfaced for " +
		"the boss to approve. For tiny one-line/deterministic edits, fs_edit/claude_code__Edit are fine; " +
		"for anything real, use this."
}

func (t *codeAgent) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "Complete, self-contained coding brief: what to build/change, in which files/area, acceptance criteria, and any build/test command to run. Claude Code only sees this brief plus the repo - it has no chat history.",
			},
			"repo": map[string]any{
				"type":        "string",
				"description": "Absolute path (or workspace-relative) of the repo to work in. Defaults to the bridge workspace root.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional Claude model id or alias for this run (e.g. 'claude-opus-5[1m]', 'sonnet'). Leave empty for the configured default (INFINITY_CODE_AGENT_MODEL, else the Mac's Claude Code setting).",
			},
			"effort": map[string]any{
				"type":        "string",
				"enum":        []string{"low", "medium", "high", "xhigh", "max"},
				"description": "Optional Claude Code effort for this run. Leave empty for the configured default (INFINITY_CODE_AGENT_EFFORT, else the Mac's setting).",
			},
		},
		"required": []string{"task"},
	}
}

func (t *codeAgent) Execute(ctx context.Context, in map[string]any) (string, error) {
	task := strings.TrimSpace(strString(in, "task"))
	if task == "" {
		return "", fmt.Errorf("code_agent: task is required")
	}
	repo := strString(in, "repo")
	// Model + effort for the nested Claude Code: the call's own choice first,
	// else Infinity's defaults (INFINITY_CODE_AGENT_MODEL / _EFFORT, e.g.
	// "claude-opus-5[1m]" / "high"), else the Mac's own Claude settings.
	model := strDefault(in, "model", strings.TrimSpace(os.Getenv("INFINITY_CODE_AGENT_MODEL")))
	effort := strDefault(in, "effort", strings.TrimSpace(os.Getenv("INFINITY_CODE_AGENT_EFFORT")))

	// Only meaningful on the Mac bridge - that's where the Max-billed
	// Claude Code CLI lives. On Cloud, the chat model codes directly.
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("code_agent: %s", why)
	}
	if b.Name() != bridge.KindMac {
		return fmt.Sprintf("code_agent only runs on the Mac bridge (it delegates to the boss's Claude Code "+
			"Max subscription). The active bridge is %q. On the Cloud bridge, write the code yourself with "+
			"fs_save/fs_edit in /workspace.", b.Name()), nil
	}
	// A cloud-flavored repo path (e.g. /workspace/infinity) still resolves on
	// the Mac: translate it to the Mac layout before launching.
	repo = bridge.NormalizePath(b, repo)

	// Stop retrying something dead: while the boss's Claude plan is known
	// spent (the last run said "out of extra usage"), don't launch another
	// `claude -p` that will fail the same way. Held in the shared quota
	// ledger so Settings can show it alongside the chat brain's state.
	if until, detail, spent := llm.Exhausted(claudeCodeQuotaKey); spent {
		return claudeCodeHeldGuidance(until, detail), nil
	}

	// Book the run so Studio shows a live, navigation-proof spinner.
	// runs.Handle is the real API (Begin → Progress → Finish); it's
	// nil-safe, so this degrades cleanly when the pool isn't wired.
	label := "Claude Code: " + truncateForLabel(task, 80)
	handle := t.tracker.Begin(ctx, runs.Kind("code_agent"), "", label, runs.SourceAgent)
	// Use context.Background() for heartbeat so progress notes persist even
	// when ctx is cancelled (Stop button or core shutdown). The last-known
	// progress_label survives a restart and RecoverStranded surfaces it.
	heartbeat := func(note string) { handle.Progress(context.Background(), 0, note) }
	jobID := handle.ID()
	if jobID == "" {
		jobID = fmt.Sprintf("job-%d", time.Now().UnixNano())
	}

	// The run row carries which model is coding (Studio's bridge pill flashes
	// it while the run is live): the requested id now, the exact id Claude
	// reports once it finishes.
	setMeta := func(key, value string) {
		if value != "" {
			handle.SetMetaString(context.Background(), key, value)
		}
	}
	setMeta("engine", "claude_code")
	setMeta("model", model)
	setMeta("effort", effort)

	summary, runErr := t.run(ctx, b, jobID, task, repo, model, effort, heartbeat, setMeta)
	// Always close the run row on a fresh context. Using the tool ctx here
	// means a cancelled ctx (Stop button, graceful shutdown) silently drops
	// the Finish UPDATE and leaves the row stuck 'running' until the reaper.
	finCtx, finCancel := context.WithTimeout(context.Background(), 5*time.Second)
	handle.Finish(finCtx, runErr, summary)
	finCancel()
	if runErr != nil {
		// A bridge/launch failure (the Mac is unreachable — the 404 that used to
		// stall nightly-self-improve silently) is NOT a dead end. Return it as
		// legible GUIDANCE (string, nil) so the agent falls back to writing the
		// change itself with fs_edit/bash_run on the reachable bridge, instead
		// of surfacing a raw "launch via mac failed (status=404)". The mem_runs
		// row already carries the humanized error via runs.Finish.
		var rejected *launchRejectedError
		if errors.As(runErr, &rejected) {
			return rejected.guidance(), nil
		}
		h := errs.Humanize(runErr)
		if h.Category == errs.CatBridge {
			return fmt.Sprintf("code_agent couldn't run: %s — the Mac bridge is unreachable. "+
				"Don't stop: write the change yourself with fs_edit/fs_save in /workspace, then "+
				"`bash_run` go build ./... && go vet ./... && go test ./..., then git_commit (and git_push if autonomy is on). "+
				"The cloud workspace has the Go toolchain pre-installed.", h.Summary), nil
		}
		// The boss's Claude plan is spent: the run row is red (the truth is
		// in Agent Work), and the model gets a directive rather than a raw
		// error it would retry to the iteration cap.
		if q, ok := llm.AsQuota(runErr); ok {
			return claudeCodeHeldGuidance(q.ResetsAt, q.Detail), nil
		}
		return "", runErr
	}
	return summary, nil
}

// launchRejectedError: the Mac bridge answered 4xx to the launch request.
type launchRejectedError struct {
	bridge string
	status int
	detail string
}

func (e *launchRejectedError) Error() string {
	return fmt.Sprintf("code_agent: the %s rejected the launch request (HTTP %d): %s", e.bridge, e.status, e.detail)
}

// guidance is the tool result for a rejected launch: the model fixes the
// request and stays on the Mac.
func (e *launchRejectedError) guidance() string {
	return fmt.Sprintf("code_agent did not start: the %s bridge is UP and rejected the request (HTTP %d): %s. "+
		"This is a request problem, not an outage; do NOT move the work to the cloud. Repos on the Mac live under "+
		"~/Dev/<repo> (the Infinity repo is ~/Dev/infinity); pass that as repo and call code_agent again.",
		e.bridge, e.status, e.detail)
}

// bridgeErrorDetail pulls the {"error": "..."} out of a bridge reply, else
// the raw body.
func bridgeErrorDetail(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && strings.TrimSpace(e.Error) != "" {
		return strings.TrimSpace(e.Error)
	}
	return strings.TrimSpace(string(body))
}

// claudeCodeQuotaKey is the quota-ledger id for the boss's Claude plan behind
// `claude -p` (distinct from the "anthropic" API-key provider).
const claudeCodeQuotaKey = "claude_code"

// claudeCodeHeldGuidance is the tool result while Claude Code is out of usage.
func claudeCodeHeldGuidance(until time.Time, detail string) string {
	when := "for now"
	if !until.IsZero() {
		when = "until about " + llm.FormatLocalClock(until)
	}
	if detail == "" {
		detail = "its plan's usage allowance is spent"
	}
	return fmt.Sprintf("HOLD: Claude Code (the boss's Claude plan) is out of usage %s (%s). "+
		"Do NOT call code_agent again before then; it will fail the same way. Tell the boss plainly that his Claude plan "+
		"is out of usage %s. If he wants the change now anyway, make it yourself with claude_code__Edit / fs_edit "+
		"(that bills the chat model instead), otherwise pick the work back up after the reset.", when, detail, when)
}

// run launches the detached claude -p and polls to completion.
func (t *codeAgent) run(ctx context.Context, b bridge.Bridge, jobID, task, repo, model, effort string, heartbeat func(string), setMeta func(key, value string)) (string, error) {
	out := fmt.Sprintf("%s/%s.out", codeAgentTmpDir, jobID)
	errf := fmt.Sprintf("%s/%s.err", codeAgentTmpDir, jobID)
	status := fmt.Sprintf("%s/%s.status", codeAgentTmpDir, jobID)
	pidf := fmt.Sprintf("%s/%s.pid", codeAgentTmpDir, jobID)
	settings := fmt.Sprintf("%s/settings.json", codeAgentTmpDir)

	// One round-trip: ensure tmp dir, (re)write the delete-gate settings,
	// then launch claude -p DETACHED. Task/model go via exported env so
	// the inner single-quoted bash -c needs no fragile nested quoting.
	inner := fmt.Sprintf(
		`claude -p "$INF_TASK" ${INF_MODEL:+--model "$INF_MODEL"} ${INF_EFFORT:+--effort "$INF_EFFORT"} --output-format json `+
			`--permission-mode bypassPermissions --settings %s > %s 2> %s; echo $? > %s`,
		settings, out, errf, status,
	)
	launch := strings.Join([]string{
		"mkdir -p " + codeAgentTmpDir,
		"cat > " + settings + " <<'INFEOF'\n" + codeAgentSettingsJSON() + "\nINFEOF",
		"export INF_TASK=" + shellQuote(task),
		"export INF_MODEL=" + shellQuote(model),
		"export INF_EFFORT=" + shellQuote(effort),
		"nohup bash -c " + shellQuote(inner) + " >/dev/null 2>&1 &",
		// Record the wrapper's pid so a Stop / mid-turn steer can actually
		// kill the job (2026-08-26: the boss said "don't build", the job kept
		// editing his repo for nine minutes and "couldn't be stopped").
		`echo $! > ` + pidf,
		`echo "PID:$!"`,
		// When no model was pinned, the Mac's own Claude default is what will
		// code; report it so the run row (and the pill) name the real model.
		`echo "SETTINGS:$(cat ~/.claude/settings.json 2>/dev/null | tr -d '\n')"`,
	}, "\n")

	body, code, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         launch,
		"cwd":         repo,
		"timeout_sec": 20,
	})
	if ok && code >= 400 && code < 500 {
		// The bridge ANSWERED and rejected the request (a bad cwd, a bad
		// arg). That is a request problem, never an outage: it must not
		// read as "the Mac dropped out" and push the work to the cloud
		// (2026-08-27 06:51, repo "/Users/kai/Dev/infinity").
		return "", &launchRejectedError{bridge: string(b.Name()), status: code, detail: bridgeErrorDetail(body)}
	}
	if !ok || code >= 300 {
		return "", fmt.Errorf("code_agent: launch via %s failed (status=%d): %s", b.Name(), code, string(body))
	}
	if model == "" || effort == "" {
		launchOut, _ := bridgeBashOutput(body)
		dm, de := macClaudeDefaults(launchOut)
		if model == "" && setMeta != nil {
			setMeta("model", dm)
		}
		if effort == "" && setMeta != nil {
			setMeta("effort", de)
		}
	}
	heartbeat("launched claude -p; working…")

	// Poll the status file. Each call is sub-second, so the 60s bridge
	// client cap and the 600s server cap never bite the long run.
	pollCmd := fmt.Sprintf(`if [ -f %s ]; then echo "DONE:$(cat %s)"; else echo RUNNING; fi`, status, status)
	deadline := time.Now().Add(codeAgentMaxWait)
	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			// The caller's context was cancelled: the Stop button, a mid-turn
			// message from the boss (SteerInterruptible), or a shutdown. KILL
			// the detached claude -p — its edits are git-reversible, and a job
			// the boss just interrupted must not keep rewriting his repo.
			killed := t.kill(b, repo, pidf)
			return fmt.Sprintf(
				"code_agent was STOPPED after %s (Claude Code run %s %s). Any edits it had already "+
					"made are uncommitted in %s — check git_status/git_diff before touching that repo. "+
					"To pick the job back up later, run code_agent again; `claude --continue` in that "+
					"directory resumes its conversation.",
				time.Since(started).Round(time.Second), jobID, killed, repoOrRoot(repo)), nil
		case <-time.After(codeAgentPollEach):
		}

		pb, pc, pok := b.Post(ctx, "/bash", map[string]any{"cmd": pollCmd, "cwd": repo, "timeout_sec": 15})
		if pok && pc < 300 {
			outStr, _ := bridgeBashOutput(pb)
			if strings.Contains(outStr, "DONE:") {
				return t.collect(ctx, b, out, errf, repo, setMeta)
			}
		}
		heartbeat(fmt.Sprintf("working… %s elapsed", time.Since(started).Round(time.Second)))

		if time.Now().After(deadline) {
			return fmt.Sprintf("⏳ Claude Code is still working after %s (run %s). It keeps going on the Mac; "+
				"this is past the inline wait window. Check back, or for jobs this long hand them to background_build. "+
				"Resume the same session with `claude --continue` in %s.",
				codeAgentMaxWait, jobID, repoOrRoot(repo)), nil
		}
	}
}

// kill terminates the detached claude -p (wrapper + children) recorded in
// pidf. Runs on a FRESH context because the tool ctx is already cancelled.
// Returns a short human clause for the tool result.
func (t *codeAgent) kill(b bridge.Bridge, repo, pidf string) string {
	kctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := fmt.Sprintf(
		`P=$(cat %s 2>/dev/null); if [ -z "$P" ]; then echo NOPID; exit 0; fi; `+
			`pkill -TERM -P "$P" 2>/dev/null; kill -TERM "$P" 2>/dev/null; sleep 1; `+
			`if kill -0 "$P" 2>/dev/null; then pkill -KILL -P "$P" 2>/dev/null; kill -KILL "$P" 2>/dev/null; fi; echo KILLED`,
		pidf)
	body, code, ok := b.Post(kctx, "/bash", map[string]any{"cmd": cmd, "cwd": repo, "timeout_sec": 10})
	if !ok || code >= 300 {
		return "could not be killed: the Mac bridge did not answer, it may still be running"
	}
	if kout, _ := bridgeBashOutput(body); strings.Contains(kout, "NOPID") {
		return "had no recorded pid, it may still be running"
	}
	return "was killed"
}

// InterruptOnSteer opts code_agent into the loop's steer-interrupt: a message
// from the boss while Claude Code is working cancels the job (and kills it)
// so he is answered now, not after the job.
func (t *codeAgent) InterruptOnSteer() bool { return true }

// collect reads the finished output, parses claude's JSON result, and
// folds in any blocked-delete notice for the boss to approve.
func (t *codeAgent) collect(ctx context.Context, b bridge.Bridge, out, errf, repo string, setMeta func(key, value string)) (string, error) {
	fetch := fmt.Sprintf(`echo "===OUT==="; cat %s 2>/dev/null; echo "===ERR==="; tail -c 4000 %s 2>/dev/null`, out, errf)
	fb, fc, fok := b.Post(ctx, "/bash", map[string]any{"cmd": fetch, "cwd": repo, "timeout_sec": 20})
	if !fok || fc >= 300 {
		return "", fmt.Errorf("code_agent: finished but reading output failed (status=%d)", fc)
	}
	// Decode the bridge's {output, exit_code} JSON for real. The old
	// first-quote scanner cut Claude's own JSON at its first escaped quote,
	// so every result (including "You're out of extra usage") came back as
	// `{\` and was reported ok (2026-08-26).
	raw, _ := bridgeBashOutput(fb)
	outPart, errPart := splitMarker(raw, "===OUT===", "===ERR===")

	res := parseClaudeResult(outPart)
	if res.parsed && setMeta != nil {
		// Claude names the model that actually ran; that beats our request.
		for m := range res.ModelUsage {
			setMeta("model", m)
			break
		}
	}
	if res.parsed && res.IsError {
		msg := strings.TrimSpace(res.Result)
		if msg == "" {
			msg = "Claude Code reported an error"
			if res.Subtype != "" {
				msg += " (" + res.Subtype + ")"
			}
		}
		// A spent Claude plan: hold further launches until its reset (parsed
		// from Claude's own copy when it names one) and fail LOUD, typed, so
		// the run row, the tool result and the chat all say what happened.
		if res.APIErrorStatus == 429 || looksLikeUsageCap(msg) {
			until, ok := llm.ParseResetClock(msg, time.Now())
			if !ok {
				until = time.Time{}
			}
			q := &llm.QuotaError{Provider: claudeCodeQuotaKey, ResetsAt: until, Detail: msg}
			llm.MarkExhausted(claudeCodeQuotaKey, until, msg)
			return "", q
		}
		return "", fmt.Errorf("code_agent: Claude Code failed: %s", msg)
	}
	var sb strings.Builder
	if res.Result != "" {
		sb.WriteString(res.Result)
	} else {
		// Fall back to raw stdout if JSON parse failed.
		sb.WriteString(strings.TrimSpace(outPart))
	}

	if strings.Contains(errPart, "INFINITY_DELETE_BLOCKED") {
		sb.WriteString("\n\n⚠️ **Delete blocked — needs your approval.** Claude Code wanted to run a destructive/delete " +
			"command and the gate stopped it (see its summary above for what). Tell me to go ahead and I'll run that one " +
			"command through the normal Trust approval.")
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "code_agent finished but returned no output. Check the run logs.", nil
	}
	return sb.String(), nil
}

// claudeResult is the shape of `claude -p --output-format json` we act on.
// APIErrorStatus is the upstream HTTP status Claude Code saw (429 = the plan
// is out of usage); nil in the JSON when the run succeeded.
type claudeResult struct {
	Result         string                     `json:"result"`
	IsError        bool                       `json:"is_error"`
	Subtype        string                     `json:"subtype"`
	APIErrorStatus int                        `json:"api_error_status"`
	ModelUsage     map[string]json.RawMessage `json:"modelUsage"`
	parsed         bool
}

// macClaudeDefaults reads the "SETTINGS:{...}" line the launch script
// appends (the Mac's ~/.claude/settings.json) and returns its model and
// effortLevel, "" when absent.
func macClaudeDefaults(launchOut string) (model, effort string) {
	i := strings.Index(launchOut, "SETTINGS:")
	if i < 0 {
		return "", ""
	}
	var cfg struct {
		Model       string `json:"model"`
		EffortLevel string `json:"effortLevel"`
	}
	line := strings.TrimSpace(launchOut[i+len("SETTINGS:"):])
	if nl := strings.IndexByte(line, '\n'); nl >= 0 {
		line = line[:nl]
	}
	if err := json.Unmarshal([]byte(line), &cfg); err != nil {
		return "", ""
	}
	return strings.TrimSpace(cfg.Model), strings.TrimSpace(cfg.EffortLevel)
}

// parseClaudeResult decodes the JSON result object out of claude's stdout.
// parsed is false when there is no decodable object (raw stdout is used).
func parseClaudeResult(s string) claudeResult {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return claudeResult{}
	}
	var res claudeResult
	if err := json.Unmarshal([]byte(s[start:end+1]), &res); err != nil {
		return claudeResult{}
	}
	res.Result = strings.TrimSpace(res.Result)
	res.parsed = true
	return res
}

// looksLikeUsageCap recognises Claude Code's plan-cap copy.
func looksLikeUsageCap(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "out of extra usage") || strings.Contains(m, "out of usage") ||
		strings.Contains(m, "usage limit") || strings.Contains(m, "hit your limit")
}

// bridgeBashOutput decodes a bridge /bash reply ({output, exit_code, ...}).
// Falls back to the raw body when it is not that JSON so a bridge that
// answers plain text still reads.
func bridgeBashOutput(body []byte) (string, int) {
	var r struct {
		Output   string `json:"output"`
		ExitCode int    `json:"exit_code"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return string(body), 0
	}
	return r.Output, r.ExitCode
}

// splitMarker carves a "===A===…===B===…" blob into its two parts.
func splitMarker(s, a, bMark string) (string, string) {
	ai := strings.Index(s, a)
	bi := strings.Index(s, bMark)
	if ai < 0 || bi < 0 || bi < ai {
		return s, ""
	}
	return s[ai+len(a) : bi], s[bi+len(bMark):]
}

func repoOrRoot(repo string) string {
	if strings.TrimSpace(repo) == "" {
		return "the workspace root"
	}
	return repo
}

func truncateForLabel(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
