package browser

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/dopesoft/infinity/core/internal/tools"
)

// fakeSidecar models the browser sidecar closely enough to reproduce the
// outage this file guards: a hard concurrent-session cap, ids that keep
// occupying a slot until Close, and sessions whose browser context can die
// while the id still looks perfectly live.
type fakeSidecar struct {
	mu sync.Mutex

	max int
	seq int

	live   map[string]bool // id → still held by the sidecar
	closed []string
	opened []string

	// deadOnArrival makes the next N created sessions come back the way the
	// real sidecar did: HTTP 200, a real session id, and "context canceled"
	// in the error field.
	deadOnArrival int

	// verbErr is returned by navigate/observe/extract/act for these ids.
	verbErr map[string]error

	// subscribes counts screencast attachments; dropStreams makes that many
	// of them end immediately with an error while the session stays alive,
	// which is the "the view broke, the browser is fine" case.
	subscribes  int
	dropStreams int
}

func newFakeSidecar(max int) *fakeSidecar {
	return &fakeSidecar{max: max, live: map[string]bool{}, verbErr: map[string]error{}}
}

func (f *fakeSidecar) liveCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.live)
}

func (f *fakeSidecar) wasClosed(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.closed {
		if c == id {
			return true
		}
	}
	return false
}

// killSession is the sidecar losing a session's browser context: the id stays
// allocated against the cap, but every verb from now on fails.
func (f *fakeSidecar) killSession(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.verbErr[id] = errors.New("browser sidecar: context canceled")
}

// forget drops a session from the sidecar's own registry, so ListSessions no
// longer names it — the session is genuinely gone, not merely broken.
func (f *fakeSidecar) forget(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, id)
}

func (f *fakeSidecar) errFor(id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.live[id] {
		return errors.New("browser sidecar: session not found or expired")
	}
	return f.verbErr[id]
}

func (f *fakeSidecar) CreateSession(ctx context.Context, url string) (*SessionInfo, error) {
	f.mu.Lock()
	if len(f.live) >= f.max {
		f.mu.Unlock()
		return nil, errors.New("browser sidecar: max 2 concurrent browser sessions reached — close one first")
	}
	f.seq++
	id := "br_" + string(rune('a'+f.seq-1))
	f.live[id] = true
	f.opened = append(f.opened, id)
	doa := f.deadOnArrival > 0
	if doa {
		f.deadOnArrival--
		f.verbErr[id] = errors.New("browser sidecar: context canceled")
	}
	f.mu.Unlock()

	info := &SessionInfo{SessionID: id, URL: url, Title: "Fake"}
	if url == "" {
		info.URL = "about:blank"
	}
	if doa {
		info.URL = "about:blank"
		info.Title = ""
		info.Error = "context canceled"
	}
	return info, nil
}

func (f *fakeSidecar) Close(ctx context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, id)
	if !f.live[id] {
		return errors.New("browser sidecar: session not found or expired")
	}
	delete(f.live, id)
	delete(f.verbErr, id)
	return nil
}

func (f *fakeSidecar) Navigate(ctx context.Context, id, url string) (*NavResult, error) {
	if err := f.errFor(id); err != nil {
		return nil, err
	}
	return &NavResult{URL: url, Title: "Fake"}, nil
}

func (f *fakeSidecar) Observe(ctx context.Context, id string) (*ObserveResult, error) {
	if err := f.errFor(id); err != nil {
		return nil, err
	}
	return &ObserveResult{Title: "Fake", URL: "https://example.com", Text: "hello"}, nil
}

func (f *fakeSidecar) Extract(ctx context.Context, id, format string) (*ExtractResult, error) {
	if err := f.errFor(id); err != nil {
		return nil, err
	}
	return &ExtractResult{Title: "Fake", URL: "https://example.com", Content: "body"}, nil
}

func (f *fakeSidecar) Act(ctx context.Context, id string, req ActRequest) (*ActResult, error) {
	if err := f.errFor(id); err != nil {
		return nil, err
	}
	return &ActResult{OK: true, URL: "https://example.com"}, nil
}

