// project_tools.go - `project_create` is the one tool the boss explicitly
// asked for. End-to-end app-creation flow:
//
//   1. Slugify the name, pick the projects-root on the active bridge,
//      compute the absolute project path.
//   2. Create the directory, run the chosen template's scaffold command
//      (or skip for `empty`).
//   3. `git init` + first commit (the scaffold itself).
//   4. If create_github=true, hit GitHub's REST API to create the repo
//      under the boss's account, then push the initial commit.
//   5. Update the current session's `mem_sessions.project_path` so the
//      Files column auto-scopes from the next turn onwards.
//   6. Write a `mem_artifacts` row of kind='project' so future sessions
//      can find this project by name without grepping the filesystem.
//
// Gated through BridgeGate (added to its block-list in serve wiring) so
// the boss approves once per project create - no separate prompts for
// each internal bash call.
package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/jackc/pgx/v5/pgxpool"
)

// RegisterProjectTools wires `project_create` (and any future project_*
// tools) into the registry. Depends on the same bridge router + prefs
// the generic bridge_* tools use.
// ProjectTools holds the registered project tools so serve.go can late-bind the
// project-changed WS notifier (the broadcaster only exists after the server is
// constructed) — same late-bind pattern as document_create's emitter.
type ProjectTools struct {
	create *projectCreate
	clone  *projectClone
	open   *projectOpen
}

// SetNotify wires the project-switch WS push onto every project tool so a switch
// re-scopes the canvas instantly instead of waiting on the 1.5s session poll.
func (pt *ProjectTools) SetNotify(fn func(sessionID, projectPath string)) {
	if pt == nil {
		return
	}
	pt.create.notify = fn
	pt.clone.notify = fn
	pt.open.notify = fn
}

func RegisterProjectTools(r *Registry, pool *pgxpool.Pool, router *bridge.Router, prefs PreferenceFetcher) *ProjectTools {
	create := &projectCreate{pool: pool, router: router, prefs: prefs}
	clone := &projectClone{pool: pool, router: router, prefs: prefs}
	open := &projectOpen{pool: pool, router: router, prefs: prefs}
	r.Register(create)
	r.Register(clone)
	r.Register(open)
	return &ProjectTools{create: create, clone: clone, open: open}
}

// resolveProjectsRoot returns the per-bridge projects store — the disk-like home
// for apps, kept SEPARATE from Jarvis's self-clone. Cloud: /workspace/projects
// (sibling of /workspace/infinity); Mac: ~/Dev/projects. Override per device with
// INFINITY_PROJECTS_ROOT / INFINITY_DEFAULT_PROJECTS_ROOT_MAC.
func resolveProjectsRoot(b bridge.Bridge) string {
	if root := strings.TrimSpace(os.Getenv("INFINITY_PROJECTS_ROOT")); root != "" {
		return strings.TrimRight(root, "/")
	}
	if b.Name() == bridge.KindCloud {
		return "/workspace/projects"
	}
	if root := strings.TrimSpace(os.Getenv("INFINITY_DEFAULT_PROJECTS_ROOT_MAC")); root != "" {
		return strings.TrimRight(root, "/")
	}
	return "/Users/n0m4d/Dev/projects"
}

type projectCreate struct {
	pool   *pgxpool.Pool
	router *bridge.Router
	prefs  PreferenceFetcher
	notify func(sessionID, projectPath string)
}

func (t *projectCreate) Name() string { return "project_create" }
func (t *projectCreate) Description() string {
	return "Create a new project from scratch on the active bridge (Mac or Cloud). " +
		"Scaffolds with the chosen template, runs `git init` + initial commit, " +
		"optionally creates a private GitHub repo under DopeSoft and pushes the " +
		"first commit, sets the current session's project_path so the Files column " +
		"scopes to it, and indexes the project as a mem_artifacts row. " +
		"Templates: nextjs | vite-react | static-html | go | python | empty. " +
		"Use this whenever the boss asks to 'build a new app / site / tool' rather " +
		"than scattering bash/fs/git calls."
}
func (t *projectCreate) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Human-friendly project name. Gets slugified for the directory + GitHub repo (e.g. 'Habit Tracker' → 'habit-tracker').",
			},
			"template": map[string]any{
				"type": "string",
				"enum": []string{"nextjs", "vite-react", "static-html", "go", "python", "empty"},
				"default": "empty",
			},
			"description": map[string]any{
				"type":        "string",
				"description": "One-line description - used for the GitHub repo description and the artifact row.",
			},
			"create_github": map[string]any{
				"type":    "boolean",
				"default": true,
			},
			"private": map[string]any{
				"type":    "boolean",
				"default": true,
				"description": "Only used when create_github=true. Default private.",
			},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"name"},
	}
}

