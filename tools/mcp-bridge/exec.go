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
	b.gitWrite(w, r, func(req gitRequest) ([]string, error) {
		if strings.TrimSpace(req.Message) == "" {
			return nil, fmt.Errorf("message required")
		}
		return []string{"commit", "-m", req.Message}, nil
	})
}

func (b *bridge) handleGitPush(w http.ResponseWriter, r *http.Request) {
	b.gitWrite(w, r, func(req gitRequest) ([]string, error) {
		args := []string{"push"}
		if req.Remote != "" {
			args = append(args, req.Remote)
		}
		if req.Branch != "" {
			args = append(args, req.Branch)
		}
		return args, nil
	})
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
