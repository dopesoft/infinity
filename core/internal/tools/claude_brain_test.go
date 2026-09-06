package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// The MCP config is what turns this from a brilliant amnesiac into a brain
// with Infinity's memory. If its shape drifts, Claude Code silently loads no
// server and the turn still succeeds - it just answers with no tools, which
// is the exact empty-because-broken failure this codebase keeps having to fix.
func TestBrainMCPConfigPointsAtInfinity(t *testing.T) {
	raw := brainMCPConfig("https://core.example.com/", "tok-123")

	var cfg struct {
		MCPServers map[string]struct {
			Type    string            `json:"type"`
			URL     string            `json:"url"`
			Headers map[string]string `json:"headers"`
		} `json:"mcpServers"`
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("config is not valid JSON, so Claude Code would load nothing: %v", err)
	}
	srv, ok := cfg.MCPServers["infinity"]
	if !ok {
		t.Fatal("no infinity server in the config")
	}
	if srv.Type != "http" {
		t.Errorf("want type http, got %q", srv.Type)
	}
	// The trailing slash on the origin must not produce a double slash: some
	// proxies 404 that, and the brain would come up toolless.
	if srv.URL != "https://core.example.com/api/mcp/server" {
		t.Errorf("wrong endpoint: %q", srv.URL)
	}
	if srv.Headers["Authorization"] != "Bearer tok-123" {
		t.Errorf("missing or malformed bearer: %q", srv.Headers["Authorization"])
	}
}

// The launch script carries three rules that are not style preferences.
func TestBrainLaunchScriptKeepsTheSubscriptionRules(t *testing.T) {
	files := newBrainFiles("brain-test")
	script := brainLaunchScript(files, llm.BrainTurn{
		Prompt: "what did we decide about pricing?",
		Model:  "opus",
	}, brainLaunch{workspace: brainWorkspaceMac, coreURL: "https://core.example.com", mcpToken: "tok"})

	// 1. An API key in the environment outranks the Mac's sign-in, so the turn
	//    would bill pay-per-token instead of the Max plan the boss pays for.
	if !strings.Contains(script, "unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN") {
		t.Error("the launch does not clear the API key; this is how the wrong plan gets billed")
	}
	// 2. Without strict mode, whatever MCP servers happen to sit on the Mac
	//    join the session, changing the prompt prefix and killing the cache.
	if !strings.Contains(script, "--strict-mcp-config") {
		t.Error("missing --strict-mcp-config")
	}
	if !strings.Contains(script, "--mcp-config "+files.mcp) {
		t.Error("the session is not pointed at Infinity's tools")
	}
	// 3. The config holds a live bearer. It must not be world readable.
	if !strings.Contains(script, "chmod 600 "+files.mcp) {
		t.Error("the token file is not locked down")
	}
	// The prompt travels through the environment, never interpolated into the
	// command, so no quote in the boss's message can break the launch.
	if strings.Contains(script, "what did we decide about pricing?\"") {
		t.Error("the prompt was interpolated into the command line instead of exported")
	}
}

// A turn with no resume id must start cold; one with an id must not.
func TestBrainLaunchScriptResumeIsConditional(t *testing.T) {
	files := newBrainFiles("brain-test")
	cold := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi"}, brainLaunch{workspace: brainWorkspaceMac, coreURL: "https://c", mcpToken: "t"})
	if !strings.Contains(cold, `export INF_RESUME=''`) {
		t.Errorf("a cold start should export an empty resume, got:\n%s", cold)
	}
	warm := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi", Resume: "sess-9"}, brainLaunch{workspace: brainWorkspaceMac, coreURL: "https://c", mcpToken: "t"})
	if !strings.Contains(warm, "sess-9") {
		t.Error("a resumed turn lost its session id")
	}
}

