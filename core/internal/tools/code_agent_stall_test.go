package tools

import (
	"strings"
	"testing"
	"time"
)

// The ACTIVITY FINGERPRINT must be stamped by the shared poll loop, not by a
// caller's heartbeat closure.
//
// It was written in code_agent's closure first. `background_build` reaches
// Claude Code through this SAME runner but supplies its own heartbeat, so it
// never stamped the fingerprint at all — and the stall detector, finding no
// meta.activity_at, fell back to started_at and reported a perfectly healthy
// build as showing no activity twelve minutes in, while its own progress line
// was still naming the file it was reading (run a327070c, 2026-08-29 05:26).
//
// A mechanic implemented in one of two call sites is a mechanic that is
// missing (CLAUDE.md Rule #1b). These tests hold it at the chokepoint.
func TestNoteActivity_StampsOnlyWhenTheWorkActuallyChanges(t *testing.T) {
	var stamped []string
	p := &claudePoll{setMeta: func(k, v string) { stamped = append(stamped, k+"="+v) }}

	// Nothing known yet: stamping "" would reset the clock every poll and make
	// a stalled job look busy forever.
	p.noteActivity()
	if len(stamped) != 0 || p.Steps() != 0 {
		t.Fatalf("an unknown activity must not stamp: %v", stamped)
	}

	p.lastAction, p.lastDetail = "Edit", "core/x.go"
	p.noteActivity()
	if p.Steps() != 1 {
		t.Fatalf("the first real activity is step 1, got %d", p.Steps())
	}
	if len(stamped) != 2 || !strings.HasPrefix(stamped[0], "activity_key=") || !strings.HasPrefix(stamped[1], "activity_at=") {
		t.Fatalf("both halves of the fingerprint must be written: %v", stamped)
	}
	if _, err := time.Parse(time.RFC3339, strings.TrimPrefix(stamped[1], "activity_at=")); err != nil {
		t.Fatalf("activity_at must be RFC3339 — the stall query casts it to timestamptz: %v", err)
	}

	// THE LOAD-BEARING CASE. Claude sitting on one long `Bash` (a `go test`
	// that takes eight minutes) reports the same tool+target on every poll.
	// Re-stamping there would move the clock and make a genuinely stuck job
	// look alive forever; bumping the step would march the progress bar while
	// nothing happens.
	before := len(stamped)
	p.noteActivity()
	p.noteActivity()
	if len(stamped) != before || p.Steps() != 1 {
		t.Fatalf("the same work is the same step: stamps=%v steps=%d", stamped[before:], p.Steps())
	}

	// A genuinely new target is a new step.
	p.lastDetail = "core/y.go"
	p.noteActivity()
	if p.Steps() != 2 {
		t.Fatalf("a new target is a new step, got %d", p.Steps())
	}
}
