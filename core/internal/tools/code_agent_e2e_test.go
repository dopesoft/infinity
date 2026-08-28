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
		Heartbeat: func(label, _, _ string) { mu.Lock(); labels = append(labels, label); mu.Unlock() },
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
