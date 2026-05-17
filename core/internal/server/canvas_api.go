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
	"net/url"
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

// canvasDefaultProjectPath returns the path Studio should use when a session
// has no project_path attached. Set INFINITY_DEFAULT_PROJECT_PATH to the
// Jarvis repo so chat-only sessions default to "working on yourself" instead
// of forcing the boss to set a workspace root every time. Empty string means
// "leave the panel blank" (legacy behaviour).
func canvasDefaultProjectPath() string {
	if v := strings.TrimSpace(os.Getenv("INFINITY_DEFAULT_PROJECT_PATH")); v != "" {
		return filepath.Clean(v)
	}
	return ""
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

// directBridge returns the configured tunnel base URL (e.g.
// https://coder.dopesoft.io) for hitting the bridge's direct
// filesystem + git endpoints. Strips the trailing /sse since those
// endpoints live at /fs/ls, /fs/read, /git/status, /git/diff. Returns
// "" if not configured — callers should fall through to MCP.
func bridgeBaseURL() string {
	raw := strings.TrimSpace(os.Getenv("CLAUDE_CODE_TUNNEL_URL"))
	if raw == "" {
		return ""
	}
	return strings.TrimSuffix(strings.TrimSuffix(raw, "/sse"), "/")
}

// directBridgeHeaders returns the Cloudflare Access service-token headers
// configured for the claude_code MCP server. Reuses the same env vars
// (CF_ACCESS_CLIENT_ID / CF_ACCESS_CLIENT_SECRET) so we don't need a
// separate token for the direct endpoints — they live on the same host.
func directBridgeHeaders() http.Header {
	h := http.Header{}
	if id := strings.TrimSpace(os.Getenv("CF_ACCESS_CLIENT_ID")); id != "" {
		h.Set("CF-Access-Client-Id", id)
	}
	if sec := strings.TrimSpace(os.Getenv("CF_ACCESS_CLIENT_SECRET")); sec != "" {
		h.Set("CF-Access-Client-Secret", sec)
	}
	return h
}

// directBridgeHTTPClient is a small package-level http.Client used by
// the canvas direct-fs paths. Tunneled through Cloudflare so we honor
// the same Access policy as the claude_code MCP server. Short timeouts
// because these are cheap calls (ls, cat, git status).
var directBridgeHTTPClient = &http.Client{Timeout: 30 * time.Second}

// directBridgePost POSTs JSON to the bridge at the given path, attaching
// Cloudflare Access headers. Returns (body, status, ok). Used for the
// supervisor endpoints that need to send a JSON body.
func directBridgePost(ctx context.Context, urlPath string, body any) ([]byte, int, bool) {
	base := bridgeBaseURL()
	if base == "" {
		return nil, 0, false
	}
	buf, err := json.Marshal(body)
	if err != nil {
		return nil, 0, false
	}
	req, err := http.NewRequestWithContext(ctx, "POST", base+urlPath, strings.NewReader(string(buf)))
	if err != nil {
		return nil, 0, false
	}
	req.Header.Set("Content-Type", "application/json")
	for k, vs := range directBridgeHeaders() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := directBridgeHTTPClient.Do(req)
	if err != nil {
		return nil, 0, false
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	return respBody, resp.StatusCode, true
}

// directBridgeGet fetches `path` (e.g. "/fs/ls?path=...") from the
// bridge, attaching Cloudflare Access headers. Returns (body, ok).
// ok=false means the bridge endpoint isn't reachable or returned an
// error; callers can fall through to the MCP path.
func directBridgeGet(ctx context.Context, urlPath string) ([]byte, bool) {
	base := bridgeBaseURL()
	if base == "" {
		return nil, false
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+urlPath, nil)
	if err != nil {
		return nil, false
	}
	for k, vs := range directBridgeHeaders() {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := directBridgeHTTPClient.Do(req)
	if err != nil {
		return nil, false
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, false
	}
	return body, true
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

// macBridgeAvailable reports whether the claude_code__Bash tool is registered.
// We use Bash as the canary because every claude_code build exposes it; if
// this is present then LS / Read / Write are too. Used by the FS handlers to
// decide between MCP-first (production: Core on Railway, files on the Mac via
// the tunnel) and local-first (dev: Core on the same box as the files).
func (s *Server) macBridgeAvailable() bool {
	if s.loop == nil || s.loop.Tools() == nil {
		return false
	}
	_, ok := s.loop.Tools().Get("claude_code__Bash")
	return ok
}

// listViaMCP tries to list directory entries via the Mac bridge.
// Strategies in order:
//
//  1. Direct bridge GET /fs/ls — plain Go filesystem read on the Mac.
//     NO MCP. NO Claude Code session spawn. NO per-call subprocess.
//     The bridge is already running on the Mac with full FS access;
//     this is the obvious right answer for browsing files.
//  2. claude_code__LS — older fallback path if the bridge somehow
//     doesn't expose /fs/ls (older bridge build).
//  3. claude_code__Bash with `ls -1Fp` — last-ditch fallback.
//  4. claude_code__Glob — absolute last-ditch.
func (s *Server) listViaMCP(ctx context.Context, dir string) ([]fsEntry, bool) {
	// Strategy 1: direct bridge.
	if body, ok := directBridgeGet(ctx, "/fs/ls?path="+url.QueryEscape(dir)); ok {
		var resp struct {
			Entries []fsEntry `json:"entries"`
		}
		if err := json.Unmarshal(body, &resp); err == nil && len(resp.Entries) > 0 {
			return resp.Entries, true
		}
	}

	// Strategy 1: dedicated LS tool.
	for _, name := range []string{"claude_code__LS", "claude_code__ls", "claude_code__List"} {
		t, terr := s.canvasMCP(name)
		if terr != nil {
			continue
		}
		raw, execErr := t.Execute(ctx, map[string]any{"path": dir})
		if execErr != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		entries := parseLsOutput(raw, dir)
		if len(entries) == 0 {
			continue
		}
		return entries, true
	}

	// Strategy 2: Bash `ls -1Fp`. This is the workhorse — works on every
	// Mac and Linux, output is one name per line with clear type markers.
	if t, err := s.canvasMCP("claude_code__Bash"); err == nil {
		cmd := "ls -1Fp " + shellQuote(dir)
		raw, execErr := t.Execute(ctx, map[string]any{"command": cmd})
		if execErr == nil && strings.TrimSpace(raw) != "" {
			entries := parseLsDashOneFOutput(unwrapBashStdout(raw))
			if len(entries) > 0 {
				return entries, true
			}
		}
	}

	// Strategy 3: Glob the immediate children.
	for _, name := range []string{"claude_code__Glob", "claude_code__glob"} {
		t, terr := s.canvasMCP(name)
		if terr != nil {
			continue
		}
		raw, execErr := t.Execute(ctx, map[string]any{
			"pattern": "*",
			"path":    dir,
		})
		if execErr != nil || strings.TrimSpace(raw) == "" {
			continue
		}
		entries := parseGlobOutput(raw, dir)
		if len(entries) > 0 {
			return entries, true
		}
	}

	return nil, false
}

// unwrapBashStdout extracts the `stdout` string from Claude Code's Bash
// tool output. The tool returns a JSON object like:
//
//	{"stdout":"...","stderr":"","interrupted":false,"isImage":false,...}
//
// embedded in the MCP CallToolResult text content. If the input doesn't
// look like that wrapper (e.g. older Claude Code versions or a totally
// different MCP server), returns the raw string unchanged.
func unwrapBashStdout(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		return raw
	}
	var wrap struct {
		Stdout string `json:"stdout"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrap); err != nil {
		return raw
	}
	if wrap.Stdout == "" {
		return raw
	}
	return wrap.Stdout
}

// unwrapReadContent extracts the file `content` from Claude Code's Read
// tool output:
//
//	{"type":"text","file":{"filePath":"...","content":"..."}}
//
// Returns ("", false) when the response is `{"type":"file_unchanged",...}`
// — Claude Code's per-session read cache returns this when the file was
// already read in the same MCP session, with NO content body. Callers
// should fall back to a fresh read (e.g. `cat` via Bash) in that case.
//
// Falls back to stripReadHeader on raw text if the wrapper isn't a
// recognizable JSON envelope (older Claude Code versions).
func unwrapReadContent(raw string) (string, bool) {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") {
		stripped := stripReadHeader(raw)
		return stripped, stripped != ""
	}
	var wrap struct {
		Type string `json:"type"`
		File struct {
			Content string `json:"content"`
		} `json:"file"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrap); err != nil {
		stripped := stripReadHeader(raw)
		return stripped, stripped != ""
	}
	if wrap.File.Content != "" {
		return wrap.File.Content, true
	}
	// `file_unchanged` envelopes have no content. Signal the caller to
	// fall back to a fresh read path.
	return "", false
}

// parseLsDashOneFOutput parses `ls -1Fp` output: one name per line, with
// trailing "/" for directories and "@" for symlinks. Trailing "*" for
// executables is ignored (we still call it a file).
func parseLsDashOneFOutput(raw string) []fsEntry {
	out := []fsEntry{}
	seen := map[string]struct{}{}
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		ln := strings.TrimRight(line, " \t")
		if ln == "" || strings.HasPrefix(strings.TrimSpace(ln), "NOTE:") {
			continue
		}
		// Strip trailing type markers.
		typ := "file"
		switch {
		case strings.HasSuffix(ln, "/"):
			typ = "dir"
			ln = strings.TrimSuffix(ln, "/")
		case strings.HasSuffix(ln, "@"):
			typ = "symlink"
			ln = strings.TrimSuffix(ln, "@")
		case strings.HasSuffix(ln, "*"):
			ln = strings.TrimSuffix(ln, "*")
		case strings.HasSuffix(ln, "="):
			ln = strings.TrimSuffix(ln, "=")
		}
		name := strings.TrimSpace(ln)
		if name == "" || name == "." || name == ".." {
			continue
		}
		if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, fsEntry{Name: name, Type: typ})
	}
	return out
}

// parseGlobOutput handles Glob's output: one path per line, absolute or
// relative to `dir`. We collapse to direct children only.
func parseGlobOutput(raw, dir string) []fsEntry {
	out := []fsEntry{}
	seen := map[string]struct{}{}
	prefix := strings.TrimRight(dir, "/") + "/"
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		p := strings.TrimSpace(line)
		if p == "" || strings.HasPrefix(p, "#") || strings.HasPrefix(p, "NOTE:") {
			continue
		}
		// Strip the dir prefix if absolute.
		rel := strings.TrimPrefix(p, prefix)
		if rel == "" || rel == p && strings.HasPrefix(p, "/") {
			// Path didn't start with our prefix → not a child of dir.
			continue
		}
		// Only direct children — no '/' inside.
		if idx := strings.Index(rel, "/"); idx >= 0 {
			rel = rel[:idx]
		}
		if rel == "" || rel == "." || rel == ".." {
			continue
		}
		if strings.HasPrefix(rel, ".") && rel != ".gitignore" && rel != ".env.example" {
			continue
		}
		if _, dup := seen[rel]; dup {
			continue
		}
		seen[rel] = struct{}{}
		// We can't tell dir vs file from Glob output alone. Mark as file;
		// the studio will still render and the user can click in.
		out = append(out, fsEntry{Name: rel, Type: "file"})
	}
	return out
}

