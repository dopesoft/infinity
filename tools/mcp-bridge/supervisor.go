// supervisor.go — per-project dev-server lifecycle and a reverse-proxy
// switch keyed on the currently active project.
//
// The boss does not start dev servers. When they ask the agent to "build me a
// chat app", the agent attaches a project to the session (via Core's
// /api/sessions/:id/project endpoint) and then asks the bridge supervisor
// to bring that project's dev server up. Studio's Canvas preview iframe
// always points at `preview.dopesoft.io`; this file's reverse-proxy decides
// which local port that traffic lands on.
//
// Lifecycle for one project:
//
//	idle → booting → running → (crashed → backoff → booting) → idle
//
// Warm pool: the top N=3 most recently-active projects stay running. Older
// projects idle after `idleTimeout` (30 min by default) so the boss's Mac
// doesn't accumulate dev servers.

package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	warmPoolSize       = 3
	idleTimeout        = 30 * time.Minute
	bootReadyDeadline  = 90 * time.Second
	crashBackoff       = 5 * time.Second
	staticHTMLBasePort = 5050
)

// projectStatus is the public-facing lifecycle string Studio renders.
type projectStatus string

const (
	statusIdle     projectStatus = "idle"
	statusBooting  projectStatus = "booting"
	statusRunning  projectStatus = "running"
	statusCrashed  projectStatus = "crashed"
)

type project struct {
	Path     string        `json:"project_path"`
	Template string        `json:"template,omitempty"`
	Port     int           `json:"dev_port,omitempty"`
	Status   projectStatus `json:"status"`
	StartedAt *time.Time   `json:"started_at,omitempty"`
	LastReadyAt *time.Time `json:"last_ready_at,omitempty"`
	LastError string       `json:"last_error,omitempty"`
	LastUsed time.Time     `json:"last_used"`

	cmd       *exec.Cmd
	stopCh    chan struct{}
	readyOnce sync.Once
	mu        sync.Mutex
}

type supervisor struct {
	mu       sync.Mutex
	projects map[string]*project // keyed by absolute path
	active   string              // path of the project the reverse-proxy targets
	idleTick *time.Ticker
}

func newSupervisor() *supervisor {
	s := &supervisor{
		projects: map[string]*project{},
		idleTick: time.NewTicker(5 * time.Minute),
	}
	go s.idleLoop()
	return s
}

// idleLoop walks the project set every few minutes and shuts down anything
// older than idleTimeout that isn't in the warm pool. The active project
// is exempt — Studio expects it ready immediately.
func (s *supervisor) idleLoop() {
	for range s.idleTick.C {
		s.mu.Lock()
		warm := s.warmSet()
		now := time.Now()
		var toStop []*project
		for path, p := range s.projects {
			if path == s.active {
				continue
			}
			if _, isWarm := warm[path]; isWarm {
				continue
			}
			if now.Sub(p.LastUsed) > idleTimeout && p.Status == statusRunning {
				toStop = append(toStop, p)
			}
		}
		s.mu.Unlock()
		for _, p := range toStop {
			log.Printf("supervisor: idling %s (last used %s ago)", p.Path, time.Since(p.LastUsed).Truncate(time.Second))
			s.stop(p.Path)
		}
	}
}

// warmSet returns the top N most-recently-used project paths. Caller must
// hold s.mu.
func (s *supervisor) warmSet() map[string]struct{} {
	type pair struct {
		path string
		ts   time.Time
	}
	all := make([]pair, 0, len(s.projects))
	for path, p := range s.projects {
		all = append(all, pair{path, p.LastUsed})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ts.After(all[j].ts) })
	out := map[string]struct{}{}
	for i := 0; i < len(all) && i < warmPoolSize; i++ {
		out[all[i].path] = struct{}{}
	}
	return out
}

