// code_agent.go - the Mac-bridge coding muscle.
//
// THE CONTRACT (the boss's words, 2026-08-28): the chat brain - his ChatGPT
// plan, GPT-5.6 Sol - is for CONVERSATION. On the Mac bridge, CODING runs on
// Claude Code under his Claude Max subscription. On the Cloud bridge the chat
// brain codes, because there is no Claude Code there. Nothing else is ever
// supposed to write code on the Mac.
//
// The coding cognition is delegated to `claude -p` - the real Claude Code
// agent on his Mac, signed in to his Max account. Using claude_code__Edit /
// Write directly, or spinning a background loop on the settings model, makes
// the CHAT model author every byte and bills his ChatGPT plan: the leak that
// spent it on 2026-08-26 and again on 2026-08-28 while he was "connected to
// the Mac".
//
// ClaudeCodeRunner is the ONE launcher for that. code_agent (inline,
// stoppable) and background_build-on-Mac (detached) both go through it, so
// the subscription proof, the API-key guard, the delete gate, the quota
// ledger and the live progress are guaranteed by code on every path (Rule
// #1b / #1c), never by a sentence the model can drop.
//
// Every launch PROVES it is on his subscription before anything starts: the
// Mac's own sign-in (~/.claude.json oauthAccount: organizationType
// "claude_max", billingType "stripe_subscription") is read back, an API key
// or key helper that would pre-empt it is refused, and the proof is written
// onto the run row (meta.auth) so the bridge pill, the dock and the tool
// result all say which plan is paying.
//
// Mechanism: a long coding run can exceed the Mac bridge /bash 600s wall
// cap, and the bridge HTTP client caps at 60s. So we don't hold one
// request open - we LAUNCH `claude -p` detached (nohup + &, which the
// bridge's exec.CommandContext can't reap once reparented) and POLL a
// status file with cheap sub-second /bash calls until it finishes. Claude
// writes `--output-format stream-json`, so each poll also reads the tail of
// its output and reports what it is doing RIGHT NOW (which file it is
// editing, which command it is running) onto the run row - the boss never
// again watches a bare spinner for eight minutes. The whole run is booked as
// a mem_runs row so Studio shows a spinner that survives navigation /
// refresh / device (CLAUDE.md "Server-tracked progress").
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
	codeAgentMaxWait = 20 * time.Minute // inline ceiling; longer jobs → background_build
	// claudeTailBytes is how much of Claude's stream-json we read on each
	// poll to name its current activity; claudeResultTailBytes is how much we
	// read at the end to find the final `result` line (the last line of the
	// stream, per the Claude Code docs) without tripping the bridge's output
	// cap on a long run.
	claudeTailBytes       = 12000
	claudeResultTailBytes = 30000
)

// codeAgentPollEach / codeAgentTmpDir are vars so the end-to-end test can
// poll fast and keep its job files out of the real /tmp/inf-code.
var (
	codeAgentPollEach = 15 * time.Second
	codeAgentTmpDir   = "/tmp/inf-code"
)

// claudeAuthProbeCmd prints the Mac's Claude Code sign-in so a launch can
// PROVE which plan is about to pay: the oauthAccount block of ~/.claude.json
// (organizationType / billingType / email), whether an ANTHROPIC_API_KEY sits
// in the bridge's environment (presence only - the value is never printed),
// and ~/.claude/settings.json (default model/effort, and apiKeyHelper, which
// would pre-empt the subscription). jq when present, python3 otherwise - both
// ship on the Mac; `{}` when neither can read the file.
const claudeAuthProbeCmd = `echo "APIKEY:$( [ -n "${ANTHROPIC_API_KEY:-}" ] && echo present || echo absent )"
echo "AUTH:$( (command -v jq >/dev/null 2>&1 && jq -c '.oauthAccount // {}' ~/.claude.json 2>/dev/null) || python3 -c 'import json,os;d=json.load(open(os.path.expanduser("~/.claude.json")));print(json.dumps(d.get("oauthAccount") or {}))' 2>/dev/null || echo '{}' )"
echo "SETTINGS:$(cat ~/.claude/settings.json 2>/dev/null | tr -d '\n')"`

