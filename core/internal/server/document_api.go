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
func (s *Server) EmitDocumentCreated(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath, description string, bytes int64) {
	if s == nil || sessionID == "" || filename == "" {
		return
	}
	// Deterministically record the doc as a session artifact (Rule #1b: the
	// repository must NOT depend on the model remembering to call artifact_save
	// — and it must NOT, or we get a duplicate card). The id rides the event so
	// Studio dedupes this live tab against the list it fetches on load / refresh.
	id := s.recordDocumentArtifact(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath, description, bytes)
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
func (s *Server) recordDocumentArtifact(sessionID, format, filename, path, markdown, pdfPath, thumbPath, htmlPath, description string, bytes int64) string {
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
			(kind, name, description, storage_kind, storage_path, storage_size, storage_mime,
			 virtual_path, bridge, source_session_id, source_tool, metadata)
		VALUES
			('document', $1, NULLIF($8,''), 'filesystem', $2, NULLIF($3,0), NULLIF($4,''),
			 $5, 'cloud', $6, 'document_create', $7::jsonb)
		ON CONFLICT (virtual_path) WHERE deleted_at IS NULL DO UPDATE
			SET storage_path = EXCLUDED.storage_path,
			    storage_size = EXCLUDED.storage_size,
			    storage_mime = EXCLUDED.storage_mime,
			    description  = COALESCE(NULLIF(EXCLUDED.description,''), mem_artifacts.description),
			    metadata     = EXCLUDED.metadata,
			    updated_at   = NOW()
		RETURNING id::text
	`, filename, path, bytes, mimeForFormat(format), vpath, sessionID, string(metaJSON), description).Scan(&id)
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
	// UpdatedAt is the document's VERSION. Jarvis redoing a document reuses its
	// path, so nothing downstream could tell the bytes had changed: an open tab
	// kept showing the render it fetched once while the download handed over the
	// new file. The upsert in recordDocumentArtifact bumps this, so it is the
	// signal every preview keys off.
	UpdatedAt time.Time `json:"updated_at"`
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
	// Two things the raw row cannot answer on its own, both of which decide
	// whether the viewer can SHOW the document instead of offering a download:
	//
	//   path    an uploaded file's storage_path is "attachment:<id>" (bytes in
	//           Postgres). The workspace MIRROR of that upload is a real path
	//           the page-rasterizer can render, so prefer it when it exists;
	//           the download proxy resolves the "attachment:" form either way.
	//   format  only document_create stamps metadata->>'format'. An upload has
	//           none, and an empty format used to leave the viewer with no
	//           preview path at all - the filename extension is the honest
	//           answer and it is right there in the name.
	rows, err := s.pool.Query(r.Context(), `
		SELECT a.id::text, a.name,
		       COALESCE(NULLIF(at.workspace_path,''), a.storage_path, '')  AS storage_path,
		       COALESCE(a.storage_size,0)                                  AS storage_size,
		       COALESCE(
		         NULLIF(a.metadata->>'format',''),
		         lower(substring(a.name from '\.([A-Za-z0-9]+)$')),
		         ''
		       )                                                           AS format,
		       COALESCE(a.metadata->>'pdf_path','')                        AS pdf_path,
		       COALESCE(a.metadata->>'thumb_path','')                      AS thumb_path,
		       COALESCE(a.metadata->>'html_path','')                       AS html_path,
		       COALESCE(a.metadata->>'markdown','')                        AS markdown,
		       a.created_at,
		       COALESCE(a.updated_at, a.created_at)                        AS updated_at
		  FROM mem_artifacts a
		  LEFT JOIN mem_attachments at ON a.storage_path = 'attachment:' || at.id::text
		 WHERE a.kind='document' AND a.deleted_at IS NULL AND a.source_session_id = $1
		 ORDER BY a.created_at DESC
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
		if err := rows.Scan(&d.ID, &d.Filename, &d.Path, &d.Bytes, &d.Format, &d.PDFPath, &d.ThumbPath, &d.HTMLPath, &d.Markdown, &d.CreatedAt, &d.UpdatedAt); err != nil {
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
	p := strings.TrimSpace(r.URL.Query().Get("path"))
	if p == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	// A file the boss UPLOADED is an artifact like any other (it shows in the
	// gallery, it opens in the document viewer), but its bytes live in
	// Postgres under "attachment:<id>", not on the workspace volume. Resolving
	// that here - at the one seam every document fetch goes through: preview,
	// thumbnail and download - is what makes an uploaded PDF viewable instead
	// of a dead download card. Handled before the proxy so it works even when
	// the workspace mirror is missing or the volume is down.
	if id, ok := strings.CutPrefix(p, "attachment:"); ok {
		disp := "inline"
		if r.URL.Query().Get("download") == "1" {
			disp = "attachment"
		}
		s.serveAttachmentBytes(w, r, id, disp)
		return
	}
	base := strings.TrimRight(s.cfg.WorkspaceRawBase, "/")
	if base == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace not configured"})
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

// handleArtifactDelete removes a generated artifact (document OR media) the boss
// deleted from the Media tab: it soft-deletes the mem_artifacts row (so it
// leaves the gallery — realtime pushes the change) AND erases the actual files
// from the workspace volume (native file + sibling PDF + thumbnail + HTML + page
// renders). For media (image/video), it ALSO strips the asset from its
// media.generate run's meta.media — that run, not the artifact row, is what the
// Media gallery renders for images/videos, so the strip is what makes a deleted
// image actually disappear. File deletion is skipped if another live artifact
// references the same bytes (defensive — never orphan a still-listed item).
// Best-effort on each file so a missing derivative doesn't block the row delete.
//
//	POST /api/canvas/artifact/delete { "id": "<mem_artifacts id>" }
func (s *Server) handleArtifactDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	if s.pool == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "db unavailable"})
		return
	}
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || strings.TrimSpace(req.ID) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "id required"})
		return
	}
	id := strings.TrimSpace(req.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()

	var storagePath, metaRaw, kind string
	err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(storage_path,''), COALESCE(metadata::text,'{}'), kind
		  FROM mem_artifacts WHERE id::text = $1 AND deleted_at IS NULL
	`, id).Scan(&storagePath, &metaRaw, &kind)
	if err != nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "artifact not found"})
		return
	}

	// Erase files only when no OTHER live artifact points at the same bytes.
	shared := 0
	if storagePath != "" {
		_ = s.pool.QueryRow(ctx, `
			SELECT count(*) FROM mem_artifacts
			 WHERE deleted_at IS NULL AND id::text <> $1 AND storage_path = $2
		`, id, storagePath).Scan(&shared)
	}

	deletedFiles := 0
	if shared == 0 {
		var meta struct {
			PDFPath   string `json:"pdf_path"`
			ThumbPath string `json:"thumb_path"`
			HTMLPath  string `json:"html_path"`
		}
		_ = json.Unmarshal([]byte(metaRaw), &meta)
		paths := []string{storagePath, meta.PDFPath, meta.ThumbPath, meta.HTMLPath}
		// Derive the .pages/<base> render dir (from /docpages) so it's cleaned too.
		base := meta.PDFPath
		if base == "" {
			base = storagePath
		}
		if base != "" {
			stem := strings.TrimSuffix(filepath.Base(base), filepath.Ext(base))
			paths = append(paths, filepath.Join(filepath.Dir(base), ".pages", stem))
		}
		for _, p := range paths {
			if strings.TrimSpace(p) == "" {
				continue
			}
			if s.deleteWorkspaceFile(ctx, p) {
				deletedFiles++
			}
		}
	}

	// For media, also remove the asset from its run's meta.media — that's the
	// gallery's actual data source for images/videos, and mem_runs is realtime so
	// the tile vanishes live.
	if kind == "image" || kind == "video" {
		_, _ = s.pool.Exec(ctx, `
			UPDATE mem_runs
			   SET meta = jsonb_set(meta, '{media}', COALESCE((
			         SELECT jsonb_agg(elem)
			           FROM jsonb_array_elements(meta->'media') elem
			          WHERE elem->>'id' <> $1
			       ), '[]'::jsonb))
			 WHERE kind = 'media.generate'
			   AND meta->'media' @> jsonb_build_array(jsonb_build_object('id', $1::text))
		`, id)
	}

	if _, err := s.pool.Exec(ctx, `UPDATE mem_artifacts SET deleted_at = NOW() WHERE id::text = $1 AND deleted_at IS NULL`, id); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "files_removed": deletedFiles})
}

// deleteWorkspaceFile asks the workspace bridge to remove one path. Best-effort:
// returns true on a 2xx, false otherwise (a missing derivative must not abort
// the overall delete). 404/no-base are treated as "nothing to remove".
func (s *Server) deleteWorkspaceFile(ctx context.Context, path string) bool {
	base := strings.TrimRight(s.cfg.WorkspaceRawBase, "/")
	if base == "" {
		return false
	}
	body, _ := json.Marshal(map[string]string{"path": path})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/fs/delete", strings.NewReader(string(body)))
	if err != nil {
		return false
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.WorkspaceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.WorkspaceToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// handleWorkspaceDocPages proxies a page-rasterization request to the workspace
// bridge's /docpages, which renders a document's pages to individual PNGs so
// Studio can show ONE slide at a time (a proper page-by-page viewer) instead of
// the browser's native continuous-scroll PDF iframe. Studio POSTs the preview
// path; the response is { count, pages: [workspace paths] }, and each page image
// is then fetched through /api/workspace/download like the PDF/thumbnail.
//
//	POST /api/workspace/docpages { "path": "<cloud workspace path>" }
func (s *Server) handleWorkspaceDocPages(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(s.cfg.WorkspaceRawBase, "/")
	if base == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "workspace not configured"})
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	if strings.TrimSpace(req.Path) == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "path required"})
		return
	}
	// docpages can rasterize many pages (or convert an office file first), so
	// allow a generous timeout — first render of a big deck is the slow case.
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	body, _ := json.Marshal(map[string]string{"path": strings.TrimSpace(req.Path)})
	preq, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/docpages", strings.NewReader(string(body)))
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	preq.Header.Set("Content-Type", "application/json")
	if s.cfg.WorkspaceToken != "" {
		preq.Header.Set("Authorization", "Bearer "+s.cfg.WorkspaceToken)
	}
	resp, err := http.DefaultClient.Do(preq)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "workspace fetch failed: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, io.LimitReader(resp.Body, 1<<20))
}
