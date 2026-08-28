package tools

import (
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Why: 2026-08-26. Every `claude -p` on the boss's Mac returned
// {"is_error":true,"api_error_status":429,"result":"You're out of extra usage
// · resets 6:50pm"} and code_agent booked each one as ok with the summary
// `{\` because the bridge reply was read with a first-quote scanner. The
// boss's Claude plan was spent for an hour and nothing said so.

const claudeOutOfUsage = `{"type":"result","subtype":"success","is_error":true,"api_error_status":429,"duration_ms":517,` +
	`"num_turns":1,"result":"You're out of extra usage · resets 6:50pm (America/Chicago)","stop_reason":"stop_sequence"}`

func TestBridgeBashOutput_DecodesEscapedQuotes(t *testing.T) {
	// The bridge wraps stdout in JSON, so claude's own JSON arrives escaped.
	body := []byte(`{"exit_code":1,"output":"===OUT===\n` +
		`{\"type\":\"result\",\"is_error\":true,\"result\":\"You're out of extra usage\"}\n===ERR===\n","truncated":false}`)
	out, code := bridgeBashOutput(body)
	if code != 1 {
		t.Fatalf("exit_code must decode, got %d", code)
	}
	want := "===OUT===\n{\"type\":\"result\",\"is_error\":true,\"result\":\"You're out of extra usage\"}\n===ERR===\n"
	if out != want {
		t.Fatalf("output must be the full decoded string, got %q", out)
	}
	// The exact bug, pinned: the old scanner stopped at the first escaped quote.
	if old := extractJSONFieldFast(string(body), "output"); strings.Contains(old, "is_error") {
		t.Fatalf("extractJSONFieldFast unexpectedly handles escapes now (%q); this test documents why code_agent stopped using it", old)
	}
}

func TestParseClaudeResult_SurfacesErrors(t *testing.T) {
	res := parseClaudeResult(claudeOutOfUsage)
	if !res.parsed || !res.IsError || res.APIErrorStatus != 429 {
		t.Fatalf("an is_error result must parse as an error with its upstream status: %+v", res)
	}
	if !looksLikeUsageCap(res.Result) {
		t.Fatalf("Claude's out-of-usage copy must be recognised: %q", res.Result)
	}
	ok := parseClaudeResult(`{"type":"result","subtype":"success","is_error":false,"api_error_status":null,"result":"Done: edited 3 files"}`)
	if !ok.parsed || ok.IsError || ok.Result != "Done: edited 3 files" {
		t.Fatalf("a clean result must parse as success: %+v", ok)
	}
	if parseClaudeResult("not json at all").parsed {
		t.Fatal("garbage must not claim to be parsed")
	}
}

// Why: 2026-08-28. The runs now use --output-format stream-json so the boss
// can see what Claude Code is doing mid-run. The final result is the LAST
// line of that stream; reading the whole blob as one object (the old json
// path) fails on it and would have reported every run as "no output".
func TestParseClaudeStreamResult_TakesTheLastResultLine(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"system","subtype":"init","session_id":"s1","model":"claude-opus-5"}`,
		`{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"t1","name":"Edit","input":{"file_path":"core/cmd/infinity/migrate.go","old_string":"a","new_string":"b"}}]}}`,
		`{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"t1","content":"ok"}]}}`,
		`{"type":"result","subtype":"success","is_error":false,"api_error_status":null,"result":"Done. Build is clean.","modelUsage":{"claude-opus-5":{"inputTokens":10}}}`,
		``,
	}, "\n")
	res := parseClaudeStreamResult(stream)
	if !res.parsed || res.IsError || res.Result != "Done. Build is clean." {
		t.Fatalf("the result line must be found and decoded: %+v", res)
	}
	if _, ok := res.ModelUsage["claude-opus-5"]; !ok {
		t.Fatalf("modelUsage must carry the model that ran: %+v", res.ModelUsage)
	}
	// The plan-cap reply still reads as an error through the stream path.
	capped := parseClaudeStreamResult(`{"type":"system","subtype":"init"}` + "\n" + claudeOutOfUsage + "\n")
	if !capped.parsed || !capped.IsError || capped.APIErrorStatus != 429 {
		t.Fatalf("an out-of-usage result must still surface: %+v", capped)
	}
	// Legacy single-object output keeps working.
	legacy := parseClaudeStreamResult(`{"type":"result","subtype":"success","is_error":false,"result":"old shape"}`)
	if !legacy.parsed || legacy.Result != "old shape" {
		t.Fatalf("legacy json output must still parse: %+v", legacy)
	}
}

// Why: the boss watched "code_agent" spin for 480s with no idea what it was
// doing (2026-08-28 01:49) and killed it by messaging. Each poll now names
// Claude's current tool call from the stream tail.
func TestClaudeStreamActivity_NamesTheCurrentTool(t *testing.T) {
	tail := `rtial line that got cut}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"text","text":"Let me look."},{"type":"tool_use","name":"Bash","input":{"command":"cd core && go test ./cmd/...","description":"run tests"}}]}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","content":"ok"}]}}` + "\n" +
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Edit","input":{"file_path":"core/cmd/infinity/migrate.go","old_string":"x","new_string":"y"}}]}}` + "\n"
	action, detail, found := claudeStreamActivity(tail)
	if !found || action != "Edit" || detail != "core/cmd/infinity/migrate.go" {
		t.Fatalf("newest tool call must win: found=%v action=%q detail=%q", found, action, detail)
	}
	if got := claudeProgressLabel(action, detail, 200*time.Second); got != "Claude Code · Edit core/cmd/infinity/migrate.go · 3m20s" {
		t.Fatalf("progress label = %q", got)
	}
	action, detail, found = claudeStreamActivity(`{"type":"assistant","message":{"content":[{"type":"text","text":"Writing the summary now"}]}}`)
	if !found || action != "writing" || !strings.HasPrefix(detail, "Writing the summary") {
		t.Fatalf("text-only turn must read as writing: %v %q %q", found, action, detail)
	}
	if _, _, found := claudeStreamActivity("garbage\n{\"type\":\"user\"}\n"); found {
		t.Fatal("no assistant event must report nothing")
	}
	if got := claudeProgressLabel("", "", 15*time.Second); got != "Claude Code · working · 15s" {
		t.Fatalf("idle label = %q", got)
	}
}

