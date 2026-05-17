// bridge_tools.go - generic filesystem / bash / git primitives that
// route through the Bridge Router to whichever bridge (Mac or Cloud)
// is active for the current session.
//
// These coexist with the existing claude_code__* tools registered via
// MCP. The claude_code__* tools only work when the Mac bridge is up
// (they hit Claude Code's MCP server). The bridge_* tools work for
// EITHER bridge - Mac or Cloud - so Jarvis can keep working when the
// Mac is offline without dropping into a "set workspace root first"
// state.
//
// When the Mac bridge is the active route, both toolsets are usable
// and Jarvis's system prompt overlay should nudge him toward
// claude_code__* for heavy edits (Max-billed sub-agent muscle) and
// the generic bridge_* tools for primitives where a sub-agent loop
// would be wasted (single-file writes, deterministic git commands).
//
// When the Cloud bridge is active, ONLY bridge_* tools work. Jarvis
// is the only brain; primitives are all he needs.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/jackc/pgx/v5/pgxpool"
)

// PreferenceFetcher resolves a session's bridge preference. We accept
// an interface so tests can plug a stub without a Postgres pool. In
// production it's just a SELECT on mem_sessions.
type PreferenceFetcher func(ctx context.Context, sessionID string) bridge.Preference

// NewDBPreferenceFetcher wires the canonical Postgres lookup. Missing
// session_id or missing row → default 'auto'.
func NewDBPreferenceFetcher(pool *pgxpool.Pool) PreferenceFetcher {
	return func(ctx context.Context, sessionID string) bridge.Preference {
		if sessionID == "" || pool == nil {
			return bridge.PrefAuto
		}
		var p string
		err := pool.QueryRow(ctx,
			`SELECT bridge_preference FROM mem_sessions WHERE id::text = $1`,
			sessionID,
		).Scan(&p)
		if err != nil || p == "" {
			return bridge.PrefAuto
		}
		switch bridge.Preference(p) {
		case bridge.PrefMac, bridge.PrefCloud, bridge.PrefAuto:
			return bridge.Preference(p)
		}
		return bridge.PrefAuto
	}
}

// RegisterBridgeTools registers all the bridge_* primitives on the
// registry. They share the same router and preference-fetcher.
func RegisterBridgeTools(r *Registry, router *bridge.Router, prefs PreferenceFetcher) {
	r.Register(&bridgeFSRead{router: router, prefs: prefs})
	r.Register(&bridgeFSLS{router: router, prefs: prefs})
	r.Register(&bridgeFSSave{router: router, prefs: prefs})
	r.Register(&bridgeFSEdit{router: router, prefs: prefs})
	r.Register(&bridgeBash{router: router, prefs: prefs})
	r.Register(&bridgeGitStatus{router: router, prefs: prefs})
	r.Register(&bridgeGitDiff{router: router, prefs: prefs})
	r.Register(&bridgeGitStage{router: router, prefs: prefs})
	r.Register(&bridgeGitCommit{router: router, prefs: prefs})
	r.Register(&bridgeGitPush{router: router, prefs: prefs})
	r.Register(&bridgeGitPull{router: router, prefs: prefs})
}

// pickBridge resolves the active bridge for the current session. All
// bridge_* tools call this first; failure → error string the agent
// can read and decide what to do (often: "ask the boss to bring the
// Mac online or pin the session to cloud").
func pickBridge(ctx context.Context, router *bridge.Router, prefs PreferenceFetcher) (bridge.Bridge, string, error) {
	if router == nil {
		return nil, "", errors.New("bridge router not configured")
	}
	sid := SessionIDFromContext(ctx)
	pref := bridge.PrefAuto
	if prefs != nil {
		pref = prefs(ctx, sid)
	}
	return router.For(ctx, pref)
}

