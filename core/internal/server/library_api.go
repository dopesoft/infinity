package server

import (
	"encoding/json"
	"net/http"
	"time"
)

// library_api.go — the Files tab IS the library.
//
//   GET /api/library/tree
//
// Returns mem_artifacts grouped by kind so Studio can render a
// collapsible "Library" section at the top of the Files panel. Projects,
// Images, Audio, Video, Documents, Datasets, Other. The boss browses
// "like a normal computer" without us having to invent a new tab.
//
// Clicking a project in this view → Studio swaps store.root to the
// project's storage_path AND updates mem_sessions.project_path so the
// Files tree below auto-scopes from that moment on.
//
// Clicking a media artifact (image/audio/video/doc) → Studio opens it
// via storage_path (R2/Supabase signed URL) in the right pane viewer.

type libraryEntry struct {
	ID          string    `json:"id"`
	Kind        string    `json:"-"` // used for grouping; not emitted (the group carries it)
	Name        string    `json:"name"`
	Description string    `json:"description,omitempty"`
	VirtualPath string    `json:"virtual_path"`
	StorageKind string    `json:"storage_kind"`
	StoragePath string    `json:"storage_path,omitempty"`
	StorageMime string    `json:"storage_mime,omitempty"`
	GitHubURL   string    `json:"github_url,omitempty"`
	Bridge      string    `json:"bridge,omitempty"`
	Tags        []string  `json:"tags"`
	CreatedAt   time.Time `json:"created_at"`
}

type libraryGroup struct {
	Kind    string         `json:"kind"`
	Count   int            `json:"count"`
	Entries []libraryEntry `json:"entries"`
}

// canonicalKindOrder is what Studio renders top-to-bottom. New kinds
// added later just append; unknown ones fall through under "other".
var canonicalKindOrder = []string{
	"project", "image", "audio", "video", "document", "dataset", "memory", "other",
}

func (s *Server) handleLibraryTree(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"groups": []libraryGroup{}})
		return
	}
	// Per-kind cap so a runaway image-generation session doesn't ship
	// 10k rows to the browser. Library is for navigation, not bulk
	// listing — Studio's existing artifact_list tool handles filtered
	// queries for the agent.
	const perKindCap = 200

	rows, err := s.pool.Query(r.Context(), `
		WITH ranked AS (
			SELECT id::text, kind, name, COALESCE(description, '') AS description,
			       virtual_path, storage_kind,
			       COALESCE(storage_path, '') AS storage_path,
			       COALESCE(storage_mime, '') AS storage_mime,
			       COALESCE(github_url, '')   AS github_url,
			       COALESCE(bridge, '')       AS bridge,
			       tags::text  AS tags_json,
			       created_at,
			       ROW_NUMBER() OVER (PARTITION BY kind ORDER BY created_at DESC) AS rn
			  FROM mem_artifacts
			 WHERE deleted_at IS NULL
		)
		SELECT id, kind, name, description, virtual_path, storage_kind,
		       storage_path, storage_mime, github_url, bridge, tags_json, created_at
		  FROM ranked
		 WHERE rn <= $1
		 ORDER BY kind, created_at DESC
	`, perKindCap)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()

	byKind := map[string][]libraryEntry{}
	for rows.Next() {
		var e libraryEntry
		var tagsJSON string
		if err := rows.Scan(
			&e.ID, &e.Kind, &e.Name, &e.Description,
			&e.VirtualPath, &e.StorageKind,
			&e.StoragePath, &e.StorageMime, &e.GitHubURL, &e.Bridge,
			&tagsJSON, &e.CreatedAt,
		); err != nil {
			continue
		}
		_ = json.Unmarshal([]byte(tagsJSON), &e.Tags)
		if e.Tags == nil {
			e.Tags = []string{}
		}
		byKind[e.Kind] = append(byKind[e.Kind], e)
	}
	_ = rows.Err()

	out := make([]libraryGroup, 0, len(byKind))
	// First, emit known kinds in canonical order so the UI is stable.
	seen := map[string]bool{}
	for _, k := range canonicalKindOrder {
		if entries, ok := byKind[k]; ok {
			out = append(out, libraryGroup{Kind: k, Count: len(entries), Entries: entries})
			seen[k] = true
		}
	}
	// Then any kinds we don't have explicit order for (future-proof).
	for k, entries := range byKind {
		if seen[k] {
			continue
		}
		out = append(out, libraryGroup{Kind: k, Count: len(entries), Entries: entries})
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}
