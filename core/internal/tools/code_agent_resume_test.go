package tools

import (
	"strings"
	"testing"
)

// Why these tests exist.
//
// Every coding run has always written Claude's own session id onto its run row
// - and that id was UNUSABLE. The raw-shell guard blocks the model from running
// `claude --resume` itself (correctly: that path leaves the subscription proof
// and the delete gate behind), and the tool had no parameter for it. So a job
// that got interrupted could only ever be picked back up by starting COLD and
// re-deriving work that was already on disk, which is how a 14-minute build
// turns into two 14-minute builds.
//
// These pin the sanctioned way in: the tool launches the resume itself, keeps
// every guarantee of a fresh launch, and refuses an id that isn't one rather
// than reporting a bad argument as a broken Mac.

func TestClaudeLaunchScript_ResumesTheSessionWithoutLosingAnyGuarantee(t *testing.T) {
	f := newClaudeJobFiles("job-resume")
	const sess = "f4fcf5d1-dffc-407e-bf64-9330f8b4b329"
	script := claudeLaunchScript(f, "finish the last two files", "claude-opus-5[1m]", "high", sess)

	if !strings.Contains(script, "export INF_RESUME='"+sess+"'") {
		t.Fatalf("the session id must reach the launch as an env var:\n%s", script)
	}
	if !strings.Contains(script, `${INF_RESUME:+--resume "$INF_RESUME"}`) {
		t.Fatalf("the launch must pass --resume when INF_RESUME is set:\n%s", script)
	}
	// --fork-session would mint a NEW session id, so meta.claude_session_id
	// would stop being the one handle to the chain and the next continuation
	// would have nothing to resume.
	if strings.Contains(script, "--fork-session") {
		t.Fatalf("a resumed run must keep the original session id:\n%s", script)
	}
	// A resume is still a real launch: the same subscription guard, the same
	// delete gate, the same streaming. Losing one of these on the resume path
	// would mean the second pass of a job is less safe than the first.
	unset := strings.Index(script, "unset ANTHROPIC_API_KEY ANTHROPIC_AUTH_TOKEN")
	launch := strings.Index(script, "nohup bash -c")
	if unset < 0 || launch < 0 || unset > launch {
		t.Fatalf("the API key must still be stripped BEFORE the launch:\n%s", script)
	}
	if !strings.Contains(script, "--permission-mode bypassPermissions --settings "+f.settings) {
		t.Fatalf("the delete gate must still be attached on a resume:\n%s", script)
	}
	if !strings.Contains(script, "--output-format stream-json --verbose") {
		t.Fatalf("a resumed run must still stream so polls can name its activity:\n%s", script)
	}
}

// A cold start must stay cold: an empty resume id leaves the flag out of the
// command entirely rather than passing `--resume ""`, which claude rejects.
func TestClaudeLaunchScript_OmitsResumeWhenThereIsNone(t *testing.T) {
	script := claudeLaunchScript(newClaudeJobFiles("job-cold"), "build it", "", "", "")
	if !strings.Contains(script, "export INF_RESUME=''") {
		t.Fatalf("INF_RESUME must be exported empty, so the ${:+} expansion drops the flag:\n%s", script)
	}
	if strings.Contains(script, "--resume ''") || strings.Contains(script, `--resume ""`) {
		t.Fatalf("an empty resume must produce NO --resume flag at all:\n%s", script)
	}
}

// Why: a bad id makes `claude --resume` die seconds in, and the run would
// report a LAUNCH failure - which reads as "the Mac dropped out" and sends the
// work to the cloud. It's a bad argument; say so.
func TestIsClaudeSessionID(t *testing.T) {
	good := []string{
		"f4fcf5d1-dffc-407e-bf64-9330f8b4b329",
		"F4FCF5D1-DFFC-407E-BF64-9330F8B4B329",
	}
	for _, s := range good {
		if !isClaudeSessionID(s) {
			t.Fatalf("%q is a session id", s)
		}
	}
	bad := []string{
		"",
		"session-2",
		"f4fcf5d1dffc407ebf649330f8b4b329",         // no dashes
		"f4fcf5d1-dffc-407e-bf64-9330f8b4b32",      // short
		"f4fcf5d1-dffc-407e-bf64-9330f8b4b3299",    // long
		"g4fcf5d1-dffc-407e-bf64-9330f8b4b329",     // non-hex
		"f4fcf5d1_dffc_407e_bf64_9330f8b4b329",     // wrong separator
		"; rm -rf ~/Dev # -bf64-9330f8b4b329-xxxx", // shell metacharacters
	}
	for _, s := range bad {
		if isClaudeSessionID(s) {
			t.Fatalf("%q is not a session id", s)
		}
	}
}
