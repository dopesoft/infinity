package sentinel

import (
	"encoding/json"
	"net/http"
	"strings"
)

type API struct {
	manager *Manager
}

func NewAPI(m *Manager) *API { return &API{manager: m} }

// Routes registers under /api/sentinels.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/sentinels", a.handleList)
	mux.HandleFunc("/api/sentinels/", a.handleScoped)
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out := a.manager.List()
		if out == nil {
			out = []Sentinel{}
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var s Sentinel
		if err := json.NewDecoder(r.Body).Decode(&s); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, err := a.manager.Upsert(r.Context(), s)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *API) handleScoped(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/sentinels/")
	if tail == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(tail, "/")
	id := parts[0]
	switch {
	case len(parts) == 1 && r.Method == http.MethodDelete:
		if err := a.manager.Delete(r.Context(), id); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case len(parts) == 2 && parts[1] == "trigger" && r.Method == http.MethodPost:
		var payload map[string]any
		if r.Body != nil {
			_ = json.NewDecoder(r.Body).Decode(&payload)
		}
		if err := a.manager.Trigger(r.Context(), id, payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "triggered"})
	default:
		http.NotFound(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