// Why: the boss's contract (2026-08-28): on the Mac bridge, coding runs on
// his Claude MAX SUBSCRIPTION via `claude -p`, and he wants that PROVEN, not
// assumed. The probe reads the Mac's own sign-in; this is its exact shape.
func TestParseClaudeAuth_RecognisesTheMaxSubscription(t *testing.T) {
	probe := "APIKEY:absent\n" +
		`AUTH:{"accountUuid":"u-1","emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription","hasExtraUsageEnabled":true,"seatTier":null}` + "\n" +
		`SETTINGS:{"model":"claude-fable-5[1m]","effortLevel":"xhigh","apiKeyHelper":null,"env":{"DISABLE_AUTOUPDATER":"1"}}` + "\n"
	a := parseClaudeAuth(probe)
	if !a.Subscription() {
		t.Fatalf("a claude_max stripe_subscription sign-in is the subscription: %+v", a)
	}
	if got := a.Label(); got != "Max subscription · kai@example.com" {
		t.Fatalf("proof label = %q", got)
	}
	if a.apiKeyInEnv || a.apiKeyHelper {
		t.Fatalf("no key present: %+v", a)
	}
	if m, e := macClaudeDefaults(probe); m != "claude-fable-5[1m]" || e != "xhigh" {
		t.Fatalf("Mac defaults must be read from the same probe: %q %q", m, e)
	}
}

