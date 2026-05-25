// supervisor.go — the cloud preview supervisor.
//
// This is the cloud half of the Canvas live-preview feature. The Mac bridge
// already boots a project's dev server and fronts it on a Cloudflare tunnel;
// the cloud workspace had fs read/write but NOTHING serving the running app,
// so a static-html dashboard (the pulse-board) had no preview to render. This
// closes that gap with the same contract the Mac supervisor speaks, so Core's
// existing /api/canvas/project/* handlers and the CanvasPreview UI work
// unchanged across both bridges.
//
// Design (single-user → ONE active preview at a time, mirroring the Mac
// supervisor's "active project"):
//
//   - /supervisor/start   {project_path, template}  → bring a preview up
//   - /supervisor/stop                              → tear it down
//   - /supervisor/status  ?project_path=            → {status, port, …}
//   - /supervisor/active  GET current / POST set+start
//   - /api/canvas/preview/* (browser-facing, via Core's reverse proxy)
//       · static templates → serve the project's files, injecting a <base>
//         tag so relative assets resolve under the proxy prefix
//       · dev templates (vite/next/node) → reverse-proxy to the dev server
//         on localhost, INCLUDING WebSocket upgrades so HMR is live
//
// The public path prefix (previewPrefix) is preserved end-to-end: Core proxies
// /api/canvas/preview/* to the bridge unchanged, the bridge serves under the
// same prefix, and dev servers are launched with that prefix as their base
// (vite --base, next basePath). One path, no rewriting, HMR intact.
package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	// previewPrefix is the public path Core proxies and dev servers base on.
	// Kept in one place so the bridge, the dev-server launch flags, and the
	// <base> injection can't drift apart.
	previewPrefix = "/api/canvas/preview"
	// devPort is the single internal port the active dev server binds. One
	// active preview → one port; no allocation table needed.
	devPort     = 4400
	devBootWait = 120 * time.Second // npm install + first compile headroom
)

type projStatus string

const (
	statusIdle    projStatus = "idle"
	statusBooting projStatus = "booting"
	statusRunning projStatus = "running"
	statusCrashed projStatus = "crashed"
)

type previewProject struct {
	Path      string
	Template  string
	Status    projStatus
	Port      int
	LastError string
	static    bool
	cmd       *exec.Cmd
	proxy     *httputil.ReverseProxy
}

type supervisor struct {
	mu     sync.Mutex
	active *previewProject
}

var sup = &supervisor{}

// staticTemplate reports whether a template is served as plain files (no dev
// server process). Empty / unknown templates and an absence of package.json
// both fall here - the safe default, since a bare index.html is the most
// common thing the boss builds.
func staticTemplate(tmpl, projectPath string) bool {
	switch strings.ToLower(strings.TrimSpace(tmpl)) {
	case "nextjs", "vite-react", "node":
		return false
	case "static-html", "empty", "":
		// Even an "empty" project is static unless it grew a package.json.
		return !fileExists(filepath.Join(projectPath, "package.json"))
	default:
		// go / python / anything else: only treat as a dev server if it has a
		// package.json with a dev script; otherwise serve files.
		return !fileExists(filepath.Join(projectPath, "package.json"))
	}
}

// devCommand builds the dev-server command for a node project, launched under
// the preview base path so its emitted asset + HMR URLs match what the browser
// requests through Core's proxy. Returns nil for templates we don't dev-serve.
func devCommand(tmpl, projectPath string) *exec.Cmd {
	base := previewPrefix + "/"
	var c *exec.Cmd
	switch strings.ToLower(strings.TrimSpace(tmpl)) {
	case "vite-react":
		// Vite honors --base for asset/HMR URLs and --port/--host directly.
		c = exec.Command("npm", "run", "dev", "--",
			"--host", "127.0.0.1", "--port", fmt.Sprint(devPort),
			"--strictPort", "--base", base)
	case "nextjs":
		// Next reads basePath/assetPrefix from next.config; the scaffold wires
		// it from NEXT_PUBLIC_BASE_PATH. We still pass the port via CLI.
		c = exec.Command("npm", "run", "dev", "--", "--port", fmt.Sprint(devPort))
	default:
		// Generic node project: best-effort `npm run dev`. PORT + BASE_PATH
		// envs let a custom server pick them up.
		c = exec.Command("npm", "run", "dev")
	}
	c.Dir = projectPath
	c.Env = append(os.Environ(),
		"PORT="+fmt.Sprint(devPort),
		"BASE_PATH="+previewPrefix,
		"NEXT_PUBLIC_BASE_PATH="+previewPrefix,
		"BROWSER=none", "CI=1",
	)
	// New process group so stop() can kill the whole tree (npm → node → esbuild).
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	return c
}