// Which machine runs the turn decides which credential pays, and getting it
// backwards is silent: a token on the Mac would outrank the real sign-in, and
// no token on the cloud box means it can't sign in at all. Neither shows up as
// an error, so both directions are asserted.
func TestBrainLaunchScriptPicksTheRightCredential(t *testing.T) {
	files := newBrainFiles("brain-test")

	cloud := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi"}, brainLaunch{
		workspace: brainWorkspaceCloud,
		coreURL:   "https://c",
		mcpToken:  "m",
		subToken:  "sk-ant-oat01-example",
		cloud:     true,
	})
	if !strings.Contains(cloud, "export CLAUDE_CODE_OAUTH_TOKEN='sk-ant-oat01-example'") {
		t.Error("the cloud box was launched with no subscription token, so it cannot sign in")
	}

	mac := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi"}, brainLaunch{
		workspace: brainWorkspaceMac,
		coreURL:   "https://c",
		mcpToken:  "m",
		subToken:  "sk-ant-oat01-example",
		cloud:     false,
	})
	if strings.Contains(mac, "export CLAUDE_CODE_OAUTH_TOKEN") {
		t.Error("a token was exported on the Mac, where it outranks the sign-in and changes which credential pays")
	}
	if !strings.Contains(mac, "unset CLAUDE_CODE_OAUTH_TOKEN") {
		t.Error("the Mac launch does not clear a stray token from the environment")
	}
}

// The cloud box works out of a different directory than the Mac, and pointing
// a turn at the wrong one means it cannot see the files it has been building.
func TestBrainWorkspaceFollowsTheBridge(t *testing.T) {
	if got := brainWorkspace(nil); got != brainWorkspaceMac {
		t.Errorf("no bridge should fall back to the Mac workspace, got %q", got)
	}
}

// An expired token is the most likely way this brain ever stops working, and
// it lands a year out when nobody remembers setting it up. Claude Code's own
// wording ("Please run /login") reads like a bug in Infinity, so it has to be
// translated into the actual fix.
func TestClaudeAuthFailureNamesTheFix(t *testing.T) {
	cloud, ok := claudeAuthFailure("OAuth token has expired · Please run /login", true)
	if !ok {
		t.Fatal("an expired token was not recognised as an auth failure")
	}
	if !strings.Contains(cloud, "setup-token") {
		t.Errorf("the cloud message must name the fix, got: %s", cloud)
	}

	mac, ok := claudeAuthFailure("Invalid API key · Please run /login", false)
	if !ok {
		t.Fatal("a signed-out Mac was not recognised")
	}
	if strings.Contains(mac, "setup-token") {
		t.Error("the Mac fix is signing in, not minting a token; this sends him down the wrong path")
	}

	// An ordinary failure must NOT be dressed up as an auth problem, or every
	// real bug gets misreported as an expired credential.
	if _, ok := claudeAuthFailure("panic: nil pointer dereference", true); ok {
		t.Error("an unrelated crash was misclassified as an auth failure")
	}
}

// Token-level streaming is the whole reason --include-partial-messages is on.
// These lines are copied from a real `claude -p` run, so if Claude Code
// changes shape this test fails instead of the chat quietly going silent.
func TestBrainStreamsRealTokenDeltas(t *testing.T) {
	events := make(chan llm.StreamEvent, 32)
	p := &brainPoll{out: events}
	p.emit(strings.Join([]string{
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"weighing it up"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"hello "}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":1,"delta":{"type":"text_delta","text":"there friend"}}}`,
		// The assembled message lines are where a tool call and its RESULT
		// both live. The partial stream carries the call alone, which is how
		// this path used to show what the brain decided to do and never what
		// came back.
		`{"type":"assistant","message":{"content":[{"type":"tool_use","id":"tu_1","name":"Bash","input":{"command":"ls"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"tu_1","content":"resume.pdf","is_error":false}]}}`,
		// The assembled message repeats everything above. Consuming it too
		// would print the whole reply a second time.
		`{"type":"assistant","message":{"content":[{"type":"text","text":"hello there friend"}]}}`,
	}, "\n"))
	close(events)

	var text, thinking strings.Builder
	tools := 0
	results := 0
	for ev := range events {
		switch ev.Kind {
		case llm.StreamText:
			text.WriteString(ev.TextDelta)
		case llm.StreamThinking:
			thinking.WriteString(ev.ThinkingDelta)
		case llm.StreamToolCall:
			tools++
			if ev.ToolCall.Name != "claude_code__bash" {
				t.Errorf("tool call lost its name: %+v", ev.ToolCall)
			}
		case llm.StreamToolResult:
			results++
			if ev.ToolOutput != "resume.pdf" {
				t.Errorf("the result never reached the boss or memory: %+v", ev)
			}
			if ev.ToolCallID != "tu_1" {
				t.Errorf("the result was not paired with its call: %+v", ev)
			}
		}
	}
	if got := text.String(); got != "hello there friend" {
		t.Errorf("text did not stream as deltas, got %q", got)
	}
	if thinking.String() != "weighing it up" {
		t.Errorf("reasoning did not stream, got %q", thinking.String())
	}
	if tools != 1 {
		t.Errorf("want 1 tool call surfaced, got %d", tools)
	}
	// Both halves, or the boss watches it decide and never learns what
	// happened - the difference between this brain and every other one.
	if results != 1 {
		t.Errorf("want 1 tool result surfaced, got %d", results)
	}
	if p.streamed != "hello there friend" {
		t.Errorf("streamed text not tracked, so finish() would print the reply twice: %q", p.streamed)
	}
}

