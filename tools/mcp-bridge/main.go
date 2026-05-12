// mcp-bridge — minimal stdio→SSE MCP bridge.
//
// Why this exists: mcp-proxy@6.4.6 has a session-routing regression where
// `tools/call` POSTs return "No active transport" after the initialize
// response. supergateway@3.4.3 only supports one SSE client per backend
// process and crashes on reconnect with "Already connected to a transport".
// Both are the popular community bridges; both are broken for Infinity's
// long-lived persistent SSE session with periodic keepalive reconnects.
//
// Design (intentionally minimal):
//   • One HTTP server, two routes: GET /sse, POST /messages.
//   • On GET /sse: mint a session ID, spawn a fresh `<command...>` child,
//     write `event: endpoint\ndata: /messages?sessionId=<id>\n\n` to the
//     response, then keep streaming the child's stdout lines as
//     `event: message\ndata: <json>\n\n`. Stays open until the client
//     disconnects, at which point we kill the child.
//   • On POST /messages?sessionId=<id>: look up the session, write the
//     body + newline to the child's stdin, return 202 Accepted. The
//     response (or absence of one for notifications) flows back through
//     the SSE stream the same way every other tool/call does.
//   • Many concurrent SSE clients are fine — each gets its own session,
//     its own child, its own stdin/stdout. No shared global MCP state.
//
// Run with: mcp-bridge -port 8765 -- claude mcp serve
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type session struct {
	id     string
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	closed chan struct{}
	// outbound lines from the child's stdout/stderr, ready to be written
	// to the SSE response. Buffered so a slow client can't block the
	// child's writer goroutine indefinitely; a full buffer drops oldest.
	out chan string
	mu  sync.Mutex
}

type bridge struct {
	command []string

	mu       sync.Mutex
	sessions map[string]*session

	supervisor *supervisor
}