func (t *projectCreate) Execute(ctx context.Context, in map[string]any) (string, error) {
	rawName := strings.TrimSpace(strString(in, "name"))
	if rawName == "" {
		return "", errors.New("name required")
	}
	slug := slugify(rawName)
	if slug == "" {
		return "", errors.New("name produced an empty slug - pick something with letters/numbers")
	}
	tmpl := strings.ToLower(strings.TrimSpace(strString(in, "template")))
	if tmpl == "" {
		tmpl = "empty"
	}
	if !isKnownTemplate(tmpl) {
		return "", fmt.Errorf("unknown template %q - choose nextjs | vite-react | static-html | go | python | empty", tmpl)
	}
	description := strString(in, "description")
	createGitHub := boolOrTrue(in, "create_github")
	private := boolOrTrue(in, "private")
	tags := stringSliceOrEmpty(in, "tags")

	// Resolve active bridge.
	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("project_create: %s", why)
	}

	root := resolveProjectsRoot(b)
	projectPath := root + "/" + slug

	// 1 + 2. Create dir + run scaffold template.
	scaffoldCmd := buildScaffoldCommand(tmpl, root, slug, description, rawName)
	bashOut, status, ok := b.Post(ctx, "/bash", map[string]any{
		"cmd":         scaffoldCmd,
		"timeout_sec": 240,
	})
	if !ok || status >= 300 {
		return "", fmt.Errorf("project_create: scaffold failed on %s (status=%d): %s",
			b.Name(), status, truncateForError(string(bashOut), 600))
	}

	// 3. git init + initial commit. Wrapped in one bash call so the
	// whole sequence either succeeds or fails atomically.
	initCmd := fmt.Sprintf(
		"cd %s && git init -b main >/dev/null 2>&1 && "+
			"git add -A && git commit -m %s >/dev/null",
		shellSingleQuote(projectPath),
		shellSingleQuote("Initial commit - scaffold via project_create ("+tmpl+")"),
	)
	gitOut, gitStatus, gitOK := b.Post(ctx, "/bash", map[string]any{
		"cmd":         initCmd,
		"timeout_sec": 30,
	})
	if !gitOK || gitStatus >= 300 {
		return "", fmt.Errorf("project_create: git init/commit failed (status=%d): %s",
			gitStatus, truncateForError(string(gitOut), 400))
	}

	// 4. Optionally create GitHub repo + push. Errors here are
	// non-fatal - the project still exists locally. We surface the
	// error in the result so Jarvis can tell the boss what's left.
	owner := envOrDefault("INFINITY_GITHUB_OWNER", "DopeSoft")
	var githubURL, pushErr string
	if createGitHub {
		ghURL, ghErr := createGitHubRepo(ctx, owner, slug, description, private)
		if ghErr != nil {
			pushErr = "github repo create failed: " + ghErr.Error()
		} else {
			githubURL = ghURL
			// Add remote + push.
			pushCmd := fmt.Sprintf(
				"cd %s && git remote add origin %s 2>/dev/null || git remote set-url origin %s && "+
					"git push -u origin main",
				shellSingleQuote(projectPath),
				shellSingleQuote(ghURL+".git"),
				shellSingleQuote(ghURL+".git"),
			)
			pushOut, pushStatus, pushOK := b.Post(ctx, "/bash", map[string]any{
				"cmd":         pushCmd,
				"timeout_sec": 60,
			})
			if !pushOK || pushStatus >= 300 {
				pushErr = "git push failed: " + truncateForError(string(pushOut), 400)
			}
		}
	}

	// 5. Update session.project_path so the next turn auto-scopes.
	sessionID := SessionIDFromContext(ctx)
	if sessionID != "" && t.pool != nil {
		_, _ = t.pool.Exec(ctx,
			`UPDATE mem_sessions SET project_path = $1 WHERE id::text = $2`,
			projectPath, sessionID,
		)
	}
	if t.notify != nil && sessionID != "" {
		t.notify(sessionID, projectPath)
	}

	// 6. Index the project as a mem_artifacts row.
	virtualPath := "/projects/" + slug
	artifactID, artErr := insertProjectArtifact(ctx, t.pool, projectArtifact{
		Slug:        slug,
		Name:        rawName,
		Description: description,
		Template:    tmpl,
		StoragePath: projectPath,
		Bridge:      string(b.Name()),
		GitHubURL:   githubURL,
		Tags:        tags,
		SessionID:   sessionID,
		VirtualPath: virtualPath,
	})
	if artErr != nil {
		// Soft failure - the project exists on disk + GitHub. The
		// artifact row is recoverable via a follow-up artifact_save.
		logInfo("project_create: mem_artifacts insert failed: %v", artErr)
	}

	result := map[string]any{
		"slug":           slug,
		"name":           rawName,
		"template":       tmpl,
		"project_path":   projectPath,
		"virtual_path":   virtualPath,
		"bridge":         string(b.Name()),
		"github_url":     githubURL,
		"artifact_id":    artifactID,
		"session_id":     sessionID,
	}
	if pushErr != "" {
		result["github_warning"] = pushErr
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// ── project_clone ──────────────────────────────────────────────────────────

type projectClone struct {
	pool   *pgxpool.Pool
	router *bridge.Router
	prefs  PreferenceFetcher
	notify func(sessionID, projectPath string)
}

func (t *projectClone) Name() string { return "project_clone" }
func (t *projectClone) Description() string {
	return "Clone an existing git repo onto the active bridge (Mac or Cloud) and open it as a project. " +
		"Lands it in the projects store (cloud: /workspace/projects/<name>, separate from Jarvis's own " +
		"infinity code), sets the session's project_path so the canvas scopes to it, and indexes it as a " +
		"mem_artifacts row so it shows in the project switcher. Use whenever the boss says 'clone this repo " +
		"and let's work on it' or gives a GitHub URL to work from. GitHub auth rides the bridge's credential " +
		"helper. After cloning, install deps with the cloud-workspace patterns as needed."
}
func (t *projectClone) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"repo_url": map[string]any{
				"type":        "string",
				"description": "Git URL to clone, e.g. https://github.com/owner/repo.git",
			},
			"name": map[string]any{
				"type":        "string",
				"description": "Optional project name/dir. Defaults to the repo name from the URL.",
			},
			"branch": map[string]any{
				"type":        "string",
				"description": "Optional branch to check out.",
			},
			"tags": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
		},
		"required": []string{"repo_url"},
	}
}