func (f *fakeSidecar) Input(ctx context.Context, id string, ev InputEvent) error { return f.errFor(id) }
func (f *fakeSidecar) Screenshot(ctx context.Context, id string) (*ShotResult, error) {
	return &ShotResult{}, f.errFor(id)
}
func (f *fakeSidecar) Health(ctx context.Context) error { return nil }

// SubscribeScreencast stays open for the life of the relay context. A channel
// that closed immediately would make the registry tear the session down on its
// own and hide the behaviour under test.
func (f *fakeSidecar) SubscribeScreencast(ctx context.Context, id string) (*Stream, error) {
	f.mu.Lock()
	f.subscribes++
	drop := f.dropStreams > 0
	if drop {
		f.dropStreams--
	}
	f.mu.Unlock()

	ch := make(chan Frame)
	s := &Stream{Frames: ch}
	if drop {
		// The view dies while the session underneath it is perfectly fine.
		s.setErr(errors.New("screencast stream: unexpected EOF"))
		close(ch)
		return s, nil
	}
	go func() {
		<-ctx.Done()
		close(ch)
	}()
	return s, nil
}

// ListSessions makes the fake a sessionLister, which is what lets the registry
// ask "is this session actually gone, or did only the view break?".
func (f *fakeSidecar) ListSessions(ctx context.Context) ([]RemoteSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]RemoteSession, 0, len(f.live))
	for id := range f.live {
		out = append(out, RemoteSession{SessionID: id})
	}
	return out, nil
}

// subscribeCount reports how many times the relay has attached to a stream.
func (f *fakeSidecar) subscribeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.subscribes
}

// dropNextStreams makes the next n subscriptions end immediately with an
// error, simulating a screencast that keeps dropping while the browser lives.
func (f *fakeSidecar) dropNextStreams(n int) {
	f.mu.Lock()
	f.dropStreams = n
	f.mu.Unlock()
}

var _ Backend = (*fakeSidecar)(nil)

func testCtx() context.Context {
	return tools.WithSessionID(context.Background(), "chat-1")
}

// ── dead-on-arrival: the session that looked open and never was ────────────

// A create that comes back with "context canceled" is a session nothing can
// drive, and the sidecar keeps holding its slot. Handing that id to the agent
// is what produced the reported loop: browser_open reports success with a
// note, every following verb fails, and the slot stays gone.
func TestOpenDiscardsDeadOnArrivalSessionAndRetries(t *testing.T) {
	f := newFakeSidecar(2)
	f.deadOnArrival = 1
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if info.Error != "" {
		t.Fatalf("Open returned a session still flagged dead: %q", info.Error)
	}
	if !f.wasClosed(f.opened[0]) {
		t.Fatalf("dead-on-arrival session %s was never closed — its sidecar slot leaks", f.opened[0])
	}
	if got := f.liveCount(); got != 1 {
		t.Fatalf("live sessions = %d, want 1 (only the healthy replacement)", got)
	}
	if id, ok := r.Resolve("chat-1", ""); !ok || id != info.SessionID {
		t.Fatalf("registry tracks %q/%v, want the healthy session %q", id, ok, info.SessionID)
	}
}

// When the sidecar cannot produce a usable session at all, Open must fail
// loudly AND leave no slots occupied. A returned-but-dead session would read
// to the agent as success.
func TestOpenFailsLoudlyAndLeaksNoSlotsWhenSidecarStaysDead(t *testing.T) {
	f := newFakeSidecar(2)
	f.deadOnArrival = 5
	r := NewRegistry(f, nil)

	if _, err := r.Open(testCtx(), "chat-1", "https://example.com"); err == nil {
		t.Fatal("Open succeeded with a dead session; want a loud error")
	}
	if got := f.liveCount(); got != 0 {
		t.Fatalf("live sessions = %d after a failed open, want 0 — capacity leaked", got)
	}
}

