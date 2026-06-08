package workflow

import (
	"encoding/json"
	"net/http"
	"strings"
)

// API exposes /api/workflows so the Studio Workflows tab can list saved
// DEFINITIONS (the gap: only runs surfaced before, transiently, on the work
// board) and run one with collected inputs. The engine, store, and run
// surfacing already exist — this is the read + run-trigger surface.
type API struct {
	store *Store
}

func NewAPI(store *Store) *API { return &API{store: store} }

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/workflows", a.handleList)
	mux.HandleFunc("/api/workflows/run", a.handleRun)
}

// handleList: GET /api/workflows → every saved definition (newest first),
// including its declared inputs schema so the Run form can build itself.
func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a == nil || a.store == nil {
		writeJSON(w, http.StatusOK, []*Workflow{})
		return
	}
	wfs, err := a.store.ListWorkflows(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if wfs == nil {
		wfs = []*Workflow{}
	}
	writeJSON(w, http.StatusOK, wfs)
}

type runReq struct {
	Workflow string         `json:"workflow"`
	Input    map[string]any `json:"input"`
}

// handleRun: POST /api/workflows/run {workflow, input} → starts a run with the
// collected inputs (the Workflows-tab Run form's submit). The engine claims it
// on the next tick and it surfaces on the Agent Work board like any run.
func (a *API) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a == nil || a.store == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workflows not configured"})
		return
	}
	var req runReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	name := strings.TrimSpace(req.Workflow)
	if name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "workflow name required"})
		return
	}
	wf, err := a.store.GetWorkflow(r.Context(), name)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if wf == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "no saved workflow named " + name})
		return
	}
	// Enforce the declared schema: every required input must be present.
	for _, def := range wf.Inputs {
		if !def.Required {
			continue
		}
		v, ok := req.Input[def.Key]
		if !ok || strings.TrimSpace(stringify(v)) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing required input: " + def.Key})
			return
		}
	}
	run, err := a.store.StartRun(r.Context(), wf.ID, wf.Name, wf.Steps, req.Input, "manual", "")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "run_id": run.ID, "status": string(run.Status)})
}

func stringify(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	if v == nil {
		return ""
	}
	b, _ := json.Marshal(v)
	return string(b)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