func (t *projectClone) Execute(ctx context.Context, in map[string]any) (string, error) {
	rawURL := strings.TrimSpace(strString(in, "repo_url"))
	if rawURL == "" {
		return "", errors.New("repo_url required")
	}
	// The URL + branch are interpolated into a bash command — reject shell
	// metacharacters so nothing can smuggle a command past the bridge.
	const bad = " \t\n\r;|&`$<>()\"'\\"
	if strings.ContainsAny(rawURL, bad) {
		return "", errors.New("repo_url contains invalid characters")
	}
	branch := strings.TrimSpace(strString(in, "branch"))
	if branch != "" && strings.ContainsAny(branch, bad) {
		return "", errors.New("branch contains invalid characters")
	}
	name := strings.TrimSpace(strString(in, "name"))
	slug := slugify(name)
	if slug == "" {
		slug = slugFromRepoURL(rawURL)
	}
	if slug == "" {
		return "", errors.New("could not derive a project name from the url - pass name")
	}
	tags := stringSliceOrEmpty(in, "tags")

	b, why, err := pickBridge(ctx, t.router, t.prefs)
	if err != nil {
		return "", fmt.Errorf("project_clone: %s", why)
	}

	root := resolveProjectsRoot(b)
	projectPath := root + "/" + slug

	// Clone into the projects store. mkdir -p the root first so a fresh volume
	// works. Auth for github rides the bridge's credential helper (no token in
	// the command).
	cloneCmd := "git clone "
	if branch != "" {
		cloneCmd += "--branch " + shellSingleQuote(branch) + " "
	}
	cloneCmd += shellSingleQuote(rawURL) + " " + shellSingleQuote(projectPath)
	full := "mkdir -p " + shellSingleQuote(root) + " && " + cloneCmd
	out, status, ok := b.Post(ctx, "/bash", map[string]any{"cmd": full, "timeout_sec": 300})
	if !ok || status >= 300 {
		return "", fmt.Errorf("project_clone: clone failed on %s (status=%d): %s",
			b.Name(), status, truncateForError(string(out), 600))
	}

	// Scope the session to the new project.
	sessionID := SessionIDFromContext(ctx)
	if sessionID != "" && t.pool != nil {
		_, _ = t.pool.Exec(ctx,
			`UPDATE mem_sessions SET project_path = $1 WHERE id::text = $2`,
			projectPath, sessionID,
		)
	}
	if t.notify != nil && sessionID != "" {
		t.notify(sessionID, projectPath)
	}

	githubURL := ""
	if strings.Contains(rawURL, "github.com") {
		githubURL = strings.TrimSuffix(rawURL, ".git")
	}
	displayName := name
	if displayName == "" {
		displayName = slug
	}
	virtualPath := "/projects/" + slug
	artifactID, artErr := insertProjectArtifact(ctx, t.pool, projectArtifact{
		Slug:        slug,
		Name:        displayName,
		Description: "Cloned from " + rawURL,
		Template:    "cloned",
		StoragePath: projectPath,
		Bridge:      string(b.Name()),
		GitHubURL:   githubURL,
		Tags:        tags,
		SessionID:   sessionID,
		VirtualPath: virtualPath,
	})
	if artErr != nil {
		logInfo("project_clone: mem_artifacts insert failed: %v", artErr)
	}

	result := map[string]any{
		"slug":         slug,
		"name":         displayName,
		"project_path": projectPath,
		"virtual_path": virtualPath,
		"bridge":       string(b.Name()),
		"source_url":   rawURL,
		"github_url":   githubURL,
		"artifact_id":  artifactID,
		"session_id":   sessionID,
	}
	outJSON, _ := json.Marshal(result)
	return string(outJSON), nil
}

