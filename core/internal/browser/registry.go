package browser

import (
	"context"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/runs"
)

// infoLog → stdout so Railway tags these severity:info, not error.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// FrameSink receives a screencast frame for a chat session. serve.go wires
// this to the WS session broadcaster so frames reach the live Studio tab;
// when unset, frames are dropped and browsing still works headless.
type FrameSink func(chatSessionID string, f Frame)

// idleReap closes a core-side session whose tools haven't touched it in
// this long. Slightly longer than the sidecar's own 30m reap so the sidecar
// is the primary GC and we just close the mem_runs row afterwards.
const idleReap = 35 * time.Minute

// Registry tracks live browser sessions, ties each to a mem_runs row, and
// runs a per-session relay goroutine that streams the sidecar's screencast
// to Studio. The relay lifetime equals the browser session — NOT a single
// tool call — so the boss watches continuously across observe/act/extract.
type Registry struct {
	backend Backend
	tracker *runs.Tracker

	mu           sync.Mutex
	sessions     map[string]*entry // browser session id → entry
	latestByChat map[string]string // chat session id → most-recent browser session id
	sink         FrameSink
}

type entry struct {
	browserID string
	chatID    string
	handle    *runs.Handle
	cancel    context.CancelFunc // stops the relay goroutine
	url       string
	lastUsed  time.Time
	done      bool
}

func NewRegistry(backend Backend, tracker *runs.Tracker) *Registry {
	r := &Registry{
		backend:      backend,
		tracker:      tracker,
		sessions:     make(map[string]*entry),
		latestByChat: make(map[string]string),
	}
	if backend != nil {
		go r.janitor()
	}
	return r
}

// Enabled reports whether the browser backend is configured.
func (r *Registry) Enabled() bool { return r != nil && r.backend != nil }

// SetSink wires the frame broadcaster. Called once from serve.go after the
// HTTP server (which owns the WS session map) is constructed.
func (r *Registry) SetSink(sink FrameSink) {
	r.mu.Lock()
	r.sink = sink
	r.mu.Unlock()
}

func (r *Registry) emit(chatID string, f Frame) {
	r.mu.Lock()
	sink := r.sink
	r.mu.Unlock()
	if sink != nil {
		sink(chatID, f)
	}
}

// Open creates a browser session, books a mem_runs row, and starts the
// screencast relay. chatID is the Studio chat session the frames route to.
func (r *Registry) Open(ctx context.Context, chatID, url string) (*SessionInfo, error) {
	info, err := r.backend.CreateSession(ctx, url)
	if err != nil {
		return nil, err
	}

	label := "browser session"
	if info.URL != "" && info.URL != "about:blank" {
		label = "browser: " + info.URL
	}
	handle := r.tracker.Begin(ctx, runs.KindBrowserSession, info.SessionID, label, runs.SourceAgent)

	// Relay context is independent of the tool-call ctx so the screencast
	// keeps streaming across every later observe/act/extract call. It ends
	// only on Close, idle-reap, or core shutdown.
	relayCtx, cancel := context.WithCancel(context.Background())
	e := &entry{
		browserID: info.SessionID,
		chatID:    chatID,
		handle:    handle,
		cancel:    cancel,
		url:       info.URL,
		lastUsed:  time.Now(),
	}
	r.mu.Lock()
	r.sessions[info.SessionID] = e
	if chatID != "" {
		r.latestByChat[chatID] = info.SessionID
	}
	r.mu.Unlock()

	go r.relay(relayCtx, e)
	infoLog.Printf("browser: opened session %s for chat %s", info.SessionID, chatID)
	return info, nil
}

// relay streams frames from the sidecar to the chat session until ctx ends
// or the sidecar stream closes.
func (r *Registry) relay(ctx context.Context, e *entry) {
	frames, err := r.backend.SubscribeScreencast(ctx, e.browserID)
	if err != nil {
		log.Printf("browser: screencast subscribe failed for %s: %v", e.browserID, err)
		return
	}
	for f := range frames {
		r.mu.Lock()
		f.URL = e.url
		r.mu.Unlock()
		f.BrowserID = e.browserID
		r.emit(e.chatID, f)
	}
	// Stream ended (sidecar session gone). Close out the run if still open.
	r.finish(context.Background(), e.browserID, nil)
}

