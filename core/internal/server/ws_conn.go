package server

import (
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// ws_conn.go - one browser socket, written by ONE goroutine, never blocking
// the thing that produces the frames.
//
// Before this, `send` was a synchronous conn.WriteJSON under a mutex, called
// straight from the turn's event drain (ws.runTurn). A half-dead socket - the
// classic mobile pattern, the tab backgrounded, a proxy that stopped
// forwarding - made every write sit on its 10s deadline, which stalled the
// drain, which filled the 128-slot event channel, which made the agent loop
// wait 5s per event, which back-pressured the LLM stream itself. The brain
// slowed down because a phone had gone to sleep.
//
// Now a producer hands a frame to the queue and returns immediately. The
// queue has a drop policy by KIND (below), and the one writer goroutine also
// owns the protocol ping, so the old leaked ping goroutine (a `for range
// ticker.C` that Stop() never released) is gone with it.

const (
	// wsQueueMaxFrames is the soft cap: past it, droppable frames are dropped
	// and non-droppable ones evict droppables to make room.
	wsQueueMaxFrames = 512
	// wsQueueHardCap is the point past which a socket that still cannot take
	// a NON-droppable frame is closed. The client reconnects and re-attaches
	// with its last seq, and the journal replays what it missed - so a slow
	// socket loses nothing it cannot recover, and the producer never waits.
	wsQueueHardCap = 2 * wsQueueMaxFrames
	// wsWriteDeadline bounds one write; a socket slower than this is dead.
	wsWriteDeadline = 10 * time.Second
	// wsPingEvery keeps proxies and NATs from idling the socket out. These
	// are protocol pings - invisible to JavaScript, which is why the turn
	// heartbeat (turn_journal.go) exists as a separate, visible frame.
	wsPingEvery = 30 * time.Second
	// wsDropLogEvery throttles the "dropped N frames" line so a socket that
	// is genuinely behind does not flood the logs one line per frame.
	wsDropLogEvery = 100
)

// droppable reports whether a frame may be discarded on a congested socket.
// A dropped delta is cosmetic: the next attach replays it from the journal.
// A dropped tool_call, tool_result, complete, error or turn_status is a lie
// on screen, so those are never dropped - they evict droppables instead, and
// if that is still not enough the socket is closed rather than the frame
// thrown away. A replayed frame is never dropped either: the client asked for
// it precisely because it is behind.
func droppable(ev wsServerEvent) bool {
	if ev.Replay {
		return false
	}
	switch ev.Type {
	case "delta", "thinking", "tool_input_delta", "heartbeat", "browser_frame", "pong":
		return true
	}
	return false
}

// wsQueue is the outbound policy with no socket attached, so the rules can be
// tested in plain Go. push never blocks. It returns false exactly when the
// connection must be closed: a non-droppable frame arrived and even after
// evicting every droppable frame there was no room under the hard cap.
type wsQueue struct {
	mu      sync.Mutex
	q       []wsServerEvent
	dropped uint64
	closed  bool
}

func (q *wsQueue) push(ev wsServerEvent) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return false
	}
	if len(q.q) < wsQueueMaxFrames {
		q.q = append(q.q, ev)
		return true
	}
	if droppable(ev) {
		q.dropped++
		return true
	}
	// Make room for a frame that matters by shedding the ones that don't.
	kept := q.q[:0]
	for _, e := range q.q {
		if droppable(e) {
			q.dropped++
			continue
		}
		kept = append(kept, e)
	}
	q.q = kept
	if len(q.q) >= wsQueueHardCap {
		q.closed = true
		return false
	}
	q.q = append(q.q, ev)
	return true
}

// drain hands back everything queued, in order, and empties the queue.
func (q *wsQueue) drain() []wsServerEvent {
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.q) == 0 {
		return nil
	}
	out := q.q
	q.q = nil
	return out
}

// droppedCount is how many droppable frames this socket shed so far.
func (q *wsQueue) droppedCount() uint64 {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.dropped
}

// wsConn is one live browser socket.
type wsConn struct {
	id    string
	conn  *websocket.Conn
	queue wsQueue
	// notify wakes the writer; capacity one, so a burst of pushes coalesces
	// into one drain instead of one wake per frame.
	notify chan struct{}
	closed chan struct{}
	once   sync.Once
	// lastDropLog is the dropped count at the last log line.
	lastDropLog uint64
	// writes counts frames actually written, for tests and the close line.
	writes atomic.Uint64
}

func newWSConn(conn *websocket.Conn) *wsConn {
	return &wsConn{
		id:     uuid.NewString(),
		conn:   conn,
		notify: make(chan struct{}, 1),
		closed: make(chan struct{}),
	}
}

// send enqueues one frame and returns at once. It is safe from any goroutine.
func (c *wsConn) send(ev wsServerEvent) {
	if c == nil {
		return
	}
	if !c.queue.push(ev) {
		// The socket cannot take a frame that must not be lost. Closing it
		// hands recovery to the client's reconnect + attach{since_seq}.
		log.Printf("ws %s: socket cannot keep up (%d droppable frames shed); closing so the client re-attaches and replays", c.id, c.queue.droppedCount())
		c.close()
		return
	}
	select {
	case c.notify <- struct{}{}:
	default:
	}
}

// writeLoop is the ONLY goroutine that writes to the socket. It exits when
// close() is called or a write fails (which closes the socket, which in turn
// makes the handler's ReadJSON return and the handler unwind).
func (c *wsConn) writeLoop() {
	ping := time.NewTicker(wsPingEvery)
	defer ping.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-c.notify:
			for _, ev := range c.queue.drain() {
				_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
				if err := c.conn.WriteJSON(ev); err != nil {
					log.Printf("ws %s write: %v", c.id, err)
					c.close()
					return
				}
				c.writes.Add(1)
			}
			if d := c.queue.droppedCount(); d-c.lastDropLog >= wsDropLogEvery {
				c.lastDropLog = d
				log.Printf("ws %s: shed %d droppable frames so far (slow socket); the journal covers the gap on re-attach", c.id, d)
			}
		case <-ping.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(wsWriteDeadline))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.close()
				return
			}
		}
	}
}

// close stops the writer and closes the socket. Idempotent.
func (c *wsConn) close() {
	if c == nil {
		return
	}
	c.once.Do(func() {
		close(c.closed)
		if c.conn != nil {
			_ = c.conn.Close()
		}
	})
}
