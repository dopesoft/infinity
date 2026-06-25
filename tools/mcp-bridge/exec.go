// exec.go — the WRITE half of the Mac bridge's direct API: bash, file
// save/edit, and the git write verbs (stage/commit/push/pull).
//
// Why this exists: the Mac bridge originally exposed only the READ subset
// (/fs/ls, /fs/read, /git/status, /git/diff) — enough for Studio's canvas —
// while core's bridge_* tools speak the FULL contract the cloud workspace
// (docker/workspace/main.go) serves. Every bash_run that routed to the Mac
// answered a plain "404 page not found", which (pre route-miss failover)
// stalled the nightly self-improve run outright, and afterwards still forced
// a noisy Mac→cloud failover on every single shell call. This file completes
// the Mac side to the same shapes the cloud serves, so Mac-first sessions
// actually run on the Mac.
//
// Contract notes (kept identical to docker/workspace/main.go so core's tools
// and the routeMiss discriminator treat both bridges the same):
//   - errors are always the {"error":"..."} JSON envelope
//   - /bash returns {exit_code, output, truncated, cwd} with a 64KB output
//     cap and a 5-minute wall clock
//   - git verbs return {repo, output, exit_code}
//
// Mac-specific: paths accept `~` / `~/...` (expanded to the user's home —
// the missing expansion was why /git/status?repo=~/Dev/infinity died with
// exit 128) and there is deliberately NO path jail: the bridge already
// fronts `claude mcp serve` (full shell via claude_code__Bash) behind
// Cloudflare Access, so these primitives add no new exposure class.
// Destructive-command gating stays core-side (ClaudeCodeGate/BridgeGate).
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	macBashOutputLimit = 64 << 10
	macBashTimeout     = 5 * time.Minute
)

// expandHome rewrites ~ / ~/rest to the user's home directory. Other shapes
// pass through unchanged.
func expandHome(p string) string {
	p = strings.TrimSpace(p)
	if p == "" || (p != "~" && !strings.HasPrefix(p, "~/")) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// defaultCwd is where a cwd-less bash lands — ~/Dev, the umbrella folder the
// agent's system prompt describes as its starting point, falling back to home.
func defaultCwd() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "."
	}
	dev := filepath.Join(home, "Dev")
	if st, err := os.Stat(dev); err == nil && st.IsDir() {
		return dev
	}
	return home
}

func writeJSONStatus(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeJSONErr(w http.ResponseWriter, status int, msg string) {
	writeJSONStatus(w, status, map[string]string{"error": msg})
}

func readJSONBody(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 32<<20))
	return dec.Decode(v)
}

// ── /bash ────────────────────────────────────────────────────────────────

