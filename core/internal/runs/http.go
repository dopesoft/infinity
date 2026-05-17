package runs

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// API exposes /api/runs. It is read-only - the only way to create / mutate
// rows is via runs.Track in the action handlers themselves. Studio uses
// this endpoint to backfill state on mount (realtime only pushes future
// events) and the useRuns() hook stitches the snapshot with the live
// stream.
type API struct {
	pool *pgxpool.Pool
}

func NewAPI(pool *pgxpool.Pool) *API { return &API{pool: pool} }

func (a *API) Routes(mux *http.ServeMux) {
	mux.HandleFunc("/api/runs", a.handleList)
}

type runDTO struct {
	ID            string  `json:"id"`
	Kind          string  `json:"kind"`
	TargetID      string  `json:"target_id"`
	Label         string  `json:"label"`
	Source        string  `json:"source"`
	Status        string  `json:"status"`
	Progress      *float32 `json:"progress,omitempty"`
	ProgressLabel string  `json:"progress_label,omitempty"`
	StartedAt     string  `json:"started_at"`
	EndedAt       *string `json:"ended_at,omitempty"`
	DurationMS    *int    `json:"duration_ms,omitempty"`
	Error         string  `json:"error,omitempty"`
	ResultSummary string  `json:"result_summary,omitempty"`
}

// handleList: GET /api/runs[?kind=X&target_id=Y&status=Z&limit=N]
//
//	?kind=cron&target_id=<uuid>     → "is THIS cron running?"
//	?status=running                  → "everything in flight"
//	(no params)                      → recent runs across kinds
func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if a == nil || a.pool == nil {
		writeJSON(w, http.StatusOK, []runDTO{})
		return
	}
	kind := r.URL.Query().Get("kind")
	targetID := r.URL.Query().Get("target_id")
	status := r.URL.Query().Get("status")
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}

	// Build a parameterised WHERE so we never interpolate user input.
	args := []any{}
	where := ""
	add := func(clause string, arg any) {
		args = append(args, arg)
		if where == "" {
			where = "WHERE " + clause + "$" + strconv.Itoa(len(args))
		} else {
			where += " AND " + clause + "$" + strconv.Itoa(len(args))
		}
	}
	if kind != "" {
		add("kind = ", kind)
	}
	if targetID != "" {
		add("target_id = ", targetID)
	}
	if status != "" {
		add("status = ", status)
	}
	args = append(args, limit)
	sql := `
		SELECT id::text, kind, target_id, label, source, status,
		       progress, progress_label,
		       started_at, ended_at, duration_ms,
		       error, result_summary
		  FROM mem_runs
		  ` + where + `
		 ORDER BY started_at DESC
		 LIMIT $` + strconv.Itoa(len(args))

	rows, err := a.pool.Query(r.Context(), sql, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()

	out := []runDTO{}
	for rows.Next() {
		var d runDTO
		var progress *float32
		var progressLabel string
		var startedAt time.Time
		var endedAt *time.Time
		var dur *int
		if err := rows.Scan(
			&d.ID, &d.Kind, &d.TargetID, &d.Label, &d.Source, &d.Status,
			&progress, &progressLabel,
			&startedAt, &endedAt, &dur,
			&d.Error, &d.ResultSummary,
		); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.Progress = progress
		d.ProgressLabel = progressLabel
		d.StartedAt = startedAt.UTC().Format(time.RFC3339Nano)
		if endedAt != nil {
			s := endedAt.UTC().Format(time.RFC3339Nano)
			d.EndedAt = &s
		}
		d.DurationMS = dur
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
