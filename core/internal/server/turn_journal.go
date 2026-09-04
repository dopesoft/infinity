package server

import (
	"encoding/json"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// turn_journal.go - the server's own record of a live turn, so a browser can
// ask "what did I miss?" and be answered with frames instead of a shrug.
//
// THE FAILURE THIS CLOSES (2026-09-04, the boss's "Job hunt workflow" chat).
// DeepSeek reasoned for 2m40s with no frame reaching the browser. Studio's
// 90-second watchdog declared the agent silent, painted an error, and
// force-closed a perfectly healthy socket. The new socket was never bound to
// the session (binding only happened on `message`), so every remaining frame
// of the turn - including `complete` - was dropped on the floor, and the
// reply that had landed in the database three minutes later was only ever
// seen by refreshing the page. Stop did nothing because there was nothing
// bound to answer it.
//
// Three things fix that, and they all live here:
//
//   - every turn frame carries a per-session `seq` and the turn's id, and the
//     current turn's frames are kept in a bounded ring, so `attach{since_seq}`
//     can replay exactly what a reconnecting socket missed;
//   - `turn_status` says, from the server's own state, whether a turn is in
//     flight, when it started and what phase it is in - the client no longer
//     infers any of that from the shape of the transcript;
//   - the phase tracker feeds a 5-second `heartbeat` while a turn runs, so a
//     brain that thinks for three minutes is visibly alive the whole time.
//
// What is journaled: the frames ws.runTurn produces for the turn (delta,
// thinking, tool_call, tool_input_delta, tool_result, effort, complete, error)
// plus steer_received, which a re-attached tab needs to see. What is NOT:
// heartbeat (it is a clock, not content), browser_frame, proactive_message
// and nested coding steps (those are persisted as PostToolUse rows and rebuilt
// by the transcript fetch).
//
// A finished turn's frames stay until the next turn begins, so a socket that
// re-attaches a few seconds after `complete` still receives it.

const (
	// journalMaxFrames / journalMaxBytes bound one turn's ring. A turn bigger
	// than this loses its OLDEST frames; the client sees `oldest_seq` on the
	// turn_status and reconciles from the database for anything before it.
	journalMaxFrames = 4096
	journalMaxBytes  = 4 << 20
	// journalIdleTTL is how long a finished journal with nothing attached is
	// kept before the sweep drops it.
	journalIdleTTL = time.Hour
	// turnPulseEvery is the heartbeat cadence while a turn is in flight.
	turnPulseEvery = 5 * time.Second
)

// Turn phases, as the boss reads them on the working row.
const (
	phaseStarting  = "starting"
	phaseThinking  = "thinking"
	phaseStreaming = "streaming"
	phaseTool      = "tool"
	phaseApproval  = "awaiting_approval"
	phaseSteering  = "steering"
	phaseStopping  = "stopping"
)

// wsTurnStatus is the payload of both `turn_status` (the answer to an attach
// or an interrupt) and `heartbeat` (the 5-second pulse while in flight).
type wsTurnStatus struct {
	TurnID   string `json:"turn_id,omitempty"`
	InFlight bool   `json:"in_flight"`
	// Seq is the newest journaled seq for this session; OldestSeq the oldest
	// still replayable. A client whose since_seq is below OldestSeq cannot be
	// caught up from the ring and reconciles from the database instead.
	Seq       uint64    `json:"seq"`
	OldestSeq uint64    `json:"oldest_seq"`
	StartedAt time.Time `json:"started_at,omitempty"`
	Phase     string    `json:"phase,omitempty"`
	ToolName  string    `json:"tool_name,omitempty"`
	ElapsedMS int64     `json:"elapsed_ms"`
	// ThinkingTokens is the running reasoning count for brains that report
	// how much rather than what (Claude Code redacts the text).
	ThinkingTokens int    `json:"thinking_tokens,omitempty"`
	Model          string `json:"model,omitempty"`
	// Replayed is how many frames follow this turn_status on an attach.
	Replayed int `json:"replayed"`
	// StopReason is how the LAST turn ended, when nothing is in flight.
	StopReason string `json:"stop_reason,omitempty"`
}

type journalFrame struct {
	ev   wsServerEvent
	size int
}

// turnJournal is one session's ring plus its turn state. Every method is
// safe for concurrent use.
type turnJournal struct {
	mu sync.Mutex

	sessionID  string
	turnID     string
	model      string
	startedAt  time.Time
	endedAt    time.Time
	inFlight   bool
	stopReason string

	// seq is per-SESSION monotonic and never resets, so a client's since_seq
	// stays meaningful across turns.
	seq    uint64
	frames []journalFrame
	bytes  int

	phase          string
	toolName       string
	thinkingTokens int
	phaseAt        time.Time
	touched        time.Time
	// activeAt is the last moment the turn showed a sign of life: a frame
	// appended or a phase change. The stall guard (turn_budget.go) reads it.
	activeAt time.Time
}

// turnActivity is the stall guard's view of a journal.
type turnActivity struct {
	inFlight  bool
	phase     string
	startedAt time.Time
	activeAt  time.Time
}

func newTurnJournal(sessionID string) *turnJournal {
	return &turnJournal{sessionID: sessionID, touched: time.Now()}
}

// begin opens a turn: the previous turn's frames are dropped, the phase
// resets, and in_flight flips true. Called BEFORE the turn goroutine starts
// so an attach racing the start already sees a live turn.
func (j *turnJournal) begin(turnID, model string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	now := time.Now()
	j.turnID = turnID
	j.model = model
	j.startedAt = now
	j.endedAt = time.Time{}
	j.inFlight = true
	j.stopReason = ""
	j.frames = nil
	j.bytes = 0
	j.phase = phaseStarting
	j.toolName = ""
	j.thinkingTokens = 0
	j.phaseAt = now
	j.touched = now
	j.activeAt = now
}

// append stamps the next seq and the turn id onto ev, records it, and hands
// back the stamped copy for fan-out. The ring evicts its oldest frames past
// the caps.
func (j *turnJournal) append(ev wsServerEvent) wsServerEvent {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.seq++
	ev.Seq = j.seq
	ev.TurnID = j.turnID
	size := frameSize(ev)
	j.frames = append(j.frames, journalFrame{ev: ev, size: size})
	j.bytes += size
	for len(j.frames) > journalMaxFrames || (j.bytes > journalMaxBytes && len(j.frames) > 1) {
		j.bytes -= j.frames[0].size
		j.frames[0] = journalFrame{}
		j.frames = j.frames[1:]
	}
	j.touched = time.Now()
	j.activeAt = j.touched
	return ev
}

// setPhase advances the phase tracker from one RunEvent. Called from the
// turn's drain, so the loop itself needs no knowledge of phases.
func (j *turnJournal) setPhase(ev agent.RunEvent) {
	j.mu.Lock()
	defer j.mu.Unlock()
	switch ev.Kind {
	case agent.EventThinking:
		j.phase = phaseThinking
		j.toolName = ""
		if ev.ThinkingTokens > j.thinkingTokens {
			j.thinkingTokens = ev.ThinkingTokens
		}
	case agent.EventDelta:
		j.phase = phaseStreaming
		j.toolName = ""
	case agent.EventToolCall:
		if ev.ToolCall != nil {
			j.toolName = ev.ToolCall.Name
			if ev.ToolCall.AwaitingApproval {
				j.phase = phaseApproval
			} else {
				j.phase = phaseTool
			}
		}
	case agent.EventToolInputDelta:
		j.phase = phaseTool
		if ev.ToolName != "" {
			j.toolName = ev.ToolName
		}
	case agent.EventToolResult:
		// The model is about to reason about what came back.
		j.phase = phaseThinking
		j.toolName = ""
	case agent.EventSteered:
		j.phase = phaseSteering
		j.toolName = ""
	default:
		return
	}
	j.phaseAt = time.Now()
	j.activeAt = j.phaseAt
}

// activity is the snapshot the stall guard decides on.
func (j *turnJournal) activity() turnActivity {
	j.mu.Lock()
	defer j.mu.Unlock()
	return turnActivity{inFlight: j.inFlight, phase: j.phase, startedAt: j.startedAt, activeAt: j.activeAt}
}

// stopping marks the turn as being cancelled, so the heartbeat says so
// honestly until the loop confirms with complete{interrupted}.
func (j *turnJournal) stopping() {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.inFlight {
		j.phase = phaseStopping
		j.phaseAt = time.Now()
	}
}

// end closes the turn. Frames are KEPT until the next begin.
func (j *turnJournal) end(stopReason string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.inFlight = false
	j.stopReason = stopReason
	j.endedAt = time.Now()
	j.touched = j.endedAt
}

// status is a snapshot for turn_status / heartbeat.
func (j *turnJournal) status() wsTurnStatus {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.statusLocked()
}

func (j *turnJournal) statusLocked() wsTurnStatus {
	st := wsTurnStatus{
		TurnID:         j.turnID,
		InFlight:       j.inFlight,
		Seq:            j.seq,
		OldestSeq:      j.oldestSeqLocked(),
		StartedAt:      j.startedAt,
		Model:          j.model,
		StopReason:     j.stopReason,
		ThinkingTokens: j.thinkingTokens,
	}
	if j.inFlight {
		st.Phase = j.phase
		st.ToolName = j.toolName
		st.ElapsedMS = time.Since(j.startedAt).Milliseconds()
	} else if !j.startedAt.IsZero() && !j.endedAt.IsZero() {
		st.ElapsedMS = j.endedAt.Sub(j.startedAt).Milliseconds()
	}
	return st
}

func (j *turnJournal) oldestSeqLocked() uint64 {
	if len(j.frames) == 0 {
		return j.seq
	}
	return j.frames[0].ev.Seq
}

// since returns the retained frames with seq > sinceSeq, flagged replay, and
// whether the ring had already evicted frames the caller has not seen.
//
// sinceSeq == 0 means "I have nothing": the whole current turn is replayed
// when it is in flight, and nothing when it is over (a cold client rebuilds
// a finished turn from the transcript, which is complete by then).
func (j *turnJournal) since(sinceSeq uint64) (frames []wsServerEvent, truncated bool) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if sinceSeq == 0 && !j.inFlight {
		return nil, false
	}
	oldest := j.oldestSeqLocked()
	if sinceSeq > 0 && len(j.frames) > 0 && oldest > sinceSeq+1 {
		truncated = true
	}
	for _, f := range j.frames {
		if f.ev.Seq <= sinceSeq {
			continue
		}
		ev := f.ev
		ev.Replay = true
		frames = append(frames, ev)
	}
	return frames, truncated
}