// formatBridgeResult attaches a short prefix telling Jarvis which
// bridge served the call. Helps when a session-pinned call fails -
// the prefix makes the source obvious.
func formatBridgeResult(b bridge.Bridge, body []byte) string {
	if b == nil {
		return string(body)
	}
	return fmt.Sprintf("[bridge=%s] %s", b.Name(), string(body))
}

// ── fs_read ──────────────────────────────────────────────────────────────

type bridgeFSRead struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeFSRead) Name() string     { return "fs_read" }
func (t *bridgeFSRead) ReadOnly() bool   { return true }
func (t *bridgeFSRead) Description() string {
	return "Read a file from the active bridge's filesystem (Mac or Cloud). " +
		"Optionally pass start/end (1-indexed line range) to read only a window - preferred for large files to keep context tight."
}
func (t *bridgeFSRead) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":  map[string]any{"type": "string", "description": "Absolute or workspace-relative path."},
			"start": map[string]any{"type": "integer", "description": "Optional 1-indexed start line."},
			"end":   map[string]any{"type": "integer", "description": "Optional 1-indexed end line."},
		},
		"required": []string{"path"},
	}
}
func (t *bridgeFSRead) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("fs_read: %s", why)
	}
	path := strString(in, "path")
	q := "/fs/read?path=" + urlEscape(path)
	if v := intOrZero(in, "start"); v > 0 {
		q += fmt.Sprintf("&start=%d", v)
	}
	if v := intOrZero(in, "end"); v > 0 {
		q += fmt.Sprintf("&end=%d", v)
	}
	body, status, ok := b.Get(ctx, q)
	if !ok || status >= 300 {
		return "", fmt.Errorf("fs_read via %s failed (status=%d)", b.Name(), status)
	}
	return formatBridgeResult(b, body), nil
}

// ── fs_ls ────────────────────────────────────────────────────────────────

type bridgeFSLS struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeFSLS) Name() string     { return "fs_ls" }
func (t *bridgeFSLS) ReadOnly() bool   { return true }
func (t *bridgeFSLS) Description() string {
	return "List a directory on the active bridge's filesystem. Returns file/dir entries with sizes."
}
func (t *bridgeFSLS) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
	}
}
func (t *bridgeFSLS) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("fs_ls: %s", why)
	}
	path := strString(in, "path")
	body, status, ok := b.Get(ctx, "/fs/ls?path="+urlEscape(path))
	if !ok || status >= 300 {
		return "", fmt.Errorf("fs_ls via %s failed (status=%d)", b.Name(), status)
	}
	return formatBridgeResult(b, body), nil
}

// ── fs_save ──────────────────────────────────────────────────────────────

type bridgeFSSave struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeFSSave) Name() string { return "fs_save" }
func (t *bridgeFSSave) Description() string {
	return "Overwrite a file at the given path with `content` on the active bridge's filesystem. " +
		"Use fs_edit for surgical changes - this clobbers the whole file."
}
func (t *bridgeFSSave) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":    map[string]any{"type": "string"},
			"content": map[string]any{"type": "string"},
		},
		"required": []string{"path", "content"},
	}
}
func (t *bridgeFSSave) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("fs_save: %s", why)
	}
	body, status, ok := b.Post(ctx, "/fs/save", map[string]any{
		"path":    strString(in, "path"),
		"content": strString(in, "content"),
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("fs_save via %s failed (status=%d): %s", b.Name(), status, string(body))
	}
	return formatBridgeResult(b, body), nil
}

// ── fs_edit ──────────────────────────────────────────────────────────────

