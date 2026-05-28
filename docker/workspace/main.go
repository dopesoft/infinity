// workspace — bridge primitives for Jarvis's cloud filesystem.
//
// This is intentionally a small, dumb HTTP server. It's the second
// implementation of the Bridge contract that Jarvis speaks (the first
// being the Mac bridge over Cloudflare Tunnel). Both speak the same
// shape; Core's router decides which one to call per session.
//
// Design notes:
//
//   - No LLM is embedded here. Jarvis lives in Core and uses ChatGPT
//     subscription via openai_oauth for cognition. This service is
//     pure substrate — file ops, bash, git — that Jarvis assembles.
//     (Per Rule #1 in CLAUDE.md: building blocks, not sub-agents.)
//
//   - Reachable only on Railway's private network at
//     workspace.railway.internal:$PORT. No public ingress. Auth via
//     WORKSPACE_BRIDGE_TOKEN bearer header so even on a leaky private
//     net we don't ship file writes to anyone but Core.
//
//   - Volume mounts at $WORKSPACE_ROOT (default /workspace). All paths
//     in requests are resolved relative to this root and rejected if
//     they escape it (..  /etc/passwd attempts return 400).
//
//   - git identity is forced to "Jarvis Cloud <jarvis@dopesoft.io>" so
//     the audit log distinguishes Mac-authored commits from cloud-
//     authored ones. GITHUB_TOKEN env unlocks pushes via HTTPS.
//
//   - Bash output is truncated at 64 KB and commands wall-time out at
//     5 minutes. Long-running stuff (dev servers, watchers) should be
//     started with `nohup` / `&` so they survive the request.
//
//   - Cold start: Railway's App Sleeping can pause this when idle.
//     The first request after sleep wakes it (~5-15s). Core pre-warms
//     on Studio's Canvas mount so the boss rarely sees it.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	bashTimeout     = 5 * time.Minute
	bashOutputLimit = 64 << 10 // 64 KB — Jarvis's context can't afford bigger
	maxReadBytes    = 4 << 20  // 4 MB hard cap on a single file read
)

var (
	workspaceRoot string
	bearerToken   string
	bootMu        sync.Once
)

// infoLog writes to stdout so Railway's log shipper tags these lines
// severity=info. The stdlib `log` package writes to stderr by default,
// which Railway stamps severity=error — so success/progress lines that
// use plain log.Printf show up as fake red errors. Route info/success
// here; reserve the default log.Printf (stderr) for genuine failures.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

func main() {
	bootMu.Do(initEnv)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/version", handleVersion)
	mux.HandleFunc("/fs/ls", auth(handleFSList))
	mux.HandleFunc("/fs/read", auth(handleFSRead))
	mux.HandleFunc("/fs/raw", auth(handleFSRaw))
	mux.HandleFunc("/fs/save", auth(handleFSSave))
	mux.HandleFunc("/fs/edit", auth(handleFSEdit))
	mux.HandleFunc("/bash", auth(handleBash))
	mux.HandleFunc("/git/status", auth(handleGitStatus))
	mux.HandleFunc("/git/diff", auth(handleGitDiff))
	mux.HandleFunc("/git/stage", auth(handleGitStage))
	mux.HandleFunc("/git/commit", auth(handleGitCommit))
	mux.HandleFunc("/git/push", auth(handleGitPush))
	mux.HandleFunc("/git/pull", auth(handleGitPull))
	mux.HandleFunc("/git/init", auth(handleGitInit))
	mux.HandleFunc("/docgen", auth(handleDocgen))

	// Preview supervisor — boots a project's dev server (or serves it static)
	// and fronts the running app. Core proxies the browser-facing prefix here;
	// the /supervisor/* control plane matches the Mac bridge's contract so
	// Core's /api/canvas/project/* handlers work across both bridges.
	mux.HandleFunc("/supervisor/start", auth(handleSupervisorStart))
	mux.HandleFunc("/supervisor/stop", auth(handleSupervisorStop))
	mux.HandleFunc("/supervisor/status", auth(handleSupervisorStatus))
	mux.HandleFunc("/supervisor/active", auth(handleSupervisorActive))
	mux.HandleFunc(previewPrefix+"/", auth(handlePreview))

	addr := ":" + envDefault("PORT", "8080")
	infoLog.Printf("workspace bridge: listening on %s (root=%s)", addr, workspaceRoot)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("workspace bridge: %v", err)
	}
}

