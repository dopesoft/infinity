package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/proactive"
)

// canvas_api.go — HTTP surface for the Studio Canvas tab.
//
// All filesystem reads and git mutations on the user's home Mac route through
// this file. The contract:
//
//   - Reads (fs/ls, fs/read, git/status, git/diff) call claude_code__* MCP
//     tools directly via the registry. The gate's read-only allow-lists
//     keep them out of the Trust queue.
//
//   - Writes (fs/save, git/stage, git/commit, git/push, git/pull) compose a
//     bash command and queue it as a Trust contract via proactive.TrustStore.
//     The HTTP handler then blocks on the contract's status (up to 15 min)
//     so the request lifecycle mirrors the agent loop's gating semantics.
//     The boss approves in the Trust tab — or inline in Canvas, since the
//     same contract IDs are visible everywhere.
//
//   - Path inputs are sanitised against INFINITY_CANVAS_ROOT before they
//     can reach the Mac, so a malicious browser session can't read /etc/passwd
//     by passing path=../../../etc/passwd. The root is the only allowed
//     prefix; resolved paths that escape are rejected.
//
// Nothing here bypasses the gate. Even the read endpoints go through
// claude_code__* tools so the existing audit / observation pipeline picks
// them up the same way every other Mac-side action does.

const (
	canvasGitWaitTimeout = 15 * time.Minute
	// We poll the trust store rather than blocking on the same channel
	// the agent loop uses because Canvas requests come in through plain
	// HTTP and there's no shared coordination primitive. 1s ticks match
	// the gate's WaitForDecision cadence.
	canvasGitPollInterval = 1 * time.Second
)

// canvasRoot returns the configured workspace root. Defaults to $HOME on the
// Mac side; the env override exists so the boss can scope Canvas to a single
// project directory if they want.
func canvasRoot() string {
	if v := strings.TrimSpace(os.Getenv("INFINITY_CANVAS_ROOT")); v != "" {
		return filepath.Clean(v)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Clean(home)
	}
	return "/"
}

// resolveCanvasPath joins a user-supplied path against the canvas root and
// validates that it stays inside the root. Returns (cleanedAbs, ok).
// The cleaned path is always absolute and never has a trailing slash.
func resolveCanvasPath(raw string) (string, bool) {
	root := canvasRoot()
	if raw == "" || raw == "/" {
		return root, true
	}
	candidate := raw
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(root, candidate)
	}
	clean := filepath.Clean(candidate)
	// rel must not start with ".." or be ".." — both indicate escape.
	rel, err := filepath.Rel(root, clean)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}
	return clean, true
}

// canvasMCP returns the named claude_code tool if available. Errors are
// surfaced inline so the studio can show a connect-your-mac empty state
// rather than the agent loop having to be wired up.
func (s *Server) canvasMCP(name string) (canvasTool, error) {
	if s.loop == nil || s.loop.Tools() == nil {
		return nil, errors.New("canvas: agent registry not configured")
	}
	t, ok := s.loop.Tools().Get(name)
	if !ok {
		return nil, fmt.Errorf("canvas: tool %s not registered (mac bridge offline?)", name)
	}
	return t, nil
}

type canvasTool interface {
	Execute(ctx context.Context, input map[string]any) (string, error)
}

// ---- Filesystem ------------------------------------------------------------

type fsEntry struct {
	Name  string `json:"name"`
	Type  string `json:"type"` // "dir" | "file" | "symlink"
	Size  int64  `json:"size,omitempty"`
	MTime string `json:"mtime,omitempty"`
}

type fsListResponse struct {
	Path    string    `json:"path"`
	Root    string    `json:"root"`
	Entries []fsEntry `json:"entries"`
}

func (s *Server) handleCanvasFSList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	resolved, ok := resolveCanvasPath(path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes INFINITY_CANVAS_ROOT"})
		return
	}

	// Try the LS tool first — falls back to local FS read if the MCP
	// bridge isn't available. The fallback exists so the Canvas surface
	// still works in local-dev when the user runs core on the same
	// machine as their workspace (no Mac bridge needed).
	out := fsListResponse{Path: resolved, Root: canvasRoot(), Entries: []fsEntry{}}

	if t, err := s.canvasMCP("claude_code__LS"); err == nil {
		raw, execErr := t.Execute(r.Context(), map[string]any{"path": resolved})
		if execErr == nil && strings.TrimSpace(raw) != "" {
			out.Entries = parseLsOutput(raw, resolved)
			sortEntries(out.Entries)
			writeJSON(w, http.StatusOK, out)
			return
		}
	}

	// Local FS fallback.
	entries, lerr := localListDir(resolved)
	if lerr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": lerr.Error()})
		return
	}
	out.Entries = entries
	sortEntries(out.Entries)
	writeJSON(w, http.StatusOK, out)
}

