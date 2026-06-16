package server

import (
	"context"
	"io"
	"net/http"
	"strings"
	"time"
)

// terminal_pty.go proxies Studio's interactive terminal (xterm.js) to the cloud
// workspace bridge's /pty/* endpoints. The workspace is Jarvis's always-on
// cloud computer (where installed CLIs + their saved credentials live), so the
// interactive shell runs there - this is the surface that can actually run a
// device `auth login`, an `ssh`, a REPL, or `vim`, which the one-shot
// /api/canvas/terminal/exec route never could.
//
// Transport mirrors the bridge: JSON POST for start/input/resize/close, and a
// pass-through SSE stream for output. We reuse WorkspaceRawBase/WorkspaceToken
// (already wired for raw workspace proxying) so no new transport or env is
// needed.
//
//	POST /api/canvas/terminal/pty/start          -> {pty_id}
//	GET  /api/canvas/terminal/pty/{id}/stream    (SSE pass-through)
//	POST /api/canvas/terminal/pty/{id}/input     {data}
//	POST /api/canvas/terminal/pty/{id}/resize    {cols, rows}
//	POST /api/canvas/terminal/pty/{id}/close
func (s *Server) handleTerminalPTY(w http.ResponseWriter, r *http.Request) {
	base := strings.TrimRight(s.cfg.WorkspaceRawBase, "/")
	if base == "" {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "interactive terminal needs the cloud workspace, which isn't configured"})
		return
	}
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/canvas/terminal/pty"), "/")

	// /start has no id; everything else is /{id}/{verb}.
	if rest == "start" {
		s.ptyProxyJSON(w, r, base+"/pty/start")
		return
	}
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pty id and verb required"})
		return
	}
	id, verb := rest[:slash], rest[slash+1:]
	target := base + "/pty/" + id + "/" + verb
	if verb == "stream" {
		s.ptyProxyStream(w, r, target)
		return
	}
	s.ptyProxyJSON(w, r, target)
}

// ptyProxyJSON forwards a POST (start/input/resize/close) to the bridge and
// relays the JSON response verbatim.
func (s *Server) ptyProxyJSON(w http.ResponseWriter, r *http.Request, target string) {
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, r.Body)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Content-Type", "application/json")
	if s.cfg.WorkspaceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.WorkspaceToken)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "workspace unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ptyProxyStream pipes the bridge's SSE output stream to the client with no
// timeout, flushing each chunk so keystrokes echo instantly.
func (s *Server) ptyProxyStream(w http.ResponseWriter, r *http.Request, target string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	// No client timeout: the stream is long-lived. It ends when the client
	// disconnects (r.Context cancels) or the shell exits.
	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, target, nil)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	req.Header.Set("Accept", "text/event-stream")
	if s.cfg.WorkspaceToken != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.WorkspaceToken)
	}
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": "workspace unreachable: " + err.Error()})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, resp.StatusCode, map[string]string{"error": "pty stream not available"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	buf := make([]byte, 32<<10)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			flusher.Flush()
		}
		if err != nil {
			return
		}
	}
}