// idleFor is how long since this journal last changed.
func (j *turnJournal) idleFor(now time.Time) time.Duration {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.inFlight {
		return 0
	}
	return now.Sub(j.touched)
}

// frameSize is the ring's byte accounting: the encoded frame, or a cheap
// estimate when encoding fails (it never should; every field is plain data).
func frameSize(ev wsServerEvent) int {
	b, err := json.Marshal(ev)
	if err != nil {
		return 256
	}
	return len(b)
}

// --- server-side registry ----------------------------------------------------

// journalFor returns the session's journal, creating it on first use.
func (s *Server) journalFor(sessionID string) *turnJournal {
	s.journalsMu.Lock()
	defer s.journalsMu.Unlock()
	if s.journals == nil {
		s.journals = make(map[string]*turnJournal)
	}
	j, ok := s.journals[sessionID]
	if !ok {
		j = newTurnJournal(sessionID)
		s.journals[sessionID] = j
	}
	// Opportunistic sweep: finished journals nobody touched in an hour go.
	// Done here rather than on a ticker because this is the only place the
	// map grows, so it is the only place it needs to shrink.
	if len(s.journals) > 64 {
		now := time.Now()
		for sid, other := range s.journals {
			if sid != sessionID && other.idleFor(now) > journalIdleTTL {
				delete(s.journals, sid)
			}
		}
	}
	return j
}

