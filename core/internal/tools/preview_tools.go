// preview_tools.go - agent-callable live-preview control.
//
// Closes the autonomous-delivery seam: until now a project's dev-server
// preview could only be booted by Studio's UI (useCurrentProject polling),
// so "build me X" from a phone produced code with no way to see it. These
// tools let the AGENT bring the preview up and hand back the tappable URL
// as part of the delivery moment.
//
// Generic shape (no per-template branches - the bridge supervisor owns
// template mechanics):
//
//	preview_start({project_path?, template?}) - boot the active session's
//	    project (defaults resolved from the session + artifact registry)
//	preview_stop({})                          - tear the preview down
//	preview_status({})                        - the currently active preview
//
// The controller is the server's supervisor seam (canvas_api.go), late-bound
// via SetController in serve.go - same pattern as ProjectTools.SetNotify.
package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PreviewController is implemented by server.Server: it routes supervisor
// calls to the session's bridge (cloud workspace or Mac tunnel) and reports
// which bridge the preview landed on.
type PreviewController interface {
	// StartPreview activates + boots a project preview. Returns the bridge's
	// project record and the bridge name ("cloud" | "mac").
	StartPreview(ctx context.Context, sessionID, projectPath, template string) (json.RawMessage, string, error)
	StopPreview(ctx context.Context, sessionID string) (json.RawMessage, error)
	PreviewStatus(ctx context.Context, sessionID string) (json.RawMessage, error)
}

// PreviewTools holds the late-bound controller.
type PreviewTools struct {
	mu   sync.Mutex
	ctrl PreviewController
	pool *pgxpool.Pool
}

// SetController late-binds the server's supervisor seam once it exists.
func (p *PreviewTools) SetController(c PreviewController) {
	p.mu.Lock()
	p.ctrl = c
	p.mu.Unlock()
}

func (p *PreviewTools) controller() PreviewController {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.ctrl
}

// RegisterPreviewTools registers the preview control tools.
func RegisterPreviewTools(r *Registry, pool *pgxpool.Pool) *PreviewTools {
	pt := &PreviewTools{pool: pool}
	r.Register(&previewStart{pt: pt})
	r.Register(&previewStop{pt: pt})
	r.Register(&previewStatus{pt: pt})
	return pt
}

// previewURL is the browser-facing path Core proxies to the active cloud
// preview (JWT-exempt; Studio rewrites /api/* to Core, so this same path is
// tappable on the public Studio origin from a push or surface card).
const previewURL = "/api/canvas/preview/"

// ── preview_start ────────────────────────────────────────────────────────

type previewStart struct{ pt *PreviewTools }

func (t *previewStart) Name() string { return "preview_start" }
func (t *previewStart) Description() string {
	return "Boot the live preview for a project (dev server on the project's " +
		"bridge) and get the URL the boss can tap. Defaults to the session's " +
		"active project. Use this as part of delivering a build: start the " +
		"preview, verify it responds, then hand the boss the link. One preview " +
		"is active at a time - starting a new one replaces the previous."
}
func (t *previewStart) Schema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project_path": map[string]any{"type": "string", "description": "Absolute project path. Default: the session's active project."},
			"template":     map[string]any{"type": "string", "description": "Project template (nextjs | vite-react | static-html | …). Default: looked up from the project registry."},
		},
	}
}
func (t *previewStart) Execute(ctx context.Context, in map[string]any) (string, error) {
	ctrl := t.pt.controller()
	if ctrl == nil {
		return "", errors.New("preview controller not wired")
	}
	sid := SessionIDFromContext(ctx)
	projectPath := strings.TrimSpace(strString(in, "project_path"))
	if projectPath == "" {
		projectPath = t.pt.sessionProjectPath(ctx, sid)
	}
	if projectPath == "" {
		return "", errors.New("no project to preview - pass project_path or open a project first (project_create / project_open)")
	}
	template := strings.TrimSpace(strString(in, "template"))
	if template == "" {
		template = t.pt.projectTemplate(ctx, projectPath)
	}
	record, bridgeName, err := ctrl.StartPreview(ctx, sid, projectPath, template)
	if err != nil {
		return "", fmt.Errorf("start preview for %s: %w", projectPath, err)
	}
	out := map[string]any{
		"ok":           true,
		"project_path": projectPath,
		"template":     template,
		"bridge":       bridgeName,
		"record":       json.RawMessage(record),
	}
	if bridgeName == "cloud" {
		out["url"] = previewURL
		out["note"] = "Tappable at " + previewURL + " on the Studio origin - use it as the url in surface_item / notify so the boss lands on the running app."
	} else {
		out["note"] = "Preview is on the Mac bridge - it renders in the Studio Canvas Preview tab (and the Mac's dev-server tunnel if configured)."
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// ── preview_stop ─────────────────────────────────────────────────────────

type previewStop struct{ pt *PreviewTools }

func (t *previewStop) Name() string { return "preview_stop" }
func (t *previewStop) Description() string {
	return "Tear down the currently active project preview (stops its dev server)."
}
func (t *previewStop) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *previewStop) Execute(ctx context.Context, _ map[string]any) (string, error) {
	ctrl := t.pt.controller()
	if ctrl == nil {
		return "", errors.New("preview controller not wired")
	}
	record, err := ctrl.StopPreview(ctx, SessionIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "record": json.RawMessage(record)})
	return string(out), nil
}

// ── preview_status ───────────────────────────────────────────────────────

type previewStatus struct{ pt *PreviewTools }

func (t *previewStatus) Name() string { return "preview_status" }
func (t *previewStatus) Description() string {
	return "Read the currently active project preview (which project, its " +
		"status and port). Use after preview_start to confirm the dev server " +
		"actually came up before handing the boss the link."
}
func (t *previewStatus) Schema() map[string]any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (t *previewStatus) Execute(ctx context.Context, _ map[string]any) (string, error) {
	ctrl := t.pt.controller()
	if ctrl == nil {
		return "", errors.New("preview controller not wired")
	}
	record, err := ctrl.PreviewStatus(ctx, SessionIDFromContext(ctx))
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{"ok": true, "record": json.RawMessage(record)})
	return string(out), nil
}

// sessionProjectPath resolves the session's active project.
func (p *PreviewTools) sessionProjectPath(ctx context.Context, sessionID string) string {
	if p.pool == nil || sessionID == "" {
		return ""
	}
	var path *string
	err := p.pool.QueryRow(ctx, `
		SELECT project_path FROM mem_sessions WHERE id = $1::uuid
	`, sessionID).Scan(&path)
	if err != nil || path == nil {
		return ""
	}
	return strings.TrimSpace(*path)
}

// projectTemplate looks up the template project_create recorded for this
// project so the supervisor launches the right dev command.
func (p *PreviewTools) projectTemplate(ctx context.Context, projectPath string) string {
	if p.pool == nil {
		return ""
	}
	var tmpl *string
	err := p.pool.QueryRow(ctx, `
		SELECT metadata->>'template' FROM mem_artifacts
		WHERE kind = 'project' AND storage_path = $1 AND deleted_at IS NULL
		ORDER BY created_at DESC LIMIT 1
	`, projectPath).Scan(&tmpl)
	if err != nil || tmpl == nil {
		return ""
	}
	return strings.TrimSpace(*tmpl)
}