// A run that streamed nothing must still produce a reply. Better a late
// answer than a blank turn.
func TestBrainFallsBackWhenNothingStreamed(t *testing.T) {
	events := make(chan llm.StreamEvent, 8)
	p := &brainPoll{out: events, b: nil}
	resp, err := p.finish(claudeResult{Result: "the answer", parsed: true})
	close(events)
	if err != nil {
		t.Fatalf("finish: %v", err)
	}
	if resp.Text != "the answer" {
		t.Errorf("the reply was lost: %q", resp.Text)
	}
	var sawText bool
	for ev := range events {
		if ev.Kind == llm.StreamText && strings.Contains(ev.TextDelta, "the answer") {
			sawText = true
		}
	}
	if !sawText {
		t.Error("nothing streamed and nothing was sent, so the boss would see a blank turn")
	}
}

// brainInitLine builds an init line the size of the real one on the boss's
// Mac: 470 tool names, 101 slash commands, 64 skills and 26 agents, so it
// lands at roughly 24,600 chars like the real thing. The fields are written
// in the order Claude Code 2.1.258 writes them (type, subtype, cwd,
// session_id, tools, ...) - a map marshal would sort them and put the
// markers the parser keys on behind the tool list, which is not the shape
// on the wire.
func brainInitLine(t *testing.T, sessionID string) string {
	t.Helper()
	names := func(prefix string, n, width int) string {
		out := make([]string, n)
		for i := range out {
			out[i] = fmt.Sprintf("%q", fmt.Sprintf("%s%0*d", prefix, width, i))
		}
		return "[" + strings.Join(out, ",") + "]"
	}
	line := `{"type":"system","subtype":"init","cwd":"/tmp/inf-brain/workspace","session_id":"` + sessionID + `",` +
		`"tools":` + names("mcp__infinity__tool_name_padding_", 470, 4) + `,` +
		`"mcp_servers":[{"name":"infinity","status":"connected"}],"model":"claude-opus-5","permissionMode":"bypassPermissions",` +
		`"slash_commands":` + names("command-name-padding-", 101, 3) + `,` +
		`"apiKeySource":"none","claude_code_version":"2.1.258","output_style":"default",` +
		`"agents":` + names("agent-name-padding-", 26, 3) + `,` +
		`"skills":` + names("skill-name-padding-", 64, 3) + `,` +
		`"uuid":"0b2c4d6e-8f01-4234-8567-89abcdef0123"}`
	if !json.Valid([]byte(line)) {
		t.Fatal("the fixture is not valid JSON")
	}
	if len(line) < 24_000 || len(line) > 26_000 {
		t.Fatalf("the fixture must be the size of the real init line, got %d chars", len(line))
	}
	return line
}

