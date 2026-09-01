package tools

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
)

// These exercise the REAL launch / poll / kill / salvage scripts against the
// local-bash stand-in for the Mac bridge (see code_agent_e2e_test.go), with a
// fake `claude` on PATH. No Claude quota is spent.

const maxAccountJSON = `{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}`

// slowFakeClaude streams its init line (carrying the session id), does one
// visible edit, then keeps working long enough for the turn to end under it.
const slowFakeClaude = `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"sess-detach-1","model":"claude-opus-5"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"core/x.go","old_string":"a","new_string":"b"}}]}}'
sleep 2
echo '{"type":"result","subtype":"success","is_error":false,"api_error_status":null,"result":"Done: finished after the turn ended","modelUsage":{"claude-opus-5":{"inputTokens":1}}}'
`

// forkingFakeClaude spawns a grandchild that outlives it - a `go build`, a
// test run, an MCP server. `pkill -TERM -P $wrapper` never reached these.
const forkingFakeClaude = `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"sess-kill-1","model":"claude-opus-5"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"core/deep.go","old_string":"a","new_string":"b"}}]}}'
( sleep 45 ) &
echo $! > "$FAKE_GRANDCHILD_PID"
sleep 45
`

func installFakeClaude(t *testing.T, mac *localMacBridge, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(mac.bin, "claude"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// alive reports whether pid is still running on this machine.
func alive(pid string) bool {
	pid = strings.TrimSpace(pid)
	if pid == "" {
		return false
	}
	return exec.Command("kill", "-0", pid).Run() == nil
}

func readFileTrim(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// Why: THE bug. INFINITY_TURN_TIMEOUT (15 min) always fired before this
// tool's own 20-minute ceiling, and because ctx.Done() was checked first, the
// turn's clock took the KILL branch every time - the "never killed" path was
// unreachable dead code and every long build died with the chat turn.
//
// The turn ending must now DETACH: the job stays alive, the tool returns
// stillRunningError, a follower keeps watching, and its real result still
// lands. This test fails on the old code (which killed and returned a
// "STOPPED" string with a nil error).
func TestClaudeCodeRunner_TurnTimeoutDetachesAndTheJobFinishes(t *testing.T) {
	mac, repo := setupLocalMac(t, maxAccountJSON)
	installFakeClaude(t, mac, slowFakeClaude)

	var (
		mu   sync.Mutex
		meta = map[string]string{}
	)
	done := make(chan string, 1)
	r := NewClaudeCodeRunner(nil, nil)

	// The chat turn's clock, not the job's. Long enough that the run is
	// genuinely under way (preflight, auth probe, launch, several polls),
	// short enough that it expires while Claude is still working.
	inline, cancelInline := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancelInline()

	_, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-detach", Task: "edit core/x.go", Repo: repo,
		MaxWait: 30 * time.Second, KillOnCancel: true,
		Inline:   inline,
		Detached: func(o JobOutcome) { done <- o.Summary },
		SetMeta:  func(k, v string) { mu.Lock(); meta[k] = v; mu.Unlock() },
	})

	var still *stillRunningError
	if !errors.As(err, &still) {
		t.Fatalf("a turn timeout must report STILL RUNNING, not kill: err=%v\ncommands:\n%s", err, strings.Join(mac.cmds, "\n----\n"))
	}
	if !still.following {
		t.Fatal("a detached job must be followed, or its run row never closes with a receipt")
	}
	// The whole point: Claude is still alive on the bridge.
	pidFile := filepath.Join(codeAgentTmpDir, "job-detach.pid")
	pid := readFileTrim(t, pidFile)
	if pid == "" {
		t.Fatal("the launch must record a pid")
	}
	if !alive(pid) {
		t.Fatal("the job was KILLED by the turn timeout — this is the exact bug being fixed")
	}
	msg := still.inlineMessage()
	for _, want := range []string{"STILL RUNNING", "job-detach", "Do NOT call code_agent again"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("the tool result must tell the truth, missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "claude --continue") {
		t.Fatalf("the old advice pointed at a shell path the raw guard blocks:\n%s", msg)
	}

	// Phase 2: the session id is on the wire from the first line and is now
	// captured - this is what makes a real resume possible later.
	mu.Lock()
	gotSession := meta["claude_session_id"]
	mu.Unlock()
	if gotSession != "sess-detach-1" {
		t.Fatalf("claude_session_id must be recorded on the run, got %+v", meta)
	}

	// The follower closes the loop: the real receipt arrives after the turn.
	select {
	case summary := <-done:
		if !strings.Contains(summary, "Done: finished after the turn ended") {
			t.Fatalf("the follower must hand back Claude's REAL result, got: %q", summary)
		}
		if !strings.Contains(summary, "Max subscription") {
			t.Fatalf("the proof line must survive the detour: %q", summary)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the follower never reported the finished job")
	}

	// Phase 2: /tmp/inf-code is cleaned per run instead of growing forever.
	if _, statErr := os.Stat(pidFile); statErr == nil {
		t.Fatalf("the settled job's files must be cleaned up: %s still exists", pidFile)
	}
}

// Why: the job's context is what the bridge, the auth probe and the launch
// all run on. Deriving it by hand (copying "the values we remembered") is how
// bridge selection silently breaks. This pins the contract: EVERY value
// survives, only the turn's cancellation and deadline are dropped.
func TestCodeAgentJobContext_KeepsEveryValueButDropsTheTurnsClock(t *testing.T) {
	active := NewActiveSet(nil)
	turn, cancelTurn := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelTurn()
	turn = WithSessionID(WithActiveSet(WithAutonomous(turn), active), "sess-42")
	turn, detach := WithDetachSignal(turn)

	job, cancelJob := codeAgentJobContext(turn)
	defer cancelJob()

	if got := SessionIDFromContext(job); got != "sess-42" {
		t.Fatalf("the session id decides which bridge answers; got %q", got)
	}
	if !IsAutonomous(job) {
		t.Fatal("the autonomy marker must survive, or gates read the job as interactive")
	}
	if ActiveSetFromContext(job) != active {
		t.Fatal("the session's ActiveSet must survive")
	}
	if DetachRequested(job) == nil {
		t.Fatal("the detach signal must survive")
	}
	detach() // fires the loop's wire; must not disturb the job context
	if job.Err() != nil {
		t.Fatalf("a detach must never end the job's own context: %v", job.Err())
	}
	// The turn's clock runs out; the job's does not.
	<-turn.Done()
	if !errors.Is(turn.Err(), context.DeadlineExceeded) {
		t.Fatalf("the turn should have timed out: %v", turn.Err())
	}
	if job.Err() != nil {
		t.Fatalf("the turn ending must NOT end the job — this is the guillotine being removed: %v", job.Err())
	}
	dl, ok := job.Deadline()
	if !ok || time.Until(dl) < codeAgentMaxWait {
		t.Fatalf("the job must carry its own budget, got deadline in %s", time.Until(dl))
	}
	cancelTurn()
	if job.Err() != nil {
		t.Fatalf("cancelling the turn outright must not cancel the job either: %v", job.Err())
	}
}

// Why: the "never killed" branch at the wait ceiling was unreachable dead
// code, because the turn's context always fired first. Even in the old
// single-context shape, a DEADLINE must now leave the job running - only an
// explicit cancel kills.
func TestClaudeCodeRunner_ADeadlineNeverKills(t *testing.T) {
	mac, repo := setupLocalMac(t, maxAccountJSON)
	installFakeClaude(t, mac, slowFakeClaude)
	files := newClaudeJobFiles("job-deadline")
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	r := NewClaudeCodeRunner(nil, nil)
	// No Inline: the caller's context IS the wait window, the old shape.
	_, err := r.Run(ctx, ClaudeCodeJob{
		Bridge: mac, JobID: "job-deadline", Task: "edit core/x.go", Repo: repo,
		MaxWait: 30 * time.Second, KillOnCancel: true,
	})
	var still *stillRunningError
	if !errors.As(err, &still) {
		t.Fatalf("a deadline must report STILL RUNNING, not kill: %v", err)
	}
	pid := readFileTrim(t, files.pid)
	if !alive(pid) {
		t.Fatal("the job was killed by a clock; only an explicit stop may kill")
	}
	t.Cleanup(func() { _ = exec.Command("kill", "-9", pid).Run() })
}

// Why: an explicit stop must still stop - and must still hand back what the
// job actually did. collect() used to be reachable only from the DONE branch,
// so a killed job's transcript (every edit it had made) was orphaned in
// /tmp/inf-code and read by nothing.
func TestClaudeCodeRunner_ExplicitStopKillsTheWholeTreeAndSalvagesTheReceipt(t *testing.T) {
	mac, repo := setupLocalMac(t, maxAccountJSON)
	grandchildFile := filepath.Join(filepath.Dir(mac.home), "grandchild.pid")
	mac.extra = append(mac.extra, "FAKE_GRANDCHILD_PID="+grandchildFile)
	installFakeClaude(t, mac, forkingFakeClaude)

	var (
		mu   sync.Mutex
		meta = map[string]string{}
	)
	r := NewClaudeCodeRunner(nil, nil)
	inline, cancelInline := context.WithCancel(context.Background())
	go func() {
		// The boss says "stop building": the loop cancels the tool context.
		time.Sleep(400 * time.Millisecond)
		cancelInline()
	}()
	defer cancelInline()

	stopReported := ""
	out, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-stop", Task: "edit core/deep.go", Repo: repo,
		MaxWait: 30 * time.Second, KillOnCancel: true, Inline: inline,
		Stopped: func(summary string) { stopReported = summary },
		SetMeta: func(k, v string) { mu.Lock(); meta[k] = v; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("a stop is not an error: %v", err)
	}
	if !strings.Contains(out, "STOPPED") || !strings.Contains(out, "was killed") {
		t.Fatalf("the result must say it was stopped: %q", out)
	}
	// The caller has to KNOW it was stopped, or it closes the run row green
	// with a "was STOPPED" summary - a job that reached no verdict rendering
	// as success (runs.StoppedInterrupted exists for exactly this).
	if stopReported != out {
		t.Fatalf("the stop must be reported to the caller, got %q", stopReported)
	}
	// The salvage: what it actually did, from the transcript nothing used to read.
	if !strings.Contains(out, "core/deep.go") {
		t.Fatalf("a killed job must hand back what it had already done: %q", out)
	}
	mu.Lock()
	gotSession := meta["claude_session_id"]
	mu.Unlock()
	if gotSession != "sess-kill-1" {
		t.Fatalf("a killed job must still record its session id (that is how it can be resumed): %+v", meta)
	}

	// The process-group kill: the grandchild `sleep` is a level below what
	// `pkill -TERM -P $wrapper` could ever reach.
	gpid := readFileTrim(t, grandchildFile)
	if gpid == "" {
		t.Skip("the fake claude never recorded a grandchild pid")
	}
	deadline := time.Now().Add(5 * time.Second)
	for alive(gpid) && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	if alive(gpid) {
		_ = exec.Command("kill", "-9", gpid).Run()
		t.Fatal("the kill orphaned a grandchild process — it must reap the whole process group")
	}
}

// Why: the whole wire, end to end at the tool boundary — the loop's detach
// signal on the context, through code_agent.Execute, to a job that is still
// alive afterwards and a result the model can read. The loop half is covered
// in agent/steer_interrupt_test.go; this is the half it hands off to.
func TestCodeAgent_DetachSignalLeavesTheJobRunningAndAnswersNow(t *testing.T) {
	mac, repo := setupLocalMac(t, maxAccountJSON)
	installFakeClaude(t, mac, slowFakeClaude)

	tool := &codeAgent{runner: NewClaudeCodeRunner(bridge.NewRouter(mac, nil), nil)}
	ctx, detach := WithDetachSignal(WithSessionID(context.Background(), "11111111-1111-4111-8111-111111111111"))
	go func() {
		// The boss types "how's it going?" while it works.
		time.Sleep(900 * time.Millisecond)
		detach()
	}()
	out, err := tool.Execute(ctx, map[string]any{"task": "edit core/x.go", "repo": repo})
	if err != nil {
		t.Fatalf("a detach must come back as a readable result, not an error: %v", err)
	}
	if !strings.Contains(out, "STILL RUNNING") || !strings.Contains(out, "Do NOT call code_agent again") {
		t.Fatalf("the model must be told the job lives and not to relaunch it:\n%s", out)
	}
	if strings.Contains(out, "STOPPED") {
		t.Fatalf("a question must never read as a stop:\n%s", out)
	}
	// The run has no id here (no database in the test), so it falls back to a
	// synthetic job id; the pid file is named after it.
	matches, _ := filepath.Glob(filepath.Join(codeAgentTmpDir, "*.pid"))
	if len(matches) != 1 {
		t.Fatalf("expected exactly one launched job, found %v", matches)
	}
	pid := readFileTrim(t, matches[0])
	if !alive(pid) {
		t.Fatal("the job must still be working on the Mac after the boss spoke")
	}
	t.Cleanup(func() { _ = exec.Command("kill", "-9", "-"+pid).Run() })
	// Let the follower see the job through to the end before the test tears
	// down its fake Mac: the whole point is that it keeps going after this
	// call returned.
	deadline := time.Now().Add(20 * time.Second)
	for alive(pid) && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	for time.Now().Before(deadline) {
		if _, statErr := os.Stat(matches[0]); statErr != nil {
			break // the follower collected the receipt and cleaned up
		}
		time.Sleep(50 * time.Millisecond)
	}
	if _, statErr := os.Stat(matches[0]); statErr == nil {
		t.Fatalf("the follower never closed out the job: %s still there", matches[0])
	}
}

// Why: the launch has to make the wrapper a process-group LEADER, or the kill
// falls back to the old one-level pkill. `setsid` does not exist on macOS, so
// this is done with monitor mode - and the pgid is proven equal to the pid.
func TestClaudeLaunchScript_PutsTheJobInItsOwnProcessGroup(t *testing.T) {
	mac, repo := setupLocalMac(t, maxAccountJSON)
	installFakeClaude(t, mac, slowFakeClaude)
	files := newClaudeJobFiles("job-pgid")
	_, code, ok := mac.Post(context.Background(), "/bash", map[string]any{
		"cmd": claudeLaunchScript(files, "task", "claude-opus-5[1m]", "", "", ""),
		"cwd": repo, "timeout_sec": 20,
	})
	if !ok || code >= 300 {
		t.Fatalf("launch failed: status=%d", code)
	}
	pid := readFileTrim(t, files.pid)
	pgid := readFileTrim(t, files.pgid)
	if pid == "" || pgid == "" {
		t.Fatalf("launch must record both pid (%q) and pgid (%q)", pid, pgid)
	}
	if pid != pgid {
		t.Fatalf("the wrapper must be its own process-group leader (pid=%s pgid=%s); "+
			"without that the kill can only reap one level", pid, pgid)
	}
	// The kill script must therefore choose the GROUP path, not the fallback.
	script := claudeKillScript(files)
	if !strings.Contains(script, `kill -TERM -"$GROUP"`) || !strings.Contains(script, `[ "$G" = "$P" ]`) {
		t.Fatalf("the kill must signal the group, and only when it is provably the job's own:\n%s", script)
	}
	t.Cleanup(func() { _ = exec.Command("kill", "-9", "-"+pgid).Run() })
}

// Why: Claude's session id rides every stream event. Nothing parsed it, so
// `claude --resume` could never be wired up. This pins the exact shape.
func TestParseClaudeSessionID(t *testing.T) {
	stream := strings.Join([]string{
		`not json`,
		`{"type":"system","subtype":"init","session_id":"abc-123","model":"claude-opus-5"}`,
		`{"type":"assistant","session_id":"abc-123","message":{"content":[]}}`,
	}, "\n")
	if got := parseClaudeSessionID(stream); got != "abc-123" {
		t.Fatalf("session id = %q, want abc-123", got)
	}
	if got := parseClaudeSessionID(`{"type":"system","subtype":"init"}`); got != "" {
		t.Fatalf("no session id must read as empty, got %q", got)
	}
	if got := parseClaudeSessionID("garbage"); got != "" {
		t.Fatalf("garbage must not invent an id, got %q", got)
	}
}

// Why: a killed or abandoned run never writes its own summary, so the receipt
// has to be reconstructed from the transcript.
func TestClaudeTouchedFiles(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read","input":{"file_path":"core/a.go"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"core/b.go"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Write","input":{"file_path":"core/c.go"}}]}}`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"core/b.go"}}]}}`,
	}, "\n")
	got := claudeTouchedFiles(stream)
	if len(got) != 2 || got[0] != "core/b.go" || got[1] != "core/c.go" {
		t.Fatalf("only written files, newest first, deduped: %v", got)
	}
	if len(claudeTouchedFiles("nothing here")) != 0 {
		t.Fatal("an empty transcript must claim nothing")
	}
}
