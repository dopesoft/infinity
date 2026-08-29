package tools

import (
	"strings"
	"testing"
)

// The verbatim command the boss watched spin for over fifteen minutes. If this
// ever runs again, this test failed to hold.
const theBabysitter = `kill 72481 72484 2>/dev/null || true; while [ ! -f /tmp/inf-code/0a4074b6-655c-4837-b693-ca983d92bb47.status ]; do sleep 2; done; printf 'exit='; tr -d '\n' < /tmp/inf-code/0a4074b6-655c-4837-b693-ca983d92bb47.status`

func TestGuardJobBabysitting_RefusesTheFifteenMinuteSpinner(t *testing.T) {
	redirect, blocked := guardJobBabysitting(theBabysitter)
	if !blocked {
		t.Fatal("the command that produced the 15-minute spinner must be refused")
	}
	// The refusal has to leave the model with a better move, not just a no.
	if !strings.Contains(redirect, "reports") {
		t.Fatalf("the redirect must say the job reports back on its own:\n%s", redirect)
	}
}

func TestGuardJobBabysitting_BlocksEachShapeIndependently(t *testing.T) {
	cases := map[string]string{
		"reads the job dir":      `cat /tmp/inf-code/abc.out`,
		"waits for the job file": `while [ ! -f /tmp/inf-code/abc.status ]; do sleep 2; done`,
		"bare wait loop":         `until curl -sf localhost:3000; do sleep 5; done`,
		"while-sleep poll":       `while true; do sleep 10; echo still going; done`,
		"kills a looked-up pid":  `kill 72481`,
		"kills several pids":     `kill -9 72481 72484`,
		"pkill by pid":           `pkill -TERM 41233`,
	}
	for name, cmd := range cases {
		if _, blocked := guardJobBabysitting(cmd); !blocked {
			t.Errorf("%s: must be refused — %q", name, cmd)
		}
	}
}

// A guard that blocks ordinary work is worse than no guard: the boss's coding
// stops and he has no idea why. These must all run untouched.
func TestGuardJobBabysitting_LeavesRealWorkAlone(t *testing.T) {
	fine := []string{
		`go build ./... && go vet ./...`,
		`go test -count=1 ./...`,
		`cd ~/Dev/infinity && git status --porcelain`,
		`pnpm install && pnpm build`,
		// Named-process cleanup is legitimate: it targets a program, not a pid
		// the agent scraped out of `ps`.
		`pkill -f "next dev"`,
		`pkill -f my-dev-server`,
		// A bounded sleep that is not a loop is fine (a deliberate pause).
		`sleep 2 && curl -sf localhost:3000/health`,
		// "while" inside a string / a filename must not trip the loop matcher
		// on its own — it needs an actual sleep to be a waiter.
		`grep -rn "while" core/internal | head`,
		`rg "sleep" --files-with-matches`,
		// The word "kill" in a path or a flag is not a signal.
		`cat docs/killswitch.md`,
	}
	for _, cmd := range fine {
		if redirect, blocked := guardJobBabysitting(cmd); blocked {
			t.Errorf("ordinary work was refused: %q\n%s", cmd, redirect)
		}
	}
	if _, blocked := guardJobBabysitting(""); blocked {
		t.Error("an empty command is not babysitting")
	}
}