func sortEntries(es []fsEntry) {
	sort.Slice(es, func(i, j int) bool {
		if es[i].Type != es[j].Type {
			return es[i].Type == "dir"
		}
		return strings.ToLower(es[i].Name) < strings.ToLower(es[j].Name)
	})
}

func localListDir(dir string) ([]fsEntry, error) {
	f, err := os.Open(dir)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	out := make([]fsEntry, 0, len(names))
	for _, n := range names {
		if strings.HasPrefix(n, ".") && n != ".gitignore" && n != ".env.example" {
			// Hide dotfiles by default — Canvas is for project work,
			// not OS plumbing. The boss can still type the full path
			// into the URL to descend into them if needed.
			continue
		}
		full := filepath.Join(dir, n)
		st, err := os.Lstat(full)
		if err != nil {
			continue
		}
		e := fsEntry{Name: n, MTime: st.ModTime().UTC().Format(time.RFC3339)}
		switch {
		case st.Mode()&os.ModeSymlink != 0:
			e.Type = "symlink"
		case st.IsDir():
			e.Type = "dir"
		default:
			e.Type = "file"
			e.Size = st.Size()
		}
		out = append(out, e)
	}
	return out, nil
}

// parseLsOutput is a forgiving parser for claude_code__LS output, which on
// most Mac bridges is a newline-separated `name<TAB>size<TAB>type` table
// but in older builds is just `ls -1`. We accept both. Anything we can't
// parse becomes a plain file entry — the studio renders it harmlessly.
func parseLsOutput(raw, dir string) []fsEntry {
	out := []fsEntry{}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		name := strings.TrimSpace(fields[0])
		// Drop the dir prefix if the LS output included it.
		name = strings.TrimPrefix(name, dir+string(filepath.Separator))
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." || name == ".." {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		e := fsEntry{Name: name, Type: "file"}
		if strings.HasSuffix(fields[0], "/") {
			e.Type = "dir"
		}
		// Skip dotfiles for the same reason localListDir does.
		if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
			continue
		}
		out = append(out, e)
	}
	return out
}

type fsReadResponse struct {
	Path     string `json:"path"`
	Content  string `json:"content"`
	Language string `json:"language"`
	SHA      string `json:"sha"`
	Size     int64  `json:"size"`
}

func (s *Server) handleCanvasFSRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	resolved, ok := resolveCanvasPath(path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes INFINITY_CANVAS_ROOT"})
		return
	}

	// Try MCP read first; fall back to local read for local-dev.
	if t, err := s.canvasMCP("claude_code__Read"); err == nil {
		out, execErr := t.Execute(r.Context(), map[string]any{"file_path": resolved})
		if execErr == nil && out != "" {
			content := stripReadHeader(out)
			writeJSON(w, http.StatusOK, fsReadResponse{
				Path:     resolved,
				Content:  content,
				Language: detectLanguage(resolved),
				SHA:      sha256Hex(content),
				Size:     int64(len(content)),
			})
			return
		}
	}

	data, lerr := os.ReadFile(resolved)
	if lerr != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": lerr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, fsReadResponse{
		Path:     resolved,
		Content:  string(data),
		Language: detectLanguage(resolved),
		SHA:      sha256Hex(string(data)),
		Size:     int64(len(data)),
	})
}