// initEnv resolves config + ensures the workspace root + git identity are
// in place. Runs once at boot. Panics on fatal misconfiguration.
func initEnv() {
	workspaceRoot = envDefault("WORKSPACE_ROOT", "/workspace")
	bearerToken = strings.TrimSpace(os.Getenv("WORKSPACE_BRIDGE_TOKEN"))
	if bearerToken == "" {
		log.Println("WARNING: WORKSPACE_BRIDGE_TOKEN unset — bridge will refuse all writes")
	}
	if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
		log.Fatalf("workspace bridge: mkdir %s: %v", workspaceRoot, err)
	}

	// Force a recognisable git identity for any commit authored here.
	// The boss's Mac commits stay under their normal identity; this
	// makes "Jarvis did this" trivially diff-able in the log.
	name := envDefault("GIT_USER_NAME", "Jarvis Cloud")
	email := envDefault("GIT_USER_EMAIL", "jarvis@dopesoft.io")
	_ = exec.Command("git", "config", "--global", "user.name", name).Run()
	_ = exec.Command("git", "config", "--global", "user.email", email).Run()
	_ = exec.Command("git", "config", "--global", "init.defaultBranch", "main").Run()
	_ = exec.Command("git", "config", "--global", "safe.directory", "*").Run()

	// Wire GitHub HTTPS auth so `git push` works without manual setup.
	// Only applies to github.com URLs.
	if tok := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); tok != "" {
		helper := fmt.Sprintf(`!f() { echo "username=oauth2"; echo "password=%s"; }; f`, tok)
		_ = exec.Command("git", "config", "--global",
			"credential.https://github.com.helper", helper).Run()
	}

	// Lay the disk out as a self-contained computer: Jarvis's own code at
	// <root>/infinity (so he can fix himself) and user projects at
	// <root>/projects/<name> (each its own git repo). Idempotent + runs every
	// boot; safe to leave wired forever.
	bootstrapLayout()
}

// bootstrapLayout splits the workspace volume into infinity/ (self) + projects/
// (apps), migrating the legacy layout where the volume ROOT was the infinity
// checkout. Non-destructive: it MOVES the existing checkout (working tree +
// .git + any uncommitted work) into <root>/infinity rather than deleting
// anything, so nothing is ever lost. On a fresh/empty volume it clones infinity
// instead. Never fatal — a bootstrap hiccup must not take the bridge down, so
// every failure logs and returns, leaving the disk as-is.
func bootstrapLayout() {
	selfDir := filepath.Join(workspaceRoot, "infinity")
	projectsDir := filepath.Join(workspaceRoot, "projects")

	// The projects store always exists — it's the disk-like home for apps.
	if err := os.MkdirAll(projectsDir, 0o755); err != nil {
		log.Printf("bootstrap: mkdir projects: %v", err)
	}

	// Already migrated (self-clone in place) — nothing to do.
	if isDir(filepath.Join(selfDir, ".git")) {
		infoLog.Printf("bootstrap: self-clone present at %s", selfDir)
		return
	}

	// Legacy layout: the volume root IS the infinity checkout. Relocate it into
	// infinity/ by MOVING every root entry (except the new dirs) — preserves
	// uncommitted work, deletes nothing.
	if isDir(filepath.Join(workspaceRoot, ".git")) {
		infoLog.Printf("bootstrap: relocating legacy /workspace checkout into %s (move, non-destructive)", selfDir)
		if dirty := gitDirty(workspaceRoot); dirty {
			infoLog.Printf("bootstrap: NOTE legacy checkout has uncommitted changes — moving preserves them all")
		}
		if !relocateRootCheckout(selfDir) {
			log.Printf("bootstrap: relocation incomplete; leaving layout as-is (re-runs next boot)")
		} else {
			infoLog.Printf("bootstrap: relocation complete — self now at %s", selfDir)
		}
		return
	}

	// Fresh volume: clone infinity into infinity/. Auth rides the credential
	// helper configured above, so the URL carries no token (nothing to leak).
	owner := envDefault("INFINITY_REPO_OWNER", "DopeSoft")
	repo := envDefault("INFINITY_REPO_NAME", "infinity")
	url := fmt.Sprintf("https://github.com/%s/%s.git", owner, repo)
	infoLog.Printf("bootstrap: empty volume — cloning %s/%s into %s", owner, repo, selfDir)
	cmd := exec.Command("git", "clone", url, selfDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		log.Printf("bootstrap: clone failed: %v (%s)", err, lastLine(string(out)))
	}
}