// THE COLD-START BUG (2026-09-04). The init line is 24,616 chars on the Mac,
// the poll clamps every line to 8,000, so it stopped being JSON, the session
// id was never seen, and 54 messages opened 54 Claude sessions - each one
// re-writing the whole prefix into cache. The parser has to find the id on
// the line whole, on the line cut by the clamp, and on a byte-cut head read
// sitting behind the SessionStart hook events - and must still refuse a
// sub-agent's id off a non-init line.
func TestParseClaudeInitSessionIDSurvivesTheClamp(t *testing.T) {
	const want = "4fd24f47-0355-42a5-bf7d-765c0ae6cdaa"
	line := brainInitLine(t, want)

	// (a) whole
	if got := parseClaudeInitSessionID(line); got != want {
		t.Fatalf("whole line: got %q", got)
	}
	// (b) cut by the poll's per-line clamp
	clamped := line[:brainLineMaxCols]
	if json.Valid([]byte(clamped)) {
		t.Fatal("the fixture must not survive the clamp as JSON, or this test proves nothing")
	}
	if got := parseClaudeInitSessionID(clamped); got != want {
		t.Fatalf("clamped line: got %q", got)
	}
	// (c) a head read: hook events first, then the init line, cut at the
	// head's byte budget mid-way through the following event.
	var stream strings.Builder
	for i := 0; i < 6; i++ {
		fmt.Fprintf(&stream, `{"type":"system","subtype":"hook_started","hook_id":"%d","hook_name":"SessionStart:startup","session_id":"%s","uuid":"h%d"}`+"\n",
			i, want, i)
	}
	stream.WriteString(line + "\n")
	stream.WriteString(`{"type":"assistant","message":{"model":"claude-opus-5","content":[{"type":"text","text":"` + strings.Repeat("y", 9000) + `"}]},"session_id":"` + want + `"}` + "\n")
	head := stream.String()[:brainInitHeadBytes]
	if got := parseClaudeInitSessionID(head); got != want {
		t.Fatalf("head read: got %q", got)
	}
	// (d) a sub-agent's id on a non-init line is never the answer.
	sub := `{"type":"assistant","message":{"content":[]},"session_id":"11111111-2222-4333-8444-555555555555","parent_tool_use_id":"toolu_1"}` + "\n" +
		`{"type":"system","subtype":"hook_started","hook_name":"SubagentStart","session_id":"11111111-2222-4333-8444-555555555555"}`
	if got := parseClaudeInitSessionID(sub); got != "" {
		t.Fatalf("a non-init line yielded %q; that id is not resumable and breaks the next message", got)
	}
	// And a cut so early that the id itself is incomplete yields nothing,
	// never a mangled id.
	midID := strings.Index(line, want) + len(want)/2
	if got := parseClaudeInitSessionID(line[:midID]); got != "" {
		t.Fatalf("an incomplete id was accepted: %q", got)
	}
}

// The head read is what carries the whole init line, and it is paid for
// only until the id is known.
func TestBrainReadSliceHeadIsPeeledOffTheStatus(t *testing.T) {
	status, head := splitBrainHead("RUNNING\n" + brainHeadMarker + "\n{\"type\":\"system\",\"subtype\":\"init\"}\n")
	if strings.TrimSpace(status) != "RUNNING" {
		t.Fatalf("status = %q", status)
	}
	if !strings.Contains(head, `"subtype":"init"`) {
		t.Fatalf("head = %q", head)
	}
	if strings.Contains(status, "DONE:") {
		t.Fatal("the head must never be matched for DONE:")
	}
	status, head = splitBrainHead("DONE:0\n")
	if status != "DONE:0\n" || head != "" {
		t.Fatalf("a poll without a head read must come back unchanged: %q / %q", status, head)
	}
}

// --max-turns rides the same conditional expansion as --resume: absent when
// the turn sets no cap, present with the number when it does.
func TestBrainLaunchScriptMaxTurnsIsConditional(t *testing.T) {
	files := newBrainFiles("brain-test")
	uncapped := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi"}, brainLaunch{workspace: brainWorkspaceMac, coreURL: "https://c", mcpToken: "t"})
	if !strings.Contains(uncapped, `export INF_MAX_TURNS=''`) {
		t.Errorf("an uncapped turn should export an empty max-turns, got:\n%s", uncapped)
	}
	if !strings.Contains(uncapped, `${INF_MAX_TURNS:+--max-turns "$INF_MAX_TURNS"}`) {
		t.Error("the launch does not expand --max-turns conditionally")
	}
	capped := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi", MaxTurns: 12}, brainLaunch{workspace: brainWorkspaceMac, coreURL: "https://c", mcpToken: "t"})
	if !strings.Contains(capped, `export INF_MAX_TURNS='12'`) {
		t.Errorf("a capped turn lost its cap:\n%s", capped)
	}
}

