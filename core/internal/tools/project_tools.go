// project_tools.go — `project_create` is the one tool the boss explicitly
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
// the boss approves once per project create — no separate prompts for
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
func RegisterProjectTools(r *Registry, pool *pgxpool.Pool, router *bridge.Router, prefs PreferenceFetcher) {
	r.Register(&projectCreate{pool: pool, router: router, prefs: prefs})
}

type projectCreate struct {
	pool   *pgxpool.Pool
	router *bridge.Router
	prefs  PreferenceFetcher
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
				"description": "One-line description — used for the GitHub repo description and the artifact row.",
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
		return "", errors.New("name produced an empty slug — pick something with letters/numbers")
	}
	tmpl := strings.ToLower(strings.TrimSpace(strString(in, "template")))
	if tmpl == "" {
		tmpl = "empty"
	}
	if !isKnownTemplate(tmpl) {
		return "", fmt.Errorf("unknown template %q — choose nextjs | vite-react | static-html | go | python | empty", tmpl)
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

	// Path layout. Configurable via env so the boss can pick the
	// projects-root per device. Defaults to <root>/projects.
	root := os.Getenv("INFINITY_PROJECTS_ROOT")
	if root == "" {
		// Sensible per-bridge default. Cloud workspace volume mounts
		// at /workspace; Mac canvas root defaults to $HOME (typically
		// /Users/<user>/Dev).
		if b.Name() == bridge.KindCloud {
			root = "/workspace/projects"
		} else {
			root = os.Getenv("INFINITY_DEFAULT_PROJECTS_ROOT_MAC")
			if root == "" {
				root = "/Users/n0m4d/Dev/projects"
			}
		}
	}
	projectPath := strings.TrimRight(root, "/") + "/" + slug

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
		shellSingleQuote("Initial commit — scaffold via project_create ("+tmpl+")"),
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
	// non-fatal — the project still exists locally. We surface the
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
		// Soft failure — the project exists on disk + GitHub. The
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
// directory and lays down the template. Same shape across templates —
// `cd <root> && <create the slug>` — so the caller doesn't need to
// special-case anything.
func buildScaffoldCommand(template, root, slug, description, displayName string) string {
	mkRoot := fmt.Sprintf("mkdir -p %s && cd %s", shellSingleQuote(root), shellSingleQuote(root))
	switch template {
	case "nextjs":
		return mkRoot + " && " + fmt.Sprintf(
			"pnpx --yes create-next-app@latest %s --typescript --tailwind --app --no-eslint --no-src-dir --no-import-alias --use-pnpm --yes",
			shellSingleQuote(slug),
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
		mainGo := "package main\n\nimport \"fmt\"\n\nfunc main() {\n\tfmt.Println(\"" + displayName + " — hello from Jarvis\")\n}\n"
		readme := fmt.Sprintf("# %s\n\n%s\n\n## Run\n\n```\ngo run .\n```\n", displayName, description)
		return mkRoot + " && " + fmt.Sprintf(
			"mkdir %s && cd %s && go mod init %s && printf %s > main.go && printf %s > README.md",
			shellSingleQuote(slug), shellSingleQuote(slug),
			shellSingleQuote(modPath),
			shellSingleQuote(mainGo),
			shellSingleQuote(readme),
		)
	case "python":
		mainPy := "def main():\n    print(\"" + displayName + " — hello from Jarvis\")\n\nif __name__ == \"__main__\":\n    main()\n"
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
// intentionally — these aren't error-severity events even though they
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