// relocateRootCheckout moves every entry at the workspace root (except the new
// infinity/ + projects/ dirs and volume artifacts) into selfDir. Returns true
// once selfDir holds a .git. os.Rename within one volume is atomic + cheap.
func relocateRootCheckout(selfDir string) bool {
	if err := os.MkdirAll(selfDir, 0o755); err != nil {
		log.Printf("bootstrap: mkdir self: %v", err)
		return false
	}
	entries, err := os.ReadDir(workspaceRoot)
	if err != nil {
		log.Printf("bootstrap: readdir root: %v", err)
		return false
	}
	skip := map[string]bool{"infinity": true, "projects": true, ".bootstrap.sh": true, "lost+found": true}
	moved := 0
	for _, e := range entries {
		if skip[e.Name()] {
			continue
		}
		if err := os.Rename(filepath.Join(workspaceRoot, e.Name()), filepath.Join(selfDir, e.Name())); err != nil {
			log.Printf("bootstrap: move %s: %v", e.Name(), err)
			return false // abort on first failure — partial move re-runs next boot
		}
		moved++
	}
	infoLog.Printf("bootstrap: moved %d root entries into %s", moved, selfDir)
	return isDir(filepath.Join(selfDir, ".git"))
}

// gitDirty reports whether the repo at dir has uncommitted changes. Best-effort;
// used only to log that a move preserves them.
func gitDirty(dir string) bool {
	out, err := exec.Command("git", "-C", dir, "status", "--porcelain").Output()
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(out)) != ""
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// lastLine returns the final non-empty line of s (for compact error logs).
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return ""
}

func envDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// auth wraps a handler in a bearer-token check. We never run unauth'd
// writes — the bridge is on Railway's private net but defence-in-depth
// is cheap. /health and /version are exempt for liveness probes.
func auth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if bearerToken == "" {
			http.Error(w, "bridge bearer token not configured", http.StatusServiceUnavailable)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got != bearerToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		h(w, r)
	}
}

// resolvePath joins a raw path against the workspace root and rejects
// any attempt to escape via "..". Always returns an absolute path.
func resolvePath(raw string) (string, error) {
	if raw == "" {
		return workspaceRoot, nil
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(workspaceRoot, candidate)
	}
	cleaned := filepath.Clean(candidate)
	rootClean := filepath.Clean(workspaceRoot)
	if cleaned != rootClean && !strings.HasPrefix(cleaned+string(os.PathSeparator), rootClean+string(os.PathSeparator)) {
		return "", errors.New("path escapes workspace root")
	}
	return cleaned, nil
}

// ── handlers ─────────────────────────────────────────────────────────────

func handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":      true,
		"service": "workspace-bridge",
		"root":    workspaceRoot,
	})
}

func handleVersion(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"service":     "workspace-bridge",
		"git_sha":     strings.TrimSpace(os.Getenv("RAILWAY_GIT_COMMIT_SHA")),
		"deployed_at": strings.TrimSpace(os.Getenv("RAILWAY_DEPLOYMENT_CREATED_AT")),
	})
}

// ── fs ───────────────────────────────────────────────────────────────────

type fsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"`
}

