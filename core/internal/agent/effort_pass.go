package agent

import (
	"os"
	"strings"
)

// Lever 3 of steal C: a single bounded adversarial-verify pass on the hardest
// turns. The model re-reads its own answer once and red-teams it before the turn
// ends. This file holds ONLY the deterministic MECHANIC (when to fire, how it's
// bounded) — Rule #1b. The judgment prose ("red-team your answer…") is DATA:
// seeded into infinity_meta and injected via Loop.SetVerifyDirective, so it's
// versioned and improvable by Voyager/GEPA, never frozen in a Go const.

// maxVerifyPerTurn bounds the verify pass to one extra LLM iteration per turn.
// It never stacks on a self-heal pass (see the loop gate), so the worst-case
// extra iterations per turn stay at maxSelfHealPerTurn + maxVerifyPerTurn.
const maxVerifyPerTurn = 1

// verifyDisabled is the INFINITY_EFFORT_VERIFY=off opt-out. Default on, but the
// pass still only fires on high/xhigh turns with a configured directive, so
// "default on" costs nothing on ordinary turns.
func verifyDisabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("INFINITY_EFFORT_VERIFY")), "off")
}

// shouldVerify is the deterministic content gate: only red-team a SUBSTANTIVE
// answer — one long enough to harbor a real flaw — and skip bare acknowledgements
// or hand-offs. Code, not an LLM (Rule #5).
func shouldVerify(text string) bool {
	t := strings.TrimSpace(text)
	if len([]rune(t)) < 240 {
		return false // too short to hide a flaw worth a whole extra pass
	}
	lower := strings.ToLower(t)
	for _, ack := range []string{"done.", "all set", "let me know", "anything else", "no changes"} {
		if strings.HasPrefix(lower, ack) {
			return false
		}
	}
	return true
}
