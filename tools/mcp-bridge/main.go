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
		command:  cmd,
		sessions: map[string]*session{},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/sse", b.handleSSE)
	mux.HandleFunc("/messages", b.handleMessages)
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})

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
		http.Error(w, "no active transport", http.StatusNotFound)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
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

// Ensure unused imports are kept honest if I rework things.
var _ = context.Background