// claudeAuth is the Mac's Claude Code sign-in as the probe reports it.
type claudeAuth struct {
	OrganizationType string
	BillingType      string
	Email            string
	// apiKeyInEnv: the bridge's shell carried ANTHROPIC_API_KEY. The launch
	// unsets it so the subscription still answers; recorded for the run.
	apiKeyInEnv bool
	// apiKeyHelper: ~/.claude/settings.json names an apiKeyHelper, which
	// Claude Code uses INSTEAD of the sign-in. That is not the subscription.
	apiKeyHelper  bool
	found         bool
	defaultModel  string
	defaultEffort string
}

// parseClaudeAuth decodes the probe output (see claudeAuthProbeCmd).
func parseClaudeAuth(out string) claudeAuth {
	var a claudeAuth
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "APIKEY:"):
			a.apiKeyInEnv = strings.TrimSpace(strings.TrimPrefix(line, "APIKEY:")) == "present"
		case strings.HasPrefix(line, "AUTH:"):
			var acct struct {
				OrganizationType string `json:"organizationType"`
				BillingType      string `json:"billingType"`
				EmailAddress     string `json:"emailAddress"`
			}
			raw := strings.TrimSpace(strings.TrimPrefix(line, "AUTH:"))
			if json.Unmarshal([]byte(raw), &acct) != nil {
				continue
			}
			a.OrganizationType = strings.TrimSpace(acct.OrganizationType)
			a.BillingType = strings.TrimSpace(acct.BillingType)
			a.Email = strings.TrimSpace(acct.EmailAddress)
			a.found = a.OrganizationType != "" || a.BillingType != "" || a.Email != ""
		case strings.HasPrefix(line, "SETTINGS:"):
			var cfg struct {
				Model        string `json:"model"`
				EffortLevel  string `json:"effortLevel"`
				APIKeyHelper string `json:"apiKeyHelper"`
			}
			raw := strings.TrimSpace(strings.TrimPrefix(line, "SETTINGS:"))
			if json.Unmarshal([]byte(raw), &cfg) != nil {
				continue
			}
			a.defaultModel = strings.TrimSpace(cfg.Model)
			a.defaultEffort = strings.TrimSpace(cfg.EffortLevel)
			a.apiKeyHelper = strings.TrimSpace(cfg.APIKeyHelper) != ""
		}
	}
	return a
}

// Subscription reports whether the sign-in is the boss's Claude subscription
// (Max / Pro / Team / Enterprise), i.e. the plan `claude -p` will bill. An
// apiKeyHelper wins over the sign-in inside Claude Code, so it disqualifies.
func (a claudeAuth) Subscription() bool {
	if a.apiKeyHelper {
		return false
	}
	return a.BillingType == "stripe_subscription" || strings.HasPrefix(a.OrganizationType, "claude_")
}

// planName renders organizationType "claude_max" as "Max".
func (a claudeAuth) planName() string {
	switch a.OrganizationType {
	case "claude_max":
		return "Max"
	case "claude_pro":
		return "Pro"
	case "claude_team":
		return "Team"
	case "claude_enterprise":
		return "Enterprise"
	}
	if rest := strings.TrimPrefix(a.OrganizationType, "claude_"); rest != "" && rest != a.OrganizationType {
		return strings.ToUpper(rest[:1]) + rest[1:]
	}
	return ""
}

// Label is the proof line the boss reads: "Max subscription · kai@…".
func (a claudeAuth) Label() string {
	switch {
	case a.apiKeyHelper:
		return "API key helper (not your subscription)"
	case !a.found:
		return "not signed in"
	case a.Subscription():
		name := a.planName()
		if name == "" {
			name = "Claude"
		}
		s := name + " subscription"
		if a.Email != "" {
			s += " · " + a.Email
		}
		return s
	}
	what := a.OrganizationType
	if what == "" {
		what = a.BillingType
	}
	return what + " (not a subscription)"
}

// notSubscriptionError: the Mac's Claude Code is not signed in to the boss's
// subscription, so the launch was refused before anything ran or billed.
type notSubscriptionError struct{ auth claudeAuth }

func (e *notSubscriptionError) Error() string {
	return "code_agent: Claude Code on the Mac is not signed in to the boss's Claude subscription (found: " +
		e.auth.Label() + "); not launching"
}

// guidance is the tool result: the model tells the boss, and never codes on
// its own plan instead.
func (e *notSubscriptionError) guidance() string {
	fix := "run `claude` on the Mac and `/login` with the Max account"
	if e.auth.apiKeyHelper {
		fix = "remove `apiKeyHelper` from ~/.claude/settings.json on the Mac (it pre-empts the subscription), then `/login` with the Max account if needed"
	}
	return fmt.Sprintf("NOT LAUNCHED: Claude Code on the Mac is not signed in to the boss's Claude subscription (found: %s). "+
		"Coding on the Mac bridge runs ONLY on his Max plan, so nothing was started and nothing was billed. "+
		"Tell the boss plainly: %s, then say continue and call code_agent again. "+
		"Do NOT write the code yourself on the chat model.", e.auth.Label(), fix)
}