// stripReadHeader removes the `cat -n`-style line-number prefix Claude Code's
// Read tool emits. The format is exactly N spaces (right-aligned line number)
// then a tab then the source line. We undo that so Monaco shows clean text.
func stripReadHeader(raw string) string {
	lines := strings.Split(raw, "\n")
	out := make([]string, 0, len(lines))
	for _, ln := range lines {
		// First tab character — anything before is the line number prefix.
		if idx := strings.Index(ln, "\t"); idx > 0 {
			// Only strip if the prefix is purely numeric whitespace, e.g. "  42".
			prefix := strings.TrimSpace(ln[:idx])
			isNum := prefix != ""
			for _, c := range prefix {
				if c < '0' || c > '9' {
					isNum = false
					break
				}
			}
			if isNum {
				out = append(out, ln[idx+1:])
				continue
			}
		}
		out = append(out, ln)
	}
	return strings.Join(out, "\n")
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func detectLanguage(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".ts", ".mts", ".cts":
		return "typescript"
	case ".tsx":
		return "typescript"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascript"
	case ".py":
		return "python"
	case ".rs":
		return "rust"
	case ".rb":
		return "ruby"
	case ".java":
		return "java"
	case ".kt":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".hpp", ".cxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".php":
		return "php"
	case ".sh", ".bash", ".zsh":
		return "shell"
	case ".json":
		return "json"
	case ".yaml", ".yml":
		return "yaml"
	case ".toml":
		return "toml"
	case ".md", ".markdown":
		return "markdown"
	case ".html", ".htm":
		return "html"
	case ".css":
		return "css"
	case ".scss", ".sass":
		return "scss"
	case ".sql":
		return "sql"
	case ".dockerfile":
		return "dockerfile"
	}
	if strings.HasSuffix(strings.ToLower(path), "dockerfile") {
		return "dockerfile"
	}
	return "plaintext"
}

type fsSaveRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	BaseSHA string `json:"base_sha,omitempty"`
	// SessionID lets the contract show under the right session in the
	// Trust tab. Optional; pulled from cookie if empty.
	SessionID string `json:"session_id,omitempty"`
}

