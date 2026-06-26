package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// EmitDocumentCreated tells the chat session's Studio tab to open a freshly
// generated document. Rides the per-session broadcaster (sessionSender) like
// browser frames, so it reaches whichever WS is bound to the session and is
// dropped silently when none is. Cloud-first: the markdown rides the event
// (rendered inline, no fetch) and binaries download via the cloud-direct
// proxy below — works on any device regardless of the session's bridge.
func (s *Server) EmitDocumentCreated(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath string, bytes int64) {
	if s == nil || sessionID == "" || filename == "" {
		return
	}
	// Deterministically record the doc as a session artifact (Rule #1b: the
	// repository must NOT depend on the model remembering to call artifact_save).
	// The id rides the event so Studio dedupes this live tab against the list it
	// fetches on load / refresh.
	id := s.recordDocumentArtifact(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath, bytes)
	s.sessionSender(sessionID)(wsServerEvent{
		Type:      "document_created",
		SessionID: sessionID,
		DocumentCreated: &wsDocumentCreated{
			Format:    format,
			Filename:  filename,
			Path:      path,
			Bytes:     bytes,
			Markdown:  markdown,
			PDFPath:   pdfPath,
			ThumbPath: thumbPath,
			HTMLPath:  htmlPath,
			ID:        id,
		},
	})
}

// mimeForFormat maps a docgen format to a storage MIME for the artifact row.
func mimeForFormat(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case "docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case "pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	case "pdf":
		return "application/pdf"
	case "md", "markdown":
		return "text/markdown"
	}
	return "application/octet-stream"
}

// recordDocumentArtifact inserts (or refreshes) a mem_artifacts row for a
// generated document so it lives in the boss's session repository (the
// Artifacts/Media gallery) regardless of whether the model called artifact_save.
// Returns the new id, or "" on failure (non-fatal — the tab still opens).
func (s *Server) recordDocumentArtifact(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath string, bytes int64) string {
	if s == nil || s.pool == nil || sessionID == "" || strings.TrimSpace(path) == "" {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	meta := map[string]any{"format": format}
	if pdfPath != "" {
		meta["pdf_path"] = pdfPath
	}
	if thumbPath != "" {
		meta["thumb_path"] = thumbPath
	}
	if htmlPath != "" {
		meta["html_path"] = htmlPath
	}
	// md/report formats render inline from markdown (no PDF/thumbnail), so keep
	// the markdown on the row — that's what lets an md tab rehydrate on refresh.
	if f := strings.ToLower(strings.TrimSpace(format)); (f == "md" || f == "markdown") && markdown != "" {
		meta["markdown"] = markdown
	}
	metaJSON, _ := json.Marshal(meta)
	// virtual_path is the unique boss-facing key; the base of the (already
	// de-collided) storage path guarantees uniqueness without a collision.
	vpath := "/artifacts/" + filepath.Base(path)
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_artifacts
			(kind, name, storage_kind, storage_path, storage_size, storage_mime,
			 virtual_path, bridge, source_session_id, source_tool, metadata)
		VALUES
			('document', $1, 'filesystem', $2, NULLIF($3,0), NULLIF($4,''),
			 $5, 'cloud', $6, 'document_create', $7::jsonb)
		ON CONFLICT (virtual_path) WHERE deleted_at IS NULL DO UPDATE
			SET storage_path = EXCLUDED.storage_path,
			    storage_size = EXCLUDED.storage_size,
			    storage_mime = EXCLUDED.storage_mime,
			    metadata     = EXCLUDED.metadata,
			    updated_at   = NOW()
		RETURNING id::text
	`, filename, path, bytes, mimeForFormat(format), vpath, sessionID, string(metaJSON)).Scan(&id)
	if err != nil {
		log.Printf("recordDocumentArtifact: %v", err)
		return ""
	}
	return id
}

// docArtifact is one generated document in the session repository.
type docArtifact struct {
	ID        string    `json:"id"`
	Filename  string    `json:"filename"`
	Path      string    `json:"path"`
	Bytes     int64     `json:"bytes"`
	Format    string    `json:"format"`
	PDFPath   string    `json:"pdf_path,omitempty"`
	ThumbPath string    `json:"thumb_path,omitempty"`
	HTMLPath  string    `json:"html_path,omitempty"`
	Markdown  string    `json:"markdown,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// handleSessionArtifacts lists the document artifacts created in a session,
// newest first — the data behind the Studio Artifacts/Media gallery and the
// open-tab rehydration after a refresh.
//
//	GET /api/canvas/artifacts?session_id=<id>[&limit=N]
func (s *Server) handleSessionArtifacts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "GET only", http.StatusMethodNotAllowed)
		return
	}
	sid := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sid == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id required"})
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusOK, map[string]any{"artifacts": []docArtifact{}})
		return
	}
	limit := 200
	if v := strings.TrimSpace(r.URL.Query().Get("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 500 {
			limit = n
		}
	}
	rows, err := s.pool.Query(r.Context(), `
		SELECT id::text, name,
		       COALESCE(storage_path,'')             AS storage_path,
		       COALESCE(storage_size,0)              AS storage_size,
		       COALESCE(metadata->>'format','')      AS format,
		       COALESCE(metadata->>'pdf_path','')    AS pdf_path,
		       COALESCE(metadata->>'thumb_path','')  AS thumb_path,
		       COALESCE(metadata->>'html_path','')   AS html_path,
		       COALESCE(metadata->>'markdown','')    AS markdown,
		       created_at
		  FROM mem_artifacts
		 WHERE kind='document' AND deleted_at IS NULL AND source_session_id = $1
		 ORDER BY created_at DESC
		 LIMIT $2
	`, sid, limit)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	defer rows.Close()
	out := []docArtifact{}
	for rows.Next() {
		var d docArtifact
		if err := rows.Scan(&d.ID, &d.Filename, &d.Path, &d.Bytes, &d.Format, &d.PDFPath, &d.ThumbPath, &d.HTMLPath, &d.Markdown, &d.CreatedAt); err != nil {
			continue
		}
		out = append(out, d)
	}
	writeJSON(w, http.StatusOK, map[string]any{"artifacts": out})
}

// handleWorkspaceDownload streams a file's raw bytes from the CLOUD workspace
// bridge so generated documents download/preview in Studio from ANY device,
// independent of the session's Mac/Cloud bridge. Authed by the global
// middleware; Studio fetches with its bearer and turns the response into a
// blob (so a binary .xlsx never round-trips through the session file API).
//
//	GET /api/workspace/download?path=<cloud workspace path>[&download=1]
func (s *Server) handleWorkspaceDownload(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(s.cfg.WorkspaceRawBase, "/")
	if base == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace not configured"})
		return
	}
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/fs/raw?path="+url.QueryEscape(p), nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	if s.cfg.WorkspaceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.WorkspaceToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "workspace fetch failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	if cl := resp.Header.Get("Content-Length"); cl != "" {
		w.Header().Set("Content-Length", cl)
	}
	disp := "inline"
	if r.URL.Query().Get("download") == "1" {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", disp+"; filename="+strconv.Quote(filepath.Base(p)))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