// stillRunningError: the wait window closed while Claude Code was still
// working on the Mac. The job was NOT killed.
type stillRunningError struct {
	jobID   string
	repo    string
	elapsed time.Duration
}

func (e *stillRunningError) Error() string {
	return fmt.Sprintf("code_agent: Claude Code is still working after %s (run %s); it was not stopped - resume with `claude --continue` in %s",
		e.elapsed.Round(time.Second), e.jobID, repoOrRoot(e.repo))
}

// inlineMessage is the friendly form for the inline tool result.
func (e *stillRunningError) inlineMessage() string {
	return fmt.Sprintf("⏳ Claude Code is still working after %s (run %s). It keeps going on the Mac; "+
		"this is past the inline wait window. Check back, or for jobs this long hand them to background_build. "+
		"Resume the same session with `claude --continue` in %s.",
		e.elapsed.Round(time.Second), e.jobID, repoOrRoot(e.repo))
}

// ClaudeCodeRunner launches Claude Code (`claude -p`) on the Mac bridge under
// the boss's Claude subscription. It is the single launcher behind
// code_agent and background_build-on-Mac.
type ClaudeCodeRunner struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

// NewClaudeCodeRunner wires the runner to the bridge router + session prefs.
func NewClaudeCodeRunner(router *bridge.Router, prefs PreferenceFetcher) *ClaudeCodeRunner {
	return &ClaudeCodeRunner{router: router, prefs: prefs}
}

// ActiveBridge resolves the session's active bridge (from ctx's session id).
func (r *ClaudeCodeRunner) ActiveBridge(ctx context.Context) (bridge.Bridge, string, error) {
	if r == nil {
		return nil, "", errors.New("claude code runner not configured")
	}
	return pickBridge(ctx, r.router, r.prefs)
}

// DefaultModel is Infinity's pinned Claude Code model (INFINITY_CODE_AGENT_MODEL,
// e.g. "claude-opus-5[1m]"); "" defers to the Mac's own Claude setting.
func (r *ClaudeCodeRunner) DefaultModel() string {
	return strings.TrimSpace(os.Getenv("INFINITY_CODE_AGENT_MODEL"))
}

// DefaultEffort is the pinned effort (INFINITY_CODE_AGENT_EFFORT).
func (r *ClaudeCodeRunner) DefaultEffort() string {
	return strings.TrimSpace(os.Getenv("INFINITY_CODE_AGENT_EFFORT"))
}

// ClaudeCodeJob describes one detached `claude -p` run.
type ClaudeCodeJob struct {
	Bridge bridge.Bridge
	JobID  string
	Task   string
	// Repo is the working directory (Mac or cloud-flavored path; normalized
	// here). Empty → the bridge workspace root.
	Repo   string
	Model  string
	Effort string
	// MaxWait bounds how long Run polls before reporting the job still
	// running (it is never killed for this). Zero → codeAgentMaxWait.
	MaxWait time.Duration
	// KillOnCancel: when ctx is cancelled (Stop button, mid-turn steer) kill
	// the detached claude -p. The inline tool sets it; a background build
	// leaves Claude working and reports that instead.
	KillOnCancel bool
	// Heartbeat receives live progress: a label for the run row, plus the
	// current tool (action) and its target (detail) when known.
	Heartbeat func(label, action, detail string)
	// SetMeta receives run-row facts as they are learned: auth (the
	// subscription proof), model, effort, currentFile.
	SetMeta func(key, value string)
}