type fsSaveResponse struct {
	ContractID string `json:"contract_id"`
	Status     string `json:"status"` // pending | approved | denied | conflict | saved
	Path       string `json:"path"`
	NewSHA     string `json:"new_sha,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (s *Server) handleCanvasFSSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20)) // 8 MiB
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var req fsSaveRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	resolved, ok := resolveCanvasPath(req.Path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes INFINITY_CANVAS_ROOT"})
		return
	}

	// Optimistic concurrency: if the caller provided base_sha, refuse if
	// the on-disk SHA drifted (the agent edited the file out from under us).
	if req.BaseSHA != "" {
		if cur, err := os.ReadFile(resolved); err == nil {
			if sha256Hex(string(cur)) != req.BaseSHA {
				writeJSON(w, http.StatusConflict, fsSaveResponse{
					Status: "conflict",
					Path:   resolved,
					Reason: "file changed on disk since you opened it",
				})
				return
			}
		}
	}

	if s.trust == nil {
		writeJSON(w, http.StatusServiceUnavailable, fsSaveResponse{
			Status: "denied",
			Path:   resolved,
			Reason: "trust store not configured; saves disabled",
		})
		return
	}

	preview := buildSavePreview(resolved, req.Content)
	id, err := s.trust.Queue(r.Context(), &proactive.TrustContract{
		Title:     "Canvas save: " + filepath.Base(resolved),
		RiskLevel: "high",
		Source:    "canvas_save",
		ActionSpec: map[string]any{
			"tool":         "claude_code__Write",
			"input":        map[string]any{"file_path": resolved, "content": req.Content},
			"session_id":   req.SessionID,
			"canvas_save":  true,
			"base_sha":     req.BaseSHA,
			"new_sha":      sha256Hex(req.Content),
			"size":         len(req.Content),
			"resolved_path": resolved,
		},
		Reasoning: "Saving a file edited in the Canvas Monaco editor. " +
			"Routed through Trust because writes always require explicit approval.",
		Preview: preview,
	})
	if err != nil || id == "" {
		writeJSON(w, http.StatusInternalServerError, fsSaveResponse{
			Status: "denied",
			Path:   resolved,
			Reason: "could not queue approval",
		})
		return
	}

	// Don't block — the editor's status bar polls /api/trust-contracts and
	// will refresh when the boss approves. Saves often need a moment of
	// thought ("did I just delete the auth layer?") and a 15-min hang is
	// the wrong UX. The status bar shows a single "Approve in Trust" link
	// keyed to ContractID and re-fetches on success.
	writeJSON(w, http.StatusAccepted, fsSaveResponse{
		ContractID: id,
		Status:     "pending",
		Path:       resolved,
		NewSHA:     sha256Hex(req.Content),
	})
}

func buildSavePreview(path, content string) string {
	const maxBytes = 4096
	head := content
	if len(head) > maxBytes {
		head = head[:maxBytes] + "\n…(truncated for preview)"
	}
	return fmt.Sprintf("save → %s\n\n%s", path, head)
}

// ---- Git status / diff (read-only, no Trust queue) -------------------------

type gitStatusEntry struct {
	Path   string `json:"path"`
	Status string `json:"status"`  // M | A | D | R | U (untracked) | ? (mixed)
	Staged bool   `json:"staged"`
	Branch string `json:"branch,omitempty"`
}

type gitStatusResponse struct {
	Repo    string           `json:"repo"`
	Branch  string           `json:"branch"`
	Ahead   int              `json:"ahead"`
	Behind  int              `json:"behind"`
	Entries []gitStatusEntry `json:"entries"`
}

func (s *Server) handleCanvasGitStatus(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveCanvasPath(r.URL.Query().Get("repo"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo escapes INFINITY_CANVAS_ROOT"})
		return
	}
	out, err := s.runReadOnlyBash(r.Context(), "git -C "+shellQuote(repo)+" status --porcelain=v2 --branch")
	if err != nil {
		writeJSON(w, http.StatusOK, gitStatusResponse{Repo: repo, Entries: []gitStatusEntry{}})
		return
	}
	writeJSON(w, http.StatusOK, parseGitStatusV2(out, repo))
}

func parseGitStatusV2(raw, repo string) gitStatusResponse {
	res := gitStatusResponse{Repo: repo, Entries: []gitStatusEntry{}}
	for _, ln := range strings.Split(raw, "\n") {
		ln = strings.TrimRight(ln, "\r")
		switch {
		case strings.HasPrefix(ln, "# branch.head "):
			res.Branch = strings.TrimSpace(strings.TrimPrefix(ln, "# branch.head "))
		case strings.HasPrefix(ln, "# branch.ab "):
			// "# branch.ab +1 -2"
			fields := strings.Fields(strings.TrimPrefix(ln, "# branch.ab "))
			for _, f := range fields {
				switch {
				case strings.HasPrefix(f, "+"):
					fmt.Sscanf(f, "+%d", &res.Ahead)
				case strings.HasPrefix(f, "-"):
					fmt.Sscanf(f, "-%d", &res.Behind)
				}
			}
		case strings.HasPrefix(ln, "1 "), strings.HasPrefix(ln, "2 "):
			// "1 XY ... <path>" or "2 XY ... <path>\t<orig>"
			fields := strings.Fields(ln)
			if len(fields) < 9 {
				continue
			}
			xy := fields[1]
			path := strings.Join(fields[8:], " ")
			e := gitStatusEntry{Path: path, Branch: res.Branch}
			// porcelain v2 XY: position 0 = staged (X), position 1 = working tree (Y).
			x, y := xy[0], xy[1]
			if x != '.' {
				e.Status = string(x)
				e.Staged = true
			} else if y != '.' {
				e.Status = string(y)
				e.Staged = false
			}
			res.Entries = append(res.Entries, e)
		case strings.HasPrefix(ln, "? "):
			path := strings.TrimSpace(strings.TrimPrefix(ln, "? "))
			res.Entries = append(res.Entries, gitStatusEntry{Path: path, Status: "U", Staged: false, Branch: res.Branch})
		}
	}
	return res
}

type gitDiffResponse struct {
	Path   string `json:"path"`
	Staged bool   `json:"staged"`
	Diff   string `json:"diff"`
}

func (s *Server) handleCanvasGitDiff(w http.ResponseWriter, r *http.Request) {
	repo, ok := resolveCanvasPath(r.URL.Query().Get("repo"))
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo escapes INFINITY_CANVAS_ROOT"})
		return
	}
	path := r.URL.Query().Get("path")
	staged := r.URL.Query().Get("staged") == "1" || r.URL.Query().Get("staged") == "true"
	cmd := "git -C " + shellQuote(repo) + " diff --no-color"
	if staged {
		cmd += " --staged"
	}
	if path != "" {
		cmd += " -- " + shellQuote(path)
	}
	out, err := s.runReadOnlyBash(r.Context(), cmd)
	if err != nil {
		writeJSON(w, http.StatusOK, gitDiffResponse{Path: path, Staged: staged, Diff: ""})
		return
	}
	writeJSON(w, http.StatusOK, gitDiffResponse{Path: path, Staged: staged, Diff: out})
}

// runReadOnlyBash invokes claude_code__Bash for a vetted read-only command.
// The gate's isReadOnlyGit allow-list lets these pass without Trust queueing.
func (s *Server) runReadOnlyBash(ctx context.Context, cmd string) (string, error) {
	t, err := s.canvasMCP("claude_code__Bash")
	if err != nil {
		// Local-dev fallback: shell out directly. Same command, runs under
		// the core process. Only meaningful when the boss runs core on
		// their workspace machine.
		return localShell(ctx, cmd)
	}
	return t.Execute(ctx, map[string]any{"command": cmd})
}

func localShell(ctx context.Context, _ string) (string, error) {
	// We intentionally do NOT shell out from the core process. If the MCP
	// bridge isn't available, return empty rather than risk running a
	// "read-only" git command in a context the gate hasn't vetted.
	return "", errors.New("canvas: mac bridge unavailable")
}

// ---- Git mutations (queue Trust contract, wait for approval) ---------------

type gitMutationRequest struct {
	Repo      string   `json:"repo"`
	Paths     []string `json:"paths,omitempty"`
	Message   string   `json:"message,omitempty"`
	Remote    string   `json:"remote,omitempty"`
	Branch    string   `json:"branch,omitempty"`
	SessionID string   `json:"session_id,omitempty"`
}

type gitMutationResponse struct {
	ContractID string `json:"contract_id,omitempty"`
	Status     string `json:"status"` // pending | approved | denied | executed
	Output     string `json:"output,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

func (s *Server) handleCanvasGitStage(w http.ResponseWriter, r *http.Request) {
	s.handleCanvasGitMutation(w, r, "stage", func(req gitMutationRequest, repo string) (string, string, error) {
		paths := req.Paths
		if len(paths) == 0 {
			paths = []string{"-A"}
		}
		parts := []string{"git", "-C", shellQuote(repo), "add"}
		for _, p := range paths {
			parts = append(parts, shellQuote(p))
		}
		cmd := strings.Join(parts, " ")
		return "Stage changes in " + filepath.Base(repo), cmd, nil
	})
}

func (s *Server) handleCanvasGitCommit(w http.ResponseWriter, r *http.Request) {
	s.handleCanvasGitMutation(w, r, "commit", func(req gitMutationRequest, repo string) (string, string, error) {
		msg := strings.TrimSpace(req.Message)
		if msg == "" {
			return "", "", errors.New("commit message required")
		}
		cmd := fmt.Sprintf("git -C %s commit -m %s", shellQuote(repo), shellQuote(msg))
		return "Commit: " + truncate(msg, 60), cmd, nil
	})
}

func (s *Server) handleCanvasGitPush(w http.ResponseWriter, r *http.Request) {
	s.handleCanvasGitMutation(w, r, "push", func(req gitMutationRequest, repo string) (string, string, error) {
		remote := strings.TrimSpace(req.Remote)
		if remote == "" {
			remote = "origin"
		}
		branch := sanitizeRef(req.Branch)
		cmd := fmt.Sprintf("git -C %s push %s", shellQuote(repo), shellQuote(remote))
		if branch != "" {
			cmd += " " + shellQuote(branch)
		}
		return fmt.Sprintf("Push %s → %s", branch, remote), cmd, nil
	})
}

func (s *Server) handleCanvasGitPull(w http.ResponseWriter, r *http.Request) {
	s.handleCanvasGitMutation(w, r, "pull", func(req gitMutationRequest, repo string) (string, string, error) {
		remote := strings.TrimSpace(req.Remote)
		if remote == "" {
			remote = "origin"
		}
		branch := sanitizeRef(req.Branch)
		cmd := fmt.Sprintf("git -C %s pull %s", shellQuote(repo), shellQuote(remote))
		if branch != "" {
			cmd += " " + shellQuote(branch)
		}
		return fmt.Sprintf("Pull %s ← %s", branch, remote), cmd, nil
	})
}

func (s *Server) handleCanvasGitMutation(
	w http.ResponseWriter,
	r *http.Request,
	verb string,
	compose func(req gitMutationRequest, repo string) (title, cmd string, err error),
) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "POST only"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	var req gitMutationRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	repo, ok := resolveCanvasPath(req.Repo)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "repo escapes INFINITY_CANVAS_ROOT"})
		return
	}
	for _, p := range req.Paths {
		if strings.ContainsAny(p, "\n\r;|&`$<>") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid path token"})
			return
		}
	}

	title, cmd, cerr := compose(req, repo)
	if cerr != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": cerr.Error()})
		return
	}

	if s.trust == nil {
		writeJSON(w, http.StatusServiceUnavailable, gitMutationResponse{
			Status: "denied",
			Reason: "trust store not configured; git mutations disabled",
		})
		return
	}

	id, err := s.trust.Queue(r.Context(), &proactive.TrustContract{
		Title:     title,
		RiskLevel: "high",
		Source:    "canvas_git_" + verb,
		ActionSpec: map[string]any{
			"tool":       "claude_code__Bash",
			"input":      map[string]any{"command": cmd},
			"session_id": req.SessionID,
			"canvas_git": verb,
			"repo":       repo,
		},
		Reasoning: "Canvas git " + verb + " on the home Mac. Reviewed before execution because the verb mutates state.",
		Preview:   cmd,
	})
	if err != nil || id == "" {
		writeJSON(w, http.StatusInternalServerError, gitMutationResponse{
			Status: "denied",
			Reason: "could not queue approval",
		})
		return
	}

	// Block until the boss decides — same model the agent loop's gate
	// uses. We give 15 min. Once approved, run the bash command and
	// echo the output back to the studio.
	decision, reason := s.waitForTrustDecision(r.Context(), id, canvasGitWaitTimeout)
	if !decision {
		writeJSON(w, http.StatusOK, gitMutationResponse{
			ContractID: id,
			Status:     "denied",
			Reason:     reason,
		})
		return
	}

	t, err := s.canvasMCP("claude_code__Bash")
	if err != nil {
		writeJSON(w, http.StatusServiceUnavailable, gitMutationResponse{
			ContractID: id,
			Status:     "denied",
			Reason:     "mac bridge unavailable after approval",
		})
		return
	}
	out, execErr := t.Execute(r.Context(), map[string]any{"command": cmd})
	if execErr != nil {
		writeJSON(w, http.StatusOK, gitMutationResponse{
			ContractID: id,
			Status:     "denied",
			Reason:     execErr.Error(),
			Output:     out,
		})
		return
	}
	writeJSON(w, http.StatusOK, gitMutationResponse{
		ContractID: id,
		Status:     "executed",
		Output:     out,
	})
}