type bridgeFSEdit struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeFSEdit) Name() string { return "fs_edit" }
func (t *bridgeFSEdit) Description() string {
	return "Replace `old_string` with `new_string` in a file. Strict: old_string must " +
		"appear exactly once unless replace_all=true. Use this for precise edits - it " +
		"avoids resending the whole file and surfaces the exact replacement count."
}
func (t *bridgeFSEdit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":        map[string]any{"type": "string"},
			"old_string":  map[string]any{"type": "string"},
			"new_string":  map[string]any{"type": "string"},
			"replace_all": map[string]any{"type": "boolean", "default": false},
		},
		"required": []string{"path", "old_string", "new_string"},
	}
}
func (t *bridgeFSEdit) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("fs_edit: %s", why)
	}
	body, status, ok := b.Post(ctx, "/fs/edit", map[string]any{
		"path":        strString(in, "path"),
		"old_string":  strString(in, "old_string"),
		"new_string":  strString(in, "new_string"),
		"replace_all": boolOrFalse(in, "replace_all"),
	})
	if !ok {
		return "", fmt.Errorf("fs_edit via %s unreachable", b.Name())
	}
	if status >= 300 {
		// Surface the bridge's exact error message - Jarvis reads it.
		var msg struct{ Error string `json:"error"` }
		_ = json.Unmarshal(body, &msg)
		if msg.Error != "" {
			return "", fmt.Errorf("fs_edit via %s: %s", b.Name(), msg.Error)
		}
		return "", fmt.Errorf("fs_edit via %s failed (status=%d)", b.Name(), status)
	}
	return formatBridgeResult(b, body), nil
}

// ── bash_run ─────────────────────────────────────────────────────────────

type bridgeBash struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeBash) Name() string { return "bash_run" }
func (t *bridgeBash) Description() string {
	return "Run a bash command on the active bridge. Output is truncated past 64KB " +
		"and wall-time limited to 5 minutes. cwd is the workspace root unless specified."
}
func (t *bridgeBash) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"cmd":         map[string]any{"type": "string"},
			"cwd":         map[string]any{"type": "string"},
			"timeout_sec": map[string]any{"type": "integer"},
		},
		"required": []string{"cmd"},
	}
}
func (t *bridgeBash) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("bash_run: %s", why)
	}
	body, status, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         strString(in, "cmd"),
		"cwd":         strString(in, "cwd"),
		"timeout_sec": intOrZero(in, "timeout_sec"),
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("bash_run via %s failed (status=%d): %s", b.Name(), status, string(body))
	}
	return formatBridgeResult(b, body), nil
}

// ── git_* ────────────────────────────────────────────────────────────────

type bridgeGitStatus struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeGitStatus) Name() string   { return "git_status" }
func (t *bridgeGitStatus) ReadOnly() bool { return true }
func (t *bridgeGitStatus) Description() string {
	return "git status --porcelain=v2 --branch on the active bridge's working tree."
}
func (t *bridgeGitStatus) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo": map[string]any{"type": "string", "description": "Defaults to workspace root."},
		},
	}
}
func (t *bridgeGitStatus) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("git_status: %s", why)
	}
	body, status, ok := b.Get(ctx, "/git/status?repo="+urlEscape(strString(in, "repo")))
	if !ok || status >= 300 {
		return "", fmt.Errorf("git_status via %s failed (status=%d)", b.Name(), status)
	}
	return formatBridgeResult(b, body), nil
}

type bridgeGitDiff struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeGitDiff) Name() string   { return "git_diff" }
func (t *bridgeGitDiff) ReadOnly() bool { return true }
func (t *bridgeGitDiff) Description() string {
	return "git diff (or --staged) on the active bridge. Pass `staged=true` for index diff."
}
func (t *bridgeGitDiff) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo":   map[string]any{"type": "string"},
			"path":   map[string]any{"type": "string"},
			"staged": map[string]any{"type": "boolean"},
		},
	}
}
func (t *bridgeGitDiff) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("git_diff: %s", why)
	}
	q := "/git/diff?repo=" + urlEscape(strString(in, "repo"))
	if p := strString(in, "path"); p != "" {
		q += "&path=" + urlEscape(p)
	}
	if boolOrFalse(in, "staged") {
		q += "&staged=1"
	}
	body, status, ok := b.Get(ctx, q)
	if !ok || status >= 300 {
		return "", fmt.Errorf("git_diff via %s failed (status=%d)", b.Name(), status)
	}
	return formatBridgeResult(b, body), nil
}