// ensure brings the named project to `running`, starting it if necessary.
// Idempotent: callers can poke this on every session activation.
func (s *supervisor) ensure(path, template string) (*project, error) {
	if path == "" {
		return nil, errors.New("project_path required")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("abs: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("project not found: %s", abs)
	}

	s.mu.Lock()
	p, ok := s.projects[abs]
	if !ok {
		p = &project{Path: abs, Template: template, Status: statusIdle, LastUsed: time.Now()}
		s.projects[abs] = p
	} else {
		p.LastUsed = time.Now()
		if template != "" {
			p.Template = template
		}
	}
	s.mu.Unlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	switch p.Status {
	case statusRunning, statusBooting:
		return p, nil
	}

	// Detect template + dev command + port from disk every time we start —
	// the boss may have pivoted from vite to next without telling us.
	templ, cmd, args, port, err := detectRunner(p.Path, p.Template)
	if err != nil {
		p.LastError = err.Error()
		p.Status = statusIdle
		return p, err
	}
	p.Template = templ
	p.Port = port

	if err := s.startCmd(p, cmd, args); err != nil {
		p.LastError = err.Error()
		p.Status = statusCrashed
		return p, err
	}
	return p, nil
}

// startCmd boots the dev server. The caller already holds p.mu. We do NOT
// wait for the ready signal here — the watcher goroutine flips Status from
// booting → running when the regex hits, and the reverse-proxy retries
// against the port until then.
func (s *supervisor) startCmd(p *project, name string, args []string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = p.Path
	cmd.Env = append(os.Environ(),
		"PORT="+strconv.Itoa(p.Port),
		"CI=true",   // disable interactive prompts on first run
		"FORCE_COLOR=0",
	)
	// New process group so we can kill the dev server's children too.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", name, err)
	}

	now := time.Now()
	p.cmd = cmd
	p.Status = statusBooting
	p.StartedAt = &now
	p.LastError = ""
	p.stopCh = make(chan struct{})
	p.readyOnce = sync.Once{}

	log.Printf("supervisor: started %s template=%s port=%d pid=%d", p.Path, p.Template, p.Port, cmd.Process.Pid)

	go s.pumpOutput(p, "stdout", stdout)
	go s.pumpOutput(p, "stderr", stderr)
	go s.watchExit(p)

	// Deadline timer: if no ready signal lands by bootReadyDeadline, flag
	// the project so Studio can show "still booting" / "stuck booting".
	go func(p *project) {
		select {
		case <-time.After(bootReadyDeadline):
			p.mu.Lock()
			if p.Status == statusBooting {
				p.LastError = "boot ready signal missed; check the project's dev server output"
				log.Printf("supervisor: %s missed ready deadline (still booting)", p.Path)
			}
			p.mu.Unlock()
		case <-p.stopCh:
		}
	}(p)

	return nil
}

// readyRegex matches the most common dev-server ready logs across the
// scaffolds we support. We don't try to be cute — first hit wins.
var readyRegex = regexp.MustCompile(`(?i)(ready in|local:|listening on|started server on|server running at|compiled successfully|✓ ready|webpack compiled)`)

func (s *supervisor) pumpOutput(p *project, stream string, r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 8<<20)
	for scanner.Scan() {
		line := scanner.Text()
		log.Printf("supervisor[%s][%s]: %s", filepath.Base(p.Path), stream, line)
		if readyRegex.MatchString(line) {
			p.readyOnce.Do(func() {
				p.mu.Lock()
				p.Status = statusRunning
				t := time.Now()
				p.LastReadyAt = &t
				p.mu.Unlock()
				log.Printf("supervisor: %s ready on port %d", p.Path, p.Port)
			})
		}
	}
}

func (s *supervisor) watchExit(p *project) {
	if err := p.cmd.Wait(); err != nil {
		log.Printf("supervisor: %s exited: %v", p.Path, err)
		p.mu.Lock()
		if p.Status != statusIdle { // only flip if we didn't stop deliberately
			p.Status = statusCrashed
			p.LastError = err.Error()
		}
		p.mu.Unlock()
	} else {
		log.Printf("supervisor: %s exited cleanly", p.Path)
		p.mu.Lock()
		p.Status = statusIdle
		p.mu.Unlock()
	}
	close(p.stopCh)
}