func main() {
	port := flag.Int("port", 8765, "port to listen on")
	host := flag.String("host", "127.0.0.1", "host to bind")
	flag.Parse()

	cmd := flag.Args()
	if len(cmd) == 0 {
		log.Fatal("usage: mcp-bridge -port 8765 -- <command> [args...]")
	}

	b := &bridge{
		command:    cmd,
		sessions:   map[string]*session{},
		supervisor: newSupervisor(),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", b.handleSSE)
	mux.HandleFunc("/messages", b.handleMessages)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/supervisor/start", b.handleSupervisorStart)
	mux.HandleFunc("/supervisor/stop", b.handleSupervisorStop)
	mux.HandleFunc("/supervisor/active", b.handleSupervisorActive)
	mux.HandleFunc("/supervisor/status", b.handleSupervisorStatus)
	// Direct filesystem + git read endpoints. These bypass MCP entirely
	// — they shell out via Go's os/exec on the Mac. Used by Studio's
	// canvas instead of routing `ls` / `cat` through claude_code__Bash,
	// which is overkill (spawning a Claude Code session per directory
	// listing) and conceptually conflates a code-editing tool with
	// basic file browsing. Cloudflare Access still gates these because
	// they share the bridge's HTTP listener with /sse.
	mux.HandleFunc("/fs/ls", b.handleFSList)
	mux.HandleFunc("/fs/read", b.handleFSRead)
	mux.HandleFunc("/git/status", b.handleGitStatus)
	mux.HandleFunc("/git/diff", b.handleGitDiff)
	// Catch-all: every other path is reverse-proxied to the active
	// project's dev server. This is what makes preview.dopesoft.io a
	// single permanent tunnel that points at whatever the boss is
	// currently building.
	mux.HandleFunc("/", b.handlePreviewProxy)

	addr := fmt.Sprintf("%s:%d", *host, *port)
	log.Printf("mcp-bridge listening on %s, command: %s", addr, strings.Join(cmd, " "))
	srv := &http.Server{
		Addr:        addr,
		Handler:     mux,
		ReadTimeout: 0, // SSE is long-lived.
		IdleTimeout: 0,
	}
	if err := srv.ListenAndServe(); err != nil {
		log.Fatal(err)
	}
}

// handleSSE accepts a new SSE connection, spawns the child, and streams
// its stdout to the client until either side closes.
func (b *bridge) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	id, err := randomID()
	if err != nil {
		http.Error(w, "id generation failed", http.StatusInternalServerError)
		return
	}

	// Diagnostic — log enough of the incoming request to tell us why
	// some clients (Infinity Core via Railway) get their SSE GET closed
	// within a second while others (curl, Mac-local test programs)
	// keep it alive indefinitely.
	log.Printf("session %s: incoming GET from %s proto=%s ua=%q",
		id, r.RemoteAddr, r.Proto, r.Header.Get("User-Agent"))

	sess, err := b.spawn(id)
	if err != nil {
		log.Printf("session %s: spawn failed: %v", id, err)
		http.Error(w, "spawn failed: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer b.removeSession(id)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable proxy buffering (nginx, cloudflared)

	// Tell the client where to POST messages. This matches the
	// mcp-proxy / supergateway convention so clients written for those
	// bridges work unchanged.
	fmt.Fprintf(w, "event: endpoint\ndata: /messages?sessionId=%s\n\n", id)
	flusher.Flush()

	log.Printf("session %s: opened (pid=%d)", id, sess.cmd.Process.Pid)

	ctx := r.Context()
	keepalive := time.NewTicker(20 * time.Second)
	defer keepalive.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("session %s: client disconnected", id)
			return
		case <-sess.closed:
			log.Printf("session %s: child exited", id)
			return
		case line, ok := <-sess.out:
			if !ok {
				return
			}
			// Each child stdout line is a JSON-RPC message. Emit it as a
			// single SSE event so the client's SSE parser delivers it
			// atomically — splitting across events would break clients
			// that buffer per-event.
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", line)
			flusher.Flush()
		case <-keepalive.C:
			// Some intermediaries (cloudflared) close idle SSE streams.
			// Send a comment line every 20s as a heartbeat — invisible
			// to the client parser, keeps the socket warm.
			fmt.Fprintf(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// handleMessages forwards a POST body to the session's child stdin.
// The child's JSON-RPC response (if any) flows back through the SSE
// stream — never inline. Returns 202 Accepted unconditionally on a
// known session because that's the contract the MCP SDK expects.
func (b *bridge) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("sessionId")
	if id == "" {
		http.Error(w, "sessionId required", http.StatusBadRequest)
		return
	}
	b.mu.Lock()
	sess, ok := b.sessions[id]
	b.mu.Unlock()
	if !ok {
		log.Printf("messages POST: no session for id=%s from %s", id, r.RemoteAddr)
		http.Error(w, "no active transport", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	// Log the first 300 chars of the message so we can see what's being
	// requested. JSON-RPC payloads, never sensitive (no creds).
	preview := string(body)
	if len(preview) > 300 {
		preview = preview[:300] + "…"
	}
	log.Printf("session %s: POST body=%s", id, preview)
	// Collapse any embedded newlines, then add a trailing newline. MCP
	// stdio servers parse one JSON-RPC message per line.
	clean := strings.ReplaceAll(string(body), "\n", "")
	clean = strings.ReplaceAll(clean, "\r", "")

	sess.mu.Lock()
	defer sess.mu.Unlock()
	if _, err := io.WriteString(sess.stdin, clean+"\n"); err != nil {
		http.Error(w, "stdin write: "+err.Error(), http.StatusBadGateway)
		return
	}
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Accepted"))
}

// spawn creates a fresh child process for a session. The child's stdout
// is line-scanned and piped into the session's `out` channel; the child
// closes the session's `closed` channel when it exits.
func (b *bridge) spawn(id string) (*session, error) {
	cmd := exec.Command(b.command[0], b.command[1:]...)
	cmd.Env = os.Environ()

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	// Mirror stderr to our own stderr so the boss can see Claude Code's
	// errors during boot.
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start: %w", err)
	}

	sess := &session{
		id:     id,
		cmd:    cmd,
		stdin:  stdin,
		closed: make(chan struct{}),
		out:    make(chan string, 128),
	}

	b.mu.Lock()
	b.sessions[id] = sess
	b.mu.Unlock()

	// stdout pumper
	go func() {
		scanner := bufio.NewScanner(stdout)
		// JSON-RPC messages from Claude Code can be large (tool result
		// payloads, file contents). Default 64KiB scanner buffer is too
		// small; bump to 8MiB to match our HTTP body limit.
		buf := make([]byte, 0, 64*1024)
		scanner.Buffer(buf, 8<<20)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.TrimSpace(line) == "" {
				continue
			}
			select {
			case sess.out <- line:
			default:
				// Backpressure: drop oldest by draining one, then add.
				// In practice this shouldn't happen — the SSE handler
				// reads as fast as the child writes.
				select {
				case <-sess.out:
				default:
				}
				sess.out <- line
			}
		}
		if err := scanner.Err(); err != nil {
			log.Printf("session %s: scanner err: %v", id, err)
		}
	}()

	// child waiter — signal close when process exits.
	go func() {
		_ = cmd.Wait()
		close(sess.closed)
		close(sess.out)
	}()

	return sess, nil
}

func (b *bridge) removeSession(id string) {
	b.mu.Lock()
	sess, ok := b.sessions[id]
	if ok {
		delete(b.sessions, id)
	}
	b.mu.Unlock()
	if !ok {
		return
	}
	// Polite shutdown: close stdin so the child gets EOF, then if it
	// hasn't exited in 2 seconds, kill it.
	_ = sess.stdin.Close()
	done := make(chan struct{})
	go func() {
		<-sess.closed
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		_ = sess.cmd.Process.Kill()
	}
}

func randomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// ---- Direct filesystem + git endpoints ------------------------------------
//
// These run as plain HTTP handlers on the bridge — no MCP, no Claude Code,
// no SSE. Studio uses them for cheap operations like directory listings
// and file reads where spawning a Claude Code session per call is absurd.
// The bridge is already on the boss's Mac with full filesystem access;
// just expose the read paths directly.

type fsEntryJSON struct {
	Name  string `json:"name"`
	Type  string `json:"type"`           // "dir" | "file" | "symlink"
	Size  int64  `json:"size,omitempty"`
	MTime string `json:"mtime,omitempty"`
}

type fsListJSON struct {
	Path    string        `json:"path"`
	Entries []fsEntryJSON `json:"entries"`
}

func (b *bridge) handleFSList(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	entries, err := readDir(path)
	if err != nil {
		http.Error(w, "ls: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, fsListJSON{Path: path, Entries: entries})
}

func readDir(path string) ([]fsEntryJSON, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	names, err := f.Readdirnames(-1)
	if err != nil {
		return nil, err
	}
	out := make([]fsEntryJSON, 0, len(names))
	for _, n := range names {
		// Skip dotfiles by default (consistent with Studio's existing
		// behavior). Keep .gitignore / .env.example since they're often
		// part of a project's surface.
		if strings.HasPrefix(n, ".") && n != ".gitignore" && n != ".env.example" {
			continue
		}
		full := path + string(os.PathSeparator) + n
		st, err := os.Lstat(full)
		if err != nil {
			continue
		}
		e := fsEntryJSON{Name: n}
		switch {
		case st.Mode()&os.ModeSymlink != 0:
			e.Type = "symlink"
		case st.IsDir():
			e.Type = "dir"
		default:
			e.Type = "file"
			e.Size = st.Size()
		}
		// Optional mtime in RFC3339; cheap to compute.
		e.MTime = st.ModTime().UTC().Format("2006-01-02T15:04:05Z")
		out = append(out, e)
	}
	return out, nil
}

type fsReadJSON struct {
	Path    string `json:"path"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
}

func (b *bridge) handleFSRead(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	if path == "" {
		http.Error(w, "path required", http.StatusBadRequest)
		return
	}
	// Cap response size to keep memory predictable. Anything bigger than
	// 16MiB is too big for Monaco anyway.
	st, err := os.Stat(path)
	if err != nil {
		http.Error(w, "stat: "+err.Error(), http.StatusNotFound)
		return
	}
	const maxRead = 16 << 20
	if st.Size() > maxRead {
		http.Error(w, "file too large", http.StatusRequestEntityTooLarge)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		http.Error(w, "read: "+err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSONResponse(w, fsReadJSON{Path: path, Content: string(data), Size: st.Size()})
}

func (b *bridge) handleGitStatus(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		http.Error(w, "repo required", http.StatusBadRequest)
		return
	}
	out, err := runGit(r.Context(), repo, "status", "--porcelain=v2", "--branch")
	if err != nil {
		http.Error(w, "git status: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func (b *bridge) handleGitDiff(w http.ResponseWriter, r *http.Request) {
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		http.Error(w, "repo required", http.StatusBadRequest)
		return
	}
	args := []string{"diff", "--no-color"}
	if r.URL.Query().Get("staged") == "1" {
		args = append(args, "--staged")
	}
	if p := r.URL.Query().Get("path"); p != "" {
		args = append(args, "--", p)
	}
	out, err := runGit(r.Context(), repo, args...)
	if err != nil {
		http.Error(w, "git diff: "+err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(out))
}

func runGit(ctx context.Context, repo string, args ...string) (string, error) {
	full := append([]string{"-C", repo}, args...)
	cmd := exec.CommandContext(ctx, "git", full...)
	out, err := cmd.Output()
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}

func writeJSONResponse(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(v)
}
