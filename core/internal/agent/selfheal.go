package agent

import (
	"os"
	"regexp"
	"strings"
)

// Reactive self-heal — the STRUCTURAL backstop behind soul.md's "you are the
// engineer of yourself" directive and the self-heal procedural memory.
//
// Those tell Jarvis to fix his own failures and verify his work, but the
// runtime brain (gpt-5.x) routinely drops standing prose. This is the part the
// model can't forget: when a turn is about to END reporting an unresolved
// failure, the loop itself injects a self-heal directive and runs ONE more pass
// so the agent investigates → fixes → verifies with its own tools instead of
// punting to the boss. Capped per turn so a genuinely-stuck turn still ends and
// it can never loop. Off via INFINITY_SELF_HEAL=off.

// maxSelfHealPerTurn bounds the reactive retries. One extra pass catches the
// "reported a failure it could have fixed" case without turning every turn into
// a grind or a cost sink.
const maxSelfHealPerTurn = 1

// selfHealDisabled lets the boss kill the reflex (INFINITY_SELF_HEAL=off).
func selfHealDisabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("INFINITY_SELF_HEAL"))) {
	case "off", "false", "0", "no":
		return true
	}
	return false
}

// failureSignal matches a final reply that hands back an UNRESOLVED problem —
// the "it didn't work / I couldn't" shapes the boss is tired of seeing.
var failureSignal = regexp.MustCompile(`(?i)\b(could ?n'?t|can ?not|can'?t|was ?n'?t able|were ?n'?t able|unable to|failed to|\bfail(s|ed)?\b|didn'?t work|does ?n'?t work|not working|isn'?t working|ran into (an? )?(error|issue|problem)|something went wrong|went wrong|hit (a|an) (wall|error|snag)|blocked( on| by)|\bstuck\b|no luck|gave up|not able to|couldn'?t (get|complete|finish|find|reach|load|connect|sign)|\berror(ed)?\b|broken|timed? ?out)`)

// resolvedSignal marks a reply that already says it fixed/verified the thing,
// so a success that merely MENTIONS a past failure isn't re-healed.
var resolvedSignal = regexp.MustCompile(`(?i)(fixed it|now works|works now|confirmed|verified|sorted (it )?out|\bresolved\b|got it working|up and running|all set|all done)`)

// shouldSelfHeal reports whether the loop should inject a self-heal pass before
// completing the turn. Conservative: fires on a clear failure signal the reply
// doesn't also claim to have resolved. toolErred (a tool errored this turn) is
// a booster: an empty reply or a failure-flavored one after a tool error is a
// strong "didn't finish" signal.
func shouldSelfHeal(replyText string, toolErred bool) bool {
	if selfHealDisabled() {
		return false
	}
	t := strings.TrimSpace(replyText)
	if t == "" {
		// Empty final reply with no tool calls = confused decode; a nudge often
		// recovers it, but only bother if something actually errored.
		return toolErred
	}
	if resolvedSignal.MatchString(t) && !toolErred {
		return false // already says it fixed/verified it
	}
	return failureSignal.MatchString(t)
}

// selfHealDirective is injected as a user-role message so the model treats it
// as a fresh, top-of-mind instruction for this turn.
const selfHealDirective = `STOP — do not hand this back yet. You just ended a turn reporting a problem you did not resolve. You are the engineer of yourself: you can read your own logs (traces_search / trace_inspect), grep and read your own source (~/Dev/infinity: Go in core/, Next.js in studio/), edit / build / test / deploy your own code, drive your cloud browser, and run anything on your cloud workspace.

So actually fix it now:
1. Diagnose the real cause from evidence, not a guess.
2. Implement a fix, or try a genuinely different approach. If a sign-in is blocking you, open it in your own browser. If your own tooling or code is the bug, patch it.
3. VERIFY the fix worked — re-run it, check the output / status / row / page — and only then report.

Stop WITHOUT fixing it only if you truly need something ONLY the boss can give: a credential or login he alone holds, a deploy he has chosen to gate, or a real decision. If so, say exactly what you need and why, with the fix ready to go. Do not just restate the failure.`