func TestParseClaudeAuth_RefusesAnythingButTheSubscription(t *testing.T) {
	notSignedIn := parseClaudeAuth("APIKEY:absent\nAUTH:{}\nSETTINGS:{}\n")
	if notSignedIn.Subscription() || notSignedIn.Label() != "not signed in" {
		t.Fatalf("an empty oauthAccount is not signed in: %+v", notSignedIn)
	}
	helper := parseClaudeAuth("APIKEY:present\n" +
		`AUTH:{"emailAddress":"kai@example.com","organizationType":"claude_max","billingType":"stripe_subscription"}` + "\n" +
		`SETTINGS:{"apiKeyHelper":"/usr/local/bin/key.sh"}` + "\n")
	if helper.Subscription() {
		t.Fatal("an apiKeyHelper pre-empts the sign-in inside Claude Code; that is not the subscription")
	}
	if !helper.apiKeyInEnv {
		t.Fatal("APIKEY:present must be recorded")
	}
	if !strings.Contains((&notSubscriptionError{auth: helper}).guidance(), "apiKeyHelper") {
		t.Fatal("the refusal must tell the boss what to remove")
	}
	if strings.Contains((&notSubscriptionError{auth: helper}).guidance(), "write the code yourself with") {
		t.Fatal("a refusal must never steer the chat model into coding on its own plan")
	}
	other := parseClaudeAuth(`AUTH:{"organizationType":"api_org","billingType":""}`)
	if other.Subscription() || !strings.Contains(other.Label(), "not a subscription") {
		t.Fatalf("a non-subscription org must be refused and named: %+v %q", other, other.Label())
	}
	if pro := parseClaudeAuth(`AUTH:{"organizationType":"claude_pro","billingType":"stripe_subscription"}`); !pro.Subscription() || pro.Label() != "Pro subscription" {
		t.Fatalf("Pro is a subscription too: %q", pro.Label())
	}
}

// Why: the launch must guarantee the subscription answers - an API key in
// the bridge's shell would silently bill the API instead - and must stream
// so progress is readable. Both are mechanics in the script, not prose.
func TestClaudeLaunchScript_GuardsTheSubscriptionAndStreams(t *testing.T) {
	f := newClaudeJobFiles("job-1")
	script := claudeLaunchScript(f, "fix the thing", "claude-opus-5[1m]", "high")
	unset := strings.Index(script, "unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN")
	launch := strings.Index(script, "nohup bash -c")
	if unset < 0 || launch < 0 || unset > launch {
		t.Fatalf("the API key must be stripped BEFORE the detached launch:\n%s", script)
	}
	if !strings.Contains(script, "--output-format stream-json --verbose") {
		t.Fatalf("the run must stream so each poll can name its activity:\n%s", script)
	}
	if !strings.Contains(script, "--permission-mode bypassPermissions --settings "+f.settings) {
		t.Fatalf("the delete-gate settings must still be attached:\n%s", script)
	}
	if strings.Contains(claudeAuthProbeCmd, "echo $ANTHROPIC_API_KEY") || strings.Contains(claudeAuthProbeCmd, "set -x") {
		t.Fatal("the probe must only ever print presence, never the key")
	}
	if got := exitCodeFromDone("DONE:1\n===TAIL==="); got != 1 {
		t.Fatalf("exit code = %d", got)
	}
}

// Why: with the plan spent, the old guidance told the model to "make it
// yourself with claude_code__Edit (that bills the chat model instead)" -
// i.e. move the coding onto the boss's ChatGPT plan, the exact leak he
// keeps hitting. The hold now tells the boss and waits for the reset.
func TestClaudeCodeHeldGuidance_NamesTheResetAndForbidsRetry(t *testing.T) {
	llm.ResetQuotaLedgerForTest()
	t.Cleanup(llm.ResetQuotaLedgerForTest)
	until := time.Now().Add(25 * time.Minute)
	msg := claudeCodeHeldGuidance(until, "You're out of extra usage · resets 6:50pm (America/Chicago)")
	for _, want := range []string{"HOLD", "out of usage", "until about", "Do NOT call code_agent or background_build", "Do NOT write the code yourself", "Only if he explicitly"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("guidance missing %q:\n%s", want, msg)
		}
	}
	if strings.Contains(msg, "that bills the chat model instead") {
		t.Fatalf("the hold must not invite coding on the ChatGPT plan:\n%s", msg)
	}
	// No reset known: still a hold, still honest about it.
	msg = claudeCodeHeldGuidance(time.Time{}, "")
	if !strings.Contains(msg, "for now") || !strings.Contains(msg, "usage allowance is spent") {
		t.Fatalf("guidance without a reset must say so plainly:\n%s", msg)
	}
}
