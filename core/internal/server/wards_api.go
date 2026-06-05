package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// wardDTO is one privacy ward.
type wardDTO struct {
	ID    string `json:"id,omitempty"`
	Glob  string `json:"glob"`
	Level string `json:"level"` // private | sensitive
	Note  string `json:"note,omitempty"`
}

// /api/wards
//
//	GET            → list all wards (privacy zones), private first.
//	PUT / POST     → add/update one: { glob, level, note }.
//	DELETE?id=<id> → remove one.
//
// Wards are enforced by proactive.WardGate; this endpoint backs Settings →
// Privacy.
func (s *Server) handleWards(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		rows, err := s.pool.Query(r.Context(), `
			SELECT id::text, glob, level, note FROM mem_wards
			 ORDER BY CASE level WHEN 'private' THEN 0 ELSE 1 END, glob ASC
		`)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []wardDTO{}
		for rows.Next() {
			var d wardDTO
			if err := rows.Scan(&d.ID, &d.Glob, &d.Level, &d.Note); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			out = append(out, d)
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPut, http.MethodPost:
		var in wardDTO
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		in.Glob = strings.TrimSpace(in.Glob)
		in.Level = strings.ToLower(strings.TrimSpace(in.Level))
		if in.Glob == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "glob required"})
			return
		}
		if in.Level != "private" && in.Level != "sensitive" {
			in.Level = "private"
		}
		var id string
		err := s.pool.QueryRow(r.Context(), `
			INSERT INTO mem_wards (glob, level, note)
			VALUES ($1, $2, $3)
			ON CONFLICT (glob) DO UPDATE SET level = EXCLUDED.level, note = EXCLUDED.note
			RETURNING id::text
		`, in.Glob, in.Level, in.Note).Scan(&id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})

	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		glob := strings.TrimSpace(r.URL.Query().Get("glob"))
		if id == "" && glob == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id or glob required"})
			return
		}
		var err error
		if id != "" {
			_, err = s.pool.Exec(r.Context(), `DELETE FROM mem_wards WHERE id = $1::uuid`, id)
		} else {
			_, err = s.pool.Exec(r.Context(), `DELETE FROM mem_wards WHERE glob = $1`, glob)
		}
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		w.Header().Set("Allow", "GET, PUT, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
