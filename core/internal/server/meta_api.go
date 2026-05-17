package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// /api/meta
//
//	GET ?key=<k>  → { "key": k, "value": v }   (404 if missing)
//	POST          → upsert { "key": k, "value": v }
//
// Thin key/value over the infinity_meta table. Studio uses it to persist
// app-level flags like boss_onboarded so a one-time wizard doesn't replay
// on every login. Not a substitute for typed settings - meant for booleans,
// timestamps, and lightweight markers the agent loop doesn't care about.
func (s *Server) handleMeta(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		key := strings.TrimSpace(r.URL.Query().Get("key"))
		if key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
			return
		}
		var value string
		err := s.pool.QueryRow(r.Context(),
			`SELECT value FROM infinity_meta WHERE key = $1`, key).Scan(&value)
		if err != nil {
			// Treat any miss (no rows, etc.) as 404 so the client gets a clear signal.
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": key, "value": value})

	case http.MethodPost, http.MethodPut:
		var in struct {
			Key   string `json:"key"`
			Value string `json:"value"`
		}
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
			return
		}
		in.Key = strings.TrimSpace(in.Key)
		if in.Key == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "key required"})
			return
		}
		_, err := s.pool.Exec(r.Context(), `
			INSERT INTO infinity_meta (key, value, updated_at)
			VALUES ($1, $2, NOW())
			ON CONFLICT (key) DO UPDATE
			   SET value = EXCLUDED.value,
			       updated_at = NOW()
		`, in.Key, in.Value)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"key": in.Key, "value": in.Value})

	default:
		w.Header().Set("Allow", "GET, POST, PUT")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