type bridgeGitStage struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeGitStage) Name() string { return "git_stage" }
func (t *bridgeGitStage) Description() string {
	return "git add - stages files for commit. Pass `files: []` (empty) to stage all (-A) or a list of paths."
}
func (t *bridgeGitStage) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo":  map[string]any{"type": "string"},
			"files": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
	}
}
func (t *bridgeGitStage) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("git_stage: %s", why)
	}
	files := []string{}
	if arr, ok := in["files"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" {
				files = append(files, s)
			}
		}
	}
	body, status, ok := b.Post(ctx, "/git/stage", map[string]any{
		"repo":  strString(in, "repo"),
		"files": files,
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("git_stage via %s failed (status=%d): %s", b.Name(), status, string(body))
	}
	return formatBridgeResult(b, body), nil
}

type bridgeGitCommit struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeGitCommit) Name() string { return "git_commit" }
func (t *bridgeGitCommit) Description() string {
	return "git commit -m <message> on the active bridge. Commits use the bridge's configured " +
		"identity: Mac = the boss's git config, Cloud = 'Jarvis Cloud <jarvis@dopesoft.io>'."
}
func (t *bridgeGitCommit) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo":    map[string]any{"type": "string"},
			"message": map[string]any{"type": "string"},
		},
		"required": []string{"message"},
	}
}
func (t *bridgeGitCommit) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("git_commit: %s", why)
	}
	// Per-session branching: when commits land on the Cloud bridge,
	// auto-route them onto a session-named branch so Jarvis's work
	// is attributable + revertable without polluting main. Mac
	// commits use whatever branch the boss has checked out - he's
	// the human in that loop.
	if b.Name() == bridge.KindCloud {
		ensureSessionBranch(ctx, b, strString(in, "repo"), SessionIDFromContext(ctx))
	}
	body, status, ok := b.Post(ctx, "/git/commit", map[string]any{
		"repo":    strString(in, "repo"),
		"message": strString(in, "message"),
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("git_commit via %s failed (status=%d): %s", b.Name(), status, string(body))
	}
	return formatBridgeResult(b, body), nil
}

// ensureSessionBranch makes sure the cloud bridge's working tree is
// checked out on `jarvis/session-<shortid>` before the next commit.
// Idempotent: if the branch already exists and is current, no-op.
//
// We do this via /bash so we don't have to bake branching primitives
// into the workspace service. Cheap (<50ms) and runs once per session-
// commit cycle.
func ensureSessionBranch(ctx context.Context, b bridge.Bridge, repo, sessionID string) {
	if sessionID == "" {
		return
	}
	short := sessionID
	if len(short) > 8 {
		short = short[:8]
	}
	branch := "jarvis/session-" + short
	cmd := "git rev-parse --abbrev-ref HEAD"
	repoPath := repo
	if repoPath == "" {
		repoPath = "."
	}
	// Probe current branch.
	probe, status, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         cmd,
		"cwd":         repoPath,
		"timeout_sec": 5,
	})
	if !ok || status >= 300 {
		return // best-effort; let the commit fail noisily if the tree is bad
	}
	if extractJSONFieldFast(string(probe), "output") != "" {
		current := extractJSONFieldFast(string(probe), "output")
		// Trim trailing newline.
		for strings.HasSuffix(current, "\n") || strings.HasSuffix(current, " ") {
			current = current[:len(current)-1]
		}
		if current == branch {
			return // already on it
		}
	}
	// Create or switch. `git switch -c <branch> 2>/dev/null || git switch <branch>`
	// - first form succeeds on first call, second on subsequent calls.
	switchCmd := fmt.Sprintf(
		"git switch -c %s 2>/dev/null || git switch %s",
		shellQuote(branch), shellQuote(branch),
	)
	_, _, _ = b.Post(ctx, "/bash", map[string]any{
		"cmd":         switchCmd,
		"cwd":         repoPath,
		"timeout_sec": 5,
	})
}

