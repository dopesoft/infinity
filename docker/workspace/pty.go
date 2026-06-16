package main

// pty.go — a real interactive PTY for the Terminal tab. The one-shot /bash
// route is command→result (no stdin, no interactivity), so it can't run a
// program that waits for input (a device `auth login`, an `ssh`, a REPL, `vim`).
// This adds a persistent pseudo-terminal the boss drives keystroke-by-keystroke
// from Studio's xterm.js terminal.
//
// Transport: SSE for output (same pattern as the browser screencast) + plain
// POST for input/resize/close. No WebSocket dependency. Core proxies these
// routes per the session's active bridge; the Mac bridge mirrors them so the
// terminal is identical on both.
//
//	POST /pty/start          {cwd?, cols?, rows?}        -> {pty_id}
//	GET  /pty/{id}/stream    (SSE)                       -> data: <base64 chunk>
//	POST /pty/{id}/input     {data}            (raw keystrokes the boss typed)
//	POST /pty/{id}/resize    {cols, rows}
//	POST /pty/{id}/close
//
// Each PTY is reaped after ptyIdle with no I/O so a forgotten tab doesn't pin a
// shell forever on the scale-to-zero box.

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

// ptyNewID returns a short random hex id for a pty session.
func ptyNewID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

const (
	ptyIdle       = 30 * time.Minute
	ptyReplayCap  = 256 << 10 // bytes of scrollback replayed to a (re)connecting viewer
	ptyReadChunk  = 32 << 10
	ptyDefaultCol = 80
	ptyDefaultRow = 24
)

// ptySession is one live pseudo-terminal + its subscribers.
type ptySession struct {
	id   string
	ptmx *os.File
	cmd  *exec.Cmd

	mu       sync.Mutex
	subs     map[chan []byte]struct{}
	replay   []byte // bounded scrollback so a reconnecting viewer sees recent output
	lastUsed time.Time
	closed   bool
}

func (s *ptySession) touch() {
	s.mu.Lock()
	s.lastUsed = time.Now()
	s.mu.Unlock()
}

func (s *ptySession) publish(b []byte) {
	cp := make([]byte, len(b))
	copy(cp, b)
	s.mu.Lock()
	s.replay = append(s.replay, cp...)
	if len(s.replay) > ptyReplayCap {
		s.replay = s.replay[len(s.replay)-ptyReplayCap:]
	}
	for ch := range s.subs {
		select {
		case ch <- cp:
		default: // slow viewer — drop rather than block the shell
		}
	}
	s.mu.Unlock()
}

func (s *ptySession) subscribe() (chan []byte, []byte, func()) {
	ch := make(chan []byte, 64)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	replay := make([]byte, len(s.replay))
	copy(replay, s.replay)
	s.mu.Unlock()
	var once sync.Once
	return ch, replay, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			close(ch)
			s.mu.Unlock()
		})
	}
}

func (s *ptySession) shutdown() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.mu.Unlock()
	if s.ptmx != nil {
		_ = s.ptmx.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// ptyManager owns the live PTYs.
type ptyManager struct {
	mu       sync.Mutex
	sessions map[string]*ptySession
}

var ptyMgr = &ptyManager{sessions: make(map[string]*ptySession)}

func (m *ptyManager) get(id string) *ptySession {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id]
}

func (m *ptyManager) put(s *ptySession) {
	m.mu.Lock()
	m.sessions[s.id] = s
	m.mu.Unlock()
}

func (m *ptyManager) remove(id string) {
	m.mu.Lock()
	s := m.sessions[id]
	delete(m.sessions, id)
	m.mu.Unlock()
	if s != nil {
		s.shutdown()
	}
}

// ptyJanitor reaps idle PTYs.
func ptyJanitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		var stale []string
		ptyMgr.mu.Lock()
		for id, s := range ptyMgr.sessions {
			s.mu.Lock()
			idle := time.Since(s.lastUsed)
			s.mu.Unlock()
			if idle > ptyIdle {
				stale = append(stale, id)
			}
		}
		ptyMgr.mu.Unlock()
		for _, id := range stale {
			infoLog.Printf("workspace bridge: reaping idle pty %s", id)
			ptyMgr.remove(id)
		}
	}
}

