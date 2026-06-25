package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
	"github.com/google/uuid"
)

// API exposes the Voyager subsystem over HTTP. Mounted in serve.go via Routes.
type API struct{ m *Manager }

func NewAPI(m *Manager) *API { return &API{m: m} }

// Routes registers handlers. Endpoints:
//
//	GET  /api/voyager/status                       - manager status + counters
//	GET  /api/voyager/proposals?status=X           - list skill proposals
//	POST /api/voyager/proposals/{id}/decide        - { "decision": "promoted" | "rejected" }
//	POST /api/voyager/optimize                     - { "skill": "<name>" } GEPA evolve
//	GET  /api/voyager/code-proposals?status=X      - list code-refactor proposals
//	POST /api/voyager/code-proposals/{id}/decide   - { "decision": "approved|rejected|applied", "note": "..." }
func (api *API) Routes(mux *http.ServeMux) {
	if api == nil {
		return
	}
	mux.HandleFunc("/api/voyager/status", api.handleStatus)
	mux.HandleFunc("/api/voyager/proposals", api.handleProposals)
	mux.HandleFunc("/api/voyager/proposals/", api.handleProposalDecide)
	mux.HandleFunc("/api/voyager/optimize", api.handleOptimize)
	mux.HandleFunc("/api/voyager/verify", api.handleVerify)
	mux.HandleFunc("/api/voyager/code-proposals", api.handleCodeProposals)
	mux.HandleFunc("/api/voyager/code-proposals/", api.handleCodeProposalDecide)
	mux.HandleFunc("/api/voyager/calendar/prep", api.handleCalendarPrep)
}

// handleVerify runs the skill-verification harness on-demand for one active
// skill: { "skill": "<name>" }. It executes the skill read-only in an ephemeral
// session, asserts it returned real data, records the eval, and cleans up the
// test footprint. Returns { passed, notes }. This is how the boss (or the Lab
// UI) proves a skill like inbox-triage actually brings back data.
func (api *API) handleVerify(w http.ResponseWriter, r *http.Request) {
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
		Skill string `json:"skill"`
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
	var (
		passed bool
		notes  string
		runErr error
	)
	// runs.Track so the verification spinner survives navigation like every
	// other long action (CLAUDE.md → "Server-tracked progress").
	_ = runs.Track(r.Context(), runs.KindSkill, skillName, "verify "+skillName, runs.SourceManual, func(ctx context.Context) error {
		passed, notes, runErr = api.m.VerifySkill(ctx, skillName)
		if runErr == nil && !passed {
			return fmt.Errorf("verification failed: %s", notes)
		}
		return runErr
	})
	writeJSON(w, http.StatusOK, map[string]any{"skill": skillName, "passed": passed, "notes": notes})
}

// handleCalendarPrep triggers prep-checklist generation for one calendar
// event. Studio's Upcoming card surfaces an "ask Jarvis to plan this"
// affordance - or future Voyager hooks fire this automatically when a
// new event is ingested from the calendar connector.
//
//	POST /api/voyager/calendar/prep
//	  { "event_id": "<uuid>" }
//	→ { "ok": true }
func (api *API) handleCalendarPrep(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", "POST")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if api.m == nil {
		http.Error(w, "voyager not configured", http.StatusServiceUnavailable)
		return
	}
	var body struct {
		EventID string `json:"event_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.EventID == "" {
		http.Error(w, "event_id required", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(strings.TrimSpace(body.EventID))
	if err != nil {
		http.Error(w, "invalid event_id", http.StatusBadRequest)
		return
	}
	if err := api.m.GeneratePrepForEvent(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("content-type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true})
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
	// runs.Track surfaces "GEPA is evolving SKILL" to every device. The
	// optimizer can take 30s-3min; navigating away while it runs would
	// otherwise hide the spinner. See CLAUDE.md → "Server-tracked progress".
	var (
		result any
		runErr error
	)
	_ = runs.Track(r.Context(), runs.KindVoyagerOptimize, skillName, "optimize "+skillName, runs.SourceManual, func(ctx context.Context) error {
		result, runErr = api.m.RunOptimizer(ctx, opt, skillName, body.TraceLimit)
		return runErr
	})
	if runErr != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": runErr.Error()})
		return
	}
	writeJSON(w, http.StatusOK, result)
}

type statusDTO struct {
	Enabled         bool   `json:"enabled"`
	Status          string `json:"status"`
	OpenSessions    int    `json:"open_sessions"`
	TrackedTriplets int    `json:"tracked_triplets"`
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
	q := r.URL.Query()
	filters := ProposalFilters{
		Status:       strings.TrimSpace(q.Get("status")),
		Frontier:     strings.TrimSpace(q.Get("frontier")),
		ParentSkill:  strings.TrimSpace(q.Get("parent_skill")),
		ProposalKind: strings.TrimSpace(q.Get("proposal_kind")),
	}
	limit := 50
	props, err := api.m.ListProposalsFiltered(r.Context(), filters, limit)
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
	decision := strings.ToLower(strings.TrimSpace(body.Decision))

	// A "promoted" decision RUNS the skill's verification harness (an ephemeral
	// LLM session, up to ~90s) before it promotes — far too long to block the
	// HTTP request on (the card "just sat there" while it ran). Book a tracked
	// mem_runs row and do the verify+promote in the BACKGROUND, then return
	// immediately. Studio shows a live <RunIndicator> (kind=skill.promote,
	// target_id=proposal id) that survives navigation, and the candidate list
	// updates via the mem_skill_proposals realtime feed when it finishes —
	// success clears the card, failure shows why. (CLAUDE.md → "Server-tracked
	// progress" + "verify before promote".) "rejected" is instant; keep it sync.
	if decision == "promoted" {
		label := promoteLabel(r.Context(), api.m, id)
		go func() {
			bg, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			_ = runs.Track(bg, runs.KindSkillPromote, id, label, runs.SourceManual, func(ctx context.Context) error {
				return api.m.Decide(ctx, id, decision)
			})
		}()
		writeJSON(w, http.StatusAccepted, map[string]bool{"accepted": true})
		return
	}

	if err := api.m.Decide(r.Context(), id, decision); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// promoteLabel builds a plain-English run label from the proposal's target
// skill name (readable-names rule), falling back to a generic one. Best-effort:
// a label is cosmetic, so a failed lookup never blocks the promote.
func promoteLabel(ctx context.Context, m *Manager, id string) string {
	if m == nil || m.pool == nil {
		return "promote skill"
	}
	var name, parent string
	if err := m.pool.QueryRow(ctx,
		`SELECT COALESCE(name,''), COALESCE(parent_skill,'') FROM mem_skill_proposals WHERE id = $1`,
		id).Scan(&name, &parent); err != nil {
		return "promote skill"
	}
	if t := strings.TrimSpace(parent); t != "" {
		return "promote " + t
	}
	if t := strings.TrimSpace(name); t != "" {
		return "promote " + t
	}
	return "promote skill"
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