// Run launches the job on the Mac and polls it to completion. Errors are
// typed so callers can render them: *launchRejectedError (bridge said 4xx),
// *notSubscriptionError (refused before launch), *llm.QuotaError (the plan
// is spent), *stillRunningError (wait window closed, job left running).
func (r *ClaudeCodeRunner) Run(ctx context.Context, job ClaudeCodeJob) (string, error) {
	b := job.Bridge
	if b == nil || b.Name() != bridge.KindMac {
		return "", errors.New("code_agent: Claude Code runs on the Mac bridge only")
	}
	heartbeat := job.Heartbeat
	if heartbeat == nil {
		heartbeat = func(string, string, string) {}
	}
	setMeta := job.SetMeta
	if setMeta == nil {
		setMeta = func(string, string) {}
	}
	if job.MaxWait <= 0 {
		job.MaxWait = codeAgentMaxWait
	}
	// A cloud-flavored repo path (e.g. /workspace/infinity) still resolves on
	// the Mac: translate it to the Mac layout before launching.
	repo := bridge.NormalizePath(b, job.Repo)

	// Stop retrying something dead: while the boss's Claude plan is known
	// spent (the last run said "out of extra usage"), don't launch another
	// `claude -p` that will fail the same way.
	if until, detail, spent := llm.Exhausted(claudeCodeQuotaKey); spent {
		return "", &llm.QuotaError{Provider: claudeCodeQuotaKey, ResetsAt: until, Detail: detail}
	}

	// 1. Prove the sign-in before anything runs or bills.
	auth, err := r.probeAuth(ctx, b, repo)
	if err != nil {
		return "", err
	}
	setMeta("auth", auth.Label())
	if auth.apiKeyInEnv {
		setMeta("apikey_in_env", "unset for this run")
	}
	if !auth.Subscription() {
		return "", &notSubscriptionError{auth: auth}
	}
	// When no model/effort was pinned, the Mac's own Claude default is what
	// will code; record it so the run row (and the pill) name the real model.
	if job.Model == "" {
		setMeta("model", auth.defaultModel)
	}
	if job.Effort == "" {
		setMeta("effort", auth.defaultEffort)
	}
	heartbeat("Claude Code · signed in on your "+auth.Label(), "auth", "")

	// 2. Launch, detached.
	files := newClaudeJobFiles(job.JobID)
	launch := claudeLaunchScript(files, job.Task, job.Model, job.Effort)
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
	started := time.Now()
	heartbeat("Claude Code · launched claude -p · working…", "launch", "")

	// 3. Poll the status file. Each call is sub-second, so the 60s bridge
	// client cap and the 600s server cap never bite the long run. Each poll
	// also reads the tail of Claude's stream so the run row says what it is
	// doing right now.
	pollCmd := fmt.Sprintf(`if [ -f %s ]; then echo "DONE:$(cat %s)"; else echo RUNNING; fi; echo "===TAIL==="; tail -c %d %s 2>/dev/null`,
		files.status, files.status, claudeTailBytes, files.out)
	deadline := started.Add(job.MaxWait)
	lastAction, lastDetail := "", ""
	for {
		select {
		case <-ctx.Done():
			elapsed := time.Since(started).Round(time.Second)
			if !job.KillOnCancel {
				return "", &stillRunningError{jobID: job.JobID, repo: repo, elapsed: elapsed}
			}
			// The caller's context was cancelled: the Stop button, a mid-turn
			// message from the boss (SteerInterruptible), or a shutdown. KILL
			// the detached claude -p — its edits are git-reversible, and a job
			// the boss just interrupted must not keep rewriting his repo.
			killed := r.kill(b, repo, files.pid)
			return fmt.Sprintf(
				"code_agent was STOPPED after %s (Claude Code run %s %s). Any edits it had already "+
					"made are uncommitted in %s — check git_status/git_diff before touching that repo. "+
					"To pick the job back up later, run code_agent again; `claude --continue` in that "+
					"directory resumes its conversation.",
				elapsed, job.JobID, killed, repoOrRoot(repo)), nil
		case <-time.After(codeAgentPollEach):
		}

		pb, pc, pok := b.Post(ctx, "/bash", map[string]any{"cmd": pollCmd, "cwd": repo, "timeout_sec": 15})
		if pok && pc < 300 {
			raw, _ := bridgeBashOutput(pb)
			head, tail := splitMarker(raw, "", "===TAIL===")
			if strings.Contains(head, "DONE:") {
				exit := exitCodeFromDone(head)
				return r.collect(ctx, b, files, repo, exit, auth, started, setMeta)
			}
			if action, detail, found := claudeStreamActivity(tail); found {
				lastAction, lastDetail = action, detail
			}
		}
		elapsed := time.Since(started).Round(time.Second)
		heartbeat(claudeProgressLabel(lastAction, lastDetail, elapsed), lastAction, lastDetail)
		if lastDetail != "" {
			setMeta("currentFile", lastDetail)
		}

		if time.Now().After(deadline) {
			return "", &stillRunningError{jobID: job.JobID, repo: repo, elapsed: time.Since(started)}
		}
	}
}

