package server

import (
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// The turn journal is what lets a socket that arrives late be caught up
// instead of left to guess. Each test is one way that guarantee used to fail.

func TestJournal_SeqIsMonotonicAcrossTurns(t *testing.T) {
	j := newTurnJournal("s")
	j.begin("t1", "m")
	a := j.append(wsServerEvent{Type: "delta", Text: "a"})
	b := j.append(wsServerEvent{Type: "complete"})
	j.end("end_turn")
	j.begin("t2", "m")
	c := j.append(wsServerEvent{Type: "delta", Text: "c"})
	if !(a.Seq < b.Seq && b.Seq < c.Seq) {
		t.Fatalf("seq must never reset across turns, got %d %d %d", a.Seq, b.Seq, c.Seq)
	}
	if a.TurnID != "t1" || c.TurnID != "t2" {
		t.Fatalf("frames must carry their turn id, got %q %q", a.TurnID, c.TurnID)
	}
	// A client that last saw the OLD turn asks since its seq and gets only
	// the new turn's frames.
	frames, truncated := j.since(b.Seq)
	if truncated || len(frames) != 1 || frames[0].Text != "c" || !frames[0].Replay {
		t.Fatalf("since(old turn) = %+v truncated=%v, want just the new turn's frame flagged replay", frames, truncated)
	}
}

func TestJournal_KeepsTheFinishedTurnUntilTheNextBegins(t *testing.T) {
	j := newTurnJournal("s")
	j.begin("t1", "m")
	j.append(wsServerEvent{Type: "delta", Text: "hello"})
	done := j.append(wsServerEvent{Type: "complete"})
	j.end("end_turn")

	// A socket that missed the completion by a second still gets it.
	frames, _ := j.since(done.Seq - 1)
	if len(frames) != 1 || frames[0].Type != "complete" {
		t.Fatalf("the completion must survive until the next turn, got %+v", frames)
	}
	// But a cold client (since 0) on a finished turn gets nothing: the
	// transcript is complete by then and the database is the source.
	if frames, _ := j.since(0); len(frames) != 0 {
		t.Fatalf("a cold attach on a finished turn must replay nothing, got %d frames", len(frames))
	}
	st := j.status()
	if st.InFlight || st.StopReason != "end_turn" {
		t.Fatalf("status after end = %+v", st)
	}
	j.begin("t2", "m")
	if frames, _ := j.since(0); len(frames) != 0 {
		t.Fatal("beginning a turn must drop the previous turn's frames")
	}
}

func TestJournal_ColdAttachOnALiveTurnReplaysTheWholeTurn(t *testing.T) {
	j := newTurnJournal("s")
	j.begin("t1", "m")
	j.append(wsServerEvent{Type: "tool_call"})
	j.append(wsServerEvent{Type: "delta", Text: "x"})
	frames, truncated := j.since(0)
	if truncated || len(frames) != 2 {
		t.Fatalf("a reload mid-turn must receive everything so far, got %d truncated=%v", len(frames), truncated)
	}
	if !j.status().InFlight {
		t.Fatal("a begun turn is in flight until end()")
	}
}

func TestJournal_RingEvictsTheOldestAndSaysSo(t *testing.T) {
	j := newTurnJournal("s")
	j.begin("t1", "m")
	var first uint64
	for i := 0; i < journalMaxFrames+50; i++ {
		ev := j.append(wsServerEvent{Type: "delta", Text: "d"})
		if i == 0 {
			first = ev.Seq
		}
	}
	frames, truncated := j.since(first)
	if !truncated {
		t.Fatal("a client whose since_seq predates the ring must be told the head is gone")
	}
	if len(frames) != journalMaxFrames {
		t.Fatalf("ring must hold exactly %d frames, got %d", journalMaxFrames, len(frames))
	}
	if st := j.status(); st.OldestSeq != frames[0].Seq {
		t.Fatalf("oldest_seq %d must name the first retained frame %d", st.OldestSeq, frames[0].Seq)
	}
}

func TestJournal_ByteCapEvictsLargeToolOutputs(t *testing.T) {
	j := newTurnJournal("s")
	j.begin("t1", "m")
	big := strings.Repeat("x", 1<<20) // 1 MB each
	for i := 0; i < 8; i++ {
		j.append(wsServerEvent{Type: "tool_result", ToolResult: &wsToolEvent{ID: "c", Output: big}})
	}
	if j.bytes > journalMaxBytes+len(big) {
		t.Fatalf("ring holds %d bytes, cap is %d", j.bytes, journalMaxBytes)
	}
	if len(j.frames) >= 8 {
		t.Fatal("eight 1MB frames cannot all fit under a 4MB cap")
	}
}

func TestJournal_PhaseFollowsTheFrames(t *testing.T) {
	j := newTurnJournal("s")
	j.begin("t1", "m")
	steps := []struct {
		ev    agent.RunEvent
		phase string
		tool  string
	}{
		{agent.RunEvent{Kind: agent.EventThinking, ThinkingTokens: 40}, phaseThinking, ""},
		{agent.RunEvent{Kind: agent.EventToolCall, ToolCall: &agent.ToolEvent{Name: "bash_run"}}, phaseTool, "bash_run"},
		{agent.RunEvent{Kind: agent.EventToolResult, ToolResult: &agent.ToolEvent{Name: "bash_run"}}, phaseThinking, ""},
		{agent.RunEvent{Kind: agent.EventDelta, TextDelta: "so"}, phaseStreaming, ""},
		{agent.RunEvent{Kind: agent.EventToolCall, ToolCall: &agent.ToolEvent{Name: "fs_write", AwaitingApproval: true}}, phaseApproval, "fs_write"},
		{agent.RunEvent{Kind: agent.EventSteered}, phaseSteering, ""},
	}
	for i, s := range steps {
		j.setPhase(s.ev)
		st := j.status()
		if st.Phase != s.phase || st.ToolName != s.tool {
			t.Fatalf("step %d: phase %q tool %q, want %q %q", i, st.Phase, st.ToolName, s.phase, s.tool)
		}
	}
	if j.status().ThinkingTokens != 40 {
		t.Fatal("thinking tokens must ride the status")
	}
	j.stopping()
	if j.status().Phase != phaseStopping {
		t.Fatal("an interrupt marks the phase stopping until the loop confirms")
	}
	j.end("interrupted")
	if st := j.status(); st.InFlight || st.Phase != "" {
		t.Fatalf("a finished turn reports no phase, got %+v", st)
	}
}

func TestAttachSnapshot_TurnStatusCountsTheReplay(t *testing.T) {
	srv := &Server{journals: map[string]*turnJournal{}}
	j := srv.journalFor("chat")
	j.begin("t1", "m")
	j.append(wsServerEvent{Type: "delta", Text: "a"})
	j.append(wsServerEvent{Type: "delta", Text: "b"})
	st, frames := srv.attachSnapshot("chat", 1)
	if st.Replayed != 1 || len(frames) != 1 || frames[0].Text != "b" {
		t.Fatalf("attach since 1 must replay exactly the frame after it: %+v %+v", st, frames)
	}
	if !st.InFlight || st.TurnID != "t1" {
		t.Fatalf("status must say the turn is live: %+v", st)
	}
	// An unknown session is simply idle, never an error.
	if st, frames := srv.attachSnapshot("nobody", 0); st.InFlight || len(frames) != 0 {
		t.Fatalf("unknown session must read idle, got %+v", st)
	}
}