func handleFSList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	resolved, err := resolvePath(path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	entries, err := os.ReadDir(resolved)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out := make([]fsEntry, 0, len(entries))
	for _, e := range entries {
		info, _ := e.Info()
		size := int64(0)
		if info != nil && !e.IsDir() {
			size = info.Size()
		}
		out = append(out, fsEntry{
			Name:  e.Name(),
			Path:  filepath.Join(resolved, e.Name()),
			IsDir: e.IsDir(),
			Size:  size,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"root":    resolved,
		"entries": out,
	})
}

type fsReadResponse struct {
	Path      string `json:"path"`
	Content   string `json:"content"`
	Lines     int    `json:"lines"`
	Truncated bool   `json:"truncated,omitempty"`
	Start     int    `json:"start,omitempty"`
	End       int    `json:"end,omitempty"`
}

func handleFSRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	start := atoiOr(r.URL.Query().Get("start"), 0)
	end := atoiOr(r.URL.Query().Get("end"), 0)
	resolved, err := resolvePath(path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	f, err := os.Open(resolved)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	buf, err := io.ReadAll(io.LimitReader(f, maxReadBytes+1))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	truncated := len(buf) > maxReadBytes
	if truncated {
		buf = buf[:maxReadBytes]
	}
	content := string(buf)

	// Optional line slicing — keeps Jarvis's context tight when he
	// only needs a window. Lines are 1-indexed; end=0 means "to EOF".
	lineCount := strings.Count(content, "\n") + 1
	if start > 0 || end > 0 {
		lines := strings.Split(content, "\n")
		if start < 1 {
			start = 1
		}
		if end < 1 || end > len(lines) {
			end = len(lines)
		}
		if start > len(lines) {
			start = len(lines)
		}
		content = strings.Join(lines[start-1:end], "\n")
	}

	writeJSON(w, http.StatusOK, fsReadResponse{
		Path:      resolved,
		Content:   content,
		Lines:     lineCount,
		Truncated: truncated,
		Start:     start,
		End:       end,
	})
}

// handleFSRaw streams a file's raw bytes — binary-safe, unlike /fs/read
// (which JSON-encodes content as a string and would corrupt .xlsx/.docx/
// .pptx). Core's /api/workspace/download proxies this so generated documents
// download/preview in Studio from ANY device, independent of the session's
// Mac/Cloud bridge. GET /fs/raw?path=...
func handleFSRaw(w http.ResponseWriter, r *http.Request) {
	resolved, err := resolvePath(r.URL.Query().Get("path"))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	info, err := os.Stat(resolved)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
		return
	}
	if info.IsDir() {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path is a directory"})
		return
	}
	f, err := os.Open(resolved)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer f.Close()
	ct := mime.TypeByExtension(filepath.Ext(resolved))
	if ct == "" {
		ct = "application/octet-stream"
	}
	w.Header().Set("Content-Type", ct)
	w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
	w.Header().Set("Content-Disposition", "inline; filename="+strconv.Quote(filepath.Base(resolved)))
	_, _ = io.Copy(w, f)
}

type fsSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

func handleFSSave(w http.ResponseWriter, r *http.Request) {
	var req fsSaveRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resolved, err := resolvePath(req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if err := os.WriteFile(resolved, []byte(req.Content), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"path":  resolved,
		"bytes": len(req.Content),
	})
}

type fsEditRequest struct {
	Path      string `json:"path"`
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	// ReplaceAll defaults to false. When false, an OldString that
	// occurs more than once errors out — same strict semantics Claude
	// Code's Edit tool uses to avoid ambiguous surgery.
	ReplaceAll bool `json:"replace_all,omitempty"`
}

func handleFSEdit(w http.ResponseWriter, r *http.Request) {
	var req fsEditRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if req.OldString == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "old_string required"})
		return
	}
	if req.OldString == req.NewString {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "old_string and new_string are identical"})
		return
	}
	resolved, err := resolvePath(req.Path)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	buf, err := os.ReadFile(resolved)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	original := string(buf)
	count := strings.Count(original, req.OldString)
	if count == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "old_string not found in file",
		})
		return
	}
	if count > 1 && !req.ReplaceAll {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("old_string appears %d times — pass replace_all:true or supply a unique slice", count),
		})
		return
	}
	var updated string
	if req.ReplaceAll {
		updated = strings.ReplaceAll(original, req.OldString, req.NewString)
	} else {
		updated = strings.Replace(original, req.OldString, req.NewString, 1)
	}
	if err := os.WriteFile(resolved, []byte(updated), 0o644); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	// Authoritative edit location: the 1-based line where the (first) match sat.
	// Content before the match is unchanged, so this is exactly where the new
	// text now starts in the updated file - the canvas reveals THIS line instead
	// of guessing by matching text.
	startLine := strings.Count(original[:strings.Index(original, req.OldString)], "\n") + 1
	writeJSON(w, http.StatusOK, map[string]any{
		"path":         resolved,
		"replacements": count,
		"bytes":        len(updated),
		"start_line":   startLine,
	})
}

// ── bash ─────────────────────────────────────────────────────────────────