// probeAuth runs the sign-in probe on the Mac and decodes it.
func (r *ClaudeCodeRunner) probeAuth(ctx context.Context, b bridge.Bridge, repo string) (claudeAuth, error) {
	body, code, ok := b.Post(ctx, "/bash", map[string]any{"cmd": claudeAuthProbeCmd, "cwd": repo, "timeout_sec": 15})
	if ok && code >= 400 && code < 500 {
		return claudeAuth{}, &launchRejectedError{bridge: string(b.Name()), status: code, detail: bridgeErrorDetail(body)}
	}
	if !ok || code >= 300 {
		return claudeAuth{}, fmt.Errorf("code_agent: sign-in check via %s failed (status=%d): %s", b.Name(), code, string(body))
	}
	out, _ := bridgeBashOutput(body)
	return parseClaudeAuth(out), nil
}

// claudeJobFiles are the per-job files under codeAgentTmpDir.
type claudeJobFiles struct {
	out, err, status, pid, settings string
}

func newClaudeJobFiles(jobID string) claudeJobFiles {
	return claudeJobFiles{
		out:      fmt.Sprintf("%s/%s.out", codeAgentTmpDir, jobID),
		err:      fmt.Sprintf("%s/%s.err", codeAgentTmpDir, jobID),
		status:   fmt.Sprintf("%s/%s.status", codeAgentTmpDir, jobID),
		pid:      fmt.Sprintf("%s/%s.pid", codeAgentTmpDir, jobID),
		settings: fmt.Sprintf("%s/settings.json", codeAgentTmpDir),
	}
}

// claudeLaunchScript is the one bash round-trip that starts the detached
// `claude -p`: ensure the tmp dir, (re)write the delete-gate settings, strip
// any API key from the environment so ONLY the Mac's sign-in (the boss's
// subscription) can answer, then nohup the run. Task/model go via exported
// env so the inner single-quoted bash -c needs no fragile nested quoting.
// stream-json (+ --verbose, required with it) makes Claude's activity
// readable mid-run; its last line is the final result message.
func claudeLaunchScript(f claudeJobFiles, task, model, effort string) string {
	inner := fmt.Sprintf(
		`claude -p "$INF_TASK" ${INF_MODEL:+--model "$INF_MODEL"} ${INF_EFFORT:+--effort "$INF_EFFORT"} --output-format stream-json --verbose `+
			`--permission-mode bypassPermissions --settings %s > %s 2> %s; echo $? > %s`,
		f.settings, f.out, f.err, f.status,
	)
	return strings.Join([]string{
		"mkdir -p " + codeAgentTmpDir,
		"cat > " + f.settings + " <<'INFEOF'\n" + codeAgentSettingsJSON() + "\nINFEOF",
		// An API key in the bridge's shell would pre-empt the sign-in inside
		// Claude Code and bill the API instead of the subscription.
		"unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN",
		"export INF_TASK=" + shellQuote(task),
		"export INF_MODEL=" + shellQuote(model),
		"export INF_EFFORT=" + shellQuote(effort),
		"nohup bash -c " + shellQuote(inner) + " >/dev/null 2>&1 &",
		// Record the wrapper's pid so a Stop / mid-turn steer can actually
		// kill the job (2026-08-26: the boss said "don't build", the job kept
		// editing his repo for nine minutes and "couldn't be stopped").
		`echo $! > ` + f.pid,
		`echo "PID:$!"`,
	}, "\n")
}

// exitCodeFromDone reads "DONE:<n>" out of a poll reply.
func exitCodeFromDone(head string) int {
	i := strings.Index(head, "DONE:")
	if i < 0 {
		return 0
	}
	rest := strings.TrimSpace(head[i+len("DONE:"):])
	if nl := strings.IndexAny(rest, "\n\r"); nl >= 0 {
		rest = rest[:nl]
	}
	n := 0
	for _, ch := range strings.TrimSpace(rest) {
		if ch < '0' || ch > '9' {
			break
		}
		n = n*10 + int(ch-'0')
	}
	return n
}