// slugFromRepoURL extracts a directory slug from a git URL's final path
// segment (strips a trailing .git), e.g. https://github.com/x/cool-repo.git →
// "cool-repo".
func slugFromRepoURL(url string) string {
	u := strings.TrimRight(strings.TrimSpace(url), "/")
	u = strings.TrimSuffix(u, ".git")
	if i := strings.LastIndexAny(u, "/:"); i >= 0 {
		u = u[i+1:]
	}
	return slugify(u)
}

// ── project_open ───────────────────────────────────────────────────────────

type projectOpen struct {
	pool   *pgxpool.Pool
	router *bridge.Router
	prefs  PreferenceFetcher
	notify func(sessionID, projectPath string)
}

func (t *projectOpen) Name() string { return "project_open" }
func (t *projectOpen) Description() string {
	return "Switch the current session to an already-known project (one previously created or cloned) so the canvas, git, and memory re-scope to it. Use to jump between projects mid-conversation — 'open my-app', 'switch to the client repo'. Pass the project name (as shown in the switcher), or an absolute path. NEW project → project_create; external repo → project_clone. Switching to a different project from your own infinity code is how you avoid silently working in the wrong tree."
}
func (t *projectOpen) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Project name/slug to switch to (as shown in the project switcher).",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional absolute path to switch to directly instead of resolving by name.",
			},
		},
	}
}