// Start brings a preview up for the given project. Static projects are ready
// immediately; dev projects boot asynchronously (npm install + first compile)
// and report status via Status while they warm. Idempotent: starting the
// already-active project is a no-op that returns the current state.
func (s *supervisor) Start(projectPath, template string) previewProject {
	resolved := resolveProjectDir(projectPath)
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.active != nil && s.active.Path == resolved && s.active.Status != statusCrashed {
		return *s.active
	}
	s.stopLocked()

	if staticTemplate(template, resolved) {
		s.active = &previewProject{Path: resolved, Template: template, Status: statusRunning, static: true}
		log.Printf("supervisor: static preview ready for %s", resolved)
		return *s.active
	}

	p := &previewProject{Path: resolved, Template: template, Status: statusBooting, Port: devPort}
	target, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", devPort))
	p.proxy = newDevProxy(target)
	s.active = p
	go s.boot(p)
	log.Printf("supervisor: booting dev preview for %s (%s)", resolved, template)
	return *p
}

// boot installs deps if needed, launches the dev server, and flips status to
// running once the port answers (or crashed on failure). Runs in its own
// goroutine; mutates the active project under the lock.
func (s *supervisor) boot(p *previewProject) {
	if fileExists(filepath.Join(p.Path, "package.json")) && !fileExists(filepath.Join(p.Path, "node_modules")) {
		install := exec.Command("npm", "install", "--no-audit", "--no-fund")
		install.Dir = p.Path
		if out, err := install.CombinedOutput(); err != nil {
			s.fail(p, fmt.Sprintf("npm install failed: %s", lastLine(string(out))))
			return
		}
	}
	cmd := devCommand(p.Template, p.Path)
	if cmd == nil {
		s.fail(p, "no dev command for template "+p.Template)
		return
	}
	stderr, _ := cmd.StderrPipe()
	if err := cmd.Start(); err != nil {
		s.fail(p, "dev server start failed: "+err.Error())
		return
	}
	s.mu.Lock()
	p.cmd = cmd
	s.mu.Unlock()

	// Drain stderr into LastError tail so a crash surfaces a reason.
	go func() {
		b, _ := io.ReadAll(io.LimitReader(stderr, 8<<10))
		if len(b) > 0 {
			s.mu.Lock()
			if p.Status != statusRunning {
				p.LastError = lastLine(string(b))
			}
			s.mu.Unlock()
		}
	}()
	// Reap: if the process exits before we mark running, it crashed.
	go func() {
		_ = cmd.Wait()
		s.mu.Lock()
		if s.active == p {
			p.Status = statusCrashed
			if p.LastError == "" {
				p.LastError = "dev server exited"
			}
		}
		s.mu.Unlock()
	}()

	// Poll readiness on the dev port.
	deadline := time.Now().Add(devBootWait)
	for time.Now().Before(deadline) {
		if dialOK(devPort) {
			s.mu.Lock()
			if s.active == p && p.Status == statusBooting {
				p.Status = statusRunning
				log.Printf("supervisor: dev preview ready on :%d for %s", devPort, p.Path)
			}
			s.mu.Unlock()
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
	s.fail(p, "dev server did not become ready within "+devBootWait.String())
}

func (s *supervisor) fail(p *previewProject, msg string) {
	s.mu.Lock()
	if s.active == p {
		p.Status = statusCrashed
		p.LastError = msg
	}
	s.mu.Unlock()
	log.Printf("supervisor: %s", msg)
}

// Stop tears down the active preview (kills the dev process group).
func (s *supervisor) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopLocked()
}

func (s *supervisor) stopLocked() {
	if s.active == nil {
		return
	}
	if s.active.cmd != nil && s.active.cmd.Process != nil {
		// Negative pid → kill the whole process group.
		_ = syscall.Kill(-s.active.cmd.Process.Pid, syscall.SIGKILL)
	}
	s.active = nil
}

// Status returns the current active preview snapshot (zero-value Idle when
// nothing is running). project_path is accepted for parity with the Mac
// supervisor but ignored - there's only one active preview.
func (s *supervisor) Status() previewProject {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return previewProject{Status: statusIdle}
	}
	return *s.active
}

// serve handles a browser-facing /api/canvas/preview/* request: static file
// serving (with <base> injection) or a reverse proxy to the dev server.
func (s *supervisor) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	p := s.active
	s.mu.Unlock()
	if p == nil || p.Status == statusIdle {
		writePreviewSplash(w, "No preview running", "Ask Jarvis to build something, then it shows up here.")
		return
	}
	if p.static {
		s.serveStatic(w, r, p)
		return
	}
	if p.Status == statusBooting {
		writePreviewSplash(w, "Warming up the dev server…", "First boot installs dependencies; this can take a moment.")
		return
	}
	if p.Status == statusCrashed || p.proxy == nil {
		writePreviewSplash(w, "Dev server crashed", p.LastError)
		return
	}
	p.proxy.ServeHTTP(w, r)
}

