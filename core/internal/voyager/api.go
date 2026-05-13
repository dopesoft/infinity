package voyager

import (
	"encoding/json"
	"net/http"
	"strings"
)

// API exposes the Voyager subsystem over HTTP. Mounted in serve.go via Routes.
type API struct{ m *Manager }

func NewAPI(m *Manager) *API { return &API{m: m} }

// Routes registers handlers. Endpoints:
//
//	GET  /api/voyager/status                       — manager status + counters
//	GET  /api/voyager/proposals?status=X           — list skill proposals
//	POST /api/voyager/proposals/{id}/decide        — { "decision": "promoted" | "rejected" }
//	POST /api/voyager/optimize                     — { "skill": "<name>" } GEPA evolve
//	GET  /api/voyager/code-proposals?status=X      — list code-refactor proposals
//	POST /api/voyager/code-proposals/{id}/decide   — { "decision": "approved|rejected|applied", "note": "..." }
func (api *API) Routes(mux *http.ServeMux) {
	if api == nil {
		return
	}
	mux.HandleFunc("/api/voyager/status", api.handleStatus)
	mux.HandleFunc("/api/voyager/proposals", api.handleProposals)
	mux.HandleFunc("/api/voyager/proposals/", api.handleProposalDecide)
	mux.HandleFunc("/api/voyager/optimize", api.handleOptimize)
	mux.HandleFunc("/api/voyager/code-proposals", api.handleCodeProposals)
	mux.HandleFunc("/api/voyager/code-proposals/", api.handleCodeProposalDecide)
}

func (api *API) handleOptimize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if api.m == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "voyager disabled"})
		return
	}
	var body struct {
		Skill      string `json:"skill"`
		TraceLimit int    `json:"trace_limit,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	skillName := strings.TrimSpace(body.Skill)
	if skillName == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "skill is required"})
		return
	}
	opt := NewOptimizer()
	if !opt.Enabled() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "GEPA sidecar not configured; set GEPA_URL on core",
		})
		return
	}
	result, err := api.m.RunOptimizer(r.Context(), opt, skillName, body.TraceLimit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type statusDTO struct {
	Enabled          bool   `json:"enabled"`
	Status           string `json:"status"`
	OpenSessions     int    `json:"open_sessions"`
	TrackedTriplets  int    `json:"tracked_triplets"`
}

func (api *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	if api.m == nil {
		writeJSON(w, http.StatusOK, statusDTO{})
		return
	}
	api.m.mu.Lock()
	open := len(api.m.sessionWindows)
	triplets := len(api.m.tripletCounters)
	api.m.mu.Unlock()
	writeJSON(w, http.StatusOK, statusDTO{
		Enabled:         api.m.Enabled(),
		Status:          api.m.Status(),
		OpenSessions:    open,
		TrackedTriplets: triplets,
	})
}

func (api *API) handleProposals(w http.ResponseWriter, r *http.Request) {
	if api.m == nil {
		writeJSON(w, http.StatusOK, []ProposalDTO{})
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	limit := 50
	props, err := api.m.ListProposals(r.Context(), status, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, props)
}

func (api *API) handleProposalDecide(w http.ResponseWriter, r *http.Request) {
	if api.m == nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/voyager/proposals/")
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "decide" {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Decision string `json:"decision"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := api.m.Decide(r.Context(), id, strings.ToLower(strings.TrimSpace(body.Decision))); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (api *API) handleCodeProposals(w http.ResponseWriter, r *http.Request) {
	if api.m == nil {
		writeJSON(w, http.StatusOK, []CodeProposalDTO{})
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	props, err := api.m.ListCodeProposals(r.Context(), status, 50)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, props)
}

func (api *API) handleCodeProposalDecide(w http.ResponseWriter, r *http.Request) {
	if api.m == nil {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/voyager/code-proposals/")
	parts := strings.Split(strings.TrimSuffix(rest, "/"), "/")
	if len(parts) < 2 || parts[1] != "decide" {
		http.NotFound(w, r)
		return
	}
	id := strings.TrimSpace(parts[0])
	if id == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var body struct {
		Decision string `json:"decision"`
		Note     string `json:"note"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	if err := api.m.DecideCodeProposal(r.Context(), id,
		strings.ToLower(strings.TrimSpace(body.Decision)), body.Note); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