// The launch cwd has to be spelled the way THAT bridge resolves it. The cloud
// bridge does not expand "~": it joins the path under /workspace and stats
// "/workspace/~", the chdir fails, and the launch script never runs. That is
// how every cloud turn on 2026-09-06 "launched" and then produced nothing.
func TestBrainHomeFollowsTheBridge(t *testing.T) {
	if got := brainHome(nil); got != bridgeHome {
		t.Errorf("no bridge should launch from home on the Mac, got %q", got)
	}
	if got := brainHome(&fakeBridge{}); got != brainWorkspaceCloud {
		t.Errorf("the cloud launch must start from %s, got %q (the cloud bridge cannot resolve %q)", brainWorkspaceCloud, got, bridgeHome)
	}
}

// The cloud box runs Claude as root, and Claude refuses to run unattended as
// root unless it is told it is sandboxed. Without the flag the turn exits in
// its first millisecond; with it on the Mac it would be a lie about where the
// boss's own user is running. Both directions are asserted.
func TestBrainLaunchScriptSandboxesRootOnTheCloud(t *testing.T) {
	files := newBrainFiles("brain-test")
	cloud := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi"}, brainLaunch{
		workspace: brainWorkspaceCloud, coreURL: "https://c", mcpToken: "m", subToken: "tok", cloud: true,
	})
	if !strings.Contains(cloud, "export IS_SANDBOX=1") {
		t.Error("the cloud launch does not set IS_SANDBOX, so Claude refuses bypassPermissions as root and exits at once")
	}
	mac := brainLaunchScript(files, llm.BrainTurn{Prompt: "hi"}, brainLaunch{
		workspace: brainWorkspaceMac, coreURL: "https://c", mcpToken: "m", cloud: false,
	})
	if strings.Contains(mac, "export IS_SANDBOX=1") {
		t.Error("the Mac launch claims to be sandboxed")
	}
}

// A 200 from the bridge is not a launch. The shell it ran can fail before the
// first line (a cwd it cannot enter), and the bridge reports that as exit -1
// with no output. Treating that as "started" is how a turn polls a stream
// that will never exist for twenty minutes.
func TestBrainLaunchedRefusesAShellThatNeverRan(t *testing.T) {
	reply := func(exit int, out string) []byte {
		b, _ := json.Marshal(map[string]any{"exit_code": exit, "output": out})
		return b
	}
	b := &fakeBridge{}
	if err := brainLaunched(b, reply(0, "LAUNCHED\n")); err != nil {
		t.Fatalf("a script that ran to its marker was refused: %v", err)
	}
	err := brainLaunched(b, reply(-1, ""))
	if err == nil {
		t.Fatal("a shell that exited -1 with no output was accepted as a launch")
	}
	for _, want := range []string{"the cloud box", "exited -1", "printed nothing"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the failure does not say %q: %v", want, err)
		}
	}
	if err := brainLaunched(b, reply(0, "bash: something went wrong\n")); err == nil {
		t.Fatal("a script that never reached its LAUNCHED line was accepted")
	}
}

// A launch that never produced a stream file is a dead process, and the poll
// must say so within the start grace, not at the 20-minute ceiling. Before
// this the boss watched "thinking" for three minutes and hit Stop.
func TestBrainWaitFailsFastWhenNoStreamEverAppears(t *testing.T) {
	fb := &fakeBridge{status: brainNoStreamMarker}
	p := &brainPoll{
		b:         fb,
		workspace: brainWorkspaceCloud,
		files:     newBrainFiles("brain-test"),
		line:      1,
		// Launched longer ago than the grace: the very first poll decides.
		started: time.Now().Add(-brainStartGrace - time.Second),
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := p.wait(ctx)
	if err == nil {
		t.Fatal("a turn with no stream file past the grace was still being waited on")
	}
	if ctx.Err() != nil {
		t.Fatalf("the poll ran to the test's own deadline instead of failing fast: %v", err)
	}
	if !strings.Contains(err.Error(), "never started on the cloud box") {
		t.Errorf("the failure does not name the box or the cause: %v", err)
	}

	// Inside the grace the same status is NOT a verdict: the file appears
	// milliseconds after LAUNCHED and a slow login shell can stretch that.
	fresh := &brainPoll{b: &fakeBridge{status: brainNoStreamMarker}, workspace: brainWorkspaceCloud, files: newBrainFiles("brain-test"), line: 1, started: time.Now()}
	sctx, scancel := context.WithTimeout(context.Background(), 700*time.Millisecond)
	defer scancel()
	if _, err := fresh.wait(sctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a launch inside its grace was written off: %v", err)
	}
}
