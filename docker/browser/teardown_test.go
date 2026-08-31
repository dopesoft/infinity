package main

import "testing"

// The outage these guard, both observed in production on 2026-08-30.
//
// 1. Every session teardown panicked with "close of closed channel". shutdown()
//    closes and unregisters each subscriber channel; that wakes the screencast
//    handler's receive with !open, the handler returns, and its deferred
//    unsubscribe closes the same channel a second time. The sync.Once in
//    subscribe() guards that closure against itself, not against shutdown
//    having got there first, so the panic took down the HTTP connection on
//    every close, six for six in the logs.
//
// 2. handleAct auto-retried ANY failed action after a re-observe. Re-observing
//    re-tags data-jarvis-idx from scratch, so the index is not a stable handle
//    across the retry. On a click that already dispatched before erroring, the
//    retry clicks a second, arbitrary element. On a checkout page that is a
//    second charge, which is why this had to be fixed before any purchase work.

func newTestSession() *Session {
	return &Session{subs: make(map[chan string]struct{})}
}

func TestUnsubscribeAfterShutdownDoesNotPanic(t *testing.T) {
	s := newTestSession()
	_, unsub := s.subscribe()

	// shutdown() gets there first, exactly as it does when a session is closed
	// while a screencast is streaming.
	s.scMu.Lock()
	for ch := range s.subs {
		delete(s.subs, ch)
		close(ch)
	}
	s.scMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("unsubscribe after shutdown panicked: %v", r)
		}
	}()
	unsub()
}

func TestUnsubscribeClosesWhenItStillOwnsTheChannel(t *testing.T) {
	s := newTestSession()
	ch, unsub := s.subscribe()

	unsub()

	if _, open := <-ch; open {
		t.Fatal("unsubscribe left the channel open; the screencast handler would block forever")
	}
	if len(s.subs) != 0 {
		t.Fatalf("subs = %d, want 0 — a stale entry means publishFrame sends on a dead channel", len(s.subs))
	}
}

func TestUnsubscribeIsIdempotent(t *testing.T) {
	s := newTestSession()
	_, unsub := s.subscribe()

	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("second unsubscribe panicked: %v", r)
		}
	}()
	unsub()
	unsub()
}

func TestOnlyIdempotentActionsMayRetry(t *testing.T) {
	cases := []struct {
		name   string
		action string
		want   bool
	}{
		// Safe to run twice: nothing is committed, and a partial first
		// attempt is overwritten rather than compounded.
		{"scroll", "scroll", true},
		{"clear", "clear", true},
		{"type clears the field first", "type", true},
		{"select assigns a value", "select", true},

		// NEVER retryable. A click can dispatch and THEN error when the
		// navigation it caused tears the node down; retrying clicks whatever
		// now sits at that re-tagged index. press can be Enter, which submits.
		{"click can commit an order", "click", false},
		{"press can be Enter on a form", "press", false},

		// Anything unrecognised is treated as unsafe: fail closed.
		{"unknown verb", "frobnicate", false},
		{"empty", "", false},

		// Casing and padding must not smuggle a click past the check.
		{"uppercase click", "CLICK", false},
		{"padded click", "  click  ", false},
		{"uppercase scroll still retries", "SCROLL", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := idempotentAct(c.action); got != c.want {
				t.Fatalf("idempotentAct(%q) = %v, want %v", c.action, got, c.want)
			}
		})
	}
}
