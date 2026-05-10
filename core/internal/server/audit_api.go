package server

import (
	"encoding/json"
	"net/http"
	"strconv"
)

// auditRow is the row shape returned by /api/memory/audit. Mirrors the
// mem_audit columns but with stringified IDs.
type auditRow struct {
	ID          string         `json:"id"`
	Operation   string         `json:"operation"`
	Actor       string         `json:"actor"`
	TargetTable string         `json:"target_table"`
	TargetID    string         `json:"target_id"`
	Target      string         `json:"target"` // composed: table#id, kept for UI compat
	Diff        map[string]any `json:"diff,omitempty"`
	CreatedAt   string         `json:"created_at"`
}

func (s *Server) handleAuditLog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusOK, []auditRow{})
		return
	}
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	op := r.URL.Query().Get("op")
	args := []any{limit}
	q := `SELECT id::text, COALESCE(operation,''), COALESCE(actor,''),
	             COALESCE(target_table,''), COALESCE(target_id::text,''),
	             diff, created_at::text
	        FROM mem_audit`
	if op != "" {
		q += ` WHERE operation = $2`
		args = append(args, op)
	}
	q += ` ORDER BY created_at DESC LIMIT $1`

	rows, err := s.pool.Query(r.Context(), q, args...)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer rows.Close()
	var out []auditRow
	for rows.Next() {
		var x auditRow
		var diffRaw []byte
		if err := rows.Scan(&x.ID, &x.Operation, &x.Actor, &x.TargetTable, &x.TargetID, &diffRaw, &x.CreatedAt); err == nil {
			if x.TargetID != "" {
				x.Target = x.TargetTable + "#" + x.TargetID
			} else {
				x.Target = x.TargetTable
			}
			if len(diffRaw) > 0 {
				_ = json.Unmarshal(diffRaw, &x.Diff)
			}
			out = append(out, x)
		}
	}
	if out == nil {
		out = []auditRow{}
	}
	writeJSON(w, http.StatusOK, out)
}
