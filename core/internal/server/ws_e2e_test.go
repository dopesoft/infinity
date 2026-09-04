package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/turnctx"
	"github.com/gorilla/websocket"
)

// ws_e2e_test.go - the whole socket path, end to end, with a fake brain.
//
// The unit tests pin each piece (journal, writer, registry, guard). This is
// the story the boss actually lived: a socket dies in the middle of a turn,
// a new one arrives, and the reply must reach it. It goes through the REAL
// handler over a real WebSocket; only the agent loop is replaced, by the
// server.runLoop seam, so no provider is needed.

type loopFunc = func(ctx context.Context, sessionID, content, model string, steer <-chan agent.Steer, events chan<- agent.RunEvent) error

type socketHarness struct {
	srv *Server
	web *httptest.Server
}

func newSocketHarness(t *testing.T, run loopFunc) *socketHarness {
	t.Helper()
	s := &Server{
		turns:          map[string]*turnState{},
		broadcastConns: map[string]func(wsServerEvent){},
		runLoop:        run,
	}
	web := httptest.NewServer(http.HandlerFunc(s.handleWebSocket))
	t.Cleanup(web.Close)
	return &socketHarness{srv: s, web: web}
}

func (h *socketHarness) dial(t *testing.T) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(h.web.URL, "http") + "/ws"
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func sendFrame(t *testing.T, c *websocket.Conn, msg wsClientMessage) {
	t.Helper()
	if err := c.WriteJSON(msg); err != nil {
		t.Fatalf("send %s: %v", msg.Type, err)
	}
}

// nextOf reads frames until one of the wanted type arrives; anything else
// (pong, heartbeat, an intent reading) is skipped, because the test is about
// the turn, not about everything the socket carries.
func nextOf(t *testing.T, c *websocket.Conn, want string) wsServerEvent {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	var seen []string
	for {
		_ = c.SetReadDeadline(deadline)
		var ev wsServerEvent
		if err := c.ReadJSON(&ev); err != nil {
			t.Fatalf("waiting for %q (saw %v): %v", want, seen, err)
		}
		if ev.Type == want {
			return ev
		}
		seen = append(seen, ev.Type)
	}
}

// expectNone asserts that no frame of the given type arrives for a while.
func expectNone(t *testing.T, c *websocket.Conn, unwanted string, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for {
		_ = c.SetReadDeadline(deadline)
		var ev wsServerEvent
		if err := c.ReadJSON(&ev); err != nil {
			return // timed out: good
		}
		if ev.Type == unwanted {
			t.Fatalf("got a %q frame that must not arrive", unwanted)
		}
	}
}

// THE BOSS'S BUG, end to end. The socket that started the turn dies while the
// brain is mid-reply. A new socket attaches, is told the turn is in flight,
// receives what it missed, and then receives the rest of the turn LIVE,
// completion included. Every frame carries the one turn id the loop also saw
// on its context, and every delta says which message it belongs to.
func TestWS_ASocketLostMidTurnIsCaughtUpByAttach(t *testing.T) {
	release := make(chan struct{})
	var loopSaw struct {
		sync.Mutex
		turnID string
	}
	run := func(ctx context.Context, sessionID, content, model string, steer <-chan agent.Steer, events chan<- agent.RunEvent) error {
		loopSaw.Lock()
		loopSaw.turnID = turnctx.TurnID(ctx)
		loopSaw.Unlock()
		events <- agent.RunEvent{Kind: agent.EventDelta, SessionID: sessionID, TextDelta: "Hel", MsgIndex: 0}
		select {
		case <-release:
		case <-ctx.Done():
			return ctx.Err()
		}
		events <- agent.RunEvent{Kind: agent.EventDelta, SessionID: sessionID, TextDelta: "lo", MsgIndex: 0}
		events <- agent.RunEvent{Kind: agent.EventComplete, SessionID: sessionID, StopReason: "end_turn"}
		return nil
	}
	h := newSocketHarness(t, run)
	const sid = "11111111-1111-4111-8111-111111111111"

	c1 := h.dial(t)
	sendFrame(t, c1, wsClientMessage{Type: "message", SessionID: sid, Content: "hi", ClientID: "cm-1"})
	first := nextOf(t, c1, "delta")
	if first.Text != "Hel" || first.Seq != 1 || first.TurnID == "" || first.MsgIndex == nil || *first.MsgIndex != 0 {
		t.Fatalf("first delta = %+v", first)
	}
	// The socket dies mid-turn.
	_ = c1.Close()

	// A fresh socket attaches with nothing.
	c2 := h.dial(t)
	sendFrame(t, c2, wsClientMessage{Type: "attach", SessionID: sid, SinceSeq: 0})
	st := nextOf(t, c2, "turn_status")
	if st.TurnStatus == nil || !st.TurnStatus.InFlight || st.TurnStatus.TurnID != first.TurnID || st.TurnStatus.Replayed != 1 {
		t.Fatalf("turn_status after reconnect = %+v", st.TurnStatus)
	}
	replay := nextOf(t, c2, "delta")
	if !replay.Replay || replay.Seq != 1 || replay.Text != "Hel" {
		t.Fatalf("replayed delta = %+v", replay)
	}

	// The brain carries on; the rest of the turn lands on the NEW socket live.
	close(release)
	rest := nextOf(t, c2, "delta")
	if rest.Replay || rest.Seq != 2 || rest.Text != "lo" || rest.TurnID != first.TurnID {
		t.Fatalf("live delta after reconnect = %+v", rest)
	}
	done := nextOf(t, c2, "complete")
	if done.Seq != 3 || done.StopReason != "end_turn" || done.TurnID != first.TurnID {
		t.Fatalf("complete = %+v", done)
	}

	// The loop's mem_turns row would carry the same id as the frames.
	loopSaw.Lock()
	defer loopSaw.Unlock()
	if loopSaw.turnID != first.TurnID {
		t.Fatalf("loop saw turn id %q, frames carried %q", loopSaw.turnID, first.TurnID)
	}

	// A socket arriving after the end is told so, and gets no replay: a
	// finished turn is rebuilt from the transcript.
	c3 := h.dial(t)
	sendFrame(t, c3, wsClientMessage{Type: "attach", SessionID: sid, SinceSeq: 3})
	after := nextOf(t, c3, "turn_status")
	if after.TurnStatus == nil || after.TurnStatus.InFlight || after.TurnStatus.StopReason != "end_turn" || after.TurnStatus.Replayed != 0 {
		t.Fatalf("turn_status after the turn = %+v", after.TurnStatus)
	}
}

