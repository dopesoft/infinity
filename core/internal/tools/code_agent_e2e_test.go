package tools

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/bridge"
	"github.com/dopesoft/infinity/core/internal/llm"
)

// localMacBridge stands in for the Mac bridge by running /bash requests the
// way tools/mcp-bridge/exec.go does (`bash -lc <cmd>`, cwd, combined output,
// {output, exit_code} JSON) - so these tests exercise the REAL launch, poll,
// probe and collect scripts, with a fake `claude` on PATH and a fake HOME
// holding the sign-in files. No Claude quota is spent.
type localMacBridge struct {
	home  string
	bin   string
	extra []string
	mu    sync.Mutex
	cmds  []string
}

func (b *localMacBridge) Name() bridge.Kind           { return bridge.KindMac }
func (b *localMacBridge) BaseURL() string             { return "http://mac.local.test" }
func (b *localMacBridge) Health(context.Context) bool { return true }
func (b *localMacBridge) Get(context.Context, string) ([]byte, int, bool) {
	return nil, 404, true
}
func (b *localMacBridge) Post(ctx context.Context, path string, body any) ([]byte, int, bool) {
	if path != "/bash" {
		return []byte(`{"error":"unexpected path"}`), 404, true
	}
	req, _ := body.(map[string]any)
	cmd, _ := req["cmd"].(string)
	cwd, _ := req["cwd"].(string)
	b.mu.Lock()
	b.cmds = append(b.cmds, cmd)
	b.mu.Unlock()
	if cwd != "" {
		if st, err := os.Stat(cwd); err != nil || !st.IsDir() {
			return []byte(`{"error":"cwd not a directory: ` + cwd + `"}`), 400, true
		}
	}
	c := exec.CommandContext(ctx, "bash", "-lc", cmd)
	c.Dir = cwd
	env := []string{"HOME=" + b.home, "PATH=" + b.bin + ":" + os.Getenv("PATH")}
	env = append(env, b.extra...)
	c.Env = env
	out, err := c.CombinedOutput()
	code := 0
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		code = ee.ExitCode()
	} else if err != nil {
		return []byte(`{"error":"` + err.Error() + `"}`), 500, true
	}
	reply, _ := json.Marshal(map[string]any{"output": string(out), "exit_code": code, "truncated": false})
	return reply, 200, true
}

// fakeClaude is the stand-in `claude` CLI: it refuses if an API key reached
// it, streams the documented stream-json shape, and leaves a marker so a
// test can prove whether it was launched at all.
const fakeClaude = `#!/bin/bash
echo launched > "$FAKE_CLAUDE_MARK"
if [ -n "${ANTHROPIC_API_KEY:-}" ]; then
  echo '{"type":"result","subtype":"error","is_error":true,"result":"ANTHROPIC_API_KEY reached claude - the subscription was bypassed"}'
  exit 1
fi
case "$*" in *"--output-format stream-json"*"--verbose"*) ;; *) echo '{"type":"result","subtype":"error","is_error":true,"result":"not streaming"}'; exit 1;; esac
echo '{"type":"system","subtype":"init","model":"claude-opus-5"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"core/x.go","old_string":"a","new_string":"b"}}]}}'
sleep 0.4
echo '{"type":"result","subtype":"success","is_error":false,"api_error_status":null,"result":"Done: edited core/x.go","modelUsage":{"claude-opus-5":{"inputTokens":1}}}'
`

// gitInit makes dir a real git worktree - the preflight refuses to launch
// Claude Code anywhere that is not one.
func gitInit(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init %s: %v: %s", dir, err, out)
	}
}

// resolved is the symlink-free absolute form of p, which is what the
// preflight reports back (macOS temp dirs live under /private/var).
func resolved(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatalf("resolve %s: %v", p, err)
	}
	return r
}