type bashRequest struct {
	Cmd        string `json:"cmd"`
	Cwd        string `json:"cwd,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

func (b *bridge) handleBash(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req bashRequest
	if err := readJSONBody(r, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Cmd) == "" {
		writeJSONErr(w, http.StatusBadRequest, "cmd required")
		return
	}
	cwd := defaultCwd()
	if req.Cwd != "" {
		cwd = expandHome(req.Cwd)
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			writeJSONErr(w, http.StatusBadRequest, "cwd not a directory: "+cwd)
			return
		}
	}
	timeout := macBashTimeout
	if req.TimeoutSec > 0 && time.Duration(req.TimeoutSec)*time.Second < macBashTimeout {
		timeout = time.Duration(req.TimeoutSec) * time.Second
	}
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "bash", "-lc", req.Cmd)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	combined, err := cmd.CombinedOutput()
	exitCode := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exitCode = ee.ExitCode()
		} else {
			exitCode = -1
		}
	}
	output := string(combined)
	truncated := false
	if len(output) > macBashOutputLimit {
		output = output[:macBashOutputLimit] + "\n…[truncated, " + fmt.Sprint(len(combined)-macBashOutputLimit) + " more bytes]"
		truncated = true
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"exit_code": exitCode,
		"output":    output,
		"truncated": truncated,
		"cwd":       cwd,
	})
}

// ── /fs/save · /fs/edit ──────────────────────────────────────────────────

type fsSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func (b *bridge) handleFSSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req fsSaveRequest
	if err := readJSONBody(r, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	path := expandHome(req.Path)
	if path == "" {
		writeJSONErr(w, http.StatusBadRequest, "path required")
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := os.WriteFile(path, []byte(req.Content), 0o644); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSONStatus(w, http.StatusOK, map[string]any{"path": path, "bytes": len(req.Content)})
}

type fsEditRequest struct {
	Path       string `json:"path"`
	OldString  string `json:"old_string"`
	NewString  string `json:"new_string"`
	ReplaceAll bool   `json:"replace_all,omitempty"`
}

func (b *bridge) handleFSEdit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req fsEditRequest
	if err := readJSONBody(r, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if req.OldString == "" {
		writeJSONErr(w, http.StatusBadRequest, "old_string required")
		return
	}
	if req.OldString == req.NewString {
		writeJSONErr(w, http.StatusBadRequest, "old_string and new_string are identical")
		return
	}
	path := expandHome(req.Path)
	buf, err := os.ReadFile(path)
	if err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	original := string(buf)
	count := strings.Count(original, req.OldString)
	if count == 0 {
		writeJSONErr(w, http.StatusBadRequest, "old_string not found in file")
		return
	}
	if count > 1 && !req.ReplaceAll {
		writeJSONErr(w, http.StatusBadRequest,
			fmt.Sprintf("old_string appears %d times — pass replace_all:true or supply a unique slice", count))
		return
	}
	var updated string
	if req.ReplaceAll {
		updated = strings.ReplaceAll(original, req.OldString, req.NewString)
	} else {
		updated = strings.Replace(original, req.OldString, req.NewString, 1)
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		writeJSONErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	startLine := strings.Count(original[:strings.Index(original, req.OldString)], "\n") + 1
	writeJSONStatus(w, http.StatusOK, map[string]any{
		"path":         path,
		"replacements": count,
		"bytes":        len(updated),
		"start_line":   startLine,
	})
}

// ── git write verbs ──────────────────────────────────────────────────────

type gitRequest struct {
	Repo    string   `json:"repo,omitempty"`
	Message string   `json:"message,omitempty"`
	Files   []string `json:"files,omitempty"`
	Remote  string   `json:"remote,omitempty"`
	Branch  string   `json:"branch,omitempty"`
}

func (g gitRequest) repoPath() string {
	if strings.TrimSpace(g.Repo) == "" {
		return defaultCwd()
	}
	return expandHome(g.Repo)
}

// runGitExit mirrors the cloud runGit: combined output + exit code, with
// terminal prompts disabled so a missing credential fails instead of hanging.
func runGitExit(ctx context.Context, repo string, args ...string) (string, int) {
	full := append([]string{"-C", repo}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	combined, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			exit = -1
		}
	}
	return string(combined), exit
}

func (b *bridge) gitWrite(w http.ResponseWriter, r *http.Request, build func(gitRequest) ([]string, error)) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req gitRequest
	if err := readJSONBody(r, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	args, err := build(req)
	if err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	repo := req.repoPath()
	out, exit := runGitExit(r.Context(), repo, args...)
	writeJSONStatus(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

func (b *bridge) handleGitStage(w http.ResponseWriter, r *http.Request) {
	b.gitWrite(w, r, func(req gitRequest) ([]string, error) {
		args := []string{"add"}
		if len(req.Files) == 0 {
			args = append(args, "-A")
		} else {
			args = append(args, "--")
			args = append(args, req.Files...)
		}
		return args, nil
	})
}

func (b *bridge) handleGitCommit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req gitRequest
	if err := readJSONBody(r, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeJSONErr(w, http.StatusBadRequest, "message required")
		return
	}
	repo := req.repoPath()
	if msg := honestyRevertVeto(r.Context(), repo); msg != "" {
		// Refuse to commit a deletion/gutting of the error-visibility machinery.
		// exit_code 1 so the caller treats it as a failed git command, fails loud.
		writeJSONStatus(w, http.StatusOK, map[string]any{"repo": repo, "output": msg, "exit_code": 1})
		return
	}
	out, exit := runGitExit(r.Context(), repo, "commit", "-m", req.Message)
	writeJSONStatus(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

// protectedHonestyPaths are the error-visibility / self-healing files the
// autonomous bot must NEVER delete or gut (CLAUDE.md self-healing hard rule +
// memory project_sentry_blind_revert). Coarse path guard; honestySentinel is the
// fine one. Mirrors the lists in docker/sentry/main.go and docker/workspace —
// separate modules can't share the constant, so keep the three in sync.
var protectedHonestyPaths = []string{
	"core/internal/httpx/",
	"core/internal/cron/outcome.go",
	"core/internal/inbox/triage.go",
	"core/internal/proactive/connector_coverage.go",
	"core/db/migrations/153_http_failures.sql",
}

const honestySentinel = "honesty-machinery: do-not-revert"

// honestyRevertVeto inspects the STAGED diff and returns a non-empty block
// message if the commit would delete a protected honesty file or strip its
// do-not-revert sentinel. Deterministic guard (Rule #1b: a load-bearing mechanic
// in code, not droppable prose) so an autonomous revert of the error-visibility
// machinery can never be committed via this seam, regardless of what a recipe or
// the runtime brain decides. Returns "" when the commit is safe. Editing a
// protected file is allowed — only DELETING it or removing the sentinel is blocked.
func honestyRevertVeto(ctx context.Context, repo string) string {
	out, exit := runGitExit(ctx, repo, "diff", "--cached", "--name-status")
	if exit != 0 || strings.TrimSpace(out) == "" {
		return "" // nothing staged / can't inspect — normal commit path handles it
	}
	var hits []string
	for _, line := range strings.Split(out, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		status, path := f[0], f[len(f)-1]
		if !isProtectedHonestyPath(path) {
			continue
		}
		switch {
		case strings.HasPrefix(status, "D"): // deleted
			hits = append(hits, path+" (deleted)")
		case strings.HasPrefix(status, "R") && len(f) >= 3: // renamed away
			hits = append(hits, path+" (renamed)")
		default: // modified — block only if the sentinel is being removed
			d, _ := runGitExit(ctx, repo, "diff", "--cached", "--", path)
			if diffRemovesSentinel(d) {
				hits = append(hits, path+" (do-not-revert sentinel removed)")
			}
		}
	}
	if len(hits) == 0 {
		return ""
	}
	return "[honesty-veto] refusing to commit — this change reverts/deletes the error-visibility machinery, which auto-paths must never touch (CLAUDE.md self-healing hard rule):\n  - " +
		strings.Join(hits, "\n  - ") +
		"\nIf this is an intentional refactor, the boss must do it by hand. Unstage these files and commit the rest."
}

func isProtectedHonestyPath(path string) bool {
	for _, p := range protectedHonestyPaths {
		if path == p || strings.HasPrefix(path, p) {
			return true
		}
	}
	return false
}

// diffRemovesSentinel reports whether a unified diff removes a line carrying the
// do-not-revert sentinel (a removed line starts with '-' but not the '---' header).
func diffRemovesSentinel(diff string) bool {
	for _, l := range strings.Split(diff, "\n") {
		if strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---") && strings.Contains(l, honestySentinel) {
			return true
		}
	}
	return false
}

func (b *bridge) handleGitPush(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSONErr(w, http.StatusMethodNotAllowed, "POST required")
		return
	}
	var req gitRequest
	if err := readJSONBody(r, &req); err != nil {
		writeJSONErr(w, http.StatusBadRequest, err.Error())
		return
	}
	repo := req.repoPath()
	out, exit := safePush(r.Context(), repo, req.Remote, req.Branch)
	writeJSONStatus(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

// safePush does fetch → rebase → push so a push never collides with another
// writer's concurrent commits on a shared branch — the recurring "divergent
// branches" footgun (the bot and the boss both write main). It NEVER
// --force-pushes and NEVER rebases over a dirty tree: this bridge runs in the
// boss's own checked-out clone, so with uncommitted changes it skips the rebase
// and lets a non-fast-forward push fail loud rather than risk his work. On a
// rebase conflict it `--abort`s to leave the tree clean and bails. Bounded
// retry (re-sync once if the branch moved mid-push), then bail.
func safePush(ctx context.Context, repo, remote, branch string) (string, int) {
	if strings.TrimSpace(remote) == "" {
		remote = "origin"
	}
	branch = strings.TrimSpace(branch)
	if branch == "" {
		if o, e := runGitExit(ctx, repo, "rev-parse", "--abbrev-ref", "HEAD"); e == 0 {
			branch = strings.TrimSpace(o)
		}
	}
	var sb strings.Builder
	run := func(args ...string) int {
		o, e := runGitExit(ctx, repo, args...)
		fmt.Fprintf(&sb, "$ git %s\n%s\n", strings.Join(args, " "), strings.TrimRight(o, "\n"))
		return e
	}
	push := func() int {
		if branch != "" {
			return run("push", remote, branch)
		}
		return run("push", remote)
	}

	// Dirty tree or unknown branch → can't safely rebase. Plain push; fail loud
	// on a non-fast-forward rejection rather than --force or rebase over the
	// boss's uncommitted work.
	dirty := false
	if o, e := runGitExit(ctx, repo, "status", "--porcelain"); e == 0 && strings.TrimSpace(o) != "" {
		dirty = true
	}
	if branch == "" || dirty {
		if push() != 0 {
			if dirty {
				sb.WriteString("\n[safe-push] push rejected with uncommitted changes present — NOT auto-rebasing over your work. Commit/stash, then `git pull --rebase`, then push.\n")
			}
			return sb.String(), 1
		}
		return sb.String(), 0
	}

	// New branch (no remote ref yet) → nothing to rebase onto, just push.
	if o, e := runGitExit(ctx, repo, "ls-remote", "--heads", remote, branch); e == 0 && strings.TrimSpace(o) == "" {
		if push() != 0 {
			return sb.String(), 1
		}
		return sb.String(), 0
	}

	// Existing branch, clean tree: fetch → rebase → push, re-syncing once if the
	// branch moved between rebase and push. Never --force.
	for attempt := 0; attempt < 2; attempt++ {
		if run("fetch", remote, branch) != 0 {
			return sb.String(), 1
		}
		if run("rebase", remote+"/"+branch) != 0 {
			run("rebase", "--abort") // leave the tree clean — never a half-rebase
			sb.WriteString("\n[safe-push] rebase hit a conflict — aborted, tree left clean, nothing pushed. Resolve manually.\n")
			return sb.String(), 1
		}
		if push() == 0 {
			return sb.String(), 0
		}
	}
	sb.WriteString("\n[safe-push] push still rejected after re-sync — bailing (no --force).\n")
	return sb.String(), 1
}

func (b *bridge) handleGitPull(w http.ResponseWriter, r *http.Request) {
	b.gitWrite(w, r, func(req gitRequest) ([]string, error) {
		// ff-only: never silently merge — drift is the boss's call.
		args := []string{"pull", "--ff-only"}
		if req.Remote != "" {
			args = append(args, req.Remote)
		}
		if req.Branch != "" {
			args = append(args, req.Branch)
		}
		return args, nil
	})
}