// Stop can never be pressed into silence: with nothing running it is
// answered with the server's word that nothing is running.
func TestWS_StopWithNothingRunningIsAnswered(t *testing.T) {
	h := newSocketHarness(t, func(ctx context.Context, _, _, _ string, _ <-chan agent.Steer, _ chan<- agent.RunEvent) error {
		return nil
	})
	c := h.dial(t)
	sendFrame(t, c, wsClientMessage{Type: "interrupt", SessionID: "22222222-2222-4222-8222-222222222222"})
	st := nextOf(t, c, "turn_status")
	if st.TurnStatus == nil || st.TurnStatus.InFlight {
		t.Fatalf("Stop with nothing running answered %+v", st.TurnStatus)
	}
}

// Stop with a turn running cancels it, and the turn ends with ONE terminal
// frame that says interrupted.
func TestWS_StopCancelsTheRunningTurn(t *testing.T) {
	run := func(ctx context.Context, sessionID, _, _ string, _ <-chan agent.Steer, events chan<- agent.RunEvent) error {
		events <- agent.RunEvent{Kind: agent.EventThinking, SessionID: sessionID, ThinkingDelta: "…"}
		<-ctx.Done()
		// What the real loop does on a cancelled context.
		events <- agent.RunEvent{Kind: agent.EventComplete, SessionID: sessionID, StopReason: "interrupted"}
		return nil
	}
	h := newSocketHarness(t, run)
	const sid = "33333333-3333-4333-8333-333333333333"
	c := h.dial(t)
	sendFrame(t, c, wsClientMessage{Type: "message", SessionID: sid, Content: "go"})
	nextOf(t, c, "thinking")
	sendFrame(t, c, wsClientMessage{Type: "interrupt", SessionID: sid})
	done := nextOf(t, c, "complete")
	if done.StopReason != "interrupted" {
		t.Fatalf("complete after Stop = %+v", done)
	}
	expectNone(t, c, "complete", 100*time.Millisecond)
	expectNone(t, c, "error", 50*time.Millisecond)
}

