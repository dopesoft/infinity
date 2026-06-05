package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// gaugeReadDTO is one effort-sizing read.
type gaugeReadDTO struct {
	ID        string `json:"id"`
	SessionID string `json:"session_id,omitempty"`
	Excerpt   string `json:"turn_excerpt"`
	Tier      string `json:"tier"`
	Reason    string `json:"reason"`
	CreatedAt string `json:"created_at"`
}

// /api/gauge/recent → the most recent effort-sizing reads (the Gauge). Backs
// the Intent/effort observability chip; read-only.
func (s *Server) handleGaugeRecent(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", "GET")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 5
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, COALESCE(session_id::text,''), turn_excerpt, tier, reason, created_at
		  FROM mem_gauge_reads
		 ORDER BY created_at DESC LIMIT $1
	`, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []gaugeReadDTO{}
	for rows.Next() {
		var d gaugeReadDTO
		var created time.Time
		if err := rows.Scan(&d.ID, &d.SessionID, &d.Excerpt, &d.Tier, &d.Reason, &created); err != nil {
			continue
		}
		d.CreatedAt = created.UTC().Format(time.RFC3339)
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, out)
}
