package tools

import (
	"encoding/json"
	"strings"
	"testing"

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
