package cron

import (
	"encoding/json"
	"net/http"
	"strings"
)

type API struct {
	scheduler *Scheduler
}

func NewAPI(s *Scheduler) *API { return &API{scheduler: s} }

// Routes registers under /api/crons.
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/crons", a.handleList)
	mux.HandleFunc("/api/crons/preview", a.handlePreview)
	mux.HandleFunc("/api/crons/", a.handleScoped)
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		out, err := a.scheduler.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if out == nil {
			out = []Job{}
		}
		writeJSON(w, http.StatusOK, out)
	case http.MethodPost:
		var j Job
		if err := json.NewDecoder(r.Body).Decode(&j); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		id, err := a.scheduler.Upsert(r.Context(), j)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

type previewReq struct {
	Schedule string `json:"schedule"`
	Count    int    `json:"count"`
}

func (a *API) handlePreview(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req previewReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	times, err := a.scheduler.SimulateNext(req.Schedule, req.Count)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"next": times})
}

func (a *API) handleScoped(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/crons/")
	if tail == "" {
		http.NotFound(w, r)
		return
	}
	// /api/crons/:id/run → fire once immediately (Studio "Run now"
	// button). Reuses scheduler.RunOnce so the run status writes back
	// to mem_crons.last_run_* and surfaces in the agent-work feed the
	// same as any scheduled fire.
	if strings.HasSuffix(tail, "/run") {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		id := strings.TrimSuffix(tail, "/run")
		if id == "" {
			http.NotFound(w, r)
			return
		}
		jobs, err := a.scheduler.List(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var found *Job
		for i := range jobs {
			if jobs[i].ID == id {
				found = &jobs[i]
				break
			}
		}
		if found == nil {
			http.NotFound(w, r)
			return
		}
		runErr := a.scheduler.RunOnce(r.Context(), *found)
		if runErr != nil {
			// Report the failure but still 200 — the run completed
			// with an error, the client wants to render it. Same
			// semantics as a scheduled fire that errored.
			writeJSON(w, http.StatusOK, map[string]any{
				"ok":    false,
				"error": runErr.Error(),
			})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	id := tail
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := a.scheduler.Delete(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