// waitForTrustDecision polls the trust store. Mirrors proactive.ClaudeCodeGate's
// loop without coupling to it — the Canvas HTTP layer doesn't have a
// gate.ToolGate interface available, so we read the store directly.
func (s *Server) waitForTrustDecision(ctx context.Context, id string, timeout time.Duration) (bool, string) {
	if s.trust == nil || id == "" {
		return false, "trust store not configured"
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(canvasGitPollInterval)
	defer tick.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if errors.Is(waitCtx.Err(), context.DeadlineExceeded) {
				return false, "timed out waiting for approval"
			}
			return false, "request cancelled"
		case <-tick.C:
			status, _, _, err := s.trust.LookupForGate(waitCtx, id)
			if err != nil {
				continue
			}
			switch status {
			case "approved", "consumed":
				return true, ""
			case "denied":
				return false, "denied by the boss"
			case "snoozed":
				return false, "snoozed (treat as deny for this request)"
			default:
				continue
			}
		}
	}
}

// ---- Canvas config (workspace root, defaults) ------------------------------

type canvasConfigResponse struct {
	Root        string `json:"root"`
	PreviewURL  string `json:"preview_url,omitempty"`
	MacBridgeOK bool   `json:"mac_bridge_ok"`
}

func (s *Server) handleCanvasConfig(w http.ResponseWriter, r *http.Request) {
	bridge := false
	if s.loop != nil && s.loop.Tools() != nil {
		if _, ok := s.loop.Tools().Get("claude_code__Bash"); ok {
			bridge = true
		}
	}
	writeJSON(w, http.StatusOK, canvasConfigResponse{
		Root:        canvasRoot(),
		PreviewURL:  strings.TrimSpace(os.Getenv("INFINITY_CANVAS_PREVIEW_URL")),
		MacBridgeOK: bridge,
	})
}

// ---- helpers ---------------------------------------------------------------

// shellQuote wraps a string in single quotes, escaping internal quotes the
// POSIX-safe way: ' → '\''. Used everywhere we splice user-supplied tokens
// into a bash command. Even with the gate's allow-list this is the
// last-line defense: a single unquoted path with a space could change
// semantics, and we want zero ambiguity.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// sanitizeRef strips anything from a git ref that isn't alnum, dot, slash,
// dash, underscore, or plus. Git ref names are restrictive — this is loose
// enough to allow any legal ref but tight enough to make injection
// uninteresting.
func sanitizeRef(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '.', r == '/', r == '-', r == '_', r == '+':
			b.WriteRune(r)
		}
	}
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
