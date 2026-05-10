package proactive

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/jackc/pgx/v5/pgxpool"
)

// API exposes Phase 5 endpoints to the Studio Heartbeat / Trust Contracts /
// Live tabs.
type API struct {
	pool      *pgxpool.Pool
	heartbeat *Heartbeat
	trust     *TrustStore
	intentDB  *intent.Store
}

func NewAPI(p *pgxpool.Pool, hb *Heartbeat, ts *TrustStore, is *intent.Store) *API {
	return &API{pool: p, heartbeat: hb, trust: ts, intentDB: is}
}

// Routes registers under:
//   GET  /api/heartbeat           — recent runs + last summary
//   POST /api/heartbeat/run       — run-now button
//   GET  /api/trust-contracts     — pending queue
//   POST /api/trust-contracts/:id/decide
//   GET  /api/intent/recent       — last 50 IntentFlow decisions
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/heartbeat", a.handleHeartbeats)
	mux.HandleFunc("/api/heartbeat/run", a.handleHeartbeatRun)
	mux.HandleFunc("/api/trust-contracts", a.handleTrustList)
	mux.HandleFunc("/api/trust-contracts/", a.handleTrustScoped)
	mux.HandleFunc("/api/intent/recent", a.handleIntentRecent)
}

type heartbeatListItem struct {
	ID         string    `json:"id"`
	StartedAt  time.Time `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	DurationMS int64     `json:"duration_ms"`
	Findings   int       `json:"findings"`
	Status     string    `json:"status"`
	Summary    string    `json:"summary"`
}

func (a *API) handleHeartbeats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.pool == nil {
		writeJSON(w, http.StatusOK, []heartbeatListItem{})
		return
	}
	rows, err := a.pool.Query(r.Context(), `
		SELECT id::text, started_at, ended_at, duration_ms, findings, status, summary
		  FROM mem_heartbeats
		 ORDER BY started_at DESC
		 LIMIT 50
	`)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var out []heartbeatListItem
	for rows.Next() {
		var x heartbeatListItem
		if err := rows.Scan(&x.ID, &x.StartedAt, &x.EndedAt, &x.DurationMS,
			&x.Findings, &x.Status, &x.Summary); err == nil {
			out = append(out, x)
		}
	}
	if out == nil {
		out = []heartbeatListItem{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"interval_seconds": int64(a.heartbeatInterval().Seconds()),
		"runs":             out,
	})
}

func (a *API) heartbeatInterval() time.Duration {
	if a.heartbeat == nil {
		return 0
	}
	return a.heartbeat.Interval()
}

func (a *API) handleHeartbeatRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.heartbeat == nil {
		http.Error(w, "heartbeat not configured", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	res, err := a.heartbeat.RunOnce(ctx)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   err.Error(),
			"summary": res,
		})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (a *API) handleTrustList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	status := r.URL.Query().Get("status")
	if status == "" {
		status = "pending"
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	out, err := a.trust.List(r.Context(), status, limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if out == nil {
		out = []TrustContract{}
	}
	writeJSON(w, http.StatusOK, out)
}

type decideReq struct {
	Decision string `json:"decision"` // approved | denied | snoozed
	Note     string `json:"note,omitempty"`
}

func (a *API) handleTrustScoped(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/trust-contracts/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || parts[1] != "decide" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body decideReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch body.Decision {
	case "approved", "denied", "snoozed":
	default:
		http.Error(w, "decision must be approved | denied | snoozed", http.StatusBadRequest)
		return
	}
	if err := a.trust.Decide(r.Context(), parts[0], body.Decision, body.Note); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": body.Decision})
}

func (a *API) handleIntentRecent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	if a.intentDB == nil {
		writeJSON(w, http.StatusOK, []intent.Record{})
		return
	}
	out, err := a.intentDB.Recent(r.Context(), limit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if out == nil {
		out = []intent.Record{}
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
