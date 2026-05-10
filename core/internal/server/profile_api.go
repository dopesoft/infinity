package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// profileFactDTO is one always-on identity fact about the boss.
// Stored as a row in mem_memories with tier='semantic', project='_self'.
type profileFactDTO struct {
	ID         string `json:"id,omitempty"`
	Title      string `json:"title"`
	Content    string `json:"content"`
	Importance int    `json:"importance,omitempty"`
}

// /api/memory/profile
//
//	GET   → list all boss-profile facts (the always-on primer)
//	POST  → upsert one fact (matched by title); creates if new, updates if existing
//	DELETE?id=<uuid> → remove one fact
//
// Surfaced in the Studio Memory tab as the "Boss profile" panel. Every fact
// here is prepended to every system prompt — keep entries short and dense.
func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request) {
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "no database pool"})
		return
	}

	switch r.Method {
	case http.MethodGet:
		rows, err := s.pool.Query(r.Context(), `
			SELECT id::text, COALESCE(title, ''), COALESCE(content, ''), importance
			FROM mem_memories
			WHERE tier = 'semantic' AND status = 'active' AND project = '_self'
			ORDER BY importance DESC, updated_at DESC
		`)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		defer rows.Close()
		out := []profileFactDTO{}
		for rows.Next() {
			var f profileFactDTO
			if err := rows.Scan(&f.ID, &f.Title, &f.Content, &f.Importance); err != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			out = append(out, f)
		}
		writeJSON(w, http.StatusOK, out)

	case http.MethodPost:
		var in profileFactDTO
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			return
		}
		in.Title = strings.TrimSpace(in.Title)
		in.Content = strings.TrimSpace(in.Content)
		if in.Title == "" || in.Content == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "title and content required"})
			return
		}
		if in.Importance == 0 {
			in.Importance = 9 // boss profile is high-importance by default
		}

		var id string
		err := s.pool.QueryRow(r.Context(), `
			INSERT INTO mem_memories
				(title, content, tier, version, status, strength, importance, project,
				 fts_doc, created_at, updated_at, last_accessed_at)
			VALUES ($1, $2, 'semantic', 1, 'active', 1.0, $3, '_self',
			        to_tsvector('english', COALESCE($1,'') || ' ' || COALESCE($2,'')),
			        NOW(), NOW(), NOW())
			ON CONFLICT (project, title) WHERE project = '_self'
			DO UPDATE SET
				content = EXCLUDED.content,
				importance = EXCLUDED.importance,
				updated_at = NOW(),
				fts_doc = to_tsvector('english',
				          COALESCE(EXCLUDED.title,'') || ' ' || COALESCE(EXCLUDED.content,''))
			RETURNING id::text
		`, in.Title, in.Content, in.Importance).Scan(&id)
		if err != nil {
			// Fall back to a non-conflict insert path for older schemas without
			// the partial unique index. Match by (project, title) manually.
			var existing string
			lookupErr := s.pool.QueryRow(r.Context(), `
				SELECT id::text FROM mem_memories
				WHERE project = '_self' AND title = $1 AND status = 'active'
				LIMIT 1
			`, in.Title).Scan(&existing)
			if lookupErr == nil && existing != "" {
				_, upErr := s.pool.Exec(r.Context(), `
					UPDATE mem_memories
					SET content = $2,
					    importance = $3,
					    updated_at = NOW(),
					    fts_doc = to_tsvector('english', COALESCE(title,'') || ' ' || COALESCE($2,''))
					WHERE id = $1::uuid
				`, existing, in.Content, in.Importance)
				if upErr != nil {
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": upErr.Error()})
					return
				}
				writeJSON(w, http.StatusOK, map[string]string{"id": existing})
				return
			}
			insErr := s.pool.QueryRow(r.Context(), `
				INSERT INTO mem_memories
					(title, content, tier, version, status, strength, importance, project,
					 fts_doc, created_at, updated_at, last_accessed_at)
				VALUES ($1, $2, 'semantic', 1, 'active', 1.0, $3, '_self',
				        to_tsvector('english', COALESCE($1,'') || ' ' || COALESCE($2,'')),
				        NOW(), NOW(), NOW())
				RETURNING id::text
			`, in.Title, in.Content, in.Importance).Scan(&id)
			if insErr != nil {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": insErr.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]string{"id": id})

	case http.MethodDelete:
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
			return
		}
		_, err := s.pool.Exec(r.Context(), `
			UPDATE mem_memories SET status = 'archived', updated_at = NOW()
			WHERE id = $1::uuid AND project = '_self'
		`, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})

	default:
		w.Header().Set("Allow", "GET, POST, DELETE")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}
