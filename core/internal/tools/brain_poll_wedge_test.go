package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// The poll must never go silent on a stream that is still being written.
//
// The reference failure (2026-09-01): one line bigger than the read window
// meant the slice came back with no newline in it, nothing counted as
// complete, the read position never moved, and every poll for the rest of the
// turn returned nothing. Claude kept working and the boss watched "Working..."
// for ten minutes with no way to tell it apart from a hang.

// fakeBridge serves a canned /bash response and records what it was asked.
type fakeBridge struct {
	out  string
	last string
	cmds []string
	// status is the poll's first line; empty means RUNNING (the stream file
	// exists and the turn is still going).
	status string
}

func (f *fakeBridge) Name() bridge.Kind { return bridge.KindCloud }

func (f *fakeBridge) BaseURL() string { return "http://fake" }

func (f *fakeBridge) Health(_ context.Context) bool { return true }

func (f *fakeBridge) Post(_ context.Context, path string, body any) ([]byte, int, bool) {
	if path != "/bash" {
		return nil, 404, false
	}
	fields, _ := body.(map[string]any)
	cmd, _ := fields["cmd"].(string)
	f.cmds = append(f.cmds, cmd)
	// Mirror the real script's three-part shape.
	status := f.status
	if status == "" {
		status = "RUNNING"
	}
	payload, _ := json.Marshal(map[string]any{
		"output":    status + "\n===LAST===\n" + f.last + "\n===NEW===\n" + f.out,
		"exit_code": 0,
	})
	return payload, 200, true
}

func (f *fakeBridge) Get(_ context.Context, _ string) ([]byte, int, bool) { return nil, 404, false }

// TestBrainConsume_StepsOverALineTooBigForTheWindow.
//
// A slice that FILLED its budget and holds no newline can never complete, so
// the position must move. A slice that merely caught Claude mid-write must
// NOT move, because that half-line finishes in a moment.
func TestBrainConsume_StepsOverALineTooBigForTheWindow(t *testing.T) {
	p := &brainPoll{}
	giant := strings.Repeat("x", brainChunkBytes)

	if got := p.consume(giant, true); got != "" {
		t.Fatalf("a truncated line is not an event: got %q", got)
	}
	if p.line != 1 {
		t.Fatalf("the read position never moved past an oversized line: line=%d. "+
			"Every later poll re-reads the same bytes and the turn goes silent.", p.line)
	}

	q := &brainPoll{}
	if got := q.consume("half a line, still bein", false); got != "" {
		t.Fatalf("a half-written line is not an event: got %q", got)
	}
	if q.line != 0 {
		t.Fatalf("a line Claude is mid-way through writing must be re-read whole, not skipped: line=%d", q.line)
	}
}

// TestBrainReadSlice_ClampsEveryLine: the clamp is what makes the case above
// rare instead of routine. Without `cut -c`, one assembled message or one big
// tool result fills the window on its own.
func TestBrainReadSlice_ClampsEveryLine(t *testing.T) {
	fb := &fakeBridge{out: "{}\n"}
	p := &brainPoll{b: fb, files: newBrainFiles("job")}
	if _, _, ok := p.readSlice(context.Background()); !ok {
		t.Fatal("read failed")
	}
	if len(fb.cmds) == 0 {
		t.Fatal("no command was sent")
	}
	if !strings.Contains(fb.cmds[0], "cut -c 1-") {
		t.Fatalf("the per-line clamp is missing from the read script:\n%s", fb.cmds[0])
	}
}

// TestBrainPollOnce_DrainsABurstInOneGo. A slice that filled its budget means
// there is more waiting; reading it one window per 300ms turns a burst into a
// visible lag.
func TestBrainPollOnce_DrainsABurstInOneGo(t *testing.T) {
	// A full window of complete lines, so every read reports `full`.
	line := strings.Repeat("a", 200) + "\n"
	full := strings.Repeat(line, brainChunkBytes/len(line)+1)
	fb := &fakeBridge{out: full}
	p := &brainPoll{b: fb, files: newBrainFiles("job")}

	if _, ok := p.pollOnce(context.Background()); !ok {
		t.Fatal("poll failed")
	}
	if len(fb.cmds) != brainMaxDrains {
		t.Fatalf("a saturated stream should drain %d times in one poll, got %d reads",
			brainMaxDrains, len(fb.cmds))
	}
}

// TestBrainPollOnce_StopsDrainingWhenCaughtUp: the drain must not cost extra
// round trips on a quiet turn, which is most of them.
func TestBrainPollOnce_StopsDrainingWhenCaughtUp(t *testing.T) {
	fb := &fakeBridge{out: "{\"type\":\"x\"}\n"}
	p := &brainPoll{b: fb, files: newBrainFiles("job")}
	if _, ok := p.pollOnce(context.Background()); !ok {
		t.Fatal("poll failed")
	}
	if len(fb.cmds) != 1 {
		t.Fatalf("a caught-up stream should cost exactly one read, got %d", len(fb.cmds))
	}
}
