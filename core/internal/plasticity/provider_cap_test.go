package plasticity

import (
	"strings"
	"testing"
)

func TestRenderReflexBlockDropsLowestRankedUntilItFits(t *testing.T) {
	mk := func(label, text string) promptExample {
		return promptExample{Label: label, TaskKind: "general", Input: strings.Repeat(text, 12), Output: strings.Repeat(text, 12), Score: 0.9}
	}
	avoid := []promptExample{mk("rejected", "bad move one "), mk("rejected", "bad move two "), mk("rejected", "bad move three ")}
	apply := []promptExample{mk("accepted", "good move one "), mk("accepted", "good move two ")}
	full := renderReflexBlock(avoid, apply, 0)
	capped := renderReflexBlock(avoid, apply, len(full)/2)
	if len(capped) > len(full)/2 {
		t.Fatalf("cap breached: %d > %d", len(capped), len(full)/2)
	}
	// The best-ranked lesson of each bucket survives first.
	if !strings.Contains(capped, "bad move one") || !strings.Contains(capped, "good move one") {
		t.Fatalf("top-ranked lessons should survive:\n%s", capped)
	}
	if !strings.HasSuffix(capped, "</gym_reflex>") {
		t.Fatalf("block must stay well-formed:\n%s", capped)
	}
}

func TestRenderReflexBlockUnchangedUnderCap(t *testing.T) {
	avoid := []promptExample{{Label: "rejected", Input: "x", Output: "y", Score: 0.5}}
	if got := renderReflexBlock(avoid, nil, 100000); !strings.Contains(got, "AVOID:") {
		t.Fatalf("expected the avoid bucket:\n%s", got)
	}
}
