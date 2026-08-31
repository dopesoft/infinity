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
// secretMetaPrefixes name keys this endpoint must never read or write.
//
// infinity_meta was designed for booleans, timestamps and one-time markers, and
// the handler treats every key alike. The phone vault then started keeping the
// boss's card number, security code, date of birth and account number under
// `vault.*` here, which made GET /api/meta?key=vault.payment_card a way to read
// a live card over HTTP with nothing but a session. Those secrets now live
// sealed in mem_vault_cards, and this denylist is what stops anything from
// quietly putting them back.
var secretMetaPrefixes = []string{"vault."}

func secretMetaKey(key string) bool {
	k := strings.ToLower(strings.TrimSpace(key))
	for _, p := range secretMetaPrefixes {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

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
		if secretMetaKey(key) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "that is a vault key and is not readable here; secrets live encrypted and are only opened inside the boundary that uses them",
			})
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
		if secretMetaKey(in.Key) {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "that is a vault key; add a card through the private wallet flow instead, which never stores it in the clear",
			})
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