func (t *projectOpen) Execute(ctx context.Context, in map[string]any) (string, error) {
	path := strings.TrimSpace(strString(in, "path"))
	name := strings.TrimSpace(strString(in, "name"))
	if path == "" && name == "" {
		return "", errors.New("pass a project name or path to open")
	}

	// Active bridge — used to prefer a project that lives on the machine the
	// session is currently using (a /workspace path on cloud won't exist on Mac).
	bridgeKind := ""
	if t.router != nil {
		if b, _, err := pickBridge(ctx, t.router, t.prefs); err == nil && b != nil {
			bridgeKind = string(b.Name())
		}
	}

	if path == "" {
		if t.pool == nil {
			return "", errors.New("project registry unavailable")
		}
		row := t.pool.QueryRow(ctx, `
			SELECT storage_path FROM mem_artifacts
			WHERE kind = 'project' AND deleted_at IS NULL AND storage_path <> ''
			  AND (lower(name) = lower($1) OR virtual_path = '/projects/' || $1 OR storage_path LIKE '%/' || $1)
			ORDER BY (bridge = $2) DESC, created_at DESC
			LIMIT 1`, name, bridgeKind)
		if err := row.Scan(&path); err != nil || strings.TrimSpace(path) == "" {
			return "", fmt.Errorf("no known project matches %q - create it (project_create) or clone it (project_clone) first", name)
		}
	}

	sessionID := SessionIDFromContext(ctx)
	if sessionID != "" && t.pool != nil {
		_, _ = t.pool.Exec(ctx,
			`UPDATE mem_sessions SET project_path = $1 WHERE id::text = $2`,
			path, sessionID,
		)
	}
	if t.notify != nil && sessionID != "" {
		t.notify(sessionID, path)
	}

	result := map[string]any{
		"project_path": path,
		"session_id":   sessionID,
		"switched":     true,
	}
	out, _ := json.Marshal(result)
	return string(out), nil
}

// ── helpers ──────────────────────────────────────────────────────────────

var slugRegex = regexp.MustCompile(`[^a-z0-9]+`)

func slugify(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugRegex.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if len(s) > 64 {
		s = s[:64]
		s = strings.Trim(s, "-")
	}
	return s
}

func isKnownTemplate(t string) bool {
	switch t {
	case "nextjs", "vite-react", "static-html", "go", "python", "empty":
		return true
	}
	return false
}

