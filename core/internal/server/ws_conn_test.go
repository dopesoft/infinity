package server

import "testing"

// The outbound queue is what keeps a slow phone from slowing the brain. Its
// one rule: a frame that matters is never thrown away, a frame that can be
// replayed may be. Each test pins one half of that.

func fill(q *wsQueue, n int, typ string) {
	for i := 0; i < n; i++ {
		q.push(wsServerEvent{Type: typ})
	}
}

func TestQueue_DropsDeltasButNeverResultsWhenFull(t *testing.T) {
	q := &wsQueue{}
	fill(q, wsQueueMaxFrames, "tool_call") // full of frames that matter
	if !q.push(wsServerEvent{Type: "delta"}) {
		t.Fatal("a delta on a full queue is dropped, not a reason to close")
	}
	if q.droppedCount() != 1 {
		t.Fatalf("dropped = %d, want 1", q.droppedCount())
	}
	if !q.push(wsServerEvent{Type: "tool_result"}) {
		t.Fatal("a result on a full-but-under-hard-cap queue must be accepted")
	}
	out := q.drain()
	if out[len(out)-1].Type != "tool_result" {
		t.Fatal("the result must be queued after eviction, not dropped")
	}
}

func TestQueue_EvictsDroppablesToMakeRoomForAResult(t *testing.T) {
	q := &wsQueue{}
	fill(q, wsQueueMaxFrames, "delta")
	if !q.push(wsServerEvent{Type: "complete"}) {
		t.Fatal("complete must be accepted by shedding deltas")
	}
	out := q.drain()
	if len(out) != 1 || out[0].Type != "complete" {
		t.Fatalf("after eviction only the completion remains, got %d frames", len(out))
	}
	if q.droppedCount() != uint64(wsQueueMaxFrames) {
		t.Fatalf("every delta was shed: dropped = %d", q.droppedCount())
	}
}

func TestQueue_ReplayFramesAreNeverDropped(t *testing.T) {
	q := &wsQueue{}
	fill(q, wsQueueMaxFrames, "tool_call")
	if !q.push(wsServerEvent{Type: "delta", Replay: true}) {
		t.Fatal("a replayed delta is what the client asked for; it is queued, not dropped")
	}
	if q.droppedCount() != 0 {
		t.Fatal("replay must not count as a drop")
	}
}

func TestQueue_ClosesAConnectionThatCannotTakeAResult(t *testing.T) {
	q := &wsQueue{}
	fill(q, wsQueueHardCap, "tool_call")
	if q.push(wsServerEvent{Type: "tool_result"}) {
		t.Fatal("past the hard cap with nothing to shed, the socket must close so the client re-attaches and replays")
	}
	if q.push(wsServerEvent{Type: "delta"}) {
		t.Fatal("a closed queue accepts nothing")
	}
}

func TestDroppable_OnlyTheReplayableKinds(t *testing.T) {
	for _, typ := range []string{"delta", "thinking", "tool_input_delta", "heartbeat", "browser_frame", "pong"} {
		if !droppable(wsServerEvent{Type: typ}) {
			t.Fatalf("%s is replayable and may be shed", typ)
		}
	}
	for _, typ := range []string{"tool_call", "tool_result", "complete", "error", "turn_status", "steer_received", "proactive_message"} {
		if droppable(wsServerEvent{Type: typ}) {
			t.Fatalf("%s is a fact on screen and must never be shed", typ)
		}
	}
}