// serveStatic maps /api/canvas/preview/<rest> → <projectdir>/<rest>, defaulting
// to index.html, and injects a <base> tag into HTML so the page's relative
// asset/link URLs resolve under the proxy prefix.
func (s *supervisor) serveStatic(w http.ResponseWriter, r *http.Request, p *previewProject) {
	rest := strings.TrimPrefix(r.URL.Path, previewPrefix)
	rest = strings.TrimPrefix(rest, "/")
	if rest == "" {
		rest = "index.html"
	}
	full := filepath.Join(p.Path, filepath.Clean("/"+rest))
	// Confine to the project dir - reject traversal.
	if !strings.HasPrefix(full, filepath.Clean(p.Path)+string(os.PathSeparator)) && full != filepath.Clean(p.Path) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	info, err := os.Stat(full)
	if err == nil && info.IsDir() {
		full = filepath.Join(full, "index.html")
		info, err = os.Stat(full)
	}
	if err != nil {
		// SPA fallback: serve index.html so client-side routes resolve.
		idx := filepath.Join(p.Path, "index.html")
		if _, ierr := os.Stat(idx); ierr == nil {
			full = idx
		} else {
			http.NotFound(w, r)
			return
		}
	}
	if strings.HasSuffix(strings.ToLower(full), ".html") || strings.HasSuffix(strings.ToLower(full), ".htm") {
		writeHTMLWithBase(w, full)
		return
	}
	http.ServeFile(w, r, full)
}

// newDevProxy builds a reverse proxy to the dev server that preserves the
// request path (so the dev server's base path matches) and transparently
// forwards WebSocket upgrades for HMR.
func newDevProxy(target *url.URL) *httputil.ReverseProxy {
	rp := httputil.NewSingleHostReverseProxy(target)
	// Default Director rewrites Host; keep the path untouched (base-aligned).
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writePreviewSplash(w, "Dev server unavailable", "It may still be starting or has crashed.")
	}
	rp.FlushInterval = 100 * time.Millisecond // stream SSE/HMR promptly
	return rp
}

// ── helpers ─────────────────────────────────────────────────────────────────

func resolveProjectDir(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return workspaceRoot
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(workspaceRoot, p)
	}
	return filepath.Clean(p)
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

func dialOK(port int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// writeHTMLWithBase serves an HTML file with a <base href="{previewPrefix}/">
// injected right after <head> (or prepended) so relative URLs resolve under
// the proxy. Idempotent-ish: if the doc already declares a <base>, we leave it.
func writeHTMLWithBase(w http.ResponseWriter, path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "read error", http.StatusInternalServerError)
		return
	}
	html := string(b)
	baseTag := fmt.Sprintf(`<base href="%s/">`, previewPrefix)
	if !strings.Contains(strings.ToLower(html), "<base ") {
		if i := strings.Index(strings.ToLower(html), "<head>"); i >= 0 {
			html = html[:i+len("<head>")] + baseTag + html[i+len("<head>"):]
		} else {
			html = baseTag + html
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = io.WriteString(w, html)
}

func writePreviewSplash(w http.ResponseWriter, title, sub string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	fmt.Fprintf(w, `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title></head><body style="margin:0;height:100vh;display:flex;align-items:center;justify-content:center;font-family:system-ui,-apple-system,sans-serif;background:#0a0a0a;color:#e5e5e5"><div style="text-align:center;max-width:28rem;padding:1.5rem"><div style="font-size:1rem;font-weight:600;margin-bottom:.5rem">%s</div><div style="font-size:.8125rem;color:#a3a3a3;line-height:1.5">%s</div></div></body></html>`, title, title, sub)
}

// ── HTTP handlers (wired in main) ─────────────────────────────────────────────

func handleSupervisorStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ProjectPath string `json:"project_path"`
		Template    string `json:"template"`
	}
	if err := readJSON(r, &body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	p := sup.Start(body.ProjectPath, body.Template)
	writeJSON(w, http.StatusOK, projectDTO(p))
}

func handleSupervisorStop(w http.ResponseWriter, r *http.Request) {
	sup.Stop()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func handleSupervisorStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, projectDTO(sup.Status()))
}

// handleSupervisorActive: GET current, POST {project_path, template} sets+starts.
func handleSupervisorActive(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		handleSupervisorStart(w, r)
		return
	}
	writeJSON(w, http.StatusOK, projectDTO(sup.Status()))
}

// handlePreview is the browser-facing surface (via Core's proxy).
func handlePreview(w http.ResponseWriter, r *http.Request) {
	sup.serve(w, r)
}

// projectDTO matches the shape Core's CanvasPreview/useCurrentProject expect
// (status + port + last_error), so cloud and Mac supervisors are interchangeable.
func projectDTO(p previewProject) map[string]any {
	return map[string]any{
		"project_path": p.Path,
		"template":     p.Template,
		"status":       string(p.Status),
		"port":         p.Port,
		"last_error":   p.LastError,
		// Tags the response as cloud-served so Studio points the preview iframe
		// at Core's /api/canvas/preview proxy instead of the Mac tunnel.
		"bridge": "cloud",
	}
}
