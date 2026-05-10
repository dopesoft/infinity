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
//	GET  /api/voyager/status                — manager status + counters
//	GET  /api/voyager/proposals?status=X    — list proposals
//	POST /api/voyager/proposals/{id}/decide — { "decision": "promoted" | "rejected" }
func (api *API) Routes(mux *http.ServeMux) {
	if api == nil {
		return
	}
	mux.HandleFunc("/api/voyager/status", api.handleStatus)
	mux.HandleFunc("/api/voyager/proposals", api.handleProposals)
	mux.HandleFunc("/api/voyager/proposals/", api.handleProposalDecide)
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

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