// The reported end state, driven the way the agent actually drove it: sessions
// keep dying, the agent keeps reaching for the browser, and nobody ever calls
// browser_close on a corpse. Each dead session used to stay allocated on the
// sidecar, so a couple of rounds walled the agent off behind "max 2 concurrent
// browser sessions" while browser_close insisted there was nothing open.
// Capacity must hold no matter how many times a session dies.
//
// Two invariants, and they pull in opposite directions on purpose. Capacity
// must never leak, however many sessions die — that is the original outage.
// But recovery must also not be INFINITE: a browser that dies over and over is
// a fault, and silently reopening through it lets a broken browser report
// progress forever, which is the same empty-because-broken-reads-as-fine
// failure in a different costume. So the recovery chain caps
// (maxAutoRecoveries) and surfaces the fault, and the eviction still runs on
// the way out so refusing to reopen never costs a slot.
func TestDeadSessionsNeverExhaustCapacityAcrossVerbs(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)
	observe := &ObserveTool{Reg: r}

	surfaced := 0
	for i := 0; i < 6; i++ {
		if id, ok := r.Resolve("chat-1", ""); ok {
			f.killSession(id) // the browser dies between turns
		}
		out, err := observe.Execute(testCtx(), map[string]any{})
		if err != nil {
			surfaced++
			// A capped recovery must blame the browser, not the page — that
			// is the whole point of stopping.
			if !strings.Contains(err.Error(), "browser itself") {
				t.Fatalf("iteration %d: failure does not name the browser as the fault: %v", i, err)
			}
		} else if !strings.Contains(out, "example.com") {
			t.Fatalf("iteration %d: no page content returned:\n%s", i, out)
		}
		if got := f.liveCount(); got > 1 {
			t.Fatalf("iteration %d: %d sessions held on the sidecar, want at most 1 — dead sessions are leaking slots", i, got)
		}
	}
	if surfaced == 0 {
		t.Fatal("six sessions died in a row and every one was absorbed silently — a permanently broken browser would report progress forever")
	}
}

// ── eviction chokepoint ────────────────────────────────────────────────────

// A session that dies mid-flight must be forgotten by core AND closed on the
// sidecar. Forgetting without closing is what leaves an unnameable zombie
// holding the cap: browser_close can't name it, and creates bounce off "max
// concurrent" for the full idle timeout.
func TestEvictIfDeadClosesBothHalves(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.killSession(info.SessionID)

	if !r.EvictIfDead(testCtx(), info.SessionID, errors.New("browser sidecar: context canceled")) {
		t.Fatal("EvictIfDead returned false for a context-canceled session")
	}
	if !f.wasClosed(info.SessionID) {
		t.Fatal("sidecar half was never closed — the slot leaks")
	}
	if f.liveCount() != 0 {
		t.Fatalf("live sessions = %d, want 0", f.liveCount())
	}
	if id, ok := r.Resolve("chat-1", ""); ok {
		t.Fatalf("registry still resolves the dead session as %q", id)
	}
}

// "context canceled" is ALSO what a cancelled turn looks like, and that says
// nothing about the browser. Tearing the session down there would destroy a
// perfectly good browser every time the boss interrupts.
func TestEvictIfDeadIgnoresCallerCancellation(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	cancelled, cancel := context.WithCancel(testCtx())
	cancel()

	if r.EvictIfDead(cancelled, info.SessionID, context.Canceled) {
		t.Fatal("evicted a live session because the CALLER's context was cancelled")
	}
	if f.liveCount() != 1 {
		t.Fatalf("live sessions = %d, want 1 (session should survive)", f.liveCount())
	}
	if _, ok := r.Resolve("chat-1", ""); !ok {
		t.Fatal("registry forgot a session that is still alive")
	}
}