func (s *supervisor) stop(path string) error {
	abs, _ := filepath.Abs(path)
	s.mu.Lock()
	p, ok := s.projects[abs]
	s.mu.Unlock()
	if !ok {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd == nil || p.cmd.Process == nil {
		p.Status = statusIdle
		return nil
	}
	p.Status = statusIdle
	// Kill the entire process group so child watchers (Next/Vite spawn
	// subprocesses) actually die.
	pgid := p.cmd.Process.Pid
	_ = syscall.Kill(-pgid, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		<-p.stopCh
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	return nil
}

// detectRunner inspects the on-disk project and returns a runnable command.
// templateHint is a soft suggestion from the agent — we prefer disk evidence
// over the hint (which can drift if the project pivots between frameworks).
func detectRunner(path, templateHint string) (template, cmd string, args []string, port int, err error) {
	pkgPath := filepath.Join(path, "package.json")
	if data, e := os.ReadFile(pkgPath); e == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		if json.Unmarshal(data, &pkg) == nil {
			dev := strings.ToLower(strings.TrimSpace(pkg.Scripts["dev"]))
			switch {
			case strings.Contains(dev, "next dev"):
				return "nextjs", "pnpm", []string{"dev"}, 3000, nil
			case strings.Contains(dev, "vite"):
				return "vite", "pnpm", []string{"dev"}, 5173, nil
			case strings.Contains(dev, "expo start"):
				return "expo", "pnpm", []string{"dev"}, 8081, nil
			case strings.Contains(dev, "astro dev"):
				return "astro", "pnpm", []string{"dev"}, 4321, nil
			case strings.Contains(dev, "remix dev"):
				return "remix", "pnpm", []string{"dev"}, 3000, nil
			}
			// Fallback: whatever `pnpm dev` is, we'll try it.
			if dev != "" {
				return "nodejs", "pnpm", []string{"dev"}, 3000, nil
			}
		}
	}
	if _, e := os.Stat(filepath.Join(path, "index.html")); e == nil {
		// Static HTML — the bridge serves it directly via a tiny embedded
		// file server. The "command" is a sentinel string the start path
		// recognises; we don't actually exec anything.
		return "static-html", "(builtin-static)", nil, staticHTMLBasePort, nil
	}
	if _, e := os.Stat(filepath.Join(path, "Package.swift")); e == nil {
		return "ios-swift", "", nil, 0, errors.New("iOS projects build via Xcode — open Package.swift on the Mac")
	}
	if templateHint != "" {
		return templateHint, "", nil, 0, fmt.Errorf("template %q recognised but project layout doesn't match", templateHint)
	}
	return "", "", nil, 0, errors.New("no recognised project layout (missing package.json / index.html / Package.swift)")
}

// activeProject returns the current target for the reverse-proxy. Nil-safe.
func (s *supervisor) activeProject() *project {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == "" {
		return nil
	}
	return s.projects[s.active]
}

// setActive promotes the named project as the reverse-proxy target. Starts
// the project if it's not running.
func (s *supervisor) setActive(path, template string) (*project, error) {
	p, err := s.ensure(path, template)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.active = p.Path
	s.mu.Unlock()
	return p, nil
}

// list returns a snapshot of every tracked project. Used by Studio for the
// project picker / debug surface.
func (s *supervisor) list() []*project {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*project, 0, len(s.projects))
	for _, p := range s.projects {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastUsed.After(out[j].LastUsed) })
	return out
}

// ---- HTTP -----------------------------------------------------------------

func (b *bridge) handleSupervisorStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ProjectPath string `json:"project_path"`
		Template    string `json:"template"`
		Activate    bool   `json:"activate"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	var (
		p   *project
		err error
	)
	if body.Activate {
		p, err = b.supervisor.setActive(body.ProjectPath, body.Template)
	} else {
		p, err = b.supervisor.ensure(body.ProjectPath, body.Template)
	}
	if err != nil {
		writeJSONResponse(w, map[string]any{
			"ok":    false,
			"error": err.Error(),
		})
		return
	}
	writeJSONResponse(w, p)
}

func (b *bridge) handleSupervisorStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		ProjectPath string `json:"project_path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if err := b.supervisor.stop(body.ProjectPath); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, map[string]any{"ok": true})
}

