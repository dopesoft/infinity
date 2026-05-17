package skills

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// API exposes the skills registry over HTTP for the Studio Skills tab.
type API struct {
	registry *Registry
	runner   *Runner
	store    *Store
}

func NewAPI(reg *Registry, runner *Runner, store *Store) *API {
	return &API{registry: reg, runner: runner, store: store}
}

// Routes registers skill endpoints on the provided mux. Path prefix is
// `/api/skills` - matches the Studio client.
//
//   GET    /api/skills              → list summaries
//   GET    /api/skills/:name        → detail
//   GET    /api/skills/:name/runs   → recent runs (?limit=)
//   POST   /api/skills/:name/invoke → manual invoke
//   POST   /api/skills/reload       → re-walk the filesystem
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/skills", a.handleList)
	mux.HandleFunc("/api/skills/reload", a.handleReload)
	mux.HandleFunc("/api/skills/", a.handleSkillScoped)
}

func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var out []SkillSummary
	if a.store != nil {
		summaries, err := a.store.ListSummaries(r.Context())
		if err == nil {
			out = summaries
		}
	}
	if out == nil {
		for _, s := range a.registry.All() {
			out = append(out, SkillSummary{
				Name:          s.Name,
				Version:       s.Version,
				Description:   s.Description,
				RiskLevel:     s.RiskLevel,
				Confidence:    s.Confidence,
				Source:        s.Source,
				Status:        s.Status,
				NetworkEgress: s.NetworkEgress,
			})
		}
	}
	if out == nil {
		out = []SkillSummary{}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *API) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	errs, err := a.registry.Reload(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"loaded_at": a.registry.Loaded(),
		"errors":    errs,
		"count":     len(a.registry.All()),
	})
}

func (a *API) handleSkillScoped(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/skills/")
	if tail == "" {
		http.NotFound(w, r)
		return
	}
	parts := strings.Split(tail, "/")
	name := parts[0]
	skill, ok := a.registry.Get(name)
	if !ok {
		http.Error(w, "skill not found", http.StatusNotFound)
		return
	}
	switch {
	case len(parts) == 1 && r.Method == http.MethodGet:
		writeJSON(w, http.StatusOK, skill)
	case len(parts) == 2 && parts[1] == "runs" && r.Method == http.MethodGet:
		a.handleRuns(w, r, name)
	case len(parts) == 2 && parts[1] == "invoke" && r.Method == http.MethodPost:
		a.handleInvoke(w, r, name)
	default:
		http.NotFound(w, r)
	}
}

func (a *API) handleRuns(w http.ResponseWriter, r *http.Request, name string) {
	limit := 25
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if a.store == nil {
		writeJSON(w, http.StatusOK, []Run{})
		return
	}
	runs, err := a.store.RecentRuns(r.Context(), name, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, runs)
}

type invokeReq struct {
	Args map[string]any `json:"args"`
}

func (a *API) handleInvoke(w http.ResponseWriter, r *http.Request, name string) {
	var body invokeReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, run, err := a.runner.Invoke(ctx, "", name, body.Args, "manual")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{
			"error":  err.Error(),
			"result": res,
			"run":    run,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"result": res,
		"run":    run,
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
