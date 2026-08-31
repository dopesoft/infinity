package browser

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The outage this guards, 2026-08-30: the boss asked Jarvis to visit seven
// sites, then scrolled the Preview pane to watch. Every scroll posted a raw
// input event, Registry.Input claimed human control on ANY event, and the
// agent's next verb was refused with "the boss is driving this browser right
// now". It then called browser_request_takeover and blocked for up to five
// minutes waiting to be handed back a browser nobody had meant to take. The
// loop closed on itself: he scrolled to see why it had stalled, and the scroll
// re-took control.
//
// The invariant: touching the page takes it, looking at it does not.

func TestLookingAtThePageDoesNotTakeControl(t *testing.T) {
	watching := []string{"scroll", "move", "resize"}
	for _, ev := range watching {
		t.Run(ev, func(t *testing.T) {
			if claimsControl(ev) {
				t.Fatalf("%q claimed control; watching the agent work must never take the browser off it", ev)
			}
		})
	}
}

func TestTouchingThePageTakesControl(t *testing.T) {
	driving := []string{"click", "text", "key"}
	for _, ev := range driving {
		t.Run(ev, func(t *testing.T) {
			if !claimsControl(ev) {
				t.Fatalf("%q did not claim control; the agent could click on top of the boss mid-captcha", ev)
			}
		})
	}
}

func TestUnknownInputTypesClaimControl(t *testing.T) {
	// Fail toward the recoverable mistake. An unclassified event is far more
	// likely to be an interaction than a way of watching, and a takeover the
	// boss can hand straight back is cheaper than a click landing on top of him.
	for _, ev := range []string{"drag", "tap", "", "wheelish"} {
		if !claimsControl(ev) {
			t.Fatalf("unknown event %q did not claim control; unclassified input must fail toward safety", ev)
		}
	}
}

func TestControlClaimIgnoresCasingAndPadding(t *testing.T) {
	if claimsControl("  SCROLL  ") {
		t.Fatal("a padded, uppercased scroll claimed control; casing must not decide who is driving")
	}
	if !claimsControl(" Click ") {
		t.Fatal("a padded, capitalised click failed to claim control")
	}
}

// Recovery must stop being silent before it stops being possible: the third
// death in a chain surfaces, and the eviction still happens on the way out so
// giving up never costs a sidecar slot.
func TestRecoveryChainCapsAndStillFreesTheSlot(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	r.SetRecoveries(info.SessionID, maxAutoRecoveries)
	f.killSession(info.SessionID)

	_, err = r.recoverSession(testCtx(), info.SessionID, "", errors.New("browser sidecar: context canceled"))
	if err == nil {
		t.Fatal("recovery past the cap succeeded; a permanently broken browser would keep reporting progress")
	}
	if !strings.Contains(err.Error(), "browser itself") {
		t.Fatalf("error blames the page, not the browser: %v", err)
	}
	if got := f.liveCount(); got != 0 {
		t.Fatalf("%d sessions still held after giving up, want 0 — refusing to reopen must not cost a slot", got)
	}
}

// A deliberate browser_open starts a fresh chain. The cap is a signal that
// something is wrong, not a permanent lockout.
func TestDeliberateOpenResetsTheRecoveryChain(t *testing.T) {
	f := newFakeSidecar(2)
	r := NewRegistry(f, nil)

	first, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	r.SetRecoveries(first.SessionID, maxAutoRecoveries)

	second, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("second open: %v", err)
	}
	if got := r.Recoveries(second.SessionID); got != 0 {
		t.Fatalf("a deliberately opened session inherited %d recoveries, want 0", got)
	}
}

// Stream is the whole reason the relay can tell "the boss closed it" from "the
// connection broke". Six sessions died on 2026-08-30 and every one recorded ok.
func TestStreamReportsWhyItEnded(t *testing.T) {
	var clean Stream
	if err := clean.Err(); err != nil {
		t.Fatalf("a clean stream reported %v, want nil", err)
	}

	boom := errors.New("screencast stream: unexpected EOF")
	var broken Stream
	broken.setErr(boom)
	if !errors.Is(broken.Err(), boom) {
		t.Fatalf("Err = %v, want %v", broken.Err(), boom)
	}

	// A nil error must never overwrite a recorded failure, or a late clean
	// path would launder a real one back to green.
	broken.setErr(nil)
	if !errors.Is(broken.Err(), boom) {
		t.Fatalf("a nil setErr erased the recorded failure: %v", broken.Err())
	}

	var nilStream *Stream
	if err := nilStream.Err(); err != nil {
		t.Fatalf("nil stream reported %v, want nil", err)
	}
}

// A screencast drop must not destroy a session the agent is working in. The
// relay is the VIEWING layer, and until this changed its death was the
// session's death: one dropped SSE stream tore down a live browser mid-task,
// and the agent's next verb met a session that no longer existed.
func TestScreencastDropReattachesInsteadOfKillingTheSession(t *testing.T) {
	f := newFakeSidecar(2)
	f.dropNextStreams(1) // the view breaks once; the browser is fine
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	// The relay backs off ~1s before reattaching.
	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) && f.subscribeCount() < 2 {
		time.Sleep(20 * time.Millisecond)
	}

	if got := f.subscribeCount(); got < 2 {
		t.Fatalf("relay attached %d time(s); a dropped view must be reattached, not mourned", got)
	}
	if _, ok := r.Resolve("chat-1", ""); !ok {
		t.Fatal("core forgot the session because its screencast dropped — the agent's work died with the view")
	}
	if f.wasClosed(info.SessionID) {
		t.Fatal("a live session was closed on the sidecar because its screencast dropped")
	}
}

// The other half of the same rule: when the session really is gone, the relay
// must still tear down, and must say WHY. Passing nil here is what recorded six
// dead sessions as ok on 2026-08-30.
func TestRelayGivesUpWhenTheSessionIsGenuinelyGone(t *testing.T) {
	f := newFakeSidecar(2)
	f.dropNextStreams(1)
	r := NewRegistry(f, nil)

	info, err := r.Open(testCtx(), "chat-1", "https://example.com")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	f.killSession(info.SessionID)
	f.forget(info.SessionID) // the sidecar no longer lists it

	deadline := time.Now().Add(4 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := r.Resolve("chat-1", ""); !ok {
			return // torn down, as it should be
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the session was gone from the sidecar and core kept tracking it — Resolve would hand the agent an id that can only fail")
}