func setupLocalMac(t *testing.T, oauthAccount string) (*localMacBridge, string) {
	t.Helper()
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	bin := filepath.Join(root, "bin")
	repo := filepath.Join(root, "repo")
	for _, d := range []string{home, filepath.Join(home, ".claude"), bin, repo} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, ".claude.json"), []byte(`{"oauthAccount":`+oauthAccount+`,"projects":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"claude-fable-5[1m]","effortLevel":"xhigh"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte(fakeClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	gitInit(t, repo)
	mark := filepath.Join(root, "launched.mark")
	b := &localMacBridge{home: home, bin: bin, extra: []string{"FAKE_CLAUDE_MARK=" + mark}}

	prevTmp, prevPoll := codeAgentTmpDir, codeAgentPollEach
	codeAgentTmpDir = filepath.Join(root, "inf-code")
	codeAgentPollEach = 60 * time.Millisecond
	t.Cleanup(func() { codeAgentTmpDir, codeAgentPollEach = prevTmp, prevPoll })
	llm.ResetQuotaLedgerForTest()
	t.Cleanup(llm.ResetQuotaLedgerForTest)
	return b, repo
}

// Why: this is the whole contract end to end - the real scripts, a Max
// sign-in on the Mac, an API key in the bridge's shell that must NOT reach
// Claude, stream-json progress, and the proof line on the result.
func TestClaudeCodeRunner_EndToEnd_RunsOnTheSubscription(t *testing.T) {
	mac, repo := setupLocalMac(t, `{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}`)
	// The bridge's environment carries a key; the launch must strip it.
	mac.extra = append(mac.extra, "ANTHROPIC_API_KEY=sk-ant-test-should-never-reach-claude")

	var (
		mu     sync.Mutex
		meta   = map[string]string{}
		labels []string
	)
	r := NewClaudeCodeRunner(nil, nil)
	summary, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-e2e", Task: "edit core/x.go", Repo: repo,
		Model: "claude-opus-5[1m]", Effort: "high", MaxWait: 20 * time.Second, KillOnCancel: true,
		Heartbeat: func(label, _, _ string, _ int) { mu.Lock(); labels = append(labels, label); mu.Unlock() },
		SetMeta:   func(k, v string) { mu.Lock(); meta[k] = v; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("run: %v\ncommands:\n%s", err, strings.Join(mac.cmds, "\n----\n"))
	}
	if !strings.Contains(summary, "Done: edited core/x.go") {
		t.Fatalf("Claude's result must be the summary: %q", summary)
	}
	if !strings.Contains(summary, "Ran as `claude -p` on Claude Code (Max subscription · kai@example.com)") || !strings.Contains(summary, "model claude-opus-5") {
		t.Fatalf("the proof line must name the plan and the model that ran: %q", summary)
	}
	if meta["auth"] != "Max subscription · kai@example.com" {
		t.Fatalf("meta.auth must carry the subscription proof: %+v", meta)
	}
	if meta["apikey_in_env"] == "" {
		t.Fatalf("the stripped key must be recorded on the run: %+v", meta)
	}
	if meta["model"] != "claude-opus-5" {
		t.Fatalf("meta.model must be corrected to what Claude reports: %+v", meta)
	}
	// The audit trail: which directory, which binary, which model, whose plan.
	want := resolved(t, repo)
	if meta["repo"] != want || meta["repo_root"] != want {
		t.Fatalf("meta must record the resolved repo and its git root (want %q): %+v", want, meta)
	}
	if !strings.HasSuffix(meta["claude_bin"], "/claude") {
		t.Fatalf("meta.claude_bin must record the resolved executable: %+v", meta)
	}
	for _, frag := range []string{"repo " + want, "git root " + want, "/claude", "model claude-opus-5[1m]", "Max subscription · kai@example.com"} {
		if !strings.Contains(meta["preflight"], frag) {
			t.Fatalf("meta.preflight missing %q: %q", frag, meta["preflight"])
		}
	}
	if strings.Contains(meta["preflight"], "sk-ant") {
		t.Fatalf("the evidence line must never carry a key: %q", meta["preflight"])
	}
	if !strings.Contains(summary, "in "+want) {
		t.Fatalf("the result footer must name the repo it ran in: %q", summary)
	}
	joined := strings.Join(labels, "\n")
	if !strings.Contains(joined, "signed in on your Max subscription") {
		t.Fatalf("the sign-in must be reported before launch:\n%s", joined)
	}
	if !strings.Contains(joined, "Claude Code · Edit core/x.go") {
		t.Fatalf("mid-run polls must name Claude's current tool call:\n%s", joined)
	}
}

// Why: the boss's Max plan is the ONLY thing allowed to pay for coding on
// the Mac. A Mac whose Claude Code is not signed in to it must refuse before
// launching anything - and say so - instead of running on whatever would
// answer.
func TestClaudeCodeRunner_EndToEnd_RefusesWithoutTheSubscription(t *testing.T) {
	mac, repo := setupLocalMac(t, `{}`)
	var meta = map[string]string{}
	r := NewClaudeCodeRunner(nil, nil)
	_, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-refuse", Task: "edit core/x.go", Repo: repo, MaxWait: 5 * time.Second,
		SetMeta: func(k, v string) { meta[k] = v },
	})
	var notSub *notSubscriptionError
	if !errors.As(err, &notSub) {
		t.Fatalf("want notSubscriptionError, got %v", err)
	}
	if meta["auth"] != "not signed in" {
		t.Fatalf("the refusal must be on the run row too: %+v", meta)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(mac.home), "launched.mark")); statErr == nil {
		t.Fatal("claude must never have been launched")
	}
	if !strings.Contains(notSub.guidance(), "NOT LAUNCHED") || !strings.Contains(notSub.guidance(), "/login") {
		t.Fatalf("guidance must tell the boss how to fix it: %s", notSub.guidance())
	}
}

// Why: a blank or wrong repo used to fall through to the bridge's default
// cwd - ~/Dev, the umbrella folder holding every repo the boss owns - and
// Claude Code would start editing whatever it found in there. Every one of
// these must refuse BEFORE `claude` is ever launched.
func TestClaudeCodeRunner_EndToEnd_RefusesABadRepo(t *testing.T) {
	const maxAccount = `{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}`

	t.Run("empty", func(t *testing.T) {
		mac, _ := setupLocalMac(t, maxAccount)
		assertRepoRefused(t, mac, "", repoReasonEmpty)
	})
	t.Run("missing", func(t *testing.T) {
		mac, repo := setupLocalMac(t, maxAccount)
		assertRepoRefused(t, mac, filepath.Join(repo, "does-not-exist"), repoReasonMissing)
	})
	t.Run("file not directory", func(t *testing.T) {
		mac, repo := setupLocalMac(t, maxAccount)
		file := filepath.Join(repo, "README.md")
		if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
			t.Fatal(err)
		}
		assertRepoRefused(t, mac, file, repoReasonNotDir)
	})
	t.Run("not a git repo", func(t *testing.T) {
		mac, repo := setupLocalMac(t, maxAccount)
		plain := filepath.Join(filepath.Dir(repo), "plain")
		if err := os.MkdirAll(plain, 0o755); err != nil {
			t.Fatal(err)
		}
		assertRepoRefused(t, mac, plain, repoReasonNotGit)
	})
	t.Run("the ~/Dev umbrella", func(t *testing.T) {
		mac, _ := setupLocalMac(t, maxAccount)
		umbrella := filepath.Join(mac.home, "Dev")
		// Even a git repo AT ~/Dev is the umbrella, never a work target.
		if err := os.MkdirAll(umbrella, 0o755); err != nil {
			t.Fatal(err)
		}
		gitInit(t, umbrella)
		assertRepoRefused(t, mac, umbrella, repoReasonUmbrella)
	})
}

// assertRepoRefused runs a job against repo and proves it was rejected for
// reason, with nothing launched and a guidance line the model can act on.
func assertRepoRefused(t *testing.T, mac *localMacBridge, repo, reason string) {
	t.Helper()
	r := NewClaudeCodeRunner(nil, nil)
	_, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-repo", Task: "edit core/x.go", Repo: repo, MaxWait: 5 * time.Second,
	})
	var bad *repoRejectedError
	if !errors.As(err, &bad) {
		t.Fatalf("want repoRejectedError, got %v", err)
	}
	if bad.reason != reason {
		t.Fatalf("reason = %q, want %q (%v)", bad.reason, reason, err)
	}
	if _, statErr := os.Stat(filepath.Join(filepath.Dir(mac.home), "launched.mark")); statErr == nil {
		t.Fatal("claude must never have been launched for a bad repo")
	}
	g := bad.guidance()
	if !strings.Contains(g, "NOT LAUNCHED") || !strings.Contains(g, "~/Dev/<repo>") {
		t.Fatalf("guidance must name the fix: %s", g)
	}
	if strings.Contains(g, "write the code yourself") && !strings.Contains(g, "Do NOT write the code yourself") {
		t.Fatalf("a refusal must never steer the chat model into coding on its own plan: %s", g)
	}
}

// mcpHeldClaude is the fake that reproduces the 2026-08-29 failure exactly: it
// does real work, writes its terminal result, and then NEVER EXITS — because
// `claude -p` waits on its MCP servers, and that build was holding eight of
// them plus a headless Chrome. No `.status` file is ever written.
//
// Before the terminal-result signal, this run was invisible to the harness
// forever: 47 minutes of successful work reported to the boss as a failure.
const mcpHeldClaude = `#!/bin/bash
echo '{"type":"system","subtype":"init","session_id":"3f2a9c11-0000-4000-8000-abcdef123456"}'
echo '{"type":"assistant","message":{"content":[{"type":"tool_use","id":"toolu_a1","name":"Edit","input":{"file_path":"core/x.go","old_string":"a","new_string":"b"}}]}}'
echo '{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_a1","content":"applied","is_error":false}]}}'
echo '{"type":"result","subtype":"success","is_error":false,"duration_ms":2844766,"num_turns":170,"result":"Done. Here is the report.","modelUsage":{"claude-opus-5":{"inputTokens":1}}}'
# The MCP servers keep the process resident long after the work is over.
sleep 120
`

// Why: THE bug. A build that succeeds must be recognised as finished from its
// own terminal result, without waiting for a process exit that never comes —
// and its step must reach the boss's chat while it happens, not never.
func TestClaudeCodeRunner_EndToEnd_FinishesWhenClaudeNeverExits(t *testing.T) {
	mac, repo := setupLocalMac(t, `{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}`)
	if err := os.WriteFile(filepath.Join(mac.bin, "claude"), []byte(mcpHeldClaude), 0o755); err != nil {
		t.Fatal(err)
	}
	var (
		mu    sync.Mutex
		steps []NestedStep
		meta  = map[string]string{}
	)
	r := NewClaudeCodeRunner(nil, nil)
	r.AttachStepSink(func(_ context.Context, s NestedStep) {
		mu.Lock()
		steps = append(steps, s)
		mu.Unlock()
	})
	summary, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-mcp-held", Task: "edit core/x.go", Repo: repo,
		ParentSession: "11111111-2222-3333-4444-555555555555",
		MaxWait:       20 * time.Second, KillOnCancel: true,
		SetMeta: func(k, v string) { mu.Lock(); meta[k] = v; mu.Unlock() },
	})
	if err != nil {
		t.Fatalf("a job whose process never exits must still finish: %v", err)
	}
	if !strings.Contains(summary, "Done. Here is the report.") {
		t.Fatalf("Claude's own report must be the receipt: %q", summary)
	}
	// The resident process (and everything under it — eight MCP servers and a
	// Chrome, in the real case) has to be reaped, or every build leaks a tree.
	mac.mu.Lock()
	cmds := strings.Join(mac.cmds, "\n")
	mac.mu.Unlock()
	if !strings.Contains(cmds, "kill -TERM") {
		t.Fatalf("finishing on the result must reap the still-resident process group:\n%s", cmds)
	}
	// And the boss has to have SEEN it work.
	mu.Lock()
	defer mu.Unlock()
	var sawEdit bool
	for _, s := range steps {
		if s.Tool == "claude_code__edit" && !s.Done {
			sawEdit = true
		}
	}
	if !sawEdit {
		t.Fatalf("the nested edit must reach the chat as its own step: %+v", steps)
	}
	if meta["claude_session_id"] != "3f2a9c11-0000-4000-8000-abcdef123456" {
		t.Fatalf("the session id must be captured so the job can be resumed: %+v", meta)
	}
}

// Why: the boss pays for Opus on his Max plan. Claude Code's own default is
// Sonnet, so a run launched without a model must be pinned at the execution
// boundary, not left to whatever the Mac happens to be configured for.
func TestClaudeCodeRunner_EndToEnd_PinsOpus5WhenNoModelGiven(t *testing.T) {
	mac, repo := setupLocalMac(t, `{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}`)
	// The Mac's own Claude settings name a different model; the pin wins.
	var (
		mu   sync.Mutex
		meta = map[string]string{}
	)
	r := NewClaudeCodeRunner(nil, nil)
	if _, err := r.Run(context.Background(), ClaudeCodeJob{
		Bridge: mac, JobID: "job-pin", Task: "edit core/x.go", Repo: repo,
		MaxWait: 20 * time.Second, KillOnCancel: true,
		SetMeta: func(k, v string) { mu.Lock(); meta[k] = v; mu.Unlock() },
	}); err != nil {
		t.Fatalf("run: %v\ncommands:\n%s", err, strings.Join(mac.cmds, "\n----\n"))
	}
	if !strings.Contains(meta["preflight"], "model "+pinnedCodeModel) {
		t.Fatalf("an unspecified model must resolve to the pin on the run row: %q", meta["preflight"])
	}
	mu.Lock()
	cmds := strings.Join(mac.cmds, "\n----\n")
	mu.Unlock()
	if !strings.Contains(cmds, "export INF_MODEL="+shellQuote(pinnedCodeModel)) {
		t.Fatalf("the launch must carry the pinned model:\n%s", cmds)
	}
	if strings.Contains(cmds, "INF_MODEL=''") {
		t.Fatalf("the launch must never leave the model unset (Claude Code would pick Sonnet):\n%s", cmds)
	}
}