// buildScaffoldCommand returns the bash one-liner that creates the
// directory and lays down the template. Same shape across templates -
// `cd <root> && <create the slug>` - so the caller doesn't need to
// special-case anything.
func buildScaffoldCommand(template, root, slug, description, displayName string) string {
	mkRoot := fmt.Sprintf("mkdir -p %s && cd %s", shellSingleQuote(root), shellSingleQuote(root))
	switch template {
	case "nextjs":
		// create-next-app doesn't wire basePath, but the preview supervisor
		// serves dev projects under /api/canvas/preview and launches next
		// with NEXT_PUBLIC_BASE_PATH set (docker/workspace/supervisor.go
		// devCommand). Without a basePath-aware config every asset 404s
		// behind the proxy - so replace the generated config with one that
		// honors the env. Empty env = "" = no basePath, so `next dev` at
		// the root still works identically.
		nextConfig := "const basePath = process.env.NEXT_PUBLIC_BASE_PATH || \"\";\n\n" +
			"/** @type {import('next').NextConfig} */\n" +
			"const nextConfig = {\n" +
			"  basePath,\n" +
			"  assetPrefix: basePath || undefined,\n" +
			"};\n\n" +
			"export default nextConfig;\n"
		return mkRoot + " && " + fmt.Sprintf(
			"pnpx --yes create-next-app@latest %s --typescript --tailwind --app --no-eslint --no-src-dir --no-import-alias --use-pnpm --yes && "+
				"cd %s && rm -f next.config.ts next.config.js next.config.mjs && printf %%s %s > next.config.mjs",
			shellSingleQuote(slug), shellSingleQuote(slug), shellSingleQuote(nextConfig),
		)
	case "vite-react":
		return mkRoot + " && " + fmt.Sprintf(
			"pnpm create vite@latest %s -- --template react-ts && cd %s && pnpm install",
			shellSingleQuote(slug), shellSingleQuote(slug),
		)
	case "static-html":
		readme := fmt.Sprintf("# %s\n\n%s\n", displayName, description)
		return mkRoot + " && " + fmt.Sprintf(
			"mkdir %s && cd %s && "+
				"printf %s > index.html && "+
				"printf %s > style.css && "+
				"printf %s > main.js && "+
				"printf %s > README.md",
			shellSingleQuote(slug), shellSingleQuote(slug),
			shellSingleQuote(fmt.Sprintf(
				"<!doctype html>\n<html lang=\"en\">\n<head>\n  <meta charset=\"utf-8\">\n  <title>%s</title>\n  <link rel=\"stylesheet\" href=\"style.css\">\n</head>\n<body>\n  <main><h1>%s</h1><p>%s</p></main>\n  <script src=\"main.js\"></script>\n</body>\n</html>\n",
				displayName, displayName, description,
			)),
			shellSingleQuote("body{font-family:system-ui;margin:2rem auto;max-width:42rem;line-height:1.6}\n"),
			shellSingleQuote("console.log('"+displayName+" booted');\n"),
			shellSingleQuote(readme),
		)
	case "go":
		modPath := "github.com/dopesoft/" + slug
		mainGo := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"" + displayName + " - hello from Jarvis\")\n}\n"
		readme := fmt.Sprintf("# %s\n\n%s\n\n## Run\n\n```\ngo run .\n```\n", displayName, description)
		return mkRoot + " && " + fmt.Sprintf(
			"mkdir %s && cd %s && go mod init %s && printf %s > main.go && printf %s > README.md",
			shellSingleQuote(slug), shellSingleQuote(slug),
			shellSingleQuote(modPath),
			shellSingleQuote(mainGo),
			shellSingleQuote(readme),
		)
	case "python":
		mainPy := "def main():\n    print(\"" + displayName + " - hello from Jarvis\")\n\nif __name__ == \"__main__\":\n    main()\n"
		readme := fmt.Sprintf("# %s\n\n%s\n\n## Run\n\n```\npython -m %s\n```\n", displayName, description, slug)
		gitignore := ".venv/\n__pycache__/\n*.pyc\n.env\n"
		return mkRoot + " && " + fmt.Sprintf(
			"mkdir -p %s && cd %s && printf %s > main.py && printf %s > README.md && printf %s > .gitignore && printf '' > requirements.txt",
			shellSingleQuote(slug), shellSingleQuote(slug),
			shellSingleQuote(mainPy),
			shellSingleQuote(readme),
			shellSingleQuote(gitignore),
		)
	case "empty":
		readme := fmt.Sprintf("# %s\n\n%s\n", displayName, description)
		return mkRoot + " && " + fmt.Sprintf(
			"mkdir %s && cd %s && printf %s > README.md",
			shellSingleQuote(slug), shellSingleQuote(slug),
			shellSingleQuote(readme),
		)
	}
	return mkRoot
}

// shellSingleQuote wraps a string in single quotes safe for bash, using
// the classic '\'' escape trick for embedded singles.
func shellSingleQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

