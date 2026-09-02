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
	"log"
	"os"
	"strings"
	"sync"
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

// deleteBlockedNotice is what the boss reads when the gate stopped a delete.
// One constant, so the finished path and every salvaged path (killed,
// detached, abandoned) say it the same way - the notice must survive a job
// that never got to write its own summary.
const deleteBlockedNotice = "\n\n⚠️ **Delete blocked — needs your approval.** Claude Code wanted to run a destructive/delete " +
	"command and the gate stopped it (see its summary above for what). Tell me to go ahead and I'll run that one " +
	"command through the normal Trust approval."

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
	// codeAgentMaxWait is the job's own ceiling.
	//
	// It was 20 minutes, which is HALF the size of the work the boss actually
	// asks for: the run he watched on 2026-08-29 took 47 minutes, 170 turns
	// and 37 file edits, and finished successfully with nobody left watching.
	// Wall-clock is no longer the completion mechanism either way — a job now
	// finishes on the terminal `result` event in its own stream (see
	// claudeTerminalResult) — so a generous ceiling costs one sub-second poll
	// per 15s and buys the difference between "I can't rely on him" and "I can
	// walk away". Genuinely dead work is caught by the stall detector and the
	// liveness reaper, not by cutting a healthy build off at twenty minutes.
	codeAgentMaxWait = 90 * time.Minute
	// codeAgentJobGrace is the slack on top of MaxWait that the job's OWN
	// context gets, so the final read of the transcript is never cut off by
	// the same clock that ended the polling.
	codeAgentJobGrace = 2 * time.Minute
	// claudeTailBytes is how much of Claude's stream-json we read on each
	// poll to name its current activity; claudeResultTailBytes is how much we
	// read at the end to find the final `result` line (the last line of the
	// stream, per the Claude Code docs) without tripping the bridge's output
	// cap on a long run. claudeInitHeadBytes is the HEAD we read for the
	// `system/init` line carrying Claude's session id — it is the first line
	// of the stream, so on a long run it has long since left the tail.
	claudeTailBytes       = 12000
	claudeResultTailBytes = 30000
	claudeInitHeadBytes   = 4000
	// claudeLastLineBytes bounds the LAST line of the stream, read on every
	// poll so completion is detected from Claude's own terminal `result`
	// event. It is read separately from the slice because a long report can be
	// bigger than the activity window, and reading it as `tail -n 1` means the
	// completion check never depends on how much of the stream fits in a tail.
	claudeLastLineBytes = 24000
	// The incremental read of the stream, which is what feeds both the live
	// activity line and the nested steps forwarded into the chat.
	//
	// It is LINE-based, not byte-based, because a byte window cuts events in
	// half and the bookkeeping to stitch them back together is the kind of
	// thing that is subtly wrong forever. Lines are whole events by
	// construction. Each is capped at claudeLineMaxCols so one `Read` of a
	// 400KB file cannot drown a poll — such a line stops being valid JSON and
	// is simply skipped, and the CALL that produced it (which carries the file
	// path, the part worth showing) is its own, small line.
	//
	// 80 lines per 15s poll is ~18x the event rate of the busiest real build
	// we have measured (829 events in 47 minutes), so it never falls behind;
	// claudeMaxDrains lets a BURST of large events catch up inside one poll
	// instead of trickling out over the next minute.
	claudeLinesPerPoll = 80
	claudeLineMaxCols  = 8000
	claudeChunkBytes   = 32000
	claudeMaxDrains    = 4
)

// codeAgentPollEach / codeAgentTmpDir are vars so the end-to-end test can
// poll fast and keep its job files out of the real /tmp/inf-code.
var (
	codeAgentPollEach = 15 * time.Second
	codeAgentTmpDir   = "/tmp/inf-code"
)

// pinnedCodeModel is the model a Mac coding run uses when the call does not
// name one. Claude Code's own default is Sonnet, and on the Mac the boss is
// paying for Opus on his Max plan: a serious build must never quietly drop a
// tier because nobody passed a model. The pin is applied at the execution
// boundary (ClaudeCodeRunner.Run) so every caller - code_agent inline,
// background_build detached - gets it by construction (Rule #1b).
// INFINITY_CODE_AGENT_MODEL overrides it.
const pinnedCodeModel = "claude-opus-5[1m]"

// repo rejection reasons, in the order preflight decides them.
const (
	repoReasonEmpty    = "empty"
	repoReasonMissing  = "missing"
	repoReasonNotDir   = "notdir"
	repoReasonUmbrella = "umbrella"
	repoReasonNotGit   = "notgit"
)

// repoRejectedError: the requested working directory is not a repo Claude
// Code may be turned loose in, so NOTHING was launched. Before this check a
// missing/blank repo silently landed in the bridge's default cwd - ~/Dev, the
// umbrella folder holding every one of the boss's repos - and Claude Code
// would happily start editing whatever it found there.
type repoRejectedError struct {
	requested string
	resolved  string
	reason    string
}

func (e *repoRejectedError) Error() string {
	return "code_agent: " + e.detail() + "; not launching"
}

// detail is the plain sentence naming what is wrong with the path.
func (e *repoRejectedError) detail() string {
	where := strings.TrimSpace(e.requested)
	if where == "" {
		where = "(none given)"
	}
	switch e.reason {
	case repoReasonEmpty:
		return "no repo was given, and a coding run must never fall back to the ~/Dev umbrella folder"
	case repoReasonMissing:
		return "the repo path " + where + " does not exist on the Mac"
	case repoReasonNotDir:
		return "the repo path " + where + " is a file, not a directory"
	case repoReasonUmbrella:
		return "the path " + repoOrRoot(e.resolved) + " is the umbrella folder that holds every repo, not a repo"
	case repoReasonNotGit:
		return "the directory " + repoOrRoot(e.resolved) + " is not a git repository"
	}
	return "the repo path " + where + " is unusable"
}

// guidance is the tool result: the model fixes the path and calls again, and
// never wanders off into whatever directory happened to answer.
func (e *repoRejectedError) guidance() string {
	return fmt.Sprintf("NOT LAUNCHED: %s. Nothing was started and nothing was billed. "+
		"Claude Code only runs inside one specific git repository. Pass `repo` as the absolute path of the repo "+
		"to work in - on the Mac they live under ~/Dev/<repo> (Infinity itself is ~/Dev/infinity) - and call "+
		"code_agent again. Do NOT pass ~/Dev itself. Do NOT write the code yourself on the chat model.",
		e.detail())
}

// repoPreflightScript resolves the requested working directory ON the bridge
// and reports what is actually there: the absolute path, the git worktree
// root, the home directory (so the umbrella folders can be recognised), and
// where `claude` really lives. It runs WITHOUT a cwd on purpose - handing a
// bad path to the bridge as cwd answers with a blunt 400 "cwd not a
// directory", which then reads as "the Mac dropped out" instead of "your
// path is wrong".
func repoPreflightScript(repo string) string {
	return strings.Join([]string{
		"INF_REPO=" + shellQuote(repo),
		`D="${INF_REPO/#\~/$HOME}"`,
		// Physical HOME, so the umbrella comparison matches `pwd -P` below
		// even where the home path runs through a symlink.
		`echo "HOME:$(cd "$HOME" 2>/dev/null && pwd -P)"`,
		`echo "CLAUDEBIN:$(command -v claude 2>/dev/null)"`,
		`if [ ! -e "$D" ]; then echo "REPO:missing"; exit 0; fi`,
		`if [ ! -d "$D" ]; then echo "REPO:notdir"; exit 0; fi`,
		`P=$(cd "$D" 2>/dev/null && pwd -P)`,
		`if [ -z "$P" ]; then echo "REPO:missing"; exit 0; fi`,
		`echo "REPOPATH:$P"`,
		`echo "REPOROOT:$(cd "$P" && git rev-parse --show-toplevel 2>/dev/null)"`,
	}, "\n")
}

// repoInfo is what preflight learned about the run's working directory and
// the engine that will execute in it.
type repoInfo struct {
	// Path is the requested directory, absolute and symlink-resolved.
	Path string
	// Root is the git worktree root; "" when the directory is not in a repo.
	Root string
	// ClaudeBin is the resolved `claude` executable on the bridge.
	ClaudeBin string
	home      string
	reason    string
}

// parseRepoPreflight decodes repoPreflightScript's output.
func parseRepoPreflight(out string) repoInfo {
	var info repoInfo
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "HOME:"):
			info.home = strings.TrimSpace(strings.TrimPrefix(line, "HOME:"))
		case strings.HasPrefix(line, "CLAUDEBIN:"):
			info.ClaudeBin = strings.TrimSpace(strings.TrimPrefix(line, "CLAUDEBIN:"))
		case strings.HasPrefix(line, "REPOPATH:"):
			info.Path = strings.TrimSpace(strings.TrimPrefix(line, "REPOPATH:"))
		case strings.HasPrefix(line, "REPOROOT:"):
			info.Root = strings.TrimSpace(strings.TrimPrefix(line, "REPOROOT:"))
		case line == "REPO:missing":
			info.reason = repoReasonMissing
		case line == "REPO:notdir":
			info.reason = repoReasonNotDir
		}
	}
	return info
}