// journalPeek returns the session's journal without creating one.
func (s *Server) journalPeek(sessionID string) *turnJournal {
	s.journalsMu.Lock()
	defer s.journalsMu.Unlock()
	return s.journals[sessionID]
}

// turnStatusFor is the server's answer to "is anything running here?".
func (s *Server) turnStatusFor(sessionID string) wsTurnStatus {
	if j := s.journalPeek(sessionID); j != nil {
		return j.status()
	}
	return wsTurnStatus{}
}

// turnSender is the journaling sender a turn streams through: every frame is
// stamped and recorded FIRST, then fanned out to whichever sockets are bound
// to the session at that moment. Recording first is what makes a socket that
// arrives a second later able to ask for exactly the frames it missed.
func (s *Server) turnSender(sessionID string, j *turnJournal) func(wsServerEvent) {
	fan := s.sessionSender(sessionID)
	return func(ev wsServerEvent) {
		if !journaled(ev) {
			fan(ev)
			return
		}
		fan(j.append(ev))
	}
}

// journaled reports whether a frame is worth replaying. Speech clips are not:
// a re-attached tab must not have the last minute of Jarvis's reply spoken at
// it again, and the base64 audio would fill the ring in seconds.
func journaled(ev wsServerEvent) bool {
	switch ev.Type {
	case "voice_audio", "heartbeat", "browser_frame":
		return false
	}
	return true
}

// publish sends one frame to the session's sockets, journaling it when a turn
// is in flight (so a re-attach sees it) and not otherwise.
func (s *Server) publish(sessionID string, ev wsServerEvent) {
	if j := s.journalPeek(sessionID); j != nil && j.status().InFlight {
		s.turnSender(sessionID, j)(ev)
		return
	}
	s.sessionSender(sessionID)(ev)
}

// pulse sends a heartbeat every turnPulseEvery until stop closes. Heartbeats
// are never journaled: they are a clock, and a replayed clock is noise.
func (s *Server) pulse(sessionID string, j *turnJournal, stop <-chan struct{}) {
	fan := s.sessionSender(sessionID)
	t := time.NewTicker(turnPulseEvery)
	defer t.Stop()
	for {
		select {
		case <-stop:
			return
		case <-t.C:
			st := j.status()
			if !st.InFlight {
				return
			}
			fan(wsServerEvent{Type: "heartbeat", SessionID: sessionID, TurnID: st.TurnID, Seq: st.Seq, TurnStatus: &st})
		}
	}
}