// Resolve returns the browser session id a tool should act on: the explicit
// id if it names a live session, otherwise the most-recent session for the
// chat. The bool is false when neither resolves (agent must Open first).
func (r *Registry) Resolve(chatID, explicit string) (string, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if explicit != "" {
		if e, ok := r.sessions[explicit]; ok {
			e.lastUsed = time.Now()
			return explicit, true
		}
		return "", false
	}
	if id, ok := r.latestByChat[chatID]; ok {
		if e, ok := r.sessions[id]; ok {
			e.lastUsed = time.Now()
			return id, true
		}
	}
	return "", false
}

// UpdateURL records the session's current URL (from a nav/act result) so
// the live tab toolbar and mem_runs label stay accurate.
func (r *Registry) UpdateURL(browserID, url string) {
	if url == "" {
		return
	}
	r.mu.Lock()
	if e, ok := r.sessions[browserID]; ok {
		e.url = url
		e.lastUsed = time.Now()
	}
	r.mu.Unlock()
}

// URL returns the last known URL for a session (used by the frame event).
func (r *Registry) URL(browserID string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if e, ok := r.sessions[browserID]; ok {
		return e.url
	}
	return ""
}

// Navigate points a live session at a new URL (Studio's editable URL bar in
// the live-browser toolbar). Updates the tracked URL so the toolbar + mem_runs
// label stay accurate. Errors if the session isn't live.
func (r *Registry) Navigate(ctx context.Context, browserID, url string) error {
	if !r.isLive(browserID) {
		return fmt.Errorf("browser session not found or already closed")
	}
	res, err := r.backend.Navigate(ctx, browserID, url)
	if err != nil {
		return err
	}
	if res != nil && res.URL != "" {
		r.UpdateURL(browserID, res.URL)
	} else {
		r.UpdateURL(browserID, url)
	}
	return nil
}

// Input forwards one raw human interaction (click/type/scroll) to a live
// session - the boss's manual takeover of the screencast.
func (r *Registry) Input(ctx context.Context, browserID string, ev InputEvent) error {
	if !r.isLive(browserID) {
		return fmt.Errorf("browser session not found or already closed")
	}
	return r.backend.Input(ctx, browserID, ev)
}

// isLive reports whether a browser session id maps to a live entry and bumps
// its lastUsed so manual interaction defers the idle reaper.
func (r *Registry) isLive(browserID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	e, ok := r.sessions[browserID]
	if ok && !e.done {
		e.lastUsed = time.Now()
		return true
	}
	return false
}

// Close tears down a session: stops the sidecar browser, ends the relay,
// closes the mem_runs row.
func (r *Registry) Close(ctx context.Context, browserID string) error {
	err := r.backend.Close(ctx, browserID)
	r.finish(ctx, browserID, err)
	return err
}

// finish closes the relay + mem_runs row exactly once for a session.
func (r *Registry) finish(ctx context.Context, browserID string, runErr error) {
	r.mu.Lock()
	e, ok := r.sessions[browserID]
	if !ok || e.done {
		r.mu.Unlock()
		return
	}
	e.done = true
	delete(r.sessions, browserID)
	if r.latestByChat[e.chatID] == browserID {
		delete(r.latestByChat, e.chatID)
	}
	r.mu.Unlock()

	if e.cancel != nil {
		e.cancel()
	}
	e.handle.Finish(ctx, runErr, "")
	infoLog.Printf("browser: closed session %s", browserID)
}

// janitor reaps core-side sessions idle past idleReap so a forgotten
// session's mem_runs row doesn't stay 'running' forever.
func (r *Registry) janitor() {
	t := time.NewTicker(5 * time.Minute)
	defer t.Stop()
	for range t.C {
		var stale []string
		now := time.Now()
		r.mu.Lock()
		for id, e := range r.sessions {
			if now.Sub(e.lastUsed) > idleReap {
				stale = append(stale, id)
			}
		}
		r.mu.Unlock()
		for _, id := range stale {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			_ = r.Close(ctx, id)
			cancel()
		}
	}
}
