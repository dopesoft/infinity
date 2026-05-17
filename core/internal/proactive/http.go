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

// API exposes proactive-engine endpoints to the Studio Heartbeat /
// Trust Contracts / Live tabs.
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
//
//	GET  /api/heartbeat           - recent runs + last summary
//	POST /api/heartbeat/run       - run-now button
//	GET  /api/heartbeat/findings  - recent findings
//	POST /api/curiosity/questions/:id/decide
//	GET  /api/trust-contracts     - pending queue
//	POST /api/trust-contracts/:id/decide
//	GET  /api/intent/recent       - last 50 IntentFlow decisions
func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/heartbeat", a.handleHeartbeats)
	mux.HandleFunc("/api/heartbeat/run", a.handleHeartbeatRun)
	mux.HandleFunc("/api/heartbeat/findings", a.handleHeartbeatFindings)
	mux.HandleFunc("/api/curiosity/questions/", a.handleCuriosityScoped)
	mux.HandleFunc("/api/trust-contracts", a.handleTrustList)
	mux.HandleFunc("/api/trust-contracts/", a.handleTrustScoped)
	mux.HandleFunc("/api/intent/recent", a.handleIntentRecent)
}

type heartbeatListItem struct {
	ID         string     `json:"id"`
	StartedAt  time.Time  `json:"started_at"`
	EndedAt    *time.Time `json:"ended_at,omitempty"`
	DurationMS int64      `json:"duration_ms"`
	Findings   int        `json:"findings"`
	Status     string     `json:"status"`
	Summary    string     `json:"summary"`
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

type findingListItem struct {
	ID          string    `json:"id"`
	HeartbeatID string    `json:"heartbeat_id"`
	CuriosityID string    `json:"curiosity_id,omitempty"`
	StartedAt   time.Time `json:"started_at"`
	Kind        string    `json:"kind"`
	Title       string    `json:"title"`
	Detail      string    `json:"detail,omitempty"`
	PreApproved bool      `json:"pre_approved"`
}

func (a *API) handleHeartbeatFindings(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a.pool == nil {
		writeJSON(w, http.StatusOK, []findingListItem{})
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			limit = n
		}
	}
	kind := strings.TrimSpace(r.URL.Query().Get("kind"))
	q := `
		SELECT f.id::text, f.heartbeat_id::text,
		       COALESCE(cq.id::text, ''),
		       h.started_at, f.kind, f.title, COALESCE(f.detail, ''), f.pre_approved
		  FROM mem_heartbeat_findings f
		  JOIN mem_heartbeats h ON h.id = f.heartbeat_id
		  LEFT JOIN mem_curiosity_questions cq
		    ON f.kind = 'curiosity'
		   AND cq.status = 'open'
		   AND cq.question = f.title`
	args := []any{limit}
	if kind != "" {
		q += ` WHERE f.kind = $2 AND (f.kind <> 'curiosity' OR cq.id IS NOT NULL)`
		args = append(args, kind)
	} else {
		q += ` WHERE (f.kind <> 'curiosity' OR cq.id IS NOT NULL)`
	}
	q += ` ORDER BY h.started_at DESC, f.id DESC LIMIT $1`
	rows, err := a.pool.Query(r.Context(), q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	out := []findingListItem{}
	for rows.Next() {
		var x findingListItem
		if err := rows.Scan(&x.ID, &x.HeartbeatID, &x.CuriosityID, &x.StartedAt,
			&x.Kind, &x.Title, &x.Detail, &x.PreApproved); err == nil {
			out = append(out, x)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

type curiosityDecisionReq struct {
	Decision string `json:"decision"` // asked | answered | dismissed | approved
	Answer   string `json:"answer,omitempty"`
}

func (a *API) handleCuriosityScoped(w http.ResponseWriter, r *http.Request) {
	tail := strings.TrimPrefix(r.URL.Path, "/api/curiosity/questions/")
	parts := strings.Split(tail, "/")
	if len(parts) != 2 || parts[1] != "decide" || r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var body curiosityDecisionReq
	if r.Body != nil {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	switch body.Decision {
	// "approved" means the boss told the agent to act on the question -
	// the chat surface fires this alongside a follow-up turn that hands
	// the agent the finding to fix. It resolves the question like
	// answered/dismissed do (it's no longer open), just with a status
	// that records the boss said "go".
	case "asked", "answered", "dismissed", "approved":
	default:
		http.Error(w, "decision must be asked | answered | dismissed | approved", http.StatusBadRequest)
		return
	}
	if a.pool == nil {
		http.Error(w, "database not configured", http.StatusServiceUnavailable)
		return
	}
	_, err := a.pool.Exec(r.Context(), `
		UPDATE mem_curiosity_questions
		   SET status = $2,
		       answer = CASE WHEN $3 <> '' THEN $3 ELSE answer END,
		       asked_at = CASE WHEN $2 = 'asked' THEN NOW() ELSE asked_at END,
		       resolved_at = CASE WHEN $2 IN ('answered','dismissed','approved') THEN NOW() ELSE resolved_at END
		 WHERE id = $1::uuid
	`, parts[0], body.Decision, strings.TrimSpace(body.Answer))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": body.Decision})
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
