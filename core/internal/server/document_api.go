package server

import (
	"context"
	"io"
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
func (s *Server) EmitDocumentCreated(sessionID, format, filename, path, markdown, pdfPath string, bytes int64) {
	if s == nil || sessionID == "" || filename == "" {
		return
	}
	s.sessionSender(sessionID)(wsServerEvent{
		Type:      "document_created",
		SessionID: sessionID,
		DocumentCreated: &wsDocumentCreated{
			Format:   format,
			Filename: filename,
			Path:     path,
			Bytes:    bytes,
			Markdown: markdown,
			PDFPath:  pdfPath,
		},
	})
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