// truncateForError keeps error messages short enough that Jarvis can
// process them without burning a turn parsing 4KB of stack trace.
func truncateForError(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func boolOrTrue(in map[string]any, key string) bool {
	v, ok := in[key]
	if !ok {
		return true
	}
	if b, ok := v.(bool); ok {
		return b
	}
	return true
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// logInfo is a small wrapper for soft-failure / success lines. stdout
// intentionally - these aren't error-severity events even though they
// surface unexpected states. Avoids the `log` package name colliding
// with the package import elsewhere in tools/.
func logInfo(format string, args ...any) {
	fmt.Fprintf(os.Stdout, format+"\n", args...)
}

// ── GitHub API ───────────────────────────────────────────────────────────

// createGitHubRepo creates a repo under the given owner/user via the
// REST API. Returns the full https URL. Tokens come from
// $INFINITY_GITHUB_TOKEN, falling back to $GITHUB_MCP_TOKEN (the same
// PAT we use elsewhere) so the boss only manages one secret.
func createGitHubRepo(ctx context.Context, owner, slug, description string, private bool) (string, error) {
	token := strings.TrimSpace(os.Getenv("INFINITY_GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_MCP_TOKEN"))
	}
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	if token == "" {
		return "", errors.New("no GitHub PAT in env (set GITHUB_MCP_TOKEN or INFINITY_GITHUB_TOKEN)")
	}

	body := map[string]any{
		"name":         slug,
		"description":  description,
		"private":      private,
		"auto_init":    false, // we already committed locally
		"has_issues":   true,
		"has_projects": false,
		"has_wiki":     false,
	}
	buf, _ := json.Marshal(body)

	// Try the user-scoped endpoint first; if the token belongs to a
	// user who has access to an org named like `owner`, also try the
	// org-scoped path on a 404.
	urls := []string{"https://api.github.com/user/repos"}
	if !strings.EqualFold(owner, "") {
		urls = append(urls, "https://api.github.com/orgs/"+owner+"/repos")
	}
	var lastBody string
	var lastStatus int
	for _, url := range urls {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(buf))
		if err != nil {
			return "", err
		}
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
		req.Header.Set("User-Agent", "infinity-project-create")

		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
		resp.Body.Close()
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var parsed struct {
				HTMLURL  string `json:"html_url"`
				FullName string `json:"full_name"`
			}
			_ = json.Unmarshal(respBody, &parsed)
			if parsed.HTMLURL != "" {
				return parsed.HTMLURL, nil
			}
			if parsed.FullName != "" {
				return "https://github.com/" + parsed.FullName, nil
			}
			return "https://github.com/" + owner + "/" + slug, nil
		}
		lastBody = string(respBody)
		lastStatus = resp.StatusCode
		// 404 on /user/repos shouldn't happen; 404 on /orgs/<x>/repos
		// just means the boss isn't in that org. Skip to next attempt.
		if resp.StatusCode == http.StatusNotFound {
			continue
		}
		break
	}
	return "", fmt.Errorf("github repo create failed: status=%d body=%s", lastStatus, truncateForError(lastBody, 300))
}

// ── mem_artifacts insert ─────────────────────────────────────────────────

type projectArtifact struct {
	Slug        string
	Name        string
	Description string
	Template    string
	StoragePath string
	Bridge      string
	GitHubURL   string
	Tags        []string
	SessionID   string
	VirtualPath string
}

func insertProjectArtifact(ctx context.Context, pool *pgxpool.Pool, p projectArtifact) (string, error) {
	if pool == nil {
		return "", errors.New("no pool")
	}
	if len(p.Tags) == 0 {
		p.Tags = []string{"project"}
	} else {
		// Ensure 'project' tag is present so Library filters work.
		has := false
		for _, t := range p.Tags {
			if t == "project" {
				has = true
				break
			}
		}
		if !has {
			p.Tags = append([]string{"project"}, p.Tags...)
		}
	}
	tagsJSON, _ := json.Marshal(p.Tags)
	metaJSON, _ := json.Marshal(map[string]any{
		"template": p.Template,
	})
	var sessionPtr *string
	if p.SessionID != "" {
		sessionPtr = &p.SessionID
	}
	var bridgePtr *string
	if p.Bridge != "" {
		bridgePtr = &p.Bridge
	}
	var githubPtr *string
	if p.GitHubURL != "" {
		githubPtr = &p.GitHubURL
	}
	var id string
	err := pool.QueryRow(ctx, `
		INSERT INTO mem_artifacts
			(kind, name, description, virtual_path,
			 storage_kind, storage_path,
			 bridge, github_url,
			 source_session_id, source_tool,
			 tags, metadata)
		VALUES
			('project', $1, NULLIF($2,''), $3,
			 'filesystem', $4,
			 $5, $6,
			 $7, 'project_create',
			 $8::jsonb, $9::jsonb)
		ON CONFLICT (virtual_path) WHERE deleted_at IS NULL
		DO UPDATE SET
			storage_path = EXCLUDED.storage_path,
			bridge       = EXCLUDED.bridge,
			github_url   = COALESCE(EXCLUDED.github_url, mem_artifacts.github_url),
			updated_at   = NOW()
		RETURNING id::text
	`,
		p.Name, p.Description, p.VirtualPath,
		p.StoragePath,
		bridgePtr, githubPtr,
		sessionPtr,
		string(tagsJSON), string(metaJSON),
	).Scan(&id)
	if err != nil {
		return "", err
	}
	return id, nil
}