func (b *bridge) handleSupervisorActive(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		p := b.supervisor.activeProject()
		if p == nil {
			writeJSONResponse(w, map[string]any{"active": nil})
			return
		}
		writeJSONResponse(w, p)
	case http.MethodPost:
		var body struct {
			ProjectPath string `json:"project_path"`
			Template    string `json:"template"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid JSON", http.StatusBadRequest)
			return
		}
		p, err := b.supervisor.setActive(body.ProjectPath, body.Template)
		if err != nil {
			writeJSONResponse(w, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		writeJSONResponse(w, p)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (b *bridge) handleSupervisorStatus(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("project_path")
	if path == "" {
		writeJSONResponse(w, map[string]any{"projects": b.supervisor.list()})
		return
	}
	abs, _ := filepath.Abs(path)
	b.supervisor.mu.Lock()
	p, ok := b.supervisor.projects[abs]
	b.supervisor.mu.Unlock()
	if !ok {
		writeJSONResponse(w, map[string]any{"status": "idle", "project_path": abs})
		return
	}
	writeJSONResponse(w, p)
}

// handlePreviewProxy is the reverse-proxy. Any HTTP request that wasn't
// already routed to /sse, /messages, /fs/*, /git/*, /supervisor/* falls
// through here. We forward it to the active project's local dev port.
//
// The Cloudflare tunnel pointing preview.dopesoft.io → 127.0.0.1:8765
// makes this the boss's preview surface. No new DNS, no new tunnels —
// just swap targets per session.
func (b *bridge) handlePreviewProxy(w http.ResponseWriter, r *http.Request) {
	p := b.supervisor.activeProject()
	if p == nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, previewEmptyHTML("No project yet — tell the agent what to build."))
		return
	}
	if p.Status == statusBooting {
		w.Header().Set("Retry-After", "2")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, previewBootingHTML(p))
		return
	}
	if p.Status == statusCrashed || p.Status == statusIdle {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, previewCrashedHTML(p))
		return
	}

	// Special-case static-html: serve files directly from disk.
	if p.Template == "static-html" {
		http.FileServer(http.Dir(p.Path)).ServeHTTP(w, r)
		return
	}

	target, err := url.Parse(fmt.Sprintf("http://127.0.0.1:%d", p.Port))
	if err != nil {
		http.Error(w, "bad target", http.StatusInternalServerError)
		return
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	proxy.ErrorHandler = func(rw http.ResponseWriter, req *http.Request, err error) {
		log.Printf("supervisor: proxy err %s %s: %v", p.Path, req.URL.Path, err)
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.WriteHeader(http.StatusBadGateway)
		_, _ = fmt.Fprint(rw, previewBootingHTML(p))
	}
	proxy.ModifyResponse = func(resp *http.Response) error {
		// Strip the X-Frame-Options header dev servers sometimes set, so
		// the Studio iframe can embed the preview.
		resp.Header.Del("X-Frame-Options")
		resp.Header.Del("Content-Security-Policy")
		return nil
	}
	proxy.ServeHTTP(w, r)
}

func previewEmptyHTML(msg string) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>Canvas preview</title>
<style>html,body{height:100%;margin:0;font:14px system-ui;background:#0a0a0a;color:#888;display:flex;align-items:center;justify-content:center}</style>
</head><body><div>` + msg + `</div></body></html>`
}

func previewBootingHTML(p *project) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>Booting…</title>
<meta http-equiv="refresh" content="2">
<style>html,body{height:100%;margin:0;font:14px system-ui;background:#0a0a0a;color:#aaa;display:flex;align-items:center;justify-content:center}.b{text-align:center}.s{display:inline-block;width:18px;height:18px;border:2px solid #444;border-top-color:#bbb;border-radius:50%;animation:spin 0.8s linear infinite;margin-right:8px;vertical-align:middle}@keyframes spin{to{transform:rotate(360deg)}}</style>
</head><body><div class="b"><span class="s"></span>Booting ` + p.Template + ` dev server on port ` + strconv.Itoa(p.Port) + `…</div></body></html>`
}

func previewCrashedHTML(p *project) string {
	return `<!doctype html><html><head><meta charset="utf-8"><title>Crashed</title>
<style>html,body{height:100%;margin:0;font:14px system-ui;background:#0a0a0a;color:#f88;display:flex;align-items:center;justify-content:center}</style>
</head><body><div>Dev server crashed: ` + escapeHTML(p.LastError) + `</div></body></html>`
}

func escapeHTML(s string) string {
	r := strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", "\"", "&quot;")
	return r.Replace(s)
}
