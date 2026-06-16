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
	"regexp"
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

// bridgeCall is the one execution path for every bridge_* tool: it resolves the
// session's preferred bridge, runs fn against it, and — when that bridge fails
// at the BRIDGE level (transport error or 5xx) — automatically fails over to the
// other bridge (bridge.Router.Call). fn closes over the tool's path/body and
// just does the Get/Post, so failover is uniform with zero per-tool wiring.
//
// This is the fix for the night-after-night self-improve stall: a Mac that
// flakes mid-run no longer strands the agent — files/shell/git step straight
// onto the healthy cloud workspace. A 4xx (a real command/param error like a
// failing build) is returned as-is, never retried, so we don't mask real
// failures or storm the other bridge.
func bridgeCall(ctx context.Context, router *bridge.Router, prefs PreferenceFetcher, tool string, fn func(bridge.Bridge) ([]byte, int, bool)) (string, error) {
	if router == nil {
		return "", fmt.Errorf("%s: bridge router not configured", tool)
	}
	pref := bridge.PrefAuto
	if prefs != nil {
		pref = prefs(ctx, SessionIDFromContext(ctx))
	}
	served, body, status, failedOver, err := router.Call(ctx, pref, fn)
	if errors.Is(err, bridge.ErrBothBridgesDown) {
		// Honest, model-readable "stop and surface" — NOT a raw error the model
		// retries to the cap. The run-outcome classifier later reads this marker
		// (mem_observations) to label the run "needs you", not "done".
		return bridgeUnavailableResult(tool), nil
	}
	if err != nil {
		return "", fmt.Errorf("%s: %s", tool, err.Error())
	}
	if status >= 300 {
		// A 4xx is the bridge answering with a command/param result (it's up) —
		// surface the real reason, no failover.
		return "", fmt.Errorf("%s via %s failed (status=%d): %s", tool, served.Name(), status, bridgeErrText(body))
	}
	out := formatBridgeResult(served, body)
	if failedOver {
		out = fmt.Sprintf("[failover: the preferred bridge failed, served by %s instead]\n%s", served.Name(), out)
	}
	return out, nil
}

// bridgeUnavailableResult is the structured tool result handed to the agent when
// BOTH bridges are unreachable. Mirrors the agent loop's macBridgeDownFallback
// shape (kept here to avoid importing the agent package): a directive to stop
// and surface, not a raw ERROR the model would retry. The stable
// "bridge_unavailable" token is what the run-outcome classifier greps for.
func bridgeUnavailableResult(tool string) string {
	payload := map[string]any{
		"error":     "bridge_unavailable",
		"tool":      tool,
		"both_down": true,
		"fallback": "Both the Mac bridge and the cloud workspace are unreachable right now, so I can't run files/shell/git. Do NOT retry this in a loop. Surface a HIGH-importance system item with surface_item stating both bridges are down (copy the reason), then stop — the boss needs to bring a bridge back.",
	}
	b, _ := json.Marshal(payload)
	return string(b)
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
// bridgeErrText pulls a human reason out of a bridge error response. The bridges
// return {"error":"..."} on 4xx; surfacing it beats a bare status code - e.g.
// "path is outside the workspace root /workspace" instead of just "status=400".
func bridgeErrText(body []byte) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(body, &e) == nil && strings.TrimSpace(e.Error) != "" {
		return strings.TrimSpace(e.Error)
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	if s == "" {
		return "no reason given"
	}
	return s
}

func (t *bridgeFSRead) Execute(ctx context.Context, in map[string]any) (string, error) {
	// Path-bearing params are normalized INSIDE fn, per the bridge that
	// actually serves the call — including the failover alternate — so a
	// Mac path keeps working after a Mac→cloud failover and vice versa.
	return bridgeCall(ctx, t.router, t.prefs, "fs_read", func(b bridge.Bridge) ([]byte, int, bool) {
		q := "/fs/read?path=" + urlEscape(bridge.NormalizePath(b, strString(in, "path")))
		if v := intOrZero(in, "start"); v > 0 {
			q += fmt.Sprintf("&start=%d", v)
		}
		if v := intOrZero(in, "end"); v > 0 {
			q += fmt.Sprintf("&end=%d", v)
		}
		return b.Get(ctx, q)
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "fs_ls", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Get(ctx, "/fs/ls?path="+urlEscape(bridge.NormalizePath(b, strString(in, "path"))))
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "fs_save", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Post(ctx, "/fs/save", map[string]any{
			"path":    bridge.NormalizePath(b, strString(in, "path")),
			"content": strString(in, "content"),
		})
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "fs_edit", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Post(ctx, "/fs/edit", map[string]any{
			"path":        bridge.NormalizePath(b, strString(in, "path")),
			"old_string":  strString(in, "old_string"),
			"new_string":  strString(in, "new_string"),
			"replace_all": boolOrFalse(in, "replace_all"),
		})
	})
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
	cmd := strString(in, "cmd")
	if redirect, blocked := guardInteractiveLogin(cmd); blocked {
		return redirect, nil
	}
	return bridgeCall(ctx, t.router, t.prefs, "bash_run", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Post(ctx, "/bash", map[string]any{
			"cmd":         cmd,
			"cwd":         bridge.NormalizePath(b, strString(in, "cwd")),
			"timeout_sec": intOrZero(in, "timeout_sec"),
		})
	})
}