type bashRequest struct {
	Cmd        string `json:"cmd"`
	Cwd        string `json:"cwd,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

func handleBash(w http.ResponseWriter, r *http.Request) {
	var req bashRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Cmd) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cmd required"})
		return
	}
	cwd := workspaceRoot
	if req.Cwd != "" {
		c, err := resolvePath(req.Cwd)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		cwd = c
	}
	timeout := bashTimeout
	if req.TimeoutSec > 0 && time.Duration(req.TimeoutSec)*time.Second < bashTimeout {
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
	if len(output) > bashOutputLimit {
		output = output[:bashOutputLimit] + "\n…[truncated, " + fmt.Sprint(len(combined)-bashOutputLimit) + " more bytes]"
		truncated = true
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"exit_code": exitCode,
		"output":    output,
		"truncated": truncated,
		"cwd":       cwd,
	})
}

// ── git ──────────────────────────────────────────────────────────────────
//
// We shell out to git rather than using go-git so we get the user's full
// hooks/config/credential helper for free. Each handler is a thin wrapper
// around `git -C <repo> <verb>`.

type gitRequest struct {
	Repo    string   `json:"repo,omitempty"`    // relative or absolute; defaults to workspace root
	Message string   `json:"message,omitempty"` // for commit
	Files   []string `json:"files,omitempty"`   // for stage
	Remote  string   `json:"remote,omitempty"`  // for push/pull
	Branch  string   `json:"branch,omitempty"`  // for push/pull
}

func (g gitRequest) repoPath() (string, error) {
	repo := g.Repo
	if repo == "" {
		repo = workspaceRoot
	}
	return resolvePath(repo)
}

func runGit(ctx context.Context, repo string, args ...string) (string, int, error) {
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
	return string(combined), exit, err
}

func handleGitStatus(w http.ResponseWriter, r *http.Request) {
	repo, err := resolveRepoQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out, _, _ := runGit(r.Context(), repo, "status", "--porcelain=v2", "--branch")
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "output": out})
}

func handleGitDiff(w http.ResponseWriter, r *http.Request) {
	repo, err := resolveRepoQuery(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	args := []string{"diff", "--no-color"}
	if r.URL.Query().Get("staged") == "1" {
		args = append(args, "--staged")
	}
	if p := r.URL.Query().Get("path"); p != "" {
		args = append(args, "--", p)
	}
	out, _, _ := runGit(r.Context(), repo, args...)
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "diff": out})
}

func handleGitStage(w http.ResponseWriter, r *http.Request) {
	var req gitRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	repo, err := req.repoPath()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	args := []string{"add"}
	if len(req.Files) == 0 {
		args = append(args, "-A")
	} else {
		args = append(args, "--")
		args = append(args, req.Files...)
	}
	out, exit, _ := runGit(r.Context(), repo, args...)
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

func handleGitCommit(w http.ResponseWriter, r *http.Request) {
	var req gitRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if strings.TrimSpace(req.Message) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "message required"})
		return
	}
	repo, err := req.repoPath()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	out, exit, _ := runGit(r.Context(), repo, "commit", "-m", req.Message)
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

func handleGitPush(w http.ResponseWriter, r *http.Request) {
	var req gitRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	repo, err := req.repoPath()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	args := []string{"push"}
	if req.Remote != "" {
		args = append(args, req.Remote)
	}
	if req.Branch != "" {
		args = append(args, req.Branch)
	}
	out, exit, _ := runGit(r.Context(), repo, args...)
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

func handleGitPull(w http.ResponseWriter, r *http.Request) {
	var req gitRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	repo, err := req.repoPath()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	// Default to ff-only so we never silently merge — surprises Jarvis
	// less and forces the boss to resolve manually if there's drift.
	args := []string{"pull", "--ff-only"}
	if req.Remote != "" {
		args = append(args, req.Remote)
	}
	if req.Branch != "" {
		args = append(args, req.Branch)
	}
	out, exit, _ := runGit(r.Context(), repo, args...)
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

func handleGitInit(w http.ResponseWriter, r *http.Request) {
	var req gitRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	repo, err := req.repoPath()
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	out, exit, _ := runGit(r.Context(), repo, "init", "-b", "main")
	writeJSON(w, http.StatusOK, map[string]any{"repo": repo, "output": out, "exit_code": exit})
}

// ── docgen ─────────────────────────────────────────────────────────────────
//
// Renders a structured spec into a real Office/PDF file using the baked
// helpers in $DOCGEN_DIR. Format dispatch:
//
//	xlsx → venv python render_xlsx.py   (openpyxl)
//	docx → node render_docx.js          (docx-js)
//	pptx → node render_pptx.js          (pptxgenjs)
//	pdf  → venv python render_pdf.py    (reportlab)
//	md   → write content.markdown verbatim (rendered in Studio)
//
// The spec is the request's `content` object, piped to the helper on stdin.
// The file lands in the workspace volume (so the canvas file browser shows
// it). When `also_pdf` is set, LibreOffice headless renders a sibling .pdf
// for visual review (decks/docs).
type docgenRequest struct {
	Format   string          `json:"format"`
	Filename string          `json:"filename"`
	Content  json.RawMessage `json:"content"`
	AlsoPDF  bool            `json:"also_pdf,omitempty"`
}

func docgenDir() string { return envDefault("DOCGEN_DIR", "/opt/docgen") }

func handleDocgen(w http.ResponseWriter, r *http.Request) {
	var req docgenRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	format := strings.ToLower(strings.TrimSpace(req.Format))
	if format == "" || strings.TrimSpace(req.Filename) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "format and filename are required"})
		return
	}
	out, err := resolvePath(req.Filename)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}

	dir := docgenDir()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()

	var runErr error
	switch format {
	case "md", "markdown":
		var c struct {
			Markdown string `json:"markdown"`
		}
		_ = json.Unmarshal(req.Content, &c)
		runErr = os.WriteFile(out, []byte(c.Markdown), 0o644)
	case "xlsx":
		runErr = runDocgenHelper(ctx, dir, "venv/bin/python", "render_xlsx.py", out, req.Content)
	case "pdf":
		runErr = runDocgenHelper(ctx, dir, "venv/bin/python", "render_pdf.py", out, req.Content)
	case "docx":
		runErr = runDocgenHelper(ctx, dir, "node", "render_docx.js", out, req.Content)
	case "pptx":
		runErr = runDocgenHelper(ctx, dir, "node", "render_pptx.js", out, req.Content)
	default:
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "unsupported format: " + format})
		return
	}
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": runErr.Error()})
		return
	}

	resp := map[string]any{"path": out, "format": format}
	if info, err := os.Stat(out); err == nil {
		resp["bytes"] = info.Size()
	}
	// Optional: render a sibling PDF for visual review via LibreOffice.
	if req.AlsoPDF && format != "pdf" && format != "md" {
		if pdfPath, err := sofficeToPDF(ctx, out); err == nil {
			resp["pdf_path"] = pdfPath
		} else {
			resp["pdf_error"] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// runDocgenHelper executes a baked helper (python venv or node) with the
// spec piped on stdin and the output path as the first arg. cwd is the
// docgen dir so node's require() resolves the local node_modules.
func runDocgenHelper(ctx context.Context, dir, interp, script, out string, spec json.RawMessage) error {
	bin := interp
	if !filepath.IsAbs(interp) && strings.Contains(interp, "/") {
		bin = filepath.Join(dir, interp) // e.g. venv/bin/python
	}
	cmd := exec.CommandContext(ctx, bin, filepath.Join(dir, script), out)
	cmd.Dir = dir
	if len(spec) == 0 {
		spec = json.RawMessage("{}")
	}
	cmd.Stdin = strings.NewReader(string(spec))
	combined, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(combined))
		if len(msg) > 600 {
			msg = msg[:600] + "…"
		}
		return fmt.Errorf("%s %s: %v: %s", interp, script, err, msg)
	}
	return nil
}

// sofficeToPDF renders an Office file to a sibling PDF via LibreOffice
// headless. Returns the produced PDF path.
func sofficeToPDF(ctx context.Context, src string) (string, error) {
	outDir := filepath.Dir(src)
	cmd := exec.CommandContext(ctx, "soffice", "--headless", "--convert-to", "pdf", "--outdir", outDir, src)
	// soffice needs a writable HOME for its profile.
	cmd.Env = append(os.Environ(), "HOME="+workspaceRoot)
	if combined, err := cmd.CombinedOutput(); err != nil {
		return "", fmt.Errorf("soffice: %v: %s", err, strings.TrimSpace(string(combined)))
	}
	pdf := strings.TrimSuffix(src, filepath.Ext(src)) + ".pdf"
	if _, err := os.Stat(pdf); err != nil {
		return "", fmt.Errorf("soffice produced no pdf")
	}
	return pdf, nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func resolveRepoQuery(r *http.Request) (string, error) {
	return resolvePath(r.URL.Query().Get("repo"))
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func readJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(io.LimitReader(r.Body, 16<<20))
	return dec.Decode(dst)
}

func atoiOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return fallback
		}
		n = n*10 + int(c-'0')
	}
	return n
}