// readViaMCP tries claude_code__Read under each registered name spelling.
// Returns (content, true) on a non-empty read.
func (s *Server) readViaMCP(ctx context.Context, path string) (string, bool) {
	// Strategy 1: direct bridge /fs/read. Reads the file off the Mac
	// filesystem via Go's os.ReadFile — no MCP, no Claude Code, no
	// per-session read cache shenanigans.
	if body, ok := directBridgeGet(ctx, "/fs/read?path="+url.QueryEscape(path)); ok {
		var resp struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal(body, &resp); err == nil && resp.Content != "" {
			return resp.Content, true
		}
	}

	for _, name := range []string{"claude_code__Read", "claude_code__read"} {
		t, terr := s.canvasMCP(name)
		if terr != nil {
			continue
		}
		out, execErr := t.Execute(ctx, map[string]any{"file_path": path})
		if execErr != nil || out == "" {
			continue
		}
		content, ok := unwrapReadContent(out)
		if ok {
			return content, true
		}
		// Read returned `file_unchanged` with no body — Claude Code's
		// per-session cache. Fall through to the Bash cat path below.
	}
	// Last-ditch: cat the file via Bash. Always returns fresh bytes,
	// never hits Read's cache. Useful when the agent has already read
	// the file in this MCP session and the Read tool short-circuits.
	if t, err := s.canvasMCP("claude_code__Bash"); err == nil {
		cmd := "cat " + shellQuote(path)
		raw, execErr := t.Execute(ctx, map[string]any{"command": cmd})
		if execErr == nil {
			content := unwrapBashStdout(raw)
			if content != "" {
				return content, true
			}
		}
	}
	return "", false
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

	out := fsListResponse{Path: resolved, Root: canvasRoot(), Entries: []fsEntry{}}

	// MCP-first when the Mac bridge is connected. The bridge is the boss's
	// actual workspace; the local filesystem here is whatever the Core
	// container has mounted (on Railway, that's a distroless rootfs that
	// doesn't contain `/Users/...`). If we tried local first, an unset
	// INFINITY_CANVAS_ROOT would fall back to `os.UserHomeDir() ==
	// /home/nonroot`, succeed listing nothing, and we'd never reach MCP.
	if s.macBridgeAvailable() {
		if entries, ok := s.listViaMCP(r.Context(), resolved); ok {
			out.Entries = entries
			sortEntries(out.Entries)
			writeJSON(w, http.StatusOK, out)
			return
		}
	}

	// Local FS — used when Core runs on the same machine as the boss
	// (local dev on the Mac) or as a fallback when the bridge is connected
	// but didn't return useful output for this path.
	if entries, lerr := localListDir(resolved); lerr == nil {
		out.Entries = entries
		sortEntries(out.Entries)
		writeJSON(w, http.StatusOK, out)
		return
	}

	// Last-chance MCP attempt for the bridge-not-yet-detected case (e.g.
	// the loop wired tools after the first request landed).
	if !s.macBridgeAvailable() {
		if entries, ok := s.listViaMCP(r.Context(), resolved); ok {
			out.Entries = entries
			sortEntries(out.Entries)
			writeJSON(w, http.StatusOK, out)
			return
		}
	}

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

// parseLsOutput parses Claude Code's LS output, which is an indented-bullet
// tree:
//
//	- /Users/n0m4d/Dev/
//	  - infinity/
//	    - core/
//	  - other-project/
//	  - notes.md
//
// We extract only the direct children of `dir` (depth-1 entries — bullets
// indented 2 spaces past the root line). Trailing "/" marks a directory.
//
// Also tolerates the older tab-separated `name<TAB>size<TAB>type` shape and
// raw `ls -1` output where each line is just a name.
func parseLsOutput(raw, dir string) []fsEntry {
	out := []fsEntry{}
	seen := map[string]struct{}{}
	rootIndent := -1
	for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
		rstripped := strings.TrimRight(line, " \t")
		if rstripped == "" {
			continue
		}
		trimmed := strings.TrimSpace(rstripped)
		// Notes / annotations Claude Code occasionally prepends.
		if strings.HasPrefix(trimmed, "NOTE:") || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Bullet form: "  - name/" — count leading spaces, strip "- ".
		if strings.HasPrefix(trimmed, "- ") {
			indent := len(rstripped) - len(strings.TrimLeft(rstripped, " "))
			content := strings.TrimSpace(strings.TrimPrefix(trimmed, "- "))
			if rootIndent < 0 {
				// First bullet is the root being listed — store its
				// indent so we can pick direct children one level
				// deeper.
				rootIndent = indent
				continue
			}
			// Direct children are exactly 2 spaces deeper than the
			// root bullet. Anything deeper is a grandchild Claude
			// Code surfaced — skip it; the studio lazy-loads
			// subdirectories on expand.
			if indent != rootIndent+2 {
				continue
			}
			isDir := strings.HasSuffix(content, "/")
			name := strings.TrimSuffix(content, "/")
			name = strings.TrimPrefix(name, dir+string(filepath.Separator))
			if name == "" || name == "." || name == ".." {
				continue
			}
			if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			e := fsEntry{Name: name, Type: "file"}
			if isDir {
				e.Type = "dir"
			}
			out = append(out, e)
			continue
		}

		// Tab-separated form.
		if strings.Contains(rstripped, "\t") {
			fields := strings.Split(rstripped, "\t")
			name := strings.TrimSpace(fields[0])
			name = strings.TrimPrefix(name, dir+string(filepath.Separator))
			isDir := strings.HasSuffix(name, "/")
			name = strings.TrimSuffix(name, "/")
			if name == "" || name == "." || name == ".." {
				continue
			}
			if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
				continue
			}
			if _, dup := seen[name]; dup {
				continue
			}
			seen[name] = struct{}{}
			e := fsEntry{Name: name, Type: "file"}
			if isDir {
				e.Type = "dir"
			}
			out = append(out, e)
			continue
		}

		// Plain `ls -1` form: one name per line.
		name := strings.TrimSpace(rstripped)
		name = strings.TrimPrefix(name, dir+string(filepath.Separator))
		isDir := strings.HasSuffix(name, "/")
		name = strings.TrimSuffix(name, "/")
		if name == "" || name == "." || name == ".." {
			continue
		}
		if strings.HasPrefix(name, ".") && name != ".gitignore" && name != ".env.example" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		e := fsEntry{Name: name, Type: "file"}
		if isDir {
			e.Type = "dir"
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

	// MCP-first when the Mac bridge is connected (production). Same reasoning
	// as handleCanvasFSList: the bridge points at the boss's real files; the
	// container filesystem doesn't.
	if s.macBridgeAvailable() {
		if content, ok := s.readViaMCP(r.Context(), resolved); ok {
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

	// Local read — used when Core runs on the same box as the files, or as
	// a fallback when the bridge is connected but the read came back empty.
	if data, lerr := os.ReadFile(resolved); lerr == nil {
		writeJSON(w, http.StatusOK, fsReadResponse{
			Path:     resolved,
			Content:  string(data),
			Language: detectLanguage(resolved),
			SHA:      sha256Hex(string(data)),
			Size:     int64(len(data)),
		})
		return
	}

	if !s.macBridgeAvailable() {
		if content, ok := s.readViaMCP(r.Context(), resolved); ok {
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

	writeJSON(w, http.StatusNotFound, map[string]string{"error": "file not readable"})
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
		// Monaco distinguishes JSX-aware vs plain TS. Returning "typescript"
		// here made Monaco's TS service parse `<Component>` as a `<` comparison
		// operator, producing fake squiggles like "Operator '<' cannot be
		// applied to types 'boolean' and 'RegExp'" on every JSX tag.
		return "typescriptreact"
	case ".js", ".mjs", ".cjs":
		return "javascript"
	case ".jsx":
		return "javascriptreact"
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
	// Try the direct bridge first — runs `git status` natively on the
	// Mac, no MCP detour.
	if body, dok := directBridgeGet(r.Context(), "/git/status?repo="+url.QueryEscape(repo)); dok {
		writeJSON(w, http.StatusOK, parseGitStatusV2(string(body), repo))
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

	// Direct bridge first.
	dq := url.Values{}
	dq.Set("repo", repo)
	if staged {
		dq.Set("staged", "1")
	}
	if path != "" {
		dq.Set("path", path)
	}
	if body, dok := directBridgeGet(r.Context(), "/git/diff?"+dq.Encode()); dok {
		writeJSON(w, http.StatusOK, gitDiffResponse{Path: path, Staged: staged, Diff: string(body)})
		return
	}

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

type gitShowResponse struct {
	Path    string `json:"path"`
	Ref     string `json:"ref"`
	Content string `json:"content"`
	// Found = false when the path exists on disk but isn't tracked at the
	// requested ref (e.g. a brand-new file Jarvis just added that hasn't
	// been committed yet). The client treats this as "original side of
	// the diff is empty" — the whole file shows as additions.
	Found bool `json:"found"`
}

// handleCanvasGitShow returns the contents of a file at a specific git ref
// (defaults to HEAD). Powers the diff view's "original" side when the boss
// opens a file Jarvis edited — Monaco's DiffEditor needs the pre-edit
// version to render real diff hunks, but the on-disk content already has
// Jarvis's changes applied so the FS read alone produces an empty diff.
//
// Repo root is auto-discovered by walking up from the file looking for a
// .git entry; the boss can have multiple project repos under the canvas
// root and we shouldn't make the frontend track which one any given path
// belongs to.
func (s *Server) handleCanvasGitShow(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	resolved, ok := resolveCanvasPath(path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes INFINITY_CANVAS_ROOT"})
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if ref == "" {
		ref = "HEAD"
	}
	// Refs we accept are simple identifiers: HEAD, branch names, short or
	// full SHAs. Reject anything with shell metacharacters so a hostile
	// query can't smuggle a command through the bash gate's
	// `git show <ref>:<path>` construction.
	for _, bad := range []string{";", "&&", "||", "|", "`", "$(", ">", "<", " ", "\n", "\r"} {
		if strings.Contains(ref, bad) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid ref"})
			return
		}
	}

	repoRoot, relPath, found := findRepoRoot(resolved)
	if !found {
		writeJSON(w, http.StatusOK, gitShowResponse{Path: path, Ref: ref, Content: "", Found: false})
		return
	}
	cmd := "git -C " + shellQuote(repoRoot) + " show " + shellQuote(ref+":"+relPath)
	out, err := s.runReadOnlyBash(r.Context(), cmd)
	if err != nil {
		// File isn't tracked at this ref (new file Jarvis just added,
		// rename, etc.) — return empty content so the diff shows the
		// whole file as additions rather than failing the request.
		writeJSON(w, http.StatusOK, gitShowResponse{Path: path, Ref: ref, Content: "", Found: false})
		return
	}
	writeJSON(w, http.StatusOK, gitShowResponse{Path: path, Ref: ref, Content: out, Found: true})
}

// findRepoRoot walks up from absPath until it finds a directory containing
// .git (file or directory — submodules use a .git file pointing elsewhere).
// Returns the repo root, the path relative to that root, and ok=true when
// found. Stops at filesystem root or the canvas-root boundary.
func findRepoRoot(absPath string) (root string, relPath string, ok bool) {
	dir := absPath
	if fi, err := os.Stat(absPath); err == nil && !fi.IsDir() {
		dir = filepath.Dir(absPath)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			rel, err := filepath.Rel(dir, absPath)
			if err != nil {
				return "", "", false
			}
			return dir, filepath.ToSlash(rel), true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false
		}
		dir = parent
	}
}

// runReadOnlyBash invokes claude_code__Bash for a vetted read-only command.
// The gate's isReadOnlyGit allow-list lets these pass without Trust queueing.
// Returns the raw stdout — unwraps Claude Code's {"stdout":...} JSON envelope.
func (s *Server) runReadOnlyBash(ctx context.Context, cmd string) (string, error) {
	t, err := s.canvasMCP("claude_code__Bash")
	if err != nil {
		// Local-dev fallback: shell out directly. Same command, runs under
		// the core process. Only meaningful when the boss runs core on
		// their workspace machine.
		return localShell(ctx, cmd)
	}
	raw, execErr := t.Execute(ctx, map[string]any{"command": cmd})
	if execErr != nil {
		return raw, execErr
	}
	return unwrapBashStdout(raw), nil
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
	// Unwrap Claude Code's {"stdout":"...","stderr":"...",...} envelope so
	// the studio toast shows just the command output, not the JSON.
	clean := unwrapBashStdout(out)
	if execErr != nil {
		writeJSON(w, http.StatusOK, gitMutationResponse{
			ContractID: id,
			Status:     "denied",
			Reason:     execErr.Error(),
			Output:     clean,
		})
		return
	}
	writeJSON(w, http.StatusOK, gitMutationResponse{
		ContractID: id,
		Status:     "executed",
		Output:     clean,
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
	Root               string `json:"root"`
	RootIsSet          bool   `json:"root_is_set"`
	PreviewURL         string `json:"preview_url,omitempty"`
	MacBridgeOK        bool   `json:"mac_bridge_ok"`
	DefaultProjectPath string `json:"default_project_path,omitempty"`
}

func (s *Server) handleCanvasConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, canvasConfigResponse{
		Root:               canvasRoot(),
		RootIsSet:          strings.TrimSpace(os.Getenv("INFINITY_CANVAS_ROOT")) != "",
		PreviewURL:         strings.TrimSpace(os.Getenv("INFINITY_CANVAS_PREVIEW_URL")),
		MacBridgeOK:        s.macBridgeAvailable(),
		DefaultProjectPath: canvasDefaultProjectPath(),
	})
}

// handleCanvasDebug is a diagnostic endpoint. Returns:
//   - every claude_code__* tool registered (the actual exact name)
//   - for the requested path, the raw output and entry count from each
//     listing strategy (LS, Bash ls -1Fp, Glob)
//
// Use it from the browser:
//
//	fetch('/api/canvas/debug?path=/Users/you/Dev').then(r=>r.json()).then(console.log)
//
// Cheap to call, no Trust queue side effects, no secrets — just the
// names and a small slice of raw output so we can see what Claude Code
// is actually returning.
func (s *Server) handleCanvasDebug(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	resolved, ok := resolveCanvasPath(path)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path escapes INFINITY_CANVAS_ROOT"})
		return
	}

	out := map[string]any{
		"resolved":      resolved,
		"root":          canvasRoot(),
		"bridge_ok":     s.macBridgeAvailable(),
		"tools_present": []string{},
		"strategies":    map[string]any{},
	}

	// 1. Which claude_code__* tool names are actually registered?
	tools := []string{}
	if s.loop != nil && s.loop.Tools() != nil {
		for _, n := range s.loop.Tools().Names() {
			if strings.HasPrefix(n, "claude_code__") {
				tools = append(tools, n)
			}
		}
	}
	out["tools_present"] = tools

	strategies := map[string]any{}

	// 2. Strategy: LS tool, every spelling we attempt.
	for _, name := range []string{"claude_code__LS", "claude_code__ls", "claude_code__List"} {
		entry := map[string]any{"tool": name, "registered": false}
		if t, terr := s.canvasMCP(name); terr == nil {
			entry["registered"] = true
			raw, execErr := t.Execute(r.Context(), map[string]any{"path": resolved})
			entry["raw_head"] = head(raw, 1500)
			if execErr != nil {
				entry["error"] = execErr.Error()
			}
			entry["entries"] = len(parseLsOutput(raw, resolved))
		}
		strategies[name] = entry
	}

	// 3. Strategy: Bash `ls -1Fp <dir>`.
	bashEntry := map[string]any{"tool": "claude_code__Bash", "registered": false}
	if t, terr := s.canvasMCP("claude_code__Bash"); terr == nil {
		bashEntry["registered"] = true
		cmd := "ls -1Fp " + shellQuote(resolved)
		bashEntry["command"] = cmd
		raw, execErr := t.Execute(r.Context(), map[string]any{"command": cmd})
		bashEntry["raw_head"] = head(raw, 1500)
		if execErr != nil {
			bashEntry["error"] = execErr.Error()
		}
		bashEntry["entries"] = len(parseLsDashOneFOutput(raw))
	}
	strategies["bash_ls_1Fp"] = bashEntry

	// 3b. Probe — bash with the smallest possible command. If THIS fails
	//     too, the bridge is broken for all bash calls, not just ls
	//     (i.e. the issue is on the Mac, not in our command).
	probeEntry := map[string]any{"tool": "claude_code__Bash", "registered": false}
	if t, terr := s.canvasMCP("claude_code__Bash"); terr == nil {
		probeEntry["registered"] = true
		probeEntry["command"] = "echo hi"
		raw, execErr := t.Execute(r.Context(), map[string]any{"command": "echo hi"})
		probeEntry["raw"] = raw
		if execErr != nil {
			probeEntry["error"] = execErr.Error()
		}
	}
	strategies["bash_echo_probe"] = probeEntry

	// 3c. Read probe — try claude_code__Read on a known file. If Read
	//     works while Bash doesn't, the bridge has a specific Bash
	//     issue (probably a tool-handler crash on the Mac).
	readEntry := map[string]any{"tool": "claude_code__Read", "registered": false}
	if t, terr := s.canvasMCP("claude_code__Read"); terr == nil {
		readEntry["registered"] = true
		// Read CLAUDE.md if it exists under the workspace root; gives a
		// small, predictable file to probe against.
		probePath := filepath.Join(canvasRoot(), "infinity", "CLAUDE.md")
		readEntry["probe_path"] = probePath
		raw, execErr := t.Execute(r.Context(), map[string]any{"file_path": probePath})
		readEntry["raw_head"] = head(raw, 600)
		if execErr != nil {
			readEntry["error"] = execErr.Error()
		}
	}
	strategies["read_probe"] = readEntry

	// 4. Strategy: Glob.
	for _, name := range []string{"claude_code__Glob", "claude_code__glob"} {
		entry := map[string]any{"tool": name, "registered": false}
		if t, terr := s.canvasMCP(name); terr == nil {
			entry["registered"] = true
			raw, execErr := t.Execute(r.Context(), map[string]any{
				"pattern": "*",
				"path":    resolved,
			})
			entry["raw_head"] = head(raw, 1500)
			if execErr != nil {
				entry["error"] = execErr.Error()
			}
			entry["entries"] = len(parseGlobOutput(raw, resolved))
		}
		strategies[name] = entry
	}

	out["strategies"] = strategies
	writeJSON(w, http.StatusOK, out)
}

func head(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…(truncated)"
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

// ---- Project lifecycle (bridge supervisor) --------------------------------
//
// These thin handlers proxy to the bridge's /supervisor/* endpoints so
// Studio only ever talks to Core. Core attaches Cloudflare Access headers
// the same way it does for /fs/ls and /git/status — service tokens never
// touch the browser.

// handleCanvasProjectStart POSTs to the bridge supervisor to bring a
// project's dev server up. Body shape:
//
//	{"project_path": "/abs/path", "template": "nextjs", "activate": true}
//
// Returns the bridge's project record (status + port).
func (s *Server) handleCanvasProjectStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	respBody, status, ok := directBridgePost(r.Context(), "/supervisor/start", body)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge unreachable"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

func (s *Server) handleCanvasProjectStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
		return
	}
	respBody, status, ok := directBridgePost(r.Context(), "/supervisor/stop", body)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge unreachable"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(respBody)
}

// handleCanvasProjectActive serves both GET (current active project) and
// POST (set active). On POST it also writes mark_run + last_run_at on
// mem_sessions so the Studio sidebar shows the running project.
func (s *Server) handleCanvasProjectActive(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		respBody, ok := directBridgeGet(r.Context(), "/supervisor/active")
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge unreachable"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(respBody)
	case http.MethodPost:
		var body struct {
			ProjectPath string `json:"project_path"`
			Template    string `json:"template"`
			SessionID   string `json:"session_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		respBody, status, ok := directBridgePost(r.Context(), "/supervisor/active", map[string]any{
			"project_path": body.ProjectPath,
			"template":     body.Template,
		})
		if !ok {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge unreachable"})
			return
		}
		// Best-effort: stamp last_run_at on the session row when we have one.
		if s.pool != nil && body.SessionID != "" {
			_, _ = s.pool.Exec(r.Context(),
				`UPDATE mem_sessions SET last_run_at = NOW() WHERE id = $1::uuid`,
				body.SessionID)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(respBody)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCanvasProjectStatus(w http.ResponseWriter, r *http.Request) {
	q := r.URL.RawQuery
	urlPath := "/supervisor/status"
	if q != "" {
		urlPath += "?" + q
	}
	respBody, ok := directBridgeGet(r.Context(), urlPath)
	if !ok {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "bridge unreachable"})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(respBody)
}