func shellQuote(s string) string {
	// Single-quote for bash, escaping any embedded single-quotes via the
	// classic '"'"' dance.
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// extractJSONFieldFast is a single-key string extractor - duplicated
// from the server package's helper because importing it would create
// a cycle (server imports tools). Tiny enough that copy is fine.
func extractJSONFieldFast(raw, key string) string {
	idx := strings.Index(raw, "\""+key+"\"")
	if idx < 0 {
		return ""
	}
	colon := strings.Index(raw[idx:], ":")
	if colon < 0 {
		return ""
	}
	rest := raw[idx+colon+1:]
	rest = strings.TrimLeft(rest, " \t\n\r")
	if !strings.HasPrefix(rest, "\"") {
		return ""
	}
	rest = rest[1:]
	end := strings.Index(rest, "\"")
	if end < 0 {
		return ""
	}
	return rest[:end]
}

type bridgeGitPush struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeGitPush) Name() string { return "git_push" }
func (t *bridgeGitPush) Description() string {
	return "git push on the active bridge. Defaults to origin/current-branch. The Cloud " +
		"bridge has GITHUB_TOKEN wired into its credential helper so this just works."
}
func (t *bridgeGitPush) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo":   map[string]any{"type": "string"},
			"remote": map[string]any{"type": "string"},
			"branch": map[string]any{"type": "string"},
		},
	}
}
func (t *bridgeGitPush) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("git_push: %s", why)
	}
	body, status, ok := b.Post(ctx, "/git/push", map[string]any{
		"repo":   strString(in, "repo"),
		"remote": strString(in, "remote"),
		"branch": strString(in, "branch"),
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("git_push via %s failed (status=%d): %s", b.Name(), status, string(body))
	}
	return formatBridgeResult(b, body), nil
}

type bridgeGitPull struct {
	router *bridge.Router
	prefs  PreferenceFetcher
}

func (t *bridgeGitPull) Name() string { return "git_pull" }
func (t *bridgeGitPull) Description() string {
	return "git pull --ff-only on the active bridge. Refuses to merge - if there's drift, " +
		"the boss resolves manually. This is the canonical 'pull deploy changes' tool."
}
func (t *bridgeGitPull) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo":   map[string]any{"type": "string"},
			"remote": map[string]any{"type": "string"},
			"branch": map[string]any{"type": "string"},
		},
	}
}
func (t *bridgeGitPull) Execute(ctx context.Context, in map[string]any) (string, error) {
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("git_pull: %s", why)
	}
	body, status, ok := b.Post(ctx, "/git/pull", map[string]any{
		"repo":   strString(in, "repo"),
		"remote": strString(in, "remote"),
		"branch": strString(in, "branch"),
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("git_pull via %s failed (status=%d): %s", b.Name(), status, string(body))
	}
	return formatBridgeResult(b, body), nil
}

// ── helpers ──────────────────────────────────────────────────────────────

func intOrZero(in map[string]any, key string) int {
	v, ok := in[key]
	if !ok {
		return 0
	}
	switch x := v.(type) {
	case float64:
		return int(x)
	case int:
		return x
	case int64:
		return int(x)
	}
	return 0
}

func boolOrFalse(in map[string]any, key string) bool {
	v, _ := in[key].(bool)
	return v
}

func urlEscape(s string) string {
	// json-style minimal encoding for safe query string passthrough.
	// We don't pull in net/url here to keep this hot path tiny; the
	// bridge handler also strips its own input so a few extra chars
	// don't break anything.
	r := strings.NewReplacer(
		" ", "%20",
		"#", "%23",
		"?", "%3F",
		"&", "%26",
		"=", "%3D",
	)
	return r.Replace(s)
}