// isUmbrellaDir reports whether path is a container of repos rather than a
// repo: the filesystem root, the bridge workspace root, the boss's home, or
// ~/Dev - the folder every one of his repos sits in.
func isUmbrellaDir(path, home string) bool {
	p := strings.TrimRight(strings.TrimSpace(path), "/")
	if p == "" {
		return true
	}
	switch p {
	case "/", "/workspace", "/workspace/projects", "/Users":
		return true
	}
	h := strings.TrimRight(strings.TrimSpace(home), "/")
	return h != "" && (p == h || p == h+"/Dev")
}

// validateRepo turns a preflight reading into a launch/refuse decision.
func validateRepo(requested string, info repoInfo) error {
	reject := func(reason string) error {
		return &repoRejectedError{requested: requested, resolved: info.Path, reason: reason}
	}
	switch {
	case info.reason != "":
		return reject(info.reason)
	case strings.TrimSpace(info.Path) == "":
		return reject(repoReasonMissing)
	case isUmbrellaDir(info.Path, info.home):
		return reject(repoReasonUmbrella)
	case strings.TrimSpace(info.Root) == "":
		return reject(repoReasonNotGit)
	}
	return nil
}

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
	// cloudToken is the CLAUDE_CODE_OAUTH_TOKEN the cloud box signs in with.
	// The cloud has no ~/.claude.json to read back, so the proof is different
	// in KIND: Anthropic issues this token only against a Pro/Max/Team/
	// Enterprise plan, so holding one IS holding a subscription. Set only on
	// the cloud bridge, and never exported on the Mac, where it would
	// outrank the real sign-in.
	cloudToken string
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
	if a.cloudToken != "" {
		return true
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
	case a.cloudToken != "":
		return "your Claude subscription · cloud"
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

// stillRunningError: the inline wait window closed while Claude Code was
// still working on the Mac. The job was NOT killed and NOT cancelled - this
// is the "keep working" outcome, not a failure.
type stillRunningError struct {
	jobID   string
	repo    string
	elapsed time.Duration
	// activity is what the job was doing at the last poll ("Edit
	// core/x.go"), so the boss gets a fact instead of a spinner.
	activity string
	// following: a background follower is still watching this job and will
	// close its run row with the real receipt when it lands. Set by the
	// detach path; the caller uses it to decide whether the run row stays
	// open (it must, or the watch would settle on a job that is still going).
	following bool
	// reporting: a durable watch was registered on the run, so its completion
	// is delivered back into the chat by the watch poller. Only claimed when
	// the watch was actually created - promising a callback that no row backs
	// is the same lie as reporting a killed job as finished.
	reporting bool
}

func (e *stillRunningError) Error() string {
	return fmt.Sprintf("code_agent: Claude Code is STILL WORKING after %s (run %s in %s); it was not stopped",
		e.elapsed.Round(time.Second), e.jobID, repoOrRoot(e.repo))
}

// inlineMessage is the tool result the model reads. It tells the truth the
// old copy did not: the job is alive, this is its run id, and it reports back
// on its own - so the model neither declares failure nor launches a duplicate.
func (e *stillRunningError) inlineMessage() string {
	doing := "working"
	if strings.TrimSpace(e.activity) != "" {
		doing = e.activity
	}
	var b strings.Builder
	fmt.Fprintf(&b, "STILL RUNNING (not stopped, not failed): Claude Code is %s in %s after %s. Run id %s.",
		doing, repoOrRoot(e.repo), e.elapsed.Round(time.Second), e.jobID)
	switch {
	case e.following && e.reporting:
		b.WriteString(" I am still following it: when it finishes, its real result lands on that run AND is delivered back into this chat on its own — you will be told, you do not have to poll for it.")
	case e.following:
		b.WriteString(" I am still following it: when it finishes, its real result lands on that run (check it with the run id above).")
	default:
		b.WriteString(" It keeps working on the Mac past this turn; check the run for its result.")
	}
	b.WriteString(" Do NOT call code_agent again for the same work (that would run it twice), and do NOT tell the boss it failed or was cancelled — " +
		"tell him plainly that it is still going and what it is doing right now.")
	return b.String()
}

// duplicateJobError: a coding job is ALREADY running for this chat, so this
// launch was refused before it spent anything.
type duplicateJobError struct {
	runID string
	label string
}

func (e *duplicateJobError) Error() string {
	return fmt.Sprintf("code_agent: a coding job is already running for this conversation (run %s)", e.runID)
}

// guidance is what the model reads. It has to make the RIGHT next move
// obvious, because the wrong one - refereeing two jobs by hand with `kill`
// and a bash wait loop - is exactly what happened when nothing refused the
// second launch.
func (e *duplicateJobError) guidance() string {
	what := strings.TrimSpace(e.label)
	if what == "" {
		what = "a coding job"
	}
	return fmt.Sprintf("ALREADY RUNNING - nothing was launched, and nothing is wrong. %s is still working in this conversation (run %s). "+
		"Two coding jobs on one repo make conflicting edits and bill the boss twice, so this second launch was refused.\n\n"+
		"Do NOT try to manage it by hand: no `kill`, no `pgrep`, no bash loop that waits for a file. It reports back into this chat on "+
		"its own when it lands. Tell the boss plainly what is running and what it is doing right now. If it genuinely needs to be "+
		"replaced, ask him first - only he can call for that.", what, e.runID)
}

// LiveRunLookup answers "is a coding job already running for this chat?".
// Supplied by serve.go over mem_runs so the check is durable and shared, not
// an in-process map per tool.
type LiveRunLookup func(ctx context.Context, sessionID string) (runID, label string, found bool)

// ClaudeCodeRunner launches Claude Code (`claude -p`) on the Mac bridge under
// the boss's Claude subscription. It is the single launcher behind
// code_agent and background_build-on-Mac.
type ClaudeCodeRunner struct {
	// brain is the conversational wiring (Core's public URL + the MCP token
	// minter). Zero until AttachBrain is called; see claude_brain.go.
	brain  BrainWiring
	router *bridge.Router
	prefs  PreferenceFetcher
	// liveRuns is the one-job-per-conversation guard. Optional; without it
	// the old behaviour (no guard) applies.
	liveRuns LiveRunLookup
	// steps forwards the nested job's own tool calls into the parent chat.
	// Optional; without it the job is as opaque as it used to be.
	steps StepSink
	// plans mirrors the nested job's own checklist onto the parent chat's
	// plan, so a Claude Code build draws the same dock as every other brain.
	// Optional; without it the dock falls back to run telemetry.
	plans NestedPlanSink
}

// AttachLiveRunLookup installs the one-coding-job-per-conversation guard.
//
// It goes on the RUNNER, not on either tool, because the runner is the single
// launcher both `code_agent` and `background_build`-on-Mac go through. Putting
// it on a tool is how the gap happened: BackgroundAgent had a per-session
// in-process guard, `code_agent` had none, and neither could see the other -
// so on 2026-08-29 a background build (05:14:07) and a code_agent run
// (05:15:12) both went at the same repo for the same task.
func (r *ClaudeCodeRunner) AttachLiveRunLookup(fn LiveRunLookup) {
	if r != nil {
		r.liveRuns = fn
	}
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

// DefaultModel is Infinity's pinned Claude Code model: INFINITY_CODE_AGENT_MODEL
// when set, else pinnedCodeModel (Opus 5). It never returns "" - deferring to
// the Mac's own Claude setting is how a build silently ends up on Sonnet.
func (r *ClaudeCodeRunner) DefaultModel() string {
	if m := strings.TrimSpace(os.Getenv("INFINITY_CODE_AGENT_MODEL")); m != "" {
		return m
	}
	return pinnedCodeModel
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
	// Label is the run row's headline ("Claude Code: <task>"). It becomes the
	// title of the plan this job's checklist mirrors into, so the dock keeps
	// the headline it was already showing instead of renaming itself the
	// moment the first checklist lands.
	Label string
	// Repo is the working directory (Mac or cloud-flavored path; normalized
	// here). It must name one real git repository: empty, missing, or the
	// ~/Dev umbrella is refused before launch, never defaulted.
	Repo string
	// Model is the Claude model for this run. Empty → pinnedCodeModel /
	// INFINITY_CODE_AGENT_MODEL, applied in Run so no path falls to Sonnet.
	Model  string
	Effort string
	// ResumeSessionID continues an EARLIER Claude Code session instead of
	// starting cold: `claude --resume <id>`. Claude reloads that session's
	// full context - what it already read, wrote and tried - so a job that
	// was interrupted picks up where it stopped rather than re-deriving the
	// work from scratch and possibly redoing edits already on disk.
	//
	// The id comes off mem_runs.meta.claude_session_id, which every run
	// stamps from the first line of its own stream. Until this field existed
	// that id was captured and then unusable: the raw-shell guard blocks the
	// MODEL from running `claude --resume` (correctly - that path would leave
	// the subscription proof and the delete gate behind), and there was no
	// sanctioned way in. This is it.
	ResumeSessionID string
	// ParentSession is the chat this job's steps are forwarded into. Empty →
	// the session on ctx, which is right for an inline `code_agent` call. A
	// background build has no session on its context (it runs on a detached
	// one by design) and must pass the originating chat explicitly, or its
	// steps would have nowhere to land — the same omission that left
	// background builds out of the activity fingerprint.
	ParentSession string
	// MaxWait bounds how long Run polls before reporting the job still
	// running (it is never killed for this). Zero → codeAgentMaxWait.
	MaxWait time.Duration
	// KillOnCancel: when the INLINE window is cancelled outright (the Stop
	// button, an explicit stop order) kill the detached claude -p. A timeout
	// is never a cancel: the turn's clock running out leaves Claude working.
	// The inline tool sets it; a background build leaves Claude working and
	// reports that instead.
	KillOnCancel bool
	// Inline is the CALLER's context (the chat turn). It bounds how long Run
	// waits INLINE - never how long the job gets to work, which is ctx. When
	// it ends, the job is killed only if it was explicitly cancelled AND
	// KillOnCancel is set; a deadline detaches. Zero → ctx, the pre-detach
	// behaviour for callers that have no separate turn.
	//
	// This split is the fix for the 2026-08-28 guillotine: INFINITY_TURN_TIMEOUT
	// (15 min) always fired before this tool's own 20-minute ceiling, so every
	// long job took the kill branch and the "never killed" path was dead code.
	Inline context.Context
	// Detach fires when the loop wants the inline wait to END without killing
	// anything - the boss said something that is not a stop. Nil for callers
	// that have no such signal (crons, delegates), where a nil channel simply
	// never fires.
	Detach <-chan struct{}
	// Detached is called when a job that outlived the inline wait finally
	// settles. The caller closes its run row with it, so a detached job still
	// ends with a real receipt instead of a spinner nothing ever closes.
	Detached func(JobOutcome)
	// Stopped is called when an EXPLICIT stop tore the job down, with the same
	// summary Run returns. The caller closes its run row as interrupted rather
	// than ok: a killed job reached no verdict, and a green row carrying a
	// "was STOPPED" summary is the false green this whole path exists to
	// prevent (runs.StoppedInterrupted).
	Stopped func(summary string)
	// Heartbeat receives live progress: a label for the run row, the current
	// tool (action) and its target (detail) when known, and step - how many
	// distinct things the job has done. The runner counts steps itself so
	// both engines count the same way; they used to each keep their own
	// tally with slightly different rules.
	Heartbeat func(label, action, detail string, step int)
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
	if b == nil {
		return "", errors.New("code_agent: no bridge is reachable, so there is nowhere to run")
	}
	heartbeat := job.Heartbeat
	if heartbeat == nil {
		heartbeat = func(string, string, string, int) {}
	}
	setMeta := job.SetMeta
	if setMeta == nil {
		setMeta = func(string, string) {}
	}
	if job.MaxWait <= 0 {
		job.MaxWait = codeAgentMaxWait
	}
	// Opus, always, unless this call named something else. Enforced here, at
	// the one execution boundary, so no caller can leave it to Claude Code's
	// own Sonnet default.
	if strings.TrimSpace(job.Model) == "" {
		job.Model = r.DefaultModel()
	}
	// A cloud-flavored repo path (e.g. /workspace/infinity) still resolves on
	// the Mac: translate it to the Mac layout before launching.
	repo := bridge.NormalizePath(b, job.Repo)

	// One coding job per conversation. Checked FIRST, before the quota ledger
	// and before preflight, because the cheapest duplicate is the one that
	// never touches the Mac at all.
	if r.liveRuns != nil {
		if id, label, found := r.liveRuns(ctx, SessionIDFromContext(ctx)); found && id != job.JobID {
			return "", &duplicateJobError{runID: id, label: label}
		}
	}

	// Stop retrying something dead: while the boss's Claude plan is known
	// spent (the last run said "out of extra usage"), don't launch another
	// `claude -p` that will fail the same way.
	if until, detail, spent := llm.Exhausted(claudeCodeQuotaKey); spent {
		return "", &llm.QuotaError{Provider: claudeCodeQuotaKey, ResetsAt: until, Detail: detail}
	}

	// 1. Prove there is a real repo to work in. A blank/bad path used to fall
	// through to the bridge's default cwd - ~/Dev, the umbrella holding every
	// repo the boss owns - and Claude Code would start editing in there.
	if strings.TrimSpace(job.Repo) == "" {
		return "", &repoRejectedError{requested: job.Repo, reason: repoReasonEmpty}
	}
	info, err := r.preflightRepo(ctx, b, repo)
	if err != nil {
		return "", err
	}
	if err := validateRepo(repo, info); err != nil {
		return "", err
	}
	// From here on the run works in the resolved absolute path, not the
	// tilde/cloud spelling that was requested.
	repo = info.Path
	setMeta("repo", info.Path)
	setMeta("repo_root", info.Root)
	setMeta("claude_bin", info.ClaudeBin)
	setMeta("model", job.Model)
	setMeta("effort", job.Effort)

	// 2. Prove the sign-in before anything runs or bills.
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
	if job.Effort == "" {
		setMeta("effort", auth.defaultEffort)
	}
	// The one auditable line saying what this run resolved to before it
	// spent a token: which repo, which binary, which model, whose plan.
	setMeta("preflight", preflightEvidence(info, job.Model, auth))
	codeAgentInfo().Printf("code_agent preflight ok: %s", preflightEvidence(info, job.Model, auth))
	heartbeat("Claude Code · signed in on your "+auth.Label(), "auth", "", 0)

	// 2. Launch, detached.
	//
	// A resume id that isn't a real Claude session id would make `claude
	// --resume` fail seconds in, and the run would report a launch failure
	// for what is actually a bad argument. Refuse it here with a message the
	// model can act on, and never silently drop it - silently starting COLD
	// after being asked to resume is how work gets redone from scratch.
	resume := strings.TrimSpace(job.ResumeSessionID)
	if resume != "" && !isClaudeSessionID(resume) {
		return "", fmt.Errorf("code_agent: %q is not a Claude session id — resume_session takes the uuid from a previous run's "+
			"meta.claude_session_id (check the run with that id), or leave it empty to start fresh", resume)
	}
	if resume != "" {
		setMeta("resumed_from", resume)
	}
	files := newClaudeJobFiles(job.JobID)
	launch := claudeLaunchScript(files, job.Task, job.Model, job.Effort, resume, auth.cloudToken)
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
	heartbeat("Claude Code · launched claude -p · working…", "launch", "", 0)

	// 3. Poll the status file. Each call is sub-second, so the 60s bridge
	// client cap and the 600s server cap never bite the long run. Each poll
	// also reads the tail of Claude's stream so the run row says what it is
	// doing right now.
	parent := strings.TrimSpace(job.ParentSession)
	if parent == "" {
		parent = SessionForPublish(SessionIDFromContext(ctx))
	}
	p := &claudePoll{
		runner:        r,
		b:             b,
		files:         files,
		repo:          repo,
		auth:          auth,
		jobID:         job.JobID,
		started:       started,
		deadline:      started.Add(job.MaxWait),
		heartbeat:     heartbeat,
		setMeta:       setMeta,
		pollEach:      codeAgentPollEach,
		tmpDir:        codeAgentTmpDir,
		sink:          r.steps,
		parentSession: parent,
		sinkStamp:     nestedStepPrefix(job.JobID),
		plans:         r.plans,
		label:         strings.TrimSpace(job.Label),
	}
	inline := job.Inline
	if inline == nil {
		inline = ctx
	}
	summary, runErr, outcome := p.wait(ctx, inline, job.Detach, job.KillOnCancel)
	switch outcome {
	case waitFinished:
		return summary, runErr
	case waitKill:
		out := p.stopped()
		if job.Stopped != nil {
			job.Stopped(out)
		}
		return out, nil
	default:
		return p.detach(ctx, job)
	}
}

// JobOutcome is how a job that outlived its turn finally settled. Summary and
// Err are what Run would have returned; StoppedReason is non-empty only when
// the job never reached a verdict (runs.StoppedStillWorking), so the caller
// closes the row as interrupted instead of announcing a green it never earned.
type JobOutcome struct {
	Summary       string
	Err           error
	StoppedReason string
}

// waitOutcome is why the inline wait ended.
type waitOutcome int

const (
	waitFinished waitOutcome = iota // the job exited; the receipt is real
	waitDetach                      // stop waiting, leave the job running
	waitKill                        // stop waiting and tear the job down
)

// claudePoll is one launched job being watched. It exists so the SAME poll
// loop can run inline (inside the turn) and then, after a detach, in a
// background follower - one implementation, so a detached job gets the same
// heartbeats, the same session capture and the same receipt as an inline one.
type claudePoll struct {
	runner    *ClaudeCodeRunner
	b         bridge.Bridge
	files     claudeJobFiles
	repo      string
	auth      claudeAuth
	jobID     string
	started   time.Time
	deadline  time.Time
	heartbeat func(label, action, detail string, step int)
	setMeta   func(key, value string)
	// pollEach / tmpDir are snapshotted at launch rather than read from the
	// package vars mid-run: once a job is launched, its cadence and its file
	// layout are fixed for its whole life, including the part that runs in
	// the background follower after the turn is gone.
	pollEach time.Duration
	tmpDir   string

	// Written only by whichever single goroutine is polling: the inline
	// waiter until it detaches, the follower after.
	lastAction  string
	lastDetail  string
	sessionSeen bool
	// activityKey / steps are the fingerprint the stall detector reads. See
	// noteActivity for why they live here rather than in a caller's heartbeat.
	activityKey string
	steps       int
	// line is the 1-based line the next incremental read of the stream starts
	// at, advanced only past COMPLETE lines (see consume). It is what makes
	// each poll yield the events that are NEW since the last one, which is
	// what the chat ledger is fed from.
	line int
	// sink forwards the nested job's own steps into the parent chat. Nil when
	// nothing is wired, in which case the whole forwarding path is inert.
	sink StepSink
	// parentSession is the chat those steps belong to, and sinkStamp is the
	// id prefix that keeps two concurrent jobs from colliding on a nested id.
	parentSession string
	sinkStamp     string
	// plans mirrors the nested job's TodoWrite list onto that chat's plan;
	// label is the headline the mirrored plan carries. planPrint is the last
	// checklist written, so an unchanged list is not rewritten every poll, and
	// planDeclined latches once the conversation turns out to own a plan this
	// job did not author (see syncNestedPlan).
	plans        NestedPlanSink
	label        string
	planPrint    string
	planDeclined bool
	// sunk is the set of nested call ids already forwarded (call half and
	// result half keyed separately), so a line read twice can never produce a
	// duplicate row. sunkAt / sunkTool carry a call's start time and name
	// across to its result. sunkFull latches the runaway guard.
	sunk     map[string]bool
	sunkAt   map[string]time.Time
	sunkTool map[string]string
	sunkFull bool
}

// pollScript is the one sub-second round trip each poll makes. It asks three
// things at once:
//
//   - the exit status file, which is written only after `claude -p` EXITS;
//   - the slice of the stream that is NEW since the last poll;
//   - the LAST LINE, which is where Claude writes its terminal `result`.
//
// The last line is the one that decides completion, and that is the fix. The
// status file cannot be the signal for a real job: `claude -p` does not exit
// while its MCP servers are attached, and every serious run uses at least one.
// On 2026-08-29 the result landed at 01:02 and the process was still resident
// at 01:16 with no `.status` in sight, so a 47-minute build that SUCCEEDED was
// recorded as never finishing and reported to the boss as a failure.
func (p *claudePoll) pollScript() string {
	if p.line <= 0 {
		p.line = 1
	}
	// ORDER MATTERS. The Mac bridge hard-caps a /bash reply at 64KB and
	// truncates the TAIL, so the completion signals go FIRST and the
	// incremental slice — the only part that can be re-read next poll — goes
	// last. The two budgets below sum to well under the cap on purpose.
	//
	// `RESULT:` is an anchored grep over the UNtruncated last line, so a
	// report too long to decode inside the byte budget is still recognised as
	// completion. A stream-json line always opens with its event type, and
	// there is no event type but the terminal one whose type is `result`.
	return fmt.Sprintf(`if [ -f %s ]; then echo "DONE:$(cat %s)"; else echo RUNNING; fi
if tail -n 1 %s 2>/dev/null | head -c 64 | grep -q '^{"type":"result"'; then echo "RESULT:yes"; else echo "RESULT:no"; fi
echo "===LAST==="
tail -n 1 %s 2>/dev/null | head -c %d
echo ""
echo "===NEW==="
tail -n +%d %s 2>/dev/null | head -n %d | cut -c 1-%d | head -c %d`,
		p.files.status, p.files.status,
		p.files.out,
		p.files.out, claudeLastLineBytes,
		p.line, p.files.out, claudeLinesPerPoll, claudeLineMaxCols, claudeChunkBytes)
}

// pollRead is one reading of a running job.
type pollRead struct {
	// head carries the DONE:/RESULT: lines.
	head string
	// fresh is the COMPLETE new lines of the stream since the last read.
	fresh string
	// done: Claude wrote its terminal result. The job is finished whether or
	// not `claude -p` has exited (it usually has not — see pollScript).
	done bool
	// full: the slice filled its budget, so there is more waiting to be read.
	full bool
}

// pollOnce runs one poll round trip and advances the read position. ok=false
// means the bridge did not answer this time, which is not a verdict about the
// job — the next poll simply tries again.
func (p *claudePoll) pollOnce(ctx context.Context) (pollRead, bool) {
	pb, pc, pok := p.b.Post(ctx, "/bash", map[string]any{"cmd": p.pollScript(), "cwd": p.repo, "timeout_sec": 15})
	if !pok || pc >= 300 {
		return pollRead{}, false
	}
	raw, _ := bridgeBashOutput(pb)
	head, rest := splitMarker(raw, "", "===LAST===")
	last, region := splitMarker(rest, "", "===NEW===")
	// The marker's own `echo` contributes the newline that follows it, so the
	// slice starts one byte in. Counting that newline as a line would advance
	// the read position past a real event on EVERY poll — which is exactly how
	// the first version of this silently skipped every other step.
	region = strings.TrimPrefix(region, "\n")
	r := pollRead{head: head}
	r.full = len(region) >= claudeChunkBytes
	r.fresh = p.consume(region, r.full)
	if strings.Contains(head, "RESULT:yes") {
		r.done = true
	} else if _, ok := claudeTerminalResult(last); ok {
		r.done = true
	}
	return r, true
}

// consume advances the read position past the COMPLETE lines in a slice and
// returns just those lines.
//
// Two things can leave a trailing half-line: the byte cap, and Claude being
// mid-write when the poll landed. Only lines that arrived with their newline
// count as read, so the half is re-read whole next time — nothing is dropped
// and nothing is parsed twice. `full` distinguishes the two cases: a half-line
// in a slice that filled its budget can never complete inside the window, so
// that one is stepped over rather than wedging the read forever.
func (p *claudePoll) consume(slice string, full bool) string {
	if slice == "" {
		return ""
	}
	cut := strings.LastIndexByte(slice, '\n')
	if cut < 0 {
		if full {
			p.line++
		}
		return ""
	}
	whole := slice[:cut+1]
	p.line += strings.Count(whole, "\n")
	return whole
}

// wait polls the job until it finishes, its own lifetime (ctx) ends, or the
// inline window closes. It NEVER kills anything itself; waitKill is a request
// the caller acts on.
func (p *claudePoll) wait(ctx, inline context.Context, detach <-chan struct{}, killOnCancel bool) (string, error, waitOutcome) {
	// When the caller gave no separate inline window, ctx IS the window;
	// listening on both would make the outcome a coin flip.
	var jobDone <-chan struct{}
	if inline != ctx {
		jobDone = ctx.Done()
	}
	for {
		select {
		case <-jobDone:
			// The JOB's own lifetime ended (its budget). A clock is not the
			// boss telling it to stop, so it is left running.
			return "", nil, waitDetach
		case <-detach:
			// The boss spoke, and it was not a stop.
			return "", nil, waitDetach
		case <-inline.Done():
			// A DEADLINE is the chat turn timing out — the job outlives it.
			// An explicit CANCEL is the Stop button or a stop order, and only
			// that is allowed to tear the work down.
			if killOnCancel && !errors.Is(inline.Err(), context.DeadlineExceeded) {
				return "", nil, waitKill
			}
			return "", nil, waitDetach
		case <-time.After(p.pollEach):
		}

		// Drain rather than read once. A burst of large events can exceed one
		// read's budget, and a ledger a minute behind the work is not
		// transparency. Bounded, so a runaway stream can never hold the loop.
		for drains := 0; drains < claudeMaxDrains; drains++ {
			r, ok := p.pollOnce(ctx)
			if !ok {
				break
			}
			// Claude's own session id rides the stream from its first line;
			// the incremental read starts at line 1, so the very first slice
			// carries it and even a killed job can be resumed later.
			p.noteSession(r.fresh)
			// Every step the nested job took since the last read, forwarded
			// into the chat as real rows. This is the whole reason the read is
			// incremental: a fixed tail would show the same step twice and
			// drop the ones that scrolled past between polls.
			//
			// BEFORE the completion checks, deliberately. A short job writes
			// its work and its result inside one poll window, and returning on
			// the result first would show the boss a job that finished having
			// visibly done nothing at all.
			p.forwardSteps(ctx, r.fresh)
			// And the job's own checklist onto the chat's plan, so the dock
			// above the composer shows real steps and a real denominator
			// instead of the activity ramp. Same slice, same poll, same
			// guarantee: neither depends on the model narrating anything.
			p.syncNestedPlan(ctx, r.fresh)
			if strings.Contains(r.head, "DONE:") {
				out, err := p.finish(ctx, exitCodeFromDone(r.head), false)
				return out, err, waitFinished
			}
			// Claude said it is done. That is the authoritative signal, and it
			// arrives long before (or instead of) the process exiting. Reap
			// the group on the way out so a build does not leave eight MCP
			// servers and a headless Chrome resident on the boss's Mac.
			if r.done {
				out, err := p.finish(ctx, 0, true)
				return out, err, waitFinished
			}
			if action, detail, found := claudeStreamActivity(r.fresh); found {
				p.lastAction, p.lastDetail = action, detail
			}
			if !r.full {
				break
			}
		}
		p.noteActivity()
		elapsed := time.Since(p.started).Round(time.Second)
		p.heartbeat(claudeProgressLabel(p.lastAction, p.lastDetail, elapsed), p.lastAction, p.lastDetail, p.steps)
		if p.lastDetail != "" {
			p.setMeta("currentFile", p.lastDetail)
		}

		if time.Now().After(p.deadline) {
			return "", nil, waitDetach
		}
	}
}

// noteActivity stamps the ACTIVITY FINGERPRINT when the job starts doing
// something new: which tool on which target, and when that last changed.
//
// It has to live HERE, in the shared poll loop, and not in a caller's
// heartbeat closure. It was written in code_agent's closure first, and
// `background_build` — which reaches Claude Code through this same runner but
// supplies its own heartbeat — therefore never stamped it at all. The stall
// detector then fell back to started_at and flagged a perfectly healthy build
// as stalled twelve minutes in, while its progress line was still naming the
// file it was reading (run a327070c, 2026-08-29 05:26). A mechanic in one of
// two call sites is a mechanic that is missing (Rule #1b).
//
// progress_label cannot serve this purpose: it embeds elapsed time, so it
// changes on every 15s poll whether or not anything happened. Only a real
// change of tool+target moves the fingerprint, which is what makes "no
// evidence for N minutes" a fact rather than a guess.
func (p *claudePoll) noteActivity() {
	if strings.TrimSpace(p.lastAction) == "" {
		return
	}
	key := p.lastAction + "\x00" + p.lastDetail
	if key == p.activityKey {
		return
	}
	p.activityKey = key
	p.steps++
	p.setMeta("activity_key", key)
	p.setMeta("activity_at", time.Now().UTC().Format(time.RFC3339))
}

// Steps is how many distinct things the job has done so far, for a caller's
// progress bar. Counted here so both engines count the same way.
func (p *claudePoll) Steps() int { return p.steps }

// finish reads the completed job's transcript, turns it into the receipt, and
// clears its files off the Mac.
//
// reap is set when completion was detected from Claude's own terminal result
// rather than from the process exiting. In that case the process group is
// still resident — `claude -p` waits on its MCP servers — so it is torn down
// AFTER the transcript is read. Without that, every MCP-using build would
// leave a claude, eight MCP servers and a headless Chrome running on the Mac.
func (p *claudePoll) finish(ctx context.Context, exitCode int, reap bool) (string, error) {
	t, err := p.fetch(ctx)
	if reap {
		p.runner.kill(p.b, p.repo, p.files)
	}
	if err != nil {
		return "", err
	}
	out, ierr := p.interpret(t, exitCode)
	// Every forwarded step that never got its result is closed here too, or it
	// spins in his chat until the tab is reloaded (closeOpenSteps).
	p.closeOpenSteps(ctx, "The job ended before this step reported back.")
	// The job reached a verdict, so the plan it authored must not stay open.
	// Deterministic (Rule #1b): it does not depend on Claude's last TodoWrite
	// having ticked every box, which a job that errors out never does.
	p.settleNestedPlan(ctx, ierr != nil, out)
	p.cleanup()
	return out, ierr
}

// stopped kills the job and hands back what it ACTUALLY did. Before this the
// transcript at /tmp/inf-code was orphaned on every kill: nothing read it, so
// a stopped job reported nothing but the fact that it stopped.
func (p *claudePoll) stopped() string {
	elapsed := time.Since(p.started).Round(time.Second)
	killed := p.runner.kill(p.b, p.repo, p.files)
	var b strings.Builder
	fmt.Fprintf(&b, "code_agent was STOPPED after %s (Claude Code run %s %s).", elapsed, p.jobID, killed)
	if t, ok := p.salvage(); ok {
		if res := parseClaudeStreamResult(t.out); res.parsed && strings.TrimSpace(res.Result) != "" {
			// It had already written its own summary when the stop landed.
			b.WriteString("\n\nWhat it reported before it stopped:\n" + res.Result)
		} else if did := p.didSoFar(t.out); did != "" {
			b.WriteString("\n\nWhat it had done when it stopped: " + did)
		}
		if strings.Contains(t.err, "INFINITY_DELETE_BLOCKED") {
			b.WriteString(deleteBlockedNotice)
		}
	}
	fmt.Fprintf(&b, "\n\nAny edits it had already made are uncommitted in %s — check git_status/git_diff before touching that repo. "+
		"To pick the job back up, call code_agent again with what is left to do.", repoOrRoot(p.repo))
	p.closeOpenSteps(context.Background(), "Stopped before this step reported back.")
	// Stopped is a verdict too: the step in flight failed and the plan pauses,
	// rather than the dock going on showing a checklist nothing is working.
	p.settleNestedPlan(context.Background(), true, "Stopped before it finished.")
	p.cleanup()
	return b.String()
}

// detach leaves the job running. It first reads the transcript, because the
// job may have crossed the line in the seconds between the last poll and the
// interruption - reporting "still running" for a finished job would be its
// own lie. Otherwise it keeps FOLLOWING the job on a context of its own, so
// the run row still closes with a real receipt after the turn is gone, and
// returns the still-running result for this turn.
func (p *claudePoll) detach(ctx context.Context, job ClaudeCodeJob) (string, error) {
	if t, ok := p.salvage(); ok {
		if res := parseClaudeStreamResult(t.out); res.parsed {
			out, err := p.interpret(t, 0)
			p.cleanup()
			return out, err
		}
	}
	still := &stillRunningError{
		jobID:    p.jobID,
		repo:     p.repo,
		elapsed:  time.Since(p.started),
		activity: p.activityLine(),
	}
	if job.Detached == nil {
		return "", still
	}
	// Its own context, derived from the job's but insensitive to the caller
	// returning: the follower must outlive this call by construction.
	fctx, fcancel := context.WithTimeout(context.WithoutCancel(ctx), time.Until(p.deadline)+codeAgentJobGrace)
	still.following = true
	codeAgentInfo().Printf("code_agent detached: run %s keeps working in %s (%s)", p.jobID, repoOrRoot(p.repo), p.activityLine())
	go func() {
		defer fcancel()
		summary, err, outcome := p.wait(fctx, fctx, nil, false)
		if outcome != waitFinished {
			// No verdict: it was still going when we stopped following it.
			// Marked so the run row - and any watch on it - reads "still
			// working", never a green nobody earned.
			job.Detached(JobOutcome{Summary: p.abandoned(), StoppedReason: runs.StoppedStillWorking})
			return
		}
		job.Detached(JobOutcome{Summary: summary, Err: err})
	}()
	return "", still
}

// abandoned is the receipt for a job that outlived even the follower's
// window. It salvages the transcript first: "we stopped watching" must never
// be reported as "it produced nothing".
func (p *claudePoll) abandoned() string {
	elapsed := time.Since(p.started).Round(time.Second)
	if t, ok := p.salvage(); ok {
		if res := parseClaudeStreamResult(t.out); res.parsed && strings.TrimSpace(res.Result) != "" {
			return res.Result + "\n\n_Reported after " + elapsed.String() + ", past the window Infinity follows a run for._"
		}
		if did := p.didSoFar(t.out); did != "" {
			return fmt.Sprintf("Claude Code was STILL WORKING after %s in %s, past the window I follow a run for, so I stopped watching it. "+
				"It was not stopped. What it had done by then: %s. Check git_status/git_diff in that repo for its edits.",
				elapsed, repoOrRoot(p.repo), did)
		}
	}
	return fmt.Sprintf("Claude Code was still working after %s in %s, past the window I follow a run for, so I stopped watching it. "+
		"It was not stopped — check git_status/git_diff in that repo for its edits.", elapsed, repoOrRoot(p.repo))
}

// activityLine names what the job was doing at the last poll.
func (p *claudePoll) activityLine() string {
	switch {
	case p.lastAction != "" && p.lastDetail != "":
		return p.lastAction + " " + p.lastDetail
	case p.lastAction != "":
		return p.lastAction
	}
	return ""
}

// didSoFar reads a partial transcript and says, in one line, what the job
// actually touched. This is the salvage a killed or abandoned run hands back.
func (p *claudePoll) didSoFar(stream string) string {
	parts := []string{}
	if files := claudeTouchedFiles(stream); len(files) > 0 {
		parts = append(parts, "edited "+strings.Join(files, ", "))
	}
	action, detail, found := claudeStreamActivity(stream)
	if !found {
		action, detail = p.lastAction, p.lastDetail
	}
	if action != "" {
		last := "last: " + action
		if detail != "" {
			last += " " + detail
		}
		parts = append(parts, last)
	}
	return strings.Join(parts, "; ")
}

// transcript is one read of the job's output and stderr off the Mac.
type transcript struct{ out, err string }

// fetch reads the job's transcript. It takes the HEAD of the stream too (the
// `system/init` line carrying Claude's session id, which has long scrolled
// out of the tail on a real run) as well as the tail holding the result.
func (p *claudePoll) fetch(ctx context.Context) (transcript, error) {
	cmd := fmt.Sprintf(`echo "===INIT==="; head -c %d %s 2>/dev/null; echo "===OUT==="; tail -c %d %s 2>/dev/null; echo "===ERR==="; tail -c 4000 %s 2>/dev/null`,
		claudeInitHeadBytes, p.files.out, claudeResultTailBytes, p.files.out, p.files.err)
	fb, fc, fok := p.b.Post(ctx, "/bash", map[string]any{"cmd": cmd, "cwd": p.repo, "timeout_sec": 20})
	if !fok || fc >= 300 {
		return transcript{}, fmt.Errorf("code_agent: reading the run's output failed (status=%d)", fc)
	}
	// Decode the bridge's {output, exit_code} JSON for real. The old
	// first-quote scanner cut Claude's own JSON at its first escaped quote,
	// so every result (including "You're out of extra usage") came back as
	// `{\` and was reported ok (2026-08-26).
	raw, _ := bridgeBashOutput(fb)
	initPart, rest := splitMarker(raw, "===INIT===", "===OUT===")
	outPart, errPart := splitMarker(rest, "", "===ERR===")
	p.noteSession(initPart)
	p.noteSession(outPart)
	return transcript{out: outPart, err: errPart}, nil
}

// salvage reads the transcript on a FRESH context. Every path that calls it -
// kill, detach, abandon - runs when the caller's context is already dead or
// irrelevant, which is exactly why the transcript used to be thrown away.
func (p *claudePoll) salvage() (transcript, bool) {
	sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	t, err := p.fetch(sctx)
	if err != nil {
		log.Printf("code_agent: could not salvage run %s output: %v", p.jobID, err)
		return transcript{}, false
	}
	return t, true
}

// cleanup removes this job's files from the Mac's /tmp/inf-code and sweeps
// day-old leftovers. Nothing cleaned that directory before, so every run
// since the tool shipped left its transcript there forever. Only ever called
// for a job that has SETTLED (finished or killed) - a job that may still be
// writing keeps its files.
func (p *claudePoll) cleanup() {
	cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	f := p.files
	cmd := strings.Join([]string{
		"rm -f " + shellQuote(f.out) + " " + shellQuote(f.err) + " " + shellQuote(f.status) + " " +
			shellQuote(f.pid) + " " + shellQuote(f.pgid),
		// Leftovers from runs that predate this sweep, or whose core
		// restarted mid-flight. A live job's files are minutes old at most
		// (the ceiling is 20 minutes), so a day is a safe floor.
		"find " + shellQuote(p.tmpDir) + " -maxdepth 1 -type f -mmin +1440 ! -name settings.json -exec rm -f {} + 2>/dev/null",
		"exit 0",
	}, "\n")
	p.b.Post(cctx, "/bash", map[string]any{"cmd": cmd, "cwd": p.repo, "timeout_sec": 10})
}

// noteSession records Claude's own session id on the run row the first time
// the stream shows it. It is on the wire from the first line of every run and
// nothing read it until now - which is why a killed or detached job could
// never actually be resumed.
func (p *claudePoll) noteSession(stream string) {
	if p.sessionSeen {
		return
	}
	id := parseClaudeSessionID(stream)
	if id == "" {
		return
	}
	p.sessionSeen = true
	p.setMeta("claude_session_id", id)
}

// isClaudeSessionID reports whether s has the shape of a Claude Code session
// id (a uuid). Used to refuse a bogus resume_session before it becomes a
// launch failure the model would read as "the Mac is broken".
func isClaudeSessionID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < 36; i++ {
		c := s[i]
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
			continue
		}
		isHex := (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
		if !isHex {
			return false
		}
	}
	return true
}

// parseClaudeSessionID finds the session id in a stream-json fragment. The
// `{"type":"system","subtype":"init","session_id":…}` line is the first, but
// every event carries it, so any decodable line will do.
func parseClaudeSessionID(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || line[0] != '{' {
			continue
		}
		var ev struct {
			SessionID string `json:"session_id"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil {
			continue
		}
		if id := strings.TrimSpace(ev.SessionID); id != "" {
			return id
		}
	}
	return ""
}

// claudeTouchedFiles lists the files a (possibly partial) stream shows Claude
// writing to, newest first, capped. Deterministic evidence for a run that
// never got to write its own summary.
func claudeTouchedFiles(stream string) []string {
	const maxFiles = 8
	seen := map[string]bool{}
	out := []string{}
	lines := strings.Split(stream, "\n")
	for i := len(lines) - 1; i >= 0 && len(out) < maxFiles; i-- {
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
				} `json:"content"`
			} `json:"message"`
		}
		if json.Unmarshal([]byte(line), &ev) != nil || ev.Type != "assistant" {
			continue
		}
		for _, c := range ev.Message.Content {
			if c.Type != "tool_use" {
				continue
			}
			switch c.Name {
			case "Edit", "Write", "NotebookEdit", "MultiEdit":
			default:
				continue
			}
			path, _ := c.Input["file_path"].(string)
			if path = strings.TrimSpace(path); path == "" || seen[path] {
				continue
			}
			seen[path] = true
			out = append(out, path)
		}
	}
	return out
}

// preflightRepo resolves the working directory on the bridge. It runs with
// no cwd so a bad path comes back as a readable reading, not a bridge 400.
func (r *ClaudeCodeRunner) preflightRepo(ctx context.Context, b bridge.Bridge, repo string) (repoInfo, error) {
	body, code, ok := b.Post(ctx, "/bash", map[string]any{"cmd": repoPreflightScript(repo), "timeout_sec": 15})
	if ok && code >= 400 && code < 500 {
		return repoInfo{}, &launchRejectedError{bridge: string(b.Name()), status: code, detail: bridgeErrorDetail(body)}
	}
	if !ok || code >= 300 {
		return repoInfo{}, fmt.Errorf("code_agent: repo check via %s failed (status=%d): %s", b.Name(), code, string(body))
	}
	out, _ := bridgeBashOutput(body)
	return parseRepoPreflight(out), nil
}

// preflightEvidence is the audit line for the run row and the log: what this
// launch resolved to, with no secret in it (the probe only ever reports the
// PRESENCE of a key, never its value).
func preflightEvidence(info repoInfo, model string, auth claudeAuth) string {
	bin := info.ClaudeBin
	if bin == "" {
		bin = "claude (not on PATH)"
	}
	if model == "" {
		model = "(unset)"
	}
	return fmt.Sprintf("repo %s (git root %s) · %s · model %s · %s",
		repoOrRoot(info.Path), repoOrRoot(info.Root), bin, model, auth.Label())
}

// codeAgentInfo writes to stdout so Railway tags these lines severity=info;
// stdlib log goes to stderr, which is reserved for real failures.
func codeAgentInfo() *log.Logger {
	codeAgentLogOnce.Do(func() { codeAgentLog = log.New(os.Stdout, "", log.LstdFlags) })
	return codeAgentLog
}

var (
	codeAgentLog     *log.Logger
	codeAgentLogOnce sync.Once
)

// probeAuth runs the sign-in probe on the Mac and decodes it.
func (r *ClaudeCodeRunner) probeAuth(ctx context.Context, b bridge.Bridge, repo string) (claudeAuth, error) {
	// The cloud box has no browser, so it never has a ~/.claude.json sign-in
	// to read back. Its credential is the subscription token the boss minted
	// with `claude setup-token`, stored in Infinity and exported for the one
	// command. Absence is a hard stop, not a fallback: without it there is no
	// way to run on the plan, and running on anything else is the swap this
	// gate exists to prevent.
	if b != nil && b.Name() == bridge.KindCloud {
		if r.brain.SubscriptionToken == nil {
			return claudeAuth{}, errors.New("code_agent: the cloud box has no Claude sign-in saved. Add your token in Settings, or wake the Mac.")
		}
		token := strings.TrimSpace(r.brain.SubscriptionToken(ctx))
		if token == "" {
			return claudeAuth{}, errors.New("code_agent: the cloud box needs your Claude token first. Run `claude setup-token` on your Mac and paste it into Settings.")
		}
		return claudeAuth{found: true, cloudToken: token}, nil
	}
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

// claudeTokenExport renders the one line that decides which credential pays.
func claudeTokenExport(cloudToken string) string {
	if strings.TrimSpace(cloudToken) == "" {
		return "unset CLAUDE_CODE_OAUTH_TOKEN"
	}
	return "export CLAUDE_CODE_OAUTH_TOKEN=" + shellQuote(cloudToken)
}

// claudeJobFiles are the per-job files under codeAgentTmpDir.
type claudeJobFiles struct {
	out, err, status, pid, pgid, settings string
}

func newClaudeJobFiles(jobID string) claudeJobFiles {
	return claudeJobFiles{
		out:      fmt.Sprintf("%s/%s.out", codeAgentTmpDir, jobID),
		err:      fmt.Sprintf("%s/%s.err", codeAgentTmpDir, jobID),
		status:   fmt.Sprintf("%s/%s.status", codeAgentTmpDir, jobID),
		pid:      fmt.Sprintf("%s/%s.pid", codeAgentTmpDir, jobID),
		pgid:     fmt.Sprintf("%s/%s.pgid", codeAgentTmpDir, jobID),
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
//
// resume, when set, continues that Claude session (`-r/--resume <id>`) with
// the new task as the next prompt, so an interrupted job resumes with its own
// context instead of starting cold. Deliberately WITHOUT --fork-session: the
// resumed run keeps the original session id, so meta.claude_session_id stays
// the one handle to the whole chain however many passes it takes.
func claudeLaunchScript(f claudeJobFiles, task, model, effort, resume, cloudToken string) string {
	inner := fmt.Sprintf(
		`claude -p "$INF_TASK" ${INF_RESUME:+--resume "$INF_RESUME"} ${INF_MODEL:+--model "$INF_MODEL"} ${INF_EFFORT:+--effort "$INF_EFFORT"} --output-format stream-json --verbose `+
			`--permission-mode bypassPermissions --settings %s > %s 2> %s; echo $? > %s`,
		f.settings, f.out, f.err, f.status,
	)
	return strings.Join([]string{
		"mkdir -p " + codeAgentTmpDir,
		"cat > " + f.settings + " <<'INFEOF'\n" + codeAgentSettingsJSON() + "\nINFEOF",
		// An API key in the bridge's shell would pre-empt the sign-in inside
		// Claude Code and bill the API instead of the subscription.
		"unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN",
		// On the cloud box the subscription IS this token; on the Mac the
		// sign-in is, and a stray token would silently outrank it. One line
		// covers both because claudeTokenExport returns the unset on the Mac.
		claudeTokenExport(cloudToken),
		"export INF_TASK=" + shellQuote(task),
		"export INF_MODEL=" + shellQuote(model),
		"export INF_EFFORT=" + shellQuote(effort),
		"export INF_RESUME=" + shellQuote(resume),
		// Monitor mode puts a background job in its OWN process group even in
		// a non-interactive shell (no `setsid` on macOS). That is what makes
		// the wrapper a group leader, so a Stop can reap the WHOLE tree -
		// claude, and the `go build` / MCP server / test run it spawned -
		// instead of one level (see claudeKillScript).
		"set -m",
		"nohup bash -c " + shellQuote(inner) + " >/dev/null 2>&1 &",
		"INF_PID=$!",
		// Record the wrapper's pid so a Stop / mid-turn stop order can
		// actually kill the job (2026-08-26: the boss said "don't build", the
		// job kept editing his repo for nine minutes and "couldn't be
		// stopped"), and its process group so a kill that arrives after the
		// wrapper itself has exited can still reap the orphans.
		`echo $INF_PID > ` + f.pid,
		`ps -o pgid= -p $INF_PID 2>/dev/null | tr -d ' \t' > ` + f.pgid,
		"set +m",
		`echo "PID:$INF_PID"`,
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

// kill terminates the detached claude -p and EVERYTHING it spawned. Runs on a
// FRESH context because the tool ctx is already cancelled. Returns a short
// human clause for the tool result.
func (r *ClaudeCodeRunner) kill(b bridge.Bridge, repo string, f claudeJobFiles) string {
	kctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body, code, ok := b.Post(kctx, "/bash", map[string]any{"cmd": claudeKillScript(f), "cwd": repo, "timeout_sec": 12})
	if !ok || code >= 300 {
		return "could not be killed: the Mac bridge did not answer, it may still be running"
	}
	kout, _ := bridgeBashOutput(body)
	if strings.Contains(kout, "NOPID") {
		return "had no recorded pid, it may still be running"
	}
	return "was killed"
}

// claudeKillScript tears down the whole job tree. It used to be
// `pkill -TERM -P "$P"`, which reaps exactly ONE level: the wrapper's child
// `claude` died, and its grandchildren - a `go build`, a test run, an MCP
// server it had started - were orphaned and kept running against the boss's
// repo after he had said stop. Signalling the PROCESS GROUP reaps every
// descendant with one signal.
//
// The group is only ever signalled when it is provably the job's OWN: the
// launch runs under `set -m`, so the wrapper is a group leader and its pgid
// equals its pid. If that check fails (an older job launched before this
// change, a shell without monitor mode) we fall back to the one-level pkill
// rather than risk signalling the group the Mac bridge itself lives in.
func claudeKillScript(f claudeJobFiles) string {
	return fmt.Sprintf(`P=$(cat %s 2>/dev/null | tr -d ' \t\n')
G=$(cat %s 2>/dev/null | tr -d ' \t\n')
if [ -z "$P" ]; then echo NOPID; exit 0; fi
if [ -z "$G" ]; then G=$(ps -o pgid= -p "$P" 2>/dev/null | tr -d ' \t\n'); fi
if [ -n "$G" ] && [ "$G" = "$P" ]; then GROUP="$G"; else GROUP=""; fi
if [ -n "$GROUP" ]; then kill -TERM -"$GROUP" 2>/dev/null; echo "GROUP:$GROUP"; else pkill -TERM -P "$P" 2>/dev/null; echo "GROUP:none"; fi
kill -TERM "$P" 2>/dev/null
sleep 1
if [ -n "$GROUP" ]; then kill -KILL -"$GROUP" 2>/dev/null; fi
if kill -0 "$P" 2>/dev/null; then pkill -KILL -P "$P" 2>/dev/null; kill -KILL "$P" 2>/dev/null; fi
echo KILLED`, f.pid, f.pgid)
}

// interpret turns a captured transcript into the tool's receipt: Claude's
// final result message, the plan-cap / crash cases, and any blocked-delete
// notice for the boss to approve. exitCode is the shell's, or 0 when the
// transcript was salvaged rather than collected on completion.
func (p *claudePoll) interpret(t transcript, exitCode int) (string, error) {
	outPart, errPart := t.out, t.err
	repo, auth, setMeta, started := p.repo, p.auth, p.setMeta, p.started

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
		sb.WriteString(deleteBlockedNotice)
	}
	if strings.TrimSpace(sb.String()) == "" {
		return "code_agent finished but returned no output. Check the run logs.", nil
	}
	// The proof line: which plan paid, which model ran, how long it took.
	sb.WriteString("\n\n_Ran as `claude -p` on Claude Code (" + auth.Label() + ")")
	if model != "" {
		sb.WriteString(" · model " + model)
	}
	sb.WriteString(" · in " + repoOrRoot(repo))
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
	// DurationMS / NumTurns ride the terminal result object and nothing else,
	// which is what makes them usable as proof that a line IS that object.
	DurationMS int `json:"duration_ms"`
	NumTurns   int `json:"num_turns"`
	// RawUsage is the turn's token counts, decoded lazily by whoever needs
	// them (brainUsage). Raw rather than typed so a new field Anthropic adds
	// to the usage object never breaks the decode of the result itself.
	RawUsage json.RawMessage `json:"usage"`
	parsed   bool
}

// claudeTerminalResult finds Claude's TERMINAL result event in a stream-json
// fragment, and is deliberately stricter than parseClaudeStreamResult.
//
// This one decides that a job is FINISHED, so a false positive would close a
// live build and throw away the rest of its work. It therefore never falls
// back to the whole-blob decode (which happily accepts any JSON object,
// including a half-read assistant event), and it requires the line to actually
// be the result object: `"type":"result"` plus at least one of the two fields
// only that object carries.
func claudeTerminalResult(s string) (claudeResult, bool) {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" || line[0] != '{' {
			continue
		}
		var res claudeResult
		if json.Unmarshal([]byte(line), &res) != nil {
			continue
		}
		if res.Type != "result" || (res.DurationMS <= 0 && res.NumTurns <= 0) {
			continue
		}
		res.Result = strings.TrimSpace(res.Result)
		res.parsed = true
		return res, true
	}
	return claudeResult{}, false
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
//
// THE ONE-WORD BUG (2026-09-01). Claude Code's five-hour cap prints
//
//	You've hit your session limit · resets 8:20pm (America/Chicago)
//
// and this function did not match it. "hit your limit" is not a substring of
// "hit your session limit", and "usage limit" does not appear at all. So the
// spent plan fell through to a generic error: the quota ledger was never
// marked, `code_agent` cheerfully relaunched `claude -p` on a plan that could
// not run it, and the continuation poller woke Jarvis once a minute about work
// that could never finish. Eight jobs in one evening, every one dead in about
// a minute, and the boss was told nothing at all - he watched a spinner and
// asked why his pursuit was never built.
//
// So the matching is on the SHAPE of the copy, not one phrasing of it: the
// word "limit" alongside anything that means his plan is used up, plus the
// "out of usage" family. A vendor rewording it again should not silently
// switch this off - which is exactly what happened - so the production string
// is pinned verbatim in the test.
func looksLikeUsageCap(msg string) bool {
	m := strings.ToLower(msg)
	for _, phrase := range []string{
		"out of extra usage", "out of usage",
		"usage limit", "session limit", "rate limit", "message limit",
		"hit your limit", "hit the limit", "reached your limit",
		"limit reached", "limit exceeded", "upgrade to continue",
	} {
		if strings.Contains(m, phrase) {
			return true
		}
	}
	return false
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
// tracker books the mem_runs row. The registry is kept so a detached job can
// register its own watch through the watch substrate that is registered on
// the same registry later in boot (see watchDetached).
func RegisterCodeAgentTool(r *Registry, runner *ClaudeCodeRunner, tracker *runs.Tracker) {
	r.Register(&codeAgent{runner: runner, tracker: tracker, reg: r})
}

type codeAgent struct {
	runner  *ClaudeCodeRunner
	tracker *runs.Tracker
	// reg is the registry this tool was registered on; watchDetached uses it
	// to reach the watch substrate.
	reg *Registry
	// watches, when attached (AttachWatchCreator), is used directly instead
	// of going through the registered watch_until tool.
	watches WatchCreator
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
		"are fine; for anything real, use this. The job OUTLIVES this turn: if the boss says something while it works, " +
		"or the turn times out, you get a STILL RUNNING result naming its run id - the job was not stopped, its completion " +
		"is reported back into the chat on its own, and calling code_agent again for the same work would run it twice. " +
		"Only an explicit stop from him kills it. To pick a job back up later, pass resume_session (the run's " +
		"meta.claude_session_id) with just what is LEFT to do - it continues that session's context instead of starting over."
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
				"description": "REQUIRED: the ONE git repository to work in. `~/Dev/<repo>` is the preferred spelling and always correct on the Mac (Infinity itself is `~/Dev/infinity`) - prefer it over an absolute path, and NEVER invent a home directory from the boss's name or email. An absolute path is accepted too; the Bridge section of your context states his real home. It must be a git repo - a missing path, a plain folder, or the ~/Dev umbrella is refused and nothing runs.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional Claude model id or alias for this run (e.g. 'claude-opus-5[1m]', 'sonnet'). Leave empty for the pinned default (INFINITY_CODE_AGENT_MODEL, else Opus 5).",
			},
			"effort": map[string]any{
				"type":        "string",
				"enum":        []string{"low", "medium", "high", "xhigh", "max"},
				"description": "Optional Claude Code effort for this run. Leave empty for the configured default (INFINITY_CODE_AGENT_EFFORT, else the Mac's setting).",
			},
			"resume_session": map[string]any{
				"type": "string",
				"description": "Optional: CONTINUE an earlier Claude Code session instead of starting cold. Pass the uuid from that run's " +
					"meta.claude_session_id. Claude reloads everything it already read, wrote and tried, so 'task' should be only what is LEFT " +
					"to do, not the whole brief again. Use this whenever you are picking a job back up after it was interrupted, stalled, or " +
					"came back STILL RUNNING - starting fresh makes it re-derive work that is already on disk.",
			},
		},
		"required": []string{"task", "repo"},
	}
}

// codeAgentJobContext gives a coding job a lifetime of its OWN, derived from
// the tool context by context.WithoutCancel: every value the context carries
// - the session id the bridge preference is read from, the autonomy marker,
// the session's ActiveSet, the turn's stance holder, the detach signal - is
// preserved by construction, and ONLY cancellation and the turn's deadline
// are dropped. Nothing is copied key by key, so a value added later cannot be
// silently lost here.
//
// The budget is the job's own ceiling plus grace for the final read of the
// transcript, so the last collect is never cut off by the same clock that
// ended the polling.
func codeAgentJobContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), codeAgentMaxWait+codeAgentJobGrace)
}

func (t *codeAgent) Execute(ctx context.Context, in map[string]any) (string, error) {
	task := strings.TrimSpace(strString(in, "task"))
	if task == "" {
		return "", fmt.Errorf("code_agent: task is required")
	}
	if t.runner == nil {
		return "", errors.New("code_agent: runner not configured")
	}
	// One call is one PASS. A brief carrying a whole plan is redirected before
	// anything launches or bills — see code_agent_brief.go for why this is a
	// mechanic in code rather than a sentence in the skill.
	if guidance, split := splitBriefGuidance(task); split {
		return guidance, nil
	}
	repo := strString(in, "repo")
	// Model + effort for the nested Claude Code: the call's own choice first,
	// else Infinity's defaults (INFINITY_CODE_AGENT_MODEL / _EFFORT, e.g.
	// "claude-opus-5[1m]" / "high"), else the Mac's own Claude settings.
	model := strDefault(in, "model", t.runner.DefaultModel())
	effort := strDefault(in, "effort", t.runner.DefaultEffort())

	// Runs on EITHER bridge now: Claude Code is installed on the cloud box
	// too, so the boss's subscription writes the code whether or not his
	// laptop is awake. The credential differs by bridge (the Mac's sign-in,
	// the cloud's stored token) and probeAuth resolves that; what does NOT
	// differ is the rule that this path spends the plan or refuses.
	//
	// This replaces the old "on Cloud, write the code yourself" fallback,
	// which quietly downgraded every night the Mac slept: the chat brain
	// (DeepSeek) authored code that the Max plan was supposed to.
	b, why, err := t.runner.ActiveBridge(ctx)
	if err != nil {
		return "", fmt.Errorf("code_agent: %s", why)
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
	handle := t.tracker.Begin(ctx, runs.KindCodeAgent, "", label, runs.SourceAgent)
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
	// The run row is the one live surface a coding job has: it survives
	// refresh and a second device, and it is what the pinned dock above the
	// composer draws. The activity fingerprint the stall detector reads is
	// stamped by the runner's own poll loop (claudePoll.noteActivity), which
	// is the chokepoint BOTH engines share - keeping it here meant
	// background_build never stamped it at all.
	heartbeat := func(note, _ string, detail string, step int) {
		// A real fraction, not the hardcoded 0 that made the dock's bar
		// unmovable: the same curve background_build draws, so the same
		// amount of work looks the same whichever engine ran it.
		handle.Progress(context.Background(), ProgressForSteps(step), note)
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
	setMeta("brief", briefSummary(task))
	// The chat this job belongs to, on the row itself. Without it a run that
	// settles after the turn is gone has no address to report back to, which
	// is the whole problem the continuation poller exists to fix - it reads
	// rows, not contexts. Same meta key the cron scheduler already uses.
	setMeta("session_id", SessionIDFromContext(ctx))

	// The job gets a lifetime of its OWN, derived from context.Background()
	// via context.WithoutCancel: every value the tool ctx carries (the
	// session id the bridge preference is read from, the autonomy marker, the
	// session's ActiveSet, the turn's stance holder) is carried across
	// unchanged, and ONLY cancellation and the turn's deadline are dropped.
	// The turn ctx is handed in separately as Inline and decides one thing:
	// how long we wait here. That is the whole fix for "the 15-minute chat
	// turn kills a 20-minute build".
	jobCtx, jobCancel := codeAgentJobContext(ctx)
	defer jobCancel()

	// Detached is how a job that outlived this turn still ends with a real
	// receipt: the follower calls it when the job settles and the run row
	// closes then, not now. A settle with no verdict closes INTERRUPTED, so
	// neither Studio nor a watch on the run reads it as a finished success.
	closeRun := func(o JobOutcome) {
		fc, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if o.StoppedReason != "" {
			handle.FinishInterrupted(fc, o.StoppedReason, o.Summary)
			return
		}
		handle.Finish(fc, o.Err, o.Summary)
	}
	// stopReason is set when an explicit stop killed the job; it decides how
	// the row closes below.
	stopReason := ""

	summary, runErr := t.runner.Run(jobCtx, ClaudeCodeJob{
		Bridge:          b,
		JobID:           jobID,
		Task:            task,
		Label:           label,
		Repo:            repo,
		Model:           model,
		Effort:          effort,
		ResumeSessionID: strString(in, "resume_session"),

		MaxWait:      codeAgentMaxWait,
		KillOnCancel: true,
		Inline:       ctx,
		Detach:       DetachRequested(ctx),
		Detached:     closeRun,
		Stopped:      func(string) { stopReason = runs.StoppedInterrupted },
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
			// The turn is over and Claude Code keeps working on the Mac.
			if still.following {
				// A follower owns the run row now: closing it here would tell
				// Studio (and any watch on it) that a live job had settled.
				// Register the watch so its completion reports back into this
				// chat on its own, and leave the row running.
				t.watchDetached(ctx, jobID, label, still)
				return still.inlineMessage(), nil
			}
			handle.FinishInterrupted(finCtx, runs.StoppedStillWorking, still.inlineMessage())
			return still.inlineMessage(), nil
		}
		// A refused DUPLICATE is not a failure and must not be recorded as one:
		// nothing was launched, nothing broke, and the job that IS running is
		// fine. Closing this row 'error' would put a red card on the board for
		// the guard working correctly. Handled BEFORE Finish, because Finish
		// closes the row and the later branches can no longer change it.
		var dup *duplicateJobError
		if errors.As(runErr, &dup) {
			handle.FinishInterrupted(finCtx, runs.StoppedInterrupted, dup.guidance())
			return dup.guidance(), nil
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
		// A bad repo is a request problem the model can fix on the next call:
		// return it as guidance naming the right shape of path, rather than a
		// raw error it would retry verbatim.
		var badRepo *repoRejectedError
		if errors.As(runErr, &badRepo) {
			return badRepo.guidance(), nil
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
	if stopReason != "" {
		// The boss stopped it. It did not fail, and it did not finish either:
		// closing this 'ok' with a "was STOPPED" summary is exactly the green
		// card that reads as success.
		handle.FinishInterrupted(finCtx, stopReason, summary)
		return summary, nil
	}
	handle.Finish(finCtx, nil, summary)
	return summary, nil
}

// InterruptOnSteer opts code_agent into the loop's steer-interrupt: a message
// from the boss while Claude Code is working ends the inline wait so he is
// answered now, not after the job.
func (t *codeAgent) InterruptOnSteer() bool { return true }

// DetachOnSteer says the work OUTLIVES the turn: `claude -p` runs detached on
// the Mac, so a message that is not an explicit stop must DETACH (the job
// keeps going, the boss gets answered, completion reports back) rather than
// kill. Only an explicit stop - agent.isStopIntent, or the Stop button, which
// cancels the context outright - tears it down.
func (t *codeAgent) DetachOnSteer() bool { return true }

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
const claudeCodeQuotaKey = llm.ClaudeCodeQuotaKey

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
	return bridge.BashOutput(body)
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