// loginCmdRe matches an interactive CLI sign-in invocation — "<tool> auth login"
// or "<tool> login" — at the start of the command or after a shell separator. The
// trailing `(\s+-|\s*($|[;&|]))` requires `login` to be the END of the command or
// be followed only by a flag, so it fires on real subcommands ("vercel login",
// "gh auth login", "npm login --registry x") but NOT when "login" is just an
// argument ("grep login app.log", "cat login.txt", "git log").
var loginCmdRe = regexp.MustCompile(`(?i)(^|[;&|]\s*|\bsudo\s+)([a-z0-9][\w.-]*\s+)?(auth\s+login|login)(\s+-|\s*($|[;&|]))`)

// guardInteractiveLogin intercepts raw "<tool> auth login" / "<tool> login" bash
// commands. Such flows open a browser and/or hold an OAuth callback on whatever
// box runs them — via raw bash that can land on the boss's Mac, popping HIS
// browser and authing the wrong machine (the boss may be on his phone with no
// computer at all). Sign-in must instead run on the cloud workspace with the URL
// opened in Jarvis's OWN browser. This is the deterministic backstop behind the
// catalog steering + extension_activate tool (Rule #1b: the mechanic is in code,
// not prose the model can drop). Generic — zero per-vendor wiring. NOTE: the
// legit self-contained path (extensions Manager.startDetachedAuth) calls the
// bridge directly, NOT this tool, so it is never caught here.
// IsInteractiveLoginCmd reports whether a bash command is an interactive CLI
// sign-in ("<tool> auth login" / "<tool> login"). Exported so every shell entry
// point — the bridge bash_run tool here AND the claude_code__bash gate — enforces
// the same "sign-in happens in Jarvis's own cloud browser, never the boss's
// machine" rule from one source of truth.
func IsInteractiveLoginCmd(cmd string) bool {
	return strings.TrimSpace(cmd) != "" && loginCmdRe.MatchString(cmd)
}

func guardInteractiveLogin(cmd string) (string, bool) {
	if !IsInteractiveLoginCmd(cmd) {
		return "", false
	}
	payload := map[string]any{
		"blocked": "interactive_sign_in_via_bash",
		"why": "Running a CLI sign-in (`auth login` / `login`) directly opens a browser and holds the OAuth " +
			"callback on whatever machine runs it. Via raw bash that can land on the boss's computer, popping HIS " +
			"browser and signing in the wrong machine — and the boss may be on his phone with no computer at all.",
		"do_this_instead": "Sign in self-contained, inside YOUR cloud browser: if this is a registered cli " +
			"extension, call extension_activate \"<name>\" (it runs the sign-in on your cloud workspace and returns " +
			"auth_url), then browser_open that auth_url so the page is live in the boss's Preview pane and he approves " +
			"it there. If it isn't registered yet, extension_register it (kind=cli, with its auth_cmd) first, then " +
			"extension_activate. Never sign in via bash; never hand the boss a URL or open it on his computer.",
	}
	out, _ := json.Marshal(payload)
	return string(out), true
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
	return bridgeCall(ctx, t.router, t.prefs, "git_status", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Get(ctx, "/git/status?repo="+urlEscape(bridge.NormalizePath(b, strString(in, "repo"))))
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "git_diff", func(b bridge.Bridge) ([]byte, int, bool) {
		q := "/git/diff?repo=" + urlEscape(bridge.NormalizePath(b, strString(in, "repo")))
		if p := strString(in, "path"); p != "" {
			q += "&path=" + urlEscape(p)
		}
		if boolOrFalse(in, "staged") {
			q += "&staged=1"
		}
		return b.Get(ctx, q)
	})
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
	files := []string{}
	if arr, ok := in["files"].([]any); ok {
		for _, v := range arr {
			if s, ok := v.(string); ok && s != "" {
				files = append(files, s)
			}
		}
	}
	return bridgeCall(ctx, t.router, t.prefs, "git_stage", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Post(ctx, "/git/stage", map[string]any{
			"repo":  bridge.NormalizePath(b, strString(in, "repo")),
			"files": files,
		})
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "git_commit", func(b bridge.Bridge) ([]byte, int, bool) {
		repo := bridge.NormalizePath(b, strString(in, "repo"))
		// Per-session branching: when commits land on the Cloud bridge,
		// auto-route them onto a session-named branch so Jarvis's work
		// is attributable + revertable without polluting main. Mac
		// commits use whatever branch the boss has checked out - he's
		// the human in that loop. Inside fn so it follows the bridge that
		// actually serves (incl. after a Mac→cloud failover).
		if b.Name() == bridge.KindCloud {
			ensureSessionBranch(ctx, b, repo, SessionIDFromContext(ctx))
		}
		return b.Post(ctx, "/git/commit", map[string]any{
			"repo":    repo,
			"message": strString(in, "message"),
		})
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "git_push", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Post(ctx, "/git/push", map[string]any{
			"repo":   bridge.NormalizePath(b, strString(in, "repo")),
			"remote": strString(in, "remote"),
			"branch": strString(in, "branch"),
		})
	})
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
	return bridgeCall(ctx, t.router, t.prefs, "git_pull", func(b bridge.Bridge) ([]byte, int, bool) {
		return b.Post(ctx, "/git/pull", map[string]any{
			"repo":   bridge.NormalizePath(b, strString(in, "repo")),
			"remote": strString(in, "remote"),
			"branch": strString(in, "branch"),
		})
	})
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
