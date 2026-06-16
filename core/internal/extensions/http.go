package extensions

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
)

// API exposes /api/extensions for Studio's Settings → Connectors → Custom
// surface. It's the MANUAL self-register path (source=manual); the agent
// uses the extension_* tools for the autonomous path. Both land in the same
// mem_extensions store and activate through the same Manager.
type API struct {
	mgr *Manager
}

func NewAPI(mgr *Manager) *API {
	if mgr == nil {
		return nil // server's `if cfg.ExtensionsAPI != nil` skips mounting
	}
	return &API{mgr: mgr}
}

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/extensions", a.handleCollection) // GET list · POST register
	mux.HandleFunc("/api/extensions/", a.handleItem)      // POST .../remove · .../check
}

type extensionDTO struct {
	ID               string         `json:"id"`
	Name             string         `json:"name"`
	Kind             string         `json:"kind"`
	Description      string         `json:"description"`
	Config           map[string]any `json:"config,omitempty"`
	Enabled          bool           `json:"enabled"`
	Source           string         `json:"source"`
	Status           string         `json:"status"`
	LastError        string         `json:"last_error,omitempty"`
	AuthURL          string         `json:"auth_url,omitempty"`
	AuthInstructions string         `json:"auth_instructions,omitempty"`
	ResumeIntent     string         `json:"resume_intent,omitempty"`
	ToolName         string         `json:"tool_name,omitempty"`
	LastCheckedAt    *string        `json:"last_checked_at,omitempty"`
	CreatedAt        string         `json:"created_at"`
	UpdatedAt        string         `json:"updated_at"`
}

func toDTO(e *Extension) extensionDTO {
	d := extensionDTO{
		ID:               e.ID,
		Name:             e.Name,
		Kind:             string(e.Kind),
		Description:      e.Description,
		Config:           e.Config,
		Enabled:          e.Enabled,
		Source:           e.Source,
		Status:           string(e.Status),
		LastError:        e.LastError,
		AuthURL:          e.AuthURL,
		AuthInstructions: e.AuthInstructions,
		ResumeIntent:     e.ResumeIntent,
		CreatedAt:        e.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:        e.UpdatedAt.UTC().Format(time.RFC3339),
	}
	if e.Kind == KindHTTPTool {
		d.ToolName = extToolName(e.Name)
	}
	if e.LastCheckedAt != nil {
		s := e.LastCheckedAt.UTC().Format(time.RFC3339)
		d.LastCheckedAt = &s
	}
	return d
}

func (a *API) handleCollection(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		a.list(w, r)
	case http.MethodPost:
		a.register(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) list(w http.ResponseWriter, r *http.Request) {
	if a == nil || a.mgr == nil {
		writeJSON(w, http.StatusOK, map[string]any{"extensions": []extensionDTO{}})
		return
	}
	exts, err := a.mgr.List(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	out := make([]extensionDTO, 0, len(exts))
	for _, e := range exts {
		out = append(out, toDTO(e))
	}
	writeJSON(w, http.StatusOK, map[string]any{"extensions": out})
}

type registerReq struct {
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	Description  string         `json:"description"`
	Config       map[string]any `json:"config"`
	ResumeIntent string         `json:"resume_intent"`
}

func (a *API) register(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	if req.Name == "" {
		http.Error(w, "name is required", http.StatusBadRequest)
		return
	}
	kind := Kind(strings.TrimSpace(req.Kind))
	if !kind.Valid() {
		http.Error(w, "invalid kind (want mcp | http_tool | cli)", http.StatusBadRequest)
		return
	}
	cfg := req.Config
	if cfg == nil {
		cfg = map[string]any{}
	}
	ext := &Extension{
		Name:         req.Name,
		Kind:         kind,
		Description:  req.Description,
		Config:       cfg,
		Source:       "manual",
		ResumeIntent: strings.TrimSpace(req.ResumeIntent),
	}

	// A cli install can run for minutes (download + build). Per the
	// server-tracked-progress rule, never block the request on it: book a
	// mem_runs row and activate in the background. Studio's RunIndicator
	// (useRuns kind=extension.register) shows live progress and survives
	// navigation/refresh. mcp/http_tool activation is fast → synchronous.
	if kind == KindCLI {
		bg := context.WithoutCancel(r.Context())
		go func() {
			_ = runs.Track(bg, runs.KindExtension, ext.Name, "install "+ext.Name, runs.SourceManual,
				func(c context.Context) error { return a.mgr.Register(c, ext) })
		}()
		writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "async": true, "name": ext.Name, "status": "installing"})
		return
	}

	// Register activates + persists. Synchronous for mcp/http_tool.
	if err := a.mgr.Register(r.Context(), ext); err != nil {
		// Activation failed (e.g. bad MCP URL) - the row is still persisted
		// as status=error so the boss sees why. Surface it.
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "extension": toDTO(ext)})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "extension": toDTO(ext)})
}

func (a *API) handleItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/extensions/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	name, action := parts[0], parts[1]
	switch action {
	case "remove":
		if err := a.mgr.Remove(r.Context(), name); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": name, "status": "disabled"})
	case "check":
		a.check(w, r, name)
	case "activate":
		a.activate(w, r, name)
	default:
		http.Error(w, "unknown action", http.StatusNotFound)
	}
}

// activate drives a cli extension's install + auth flow on demand so the
// device-login URL is captured without waiting for a Core reboot. The install +
// auth probe can take minutes (download, cloud cold-start), so per the
// server-tracked-progress rule we never block the request: book a mem_runs row
// and run in the background. Studio's CanvasAuthCard subscribes to the
// mem_extensions realtime channel and re-renders the moment auth_url lands.
func (a *API) activate(w http.ResponseWriter, r *http.Request, name string) {
	if a == nil || a.mgr == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "extensions not configured"})
		return
	}
	bg := context.WithoutCancel(r.Context())
	go func() {
		_ = runs.Track(bg, runs.KindExtension, name, "sign-in setup: "+name, runs.SourceManual,
			func(c context.Context) error {
				_, err := a.mgr.Activate(c, name)
				return err
			})
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"ok": true, "async": true, "name": name, "status": "activating"})
}

func (a *API) check(w http.ResponseWriter, r *http.Request, name string) {
	ext, err := a.mgr.store.GetByName(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if ext == nil {
		http.Error(w, "no such extension", http.StatusNotFound)
		return
	}
	if ext.Kind != KindCLI {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "extension": toDTO(ext)})
		return
	}
	ready, perr := a.mgr.ProbeCLIReady(r.Context(), ext)
	if perr != nil {
		writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": perr.Error(), "extension": toDTO(ext)})
		return
	}
	if ready {
		_ = a.mgr.CompleteAuth(r.Context(), name)
	} else {
		a.mgr.touchChecked(r.Context(), name)
	}
	fresh, _ := a.mgr.store.GetByName(r.Context(), name)
	if fresh == nil {
		fresh = ext
	}
	// Auth just completed via this probe -> resume the agent automatically so
	// the boss doesn't have to message "ok, I'm signed in". The heartbeat
	// checklist would otherwise miss it (the row is already active now).
	if ready {
		a.mgr.fireAuthComplete(r.Context(), fresh)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ready": ready, "extension": toDTO(fresh)})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