// ── routing ────────────────────────────────────────────────────────────────

// routePTY dispatches /pty/{id}/{verb}. /pty/start (no id) is handled separately.
func routePTY(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/pty/"), "/")
	if rest == "start" {
		handlePTYStart(w, r)
		return
	}
	slash := strings.LastIndex(rest, "/")
	if slash <= 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "pty id and verb required"})
		return
	}
	id, verb := rest[:slash], rest[slash+1:]
	s := ptyMgr.get(id)
	if s == nil {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "pty not found or closed"})
		return
	}
	switch verb {
	case "stream":
		handlePTYStream(w, r, s)
	case "input":
		handlePTYInput(w, r, s)
	case "resize":
		handlePTYResize(w, r, s)
	case "close":
		ptyMgr.remove(id)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown verb: " + verb})
	}
}

type ptyStartRequest struct {
	Cwd  string `json:"cwd,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
}

func handlePTYStart(w http.ResponseWriter, r *http.Request) {
	var req ptyStartRequest
	_ = readJSON(r, &req) // all fields optional
	cwd := workspaceRoot
	if strings.TrimSpace(req.Cwd) != "" {
		if c, err := resolvePath(req.Cwd); err == nil {
			cwd = c
		}
	}
	cols := req.Cols
	if cols == 0 {
		cols = ptyDefaultCol
	}
	rows := req.Rows
	if rows == 0 {
		rows = ptyDefaultRow
	}

	// Login shell so PATH / env.sh (HOME pinned to the volume) are sourced —
	// the same environment the agent's tools and installed CLIs see.
	cmd := exec.Command("bash", "-l")
	cmd.Dir = cwd
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "start pty: " + err.Error()})
		return
	}

	s := &ptySession{
		id:       ptyNewID(),
		ptmx:     ptmx,
		cmd:      cmd,
		subs:     make(map[chan []byte]struct{}),
		lastUsed: time.Now(),
	}
	ptyMgr.put(s)
	infoLog.Printf("workspace bridge: opened pty %s (cwd=%s)", s.id, cwd)

	// Pump PTY output to subscribers until the shell exits.
	go func() {
		buf := make([]byte, ptyReadChunk)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				s.touch()
				s.publish(buf[:n])
			}
			if err != nil {
				break
			}
		}
		ptyMgr.remove(s.id)
		infoLog.Printf("workspace bridge: pty %s exited", s.id)
	}()

	writeJSON(w, http.StatusOK, map[string]any{"pty_id": s.id})
}

func handlePTYStream(w http.ResponseWriter, r *http.Request, s *ptySession) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "streaming unsupported"})
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	ch, replay, unsub := s.subscribe()
	defer unsub()
	s.touch()

	// Replay recent scrollback so a (re)connecting terminal isn't blank.
	if len(replay) > 0 {
		fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString(replay))
		flusher.Flush()
	}

	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n") // keep the proxy connection warm
			flusher.Flush()
		case b, ok := <-ch:
			if !ok {
				return // session closed
			}
			fmt.Fprintf(w, "data: %s\n\n", base64.StdEncoding.EncodeToString(b))
			flusher.Flush()
		}
	}
}

type ptyInputRequest struct {
	Data string `json:"data"`
}

func handlePTYInput(w http.ResponseWriter, r *http.Request, s *ptySession) {
	var req ptyInputRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	s.touch()
	if _, err := s.ptmx.WriteString(req.Data); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type ptyResizeRequest struct {
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

func handlePTYResize(w http.ResponseWriter, r *http.Request, s *ptySession) {
	var req ptyResizeRequest
	if err := readJSON(r, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if req.Cols == 0 || req.Rows == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "cols and rows required"})
		return
	}
	s.touch()
	_ = pty.Setsize(s.ptmx, &pty.Winsize{Cols: req.Cols, Rows: req.Rows})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