// kill terminates the detached claude -p (wrapper + children) recorded in
// pidf. Runs on a FRESH context because the tool ctx is already cancelled.
// Returns a short human clause for the tool result.
func (r *ClaudeCodeRunner) kill(b bridge.Bridge, repo, pidf string) string {
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

// collect reads the finished output, parses Claude's final result message,
// and folds in any blocked-delete notice for the boss to approve.
func (r *ClaudeCodeRunner) collect(ctx context.Context, b bridge.Bridge, f claudeJobFiles, repo string, exitCode int, auth claudeAuth, started time.Time, setMeta func(string, string)) (string, error) {
	fetch := fmt.Sprintf(`echo "===OUT==="; tail -c %d %s 2>/dev/null; echo "===ERR==="; tail -c 4000 %s 2>/dev/null`,
		claudeResultTailBytes, f.out, f.err)
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

	res := parseClaudeStreamResult(outPart)
	model := ""
	if res.parsed {
		// Claude names the model that actually ran; that beats our request.
		for m := range res.ModelUsage {
			model = m
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
	if !res.parsed && exitCode != 0 {
		// No result message and a non-zero exit: Claude never got to answer
		// (a crash, a bad flag, a missing binary). Never read as success.
		return "", fmt.Errorf("code_agent: claude -p exited %d without a result: %s",
			exitCode, strings.TrimSpace(lastChars(errPart+"\n"+outPart, 1500)))
	}
	var sb strings.Builder
	if res.Result != "" {
		sb.WriteString(res.Result)
	} else {
		// Fall back to raw stdout if there was no result message.
		sb.WriteString(strings.TrimSpace(lastChars(outPart, 4000)))
	}

	if strings.Contains(errPart, "INFINITY_DELETE_BLOCKED") {
		sb.WriteString("\n\n⚠️ **Delete blocked — needs your approval.** Claude Code wanted to run a destructive/delete " +
			"command and the gate stopped it (see its summary above for what). Tell me to go ahead and I'll run that one " +
			"command through the normal Trust approval.")
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "code_agent finished but returned no output. Check the run logs.", nil
	}
	// The proof line: which plan paid, which model ran, how long it took.
	sb.WriteString("\n\n_Ran as `claude -p` on Claude Code (" + auth.Label() + ")")
	if model != "" {
		sb.WriteString(" · model " + model)
	}
	sb.WriteString(" · " + time.Since(started).Round(time.Second).String() + "_")
	return sb.String(), nil
}

// lastChars returns the trailing n bytes of s.
func lastChars(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// claudeProgressLabel is the run-row label for a poll: what Claude Code is
// doing right now, and for how long the run has been going.
func claudeProgressLabel(action, detail string, elapsed time.Duration) string {
	what := "working"
	switch {
	case action != "" && detail != "":
		what = action + " " + detail
	case action != "":
		what = action
	}
	return "Claude Code · " + what + " · " + elapsed.String()
}

// claudeStreamActivity reads the newest assistant event in a stream-json
// tail and names Claude's current tool call (name + its target), or the text
// it is writing. found is false when the tail holds no assistant event.
func claudeStreamActivity(tail string) (action, detail string, found bool) {
	lines := strings.Split(tail, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Type  string         `json:"type"`
					Name  string         `json:"name"`
					Input map[string]any `json:"input"`
					Text  string         `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "assistant" {
			continue
		}
		for j := len(ev.Message.Content) - 1; j >= 0; j-- {
			c := ev.Message.Content[j]
			if c.Type == "tool_use" && c.Name != "" {
				return c.Name, claudeToolDetail(c.Input), true
			}
		}
		for j := len(ev.Message.Content) - 1; j >= 0; j-- {
			c := ev.Message.Content[j]
			if c.Type == "text" && strings.TrimSpace(c.Text) != "" {
				return "writing", clipOneLine(c.Text, 100), true
			}
		}
	}
	return "", "", false
}

// claudeToolDetail pulls the most informative target out of a tool_use
// input: the file being touched, else the command / pattern / query.
func claudeToolDetail(input map[string]any) string {
	for _, k := range []string{"file_path", "path", "notebook_path"} {
		if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	for _, k := range []string{"command", "pattern", "query", "url", "description", "prompt"} {
		if v, ok := input[k].(string); ok && strings.TrimSpace(v) != "" {
			return clipOneLine(v, 120)
		}
	}
	return ""
}

func clipOneLine(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", " "))
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

// claudeResult is the shape of Claude Code's final `result` message (the
// same object `--output-format json` prints). APIErrorStatus is the upstream
// HTTP status Claude Code saw (429 = the plan is out of usage); nil in the
// JSON when the run succeeded.
type claudeResult struct {
	Type           string                     `json:"type"`
	Result         string                     `json:"result"`
	IsError        bool                       `json:"is_error"`
	Subtype        string                     `json:"subtype"`
	APIErrorStatus int                        `json:"api_error_status"`
	ModelUsage     map[string]json.RawMessage `json:"modelUsage"`
	parsed         bool
}

// parseClaudeStreamResult finds the final `result` message in a stream-json
// tail (the last line, per the docs; scanned from the end so a trailing
// partial line or blank never hides it). Falls back to decoding the whole
// blob as one object (the legacy --output-format json shape).
func parseClaudeStreamResult(s string) claudeResult {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] != '{' {
			continue
		}
		var res claudeResult
		if err := json.Unmarshal([]byte(line), &res); err != nil {
			continue
		}
		if res.Type == "result" {
			res.Result = strings.TrimSpace(res.Result)
			res.parsed = true
			return res
		}
	}
	return parseClaudeResult(s)
}

// parseClaudeResult decodes ONE JSON result object out of claude's stdout.
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

// macClaudeDefaults reads the "SETTINGS:{...}" line the probe prints (the
// Mac's ~/.claude/settings.json) and returns its model and effortLevel, ""
// when absent.
func macClaudeDefaults(probeOut string) (model, effort string) {
	a := parseClaudeAuth(probeOut)
	return a.defaultModel, a.defaultEffort
}

// ── the code_agent tool ─────────────────────────────────────────────────

// RegisterCodeAgentTool wires the code_agent tool over the shared runner;
// tracker books the mem_runs row.
func RegisterCodeAgentTool(r *Registry, runner *ClaudeCodeRunner, tracker *runs.Tracker) {
	r.Register(&codeAgent{runner: runner, tracker: tracker})
}

type codeAgent struct {
	runner  *ClaudeCodeRunner
	tracker *runs.Tracker
}

func (t *codeAgent) Name() string   { return "code_agent" }
func (t *codeAgent) ReadOnly() bool { return false }
func (t *codeAgent) Description() string {
	return "Delegate a coding task to Claude Code on the boss's Mac (runs under his Claude Max " +
		"subscription, NOT the chat model's quota; every run is verified to be signed in to that subscription before " +
		"it starts). Give it a complete, self-contained brief - it reads the repo, writes/edits the code, runs " +
		"builds/tests, and returns a summary of what it changed. This is the ONLY way to write code on the Mac " +
		"bridge: you orchestrate, Claude Code does the implementation. It runs freely; only destructive deletes are " +
		"blocked and surfaced for the boss to approve. For a tiny one-line/deterministic edit, fs_edit/claude_code__Edit " +
		"are fine; for anything real, use this."
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
	if t.runner == nil {
		return "", errors.New("code_agent: runner not configured")
	}
	repo := strString(in, "repo")
	// Model + effort for the nested Claude Code: the call's own choice first,
	// else Infinity's defaults (INFINITY_CODE_AGENT_MODEL / _EFFORT, e.g.
	// "claude-opus-5[1m]" / "high"), else the Mac's own Claude settings.
	model := strDefault(in, "model", t.runner.DefaultModel())
	effort := strDefault(in, "effort", t.runner.DefaultEffort())

	// Only meaningful on the Mac bridge - that's where the Max-billed
	// Claude Code CLI lives. On Cloud, the chat model codes directly.
	b, why, err := t.runner.ActiveBridge(ctx)
	if err != nil {
		return "", fmt.Errorf("code_agent: %s", why)
	}
	if b.Name() != bridge.KindMac {
		return fmt.Sprintf("code_agent only runs on the Mac bridge (it delegates to the boss's Claude Code "+
			"Max subscription). The active bridge is %q. On the Cloud bridge, write the code yourself with "+
			"fs_save/fs_edit in /workspace.", b.Name()), nil
	}

	// Stop retrying something dead: while the boss's Claude plan is known
	// spent, don't book a run that will fail the same way. Held in the shared
	// quota ledger so Settings can show it alongside the chat brain's state.
	if until, detail, spent := llm.Exhausted(claudeCodeQuotaKey); spent {
		return claudeCodeHeldGuidance(until, detail), nil
	}

	// Book the run so Studio shows a live, navigation-proof spinner.
	// runs.Handle is the real API (Begin → Progress → Finish); it's
	// nil-safe, so this degrades cleanly when the pool isn't wired.
	label := "Claude Code: " + truncateForLabel(task, 80)
	handle := t.tracker.Begin(ctx, runs.Kind("code_agent"), "", label, runs.SourceAgent)
	jobID := handle.ID()
	if jobID == "" {
		jobID = fmt.Sprintf("job-%d", time.Now().UnixNano())
	}
	// Progress + meta go on context.Background() so they persist even when
	// ctx is cancelled (Stop button or core shutdown). The last-known
	// progress_label survives a restart and RecoverStranded surfaces it.
	setMeta := func(key, value string) {
		if value != "" {
			handle.SetMetaString(context.Background(), key, value)
		}
	}
	heartbeat := func(note, _ string, detail string) {
		handle.Progress(context.Background(), 0, note)
		if detail != "" {
			setMeta("currentFile", detail)
		}
	}
	// The run row carries which model is coding (Studio's bridge pill flashes
	// it while the run is live): the requested id now, the exact id Claude
	// reports once it finishes, and the subscription proof once verified.
	setMeta("engine", "claude_code")
	setMeta("model", model)
	setMeta("effort", effort)

	summary, runErr := t.runner.Run(ctx, ClaudeCodeJob{
		Bridge:       b,
		JobID:        jobID,
		Task:         task,
		Repo:         repo,
		Model:        model,
		Effort:       effort,
		MaxWait:      codeAgentMaxWait,
		KillOnCancel: true,
		Heartbeat:    heartbeat,
		SetMeta:      setMeta,
	})
	// Always close the run row on a fresh context. Using the tool ctx here
	// means a cancelled ctx (Stop button, graceful shutdown) silently drops
	// the Finish UPDATE and leaves the row stuck 'running' until the reaper.
	finCtx, finCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer finCancel()
	if runErr != nil {
		var still *stillRunningError
		if errors.As(runErr, &still) {
			// Past the inline wait window; Claude keeps working on the Mac.
			// The row closes with that truth as its summary, not as a failure.
			msg := still.inlineMessage()
			handle.Finish(finCtx, nil, msg)
			return msg, nil
		}
		handle.Finish(finCtx, runErr, summary)
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
		var notSub *notSubscriptionError
		if errors.As(runErr, &notSub) {
			return notSub.guidance(), nil
		}
		// The boss's Claude plan is spent: the run row is red (the truth is
		// in Agent Work), and the model gets a directive rather than a raw
		// error it would retry to the iteration cap.
		if q, ok := llm.AsQuota(runErr); ok {
			return claudeCodeHeldGuidance(q.ResetsAt, q.Detail), nil
		}
		h := errs.Humanize(runErr)
		if h.Category == errs.CatBridge {
			return fmt.Sprintf("code_agent couldn't run: %s — the Mac bridge is unreachable. "+
				"Don't stop: write the change yourself with fs_edit/fs_save in /workspace, then "+
				"`bash_run` go build ./... && go vet ./... && go test ./..., then git_commit (and git_push if autonomy is on). "+
				"The cloud workspace has the Go toolchain pre-installed.", h.Summary), nil
		}
		return "", runErr
	}
	handle.Finish(finCtx, nil, summary)
	return summary, nil
}

// InterruptOnSteer opts code_agent into the loop's steer-interrupt: a message
// from the boss while Claude Code is working cancels the job (and kills it)
// so he is answered now, not after the job.
func (t *codeAgent) InterruptOnSteer() bool { return true }

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

// claudeCodeHeldGuidance is the tool result while Claude Code is out of
// usage. It never steers the chat model into writing the code itself: on the
// Mac bridge coding runs on the boss's Claude plan, and quietly moving it onto
// his ChatGPT plan is the leak that spent that plan (2026-08-26, 2026-08-28).
func claudeCodeHeldGuidance(until time.Time, detail string) string {
	when := "for now"
	if !until.IsZero() {
		when = "until about " + llm.FormatLocalClock(until)
	}
	if detail == "" {
		detail = "its plan's usage allowance is spent"
	}
	return fmt.Sprintf("HOLD: Claude Code (the boss's Claude Max plan) is out of usage %s (%s). "+
		"Do NOT call code_agent or background_build again before then; they will fail the same way. "+
		"Do NOT write the code yourself with claude_code__Edit / claude_code__Write / fs_edit: on the Mac bridge "+
		"coding runs on his Claude plan, never on the chat model's ChatGPT plan. "+
		"Tell the boss plainly that his Claude plan is out of usage %s and that the coding picks back up after the reset. "+
		"Only if he explicitly tells you to do it on the chat model now may you write it yourself.", when, detail, when)
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

// splitMarker carves a "===A===…===B===…" blob into its two parts. An empty
// a means "from the start".
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
