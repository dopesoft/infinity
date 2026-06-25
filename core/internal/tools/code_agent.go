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
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/errs"
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
				"description": "Optional Claude model alias (e.g. 'opus', 'sonnet'). Defaults to the boss's configured Claude Code model.",
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
	model := strString(in, "model")

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

	summary, runErr := t.run(ctx, b, jobID, task, repo, model, heartbeat)
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
		if h := errs.Humanize(runErr); h.Category == errs.CatBridge {
			return fmt.Sprintf("code_agent couldn't run: %s — the Mac bridge is unreachable. "+
				"Don't stop: write the change yourself with fs_edit/fs_save in /workspace, then "+
				"`bash_run` go build ./... && go vet ./... && go test ./..., then git_commit (and git_push if autonomy is on). "+
				"The cloud workspace has the Go toolchain pre-installed.", h.Summary), nil
		}
		return "", runErr
	}
	return summary, nil
}

// run launches the detached claude -p and polls to completion.
func (t *codeAgent) run(ctx context.Context, b bridge.Bridge, jobID, task, repo, model string, heartbeat func(string)) (string, error) {
	out := fmt.Sprintf("%s/%s.out", codeAgentTmpDir, jobID)
	errf := fmt.Sprintf("%s/%s.err", codeAgentTmpDir, jobID)
	status := fmt.Sprintf("%s/%s.status", codeAgentTmpDir, jobID)
	settings := fmt.Sprintf("%s/settings.json", codeAgentTmpDir)

	// One round-trip: ensure tmp dir, (re)write the delete-gate settings,
	// then launch claude -p DETACHED. Task/model go via exported env so
	// the inner single-quoted bash -c needs no fragile nested quoting.
	inner := fmt.Sprintf(
		`claude -p "$INF_TASK" ${INF_MODEL:+--model "$INF_MODEL"} --output-format json `+
			`--permission-mode bypassPermissions --settings %s > %s 2> %s; echo $? > %s`,
		settings, out, errf, status,
	)
	launch := strings.Join([]string{
		"mkdir -p " + codeAgentTmpDir,
		"cat > " + settings + " <<'INFEOF'\n" + codeAgentSettingsJSON() + "\nINFEOF",
		"export INF_TASK=" + shellQuote(task),
		"export INF_MODEL=" + shellQuote(model),
		"nohup bash -c " + shellQuote(inner) + " >/dev/null 2>&1 &",
		`echo "PID:$!"`,
	}, "\n")

	body, code, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         launch,
		"cwd":         repo,
		"timeout_sec": 20,
	})
	if !ok || code >= 300 {
		return "", fmt.Errorf("code_agent: launch via %s failed (status=%d): %s", b.Name(), code, string(body))
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
			// The caller's context was cancelled (Stop button or graceful
			// shutdown). The detached `claude -p` on the Mac is unaffected —
			// it runs independently. Return guidance so the agent can report
			// this to the boss rather than surfacing a hard failure. The
			// mem_runs row is closed by the fresh-ctx Finish call in Execute.
			return fmt.Sprintf(
				"code_agent was interrupted after %s (the Claude Code run %s is still "+
					"finishing on the Mac — it can't be cancelled from here). "+
					"Re-attach with `claude --continue` in %s once it completes.",
				time.Since(started).Round(time.Second), jobID, repoOrRoot(repo)), nil
		case <-time.After(codeAgentPollEach):
		}

		pb, pc, pok := b.Post(ctx, "/bash", map[string]any{"cmd": pollCmd, "cwd": repo, "timeout_sec": 15})
		if pok && pc < 300 {
			outStr := extractJSONFieldFast(string(pb), "output")
			if strings.Contains(outStr, "DONE:") {
				return t.collect(ctx, b, out, errf, repo)
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

// collect reads the finished output, parses claude's JSON result, and
// folds in any blocked-delete notice for the boss to approve.
func (t *codeAgent) collect(ctx context.Context, b bridge.Bridge, out, errf, repo string) (string, error) {
	fetch := fmt.Sprintf(`echo "===OUT==="; cat %s 2>/dev/null; echo "===ERR==="; tail -c 4000 %s 2>/dev/null`, out, errf)
	fb, fc, fok := b.Post(ctx, "/bash", map[string]any{"cmd": fetch, "cwd": repo, "timeout_sec": 20})
	if !fok || fc >= 300 {
		return "", fmt.Errorf("code_agent: finished but reading output failed (status=%d)", fc)
	}
	raw := extractJSONFieldFast(string(fb), "output")
	outPart, errPart := splitMarker(raw, "===OUT===", "===ERR===")

	res := parseClaudeResult(outPart)
	var sb strings.Builder
	if res != "" {
		sb.WriteString(res)
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

// parseClaudeResult pulls the human-readable "result" field out of
// `claude -p --output-format json` output. Returns "" if it can't parse.
func parseClaudeResult(s string) string {
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end <= start {
		return ""
	}
	var res struct {
		Result  string `json:"result"`
		IsError bool   `json:"is_error"`
		Subtype string `json:"subtype"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &res); err != nil {
		return ""
	}
	return strings.TrimSpace(res.Result)
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