// Two tabs on one conversation both hear the turn: the phone and the laptop,
// or two browser tabs. The old registry kept one socket per session.
func TestWS_EveryTabOnTheSessionHearsTheTurn(t *testing.T) {
	release := make(chan struct{})
	run := func(ctx context.Context, sessionID, _, _ string, _ <-chan agent.Steer, events chan<- agent.RunEvent) error {
		events <- agent.RunEvent{Kind: agent.EventThinking, SessionID: sessionID, ThinkingDelta: "…"}
		<-release
		events <- agent.RunEvent{Kind: agent.EventDelta, SessionID: sessionID, TextDelta: "both", MsgIndex: 0}
		events <- agent.RunEvent{Kind: agent.EventComplete, SessionID: sessionID, StopReason: "end_turn"}
		return nil
	}
	h := newSocketHarness(t, run)
	const sid = "44444444-4444-4444-8444-444444444444"
	laptop := h.dial(t)
	phone := h.dial(t)
	sendFrame(t, laptop, wsClientMessage{Type: "message", SessionID: sid, Content: "hi"})
	// The turn is under way once the laptop hears from it; only then can the
	// phone's attach be expected to find it in flight.
	nextOf(t, laptop, "thinking")
	sendFrame(t, phone, wsClientMessage{Type: "attach", SessionID: sid})
	if st := nextOf(t, phone, "turn_status"); st.TurnStatus == nil || !st.TurnStatus.InFlight {
		t.Fatalf("the phone was not told the turn is in flight: %+v", st.TurnStatus)
	}
	close(release)
	for name, c := range map[string]*websocket.Conn{"laptop": laptop, "phone": phone} {
		if d := nextOf(t, c, "delta"); d.Text != "both" {
			t.Fatalf("%s delta = %+v", name, d)
		}
		if done := nextOf(t, c, "complete"); done.StopReason != "end_turn" {
			t.Fatalf("%s complete = %+v", name, done)
		}
	}
}

// A message typed while a turn runs becomes a steer: the loop receives it,
// and the echo carries the browser's own id so the bubble already on screen
// is matched by identity rather than by text.
func TestWS_AMessageDuringATurnIsSteeredAndEchoedById(t *testing.T) {
	var got struct {
		sync.Mutex
		text string
	}
	run := func(ctx context.Context, sessionID, _, _ string, steer <-chan agent.Steer, events chan<- agent.RunEvent) error {
		events <- agent.RunEvent{Kind: agent.EventThinking, SessionID: sessionID, ThinkingDelta: "…"}
		select {
		case st := <-steer:
			got.Lock()
			got.text = st.Text
			got.Unlock()
		case <-ctx.Done():
			return ctx.Err()
		}
		events <- agent.RunEvent{Kind: agent.EventComplete, SessionID: sessionID, StopReason: "end_turn"}
		return nil
	}
	h := newSocketHarness(t, run)
	const sid = "55555555-5555-4555-8555-555555555555"
	c := h.dial(t)
	sendFrame(t, c, wsClientMessage{Type: "message", SessionID: sid, Content: "start", ClientID: "cm-1"})
	nextOf(t, c, "thinking")
	sendFrame(t, c, wsClientMessage{Type: "message", SessionID: sid, Content: "also this", ClientID: "cm-2"})
	echo := nextOf(t, c, "steer_received")
	if echo.Text != "also this" || echo.ClientID != "cm-2" || !echo.Steered || echo.Seq == 0 {
		t.Fatalf("steer echo = %+v", echo)
	}
	nextOf(t, c, "complete")
	got.Lock()
	defer got.Unlock()
	if got.text != "also this" {
		t.Fatalf("the loop received steer %q", got.text)
	}
}

// A brain that goes silent mid-thought is cut by the stall budget, and the
// loop's terminal frame still reaches the socket. Uses the seam to install a
// fast budget the way startTurn would read it from the environment.
func TestWS_AStalledBrainIsCutAndTheTurnStillEnds(t *testing.T) {
	t.Setenv("INFINITY_TURN_STALL", "40ms")
	run := func(ctx context.Context, sessionID, _, _ string, _ <-chan agent.Steer, events chan<- agent.RunEvent) error {
		events <- agent.RunEvent{Kind: agent.EventThinking, SessionID: sessionID, ThinkingDelta: "hm"}
		<-ctx.Done()
		stalled, _ := agent.TurnBudgetCause(ctx)
		reason := "interrupted"
		if !stalled {
			reason = "not-stalled"
		}
		events <- agent.RunEvent{Kind: agent.EventComplete, SessionID: sessionID, StopReason: reason}
		return nil
	}
	h := newSocketHarness(t, run)
	// The guard ticks every few seconds in production; the test looks faster.
	h.srv.budgetTick = 2 * time.Millisecond
	const sid = "66666666-6666-4666-8666-666666666666"
	c := h.dial(t)
	sendFrame(t, c, wsClientMessage{Type: "message", SessionID: sid, Content: "think"})
	nextOf(t, c, "thinking")
	done := nextOf(t, c, "complete")
	if done.StopReason != "interrupted" {
		t.Fatalf("complete after the stall = %+v (the loop did not see the stall cause)", done)
	}
	st := h.srv.turnStatusFor(sid)
	if st.InFlight {
		t.Fatal("the journal still says in flight after the stall cut")
	}
}