// A page-level failure is not a session failure. Laundering one into the other
// would restart the browser on every bad selector and hide the real error.
func TestEvictIfDeadIgnoresPageLevelErrors(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if r.EvictIfDead(testCtx(), info.SessionID, errors.New("element 4 not found for select")) {
		t.Fatal("evicted a live session over a page-level error")
	}
	if f.liveCount() != 1 {
		t.Fatalf("live sessions = %d, want 1", f.liveCount())
	}
}

// ── verb-level recovery ────────────────────────────────────────────────────

// browser_observe on a session that died must reopen and look, not hand the
// agent a raw "browser sidecar: context canceled".
func TestObserveRecoversFromDeadSession(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dead := info.SessionID
	f.killSession(dead)

	out, err := (&ObserveTool{Reg: r}).Execute(testCtx(), map[string]any{})
	if err != nil {
		t.Fatalf("Observe did not recover: %v", err)
	}
	if !strings.Contains(out, "example.com") {
		t.Fatalf("Observe returned no page content:\n%s", out)
	}
	if !f.wasClosed(dead) {
		t.Fatal("the dead session was never closed on the sidecar")
	}
	if f.liveCount() != 1 {
		t.Fatalf("live sessions = %d, want 1 (dead one reclaimed)", f.liveCount())
	}
}

// A real observe failure on a healthy session must surface, not be swallowed
// by a session restart.
func TestObservePropagatesRealErrors(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	f.mu.Lock()
	f.verbErr[info.SessionID] = errors.New("page crashed while evaluating observe script")
	f.mu.Unlock()

	if _, err := (&ObserveTool{Reg: r}).Execute(testCtx(), map[string]any{}); err == nil {
		t.Fatal("a real observe failure was swallowed")
	} else if !strings.Contains(err.Error(), "page crashed") {
		t.Fatalf("original error was laundered: %v", err)
	}
}

// browser_navigate on a dead session must reopen straight onto the target URL.
func TestNavigateRecoversFromDeadSession(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dead := info.SessionID
	f.killSession(dead)

	out, err := (&NavigateTool{Reg: r}).Execute(testCtx(), map[string]any{"url": "https://target.test"})
	if err != nil {
		t.Fatalf("Navigate did not recover: %v", err)
	}
	if !strings.Contains(out, "https://target.test") {
		t.Fatalf("did not land on the requested URL:\n%s", out)
	}
	if !f.wasClosed(dead) {
		t.Fatal("the dead session was never closed on the sidecar")
	}
	if f.liveCount() != 1 {
		t.Fatalf("live sessions = %d, want 1", f.liveCount())
	}
}

// browser_act must reclaim the slot but never blind-retry: a replacement
// browser is a different page, so the last observe's indexes are meaningless
// and a re-click could act on the wrong element.
func TestActEvictsDeadSessionWithoutRetrying(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	dead := info.SessionID
	f.killSession(dead)

	_, err = (&ActTool{Reg: r}).Execute(testCtx(), map[string]any{"index": 1, "action": "click"})
	if err == nil {
		t.Fatal("Act silently retried on a fresh session; indexes would be stale")
	}
	if !strings.Contains(err.Error(), "browser_observe") {
		t.Fatalf("Act error does not tell the agent how to continue: %v", err)
	}
	if !f.wasClosed(dead) {
		t.Fatal("Act left the dead session holding a sidecar slot")
	}
	if f.liveCount() != 0 {
		t.Fatalf("live sessions = %d, want 0", f.liveCount())
	}
}

func TestIsDeadSessionText(t *testing.T) {
	dead := []string{
		"browser sidecar: context canceled",
		"context cancelled",
		"browser sidecar: session not found or expired",
		"browser session died before it could be used: context canceled",
		"websocket: close 1006",
	}
	for _, s := range dead {
		if !isDeadSessionText(s) {
			t.Errorf("isDeadSessionText(%q) = false, want true", s)
		}
	}
	alive := []string{"", "   ", "element 4 not found for select", "browser sidecar: HTTP 500", "navigation timed out"}
	for _, s := range alive {
		if isDeadSessionText(s) {
			t.Errorf("isDeadSessionText(%q) = true, want false", s)
		}
	}
}
