package agent

import (
	"context"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/llm"
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

// maxSelfHealPerTurn bounds the reactive retries. Two extra passes give the
// agent real grit — if the first heal attempt also dead-ends, it gets one more
// genuinely-different shot before the turn is allowed to end — without turning
// every turn into a grind or a cost sink. The LoopGate (50 calls / 5 min) is
// the hard runaway backstop underneath this.
const maxSelfHealPerTurn = 2

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

// browserPuntSignal matches a reply that hands the boss a URL to open / sign in
// to himself — the exact "go browse to it" punt the boss keeps catching. He
// CANNOT open a page in the Preview pane; only the agent can (browser_open). So
// any such instruction is a failure to drive the browser, and must trigger a
// self-heal pass even when the reply otherwise reads confident (no failure
// words). Conservative: matches explicit "open/browse/navigate to ... (and sign
// in)" instructions, not a bare external link the boss would open elsewhere.
var browserPuntSignal = regexp.MustCompile(`(?i)(in the preview.{0,120}?\bopen\b|\bbrowse to\b|\bnavigate to\b|go to .{0,120}?\b(sign|log) ?in\b|open .{0,120}?\bin the (preview|browser)\b)`)

// resolvedSignal marks a reply that already says it fixed/verified the thing,
// so a success that merely MENTIONS a past failure isn't re-healed.
var resolvedSignal = regexp.MustCompile(`(?i)(fixed it|now works|works now|confirmed|verified|sorted (it )?out|\bresolved\b|got it working|up and running|all set|all done)`)

// typographicApostrophes are the non-ASCII characters a model reaches for when
// it writes "can’t". EVERY pattern in this file spells its contractions with an
// ASCII apostrophe, so without this normalisation `can'?t` simply does not
// match `can’t` — and the whole reflex is blind to the replies it exists to
// catch. Jarvis's own soul tells him to write like a refined British butler, so
// in production essentially every contraction he emits is curly.
//
// This is what silently disarmed the self-heal loop: on 2026-07-09 he ended two
// consecutive turns with "I can’t reliably break down a YouTube link" and "that
// shell can’t see /workspace" — both textbook triggers, neither detected, no
// heal pass, and the boss got a shrug instead of a transcript.
var typographicApostrophes = strings.NewReplacer(
	"’", "'", // ’ right single quotation mark (what LLMs emit)
	"‘", "'", // ‘ left single quotation mark
	"ʼ", "'", // ʼ modifier letter apostrophe
	"´", "'", // ´ acute accent
	"`", "'", // ` grave accent
	"＇", "'", // ＇ fullwidth apostrophe
)

// shouldSelfHeal reports whether the loop should inject a self-heal pass before
// completing the turn. Conservative: fires on a clear failure signal the reply
// doesn't also claim to have resolved. toolErred (a tool errored, or a gate
// refused a call, this turn) is a booster: an empty reply or a failure-flavored
// one after a tool error is a strong "didn't finish" signal.
func shouldSelfHeal(replyText string, toolErred bool) bool {
	if selfHealDisabled() {
		return false
	}
	t := typographicApostrophes.Replace(strings.TrimSpace(replyText))
	if t == "" {
		// Empty final reply with no tool calls = confused decode; a nudge often
		// recovers it, but only bother if something actually errored.
		return toolErred
	}
	// Telling the boss to open/browse to a URL himself is always a punt — he
	// can't drive the Preview browser. Fire regardless of confident phrasing.
	if browserPuntSignal.MatchString(t) {
		return true
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

While the boss is IN this conversation, a heal fixes the CALL, never the codebase: call the tool correctly, take a different route with the tools you have, or say in one plain sentence what you could not do. Do not rewrite Infinity's own source mid-conversation (it is blocked and gets filed for the nightly self-improve loop instead), and never override an instruction he just gave you — if he said discuss, discuss.

Stop WITHOUT fixing it only if you truly need something ONLY the boss can give: a credential or login he alone holds, a deploy he has chosen to gate, or a real decision. If so, say exactly what you need and why, with the fix ready to go. Do not just restate the failure.`

// ── self-heal source guard ────────────────────────────────────────────────
//
// 2026-08-26: the boss attached a book and said "don't build, let's discuss".
// A plan_update usage error tripped the self-heal reflex, whose directive says
// "if your own code is the bug, patch it", so the model spawned code_agent to
// rewrite Infinity's plan tooling: nine minutes blocked, his repo edited under
// him, his "stop" unread. A heal in a LIVE conversation must fix the call, not
// the codebase. This is the mechanic (Rule #1b): the loop refuses source
// mutations during an interactive heal pass and FILES the intended change as
// a code proposal so the nightly self-improve loop still gets the signal.

// sourceMutationTools always count as changing Infinity's (or a repo's) code.
var sourceMutationTools = map[string]bool{
	"code_agent":                true,
	"claude_code__Edit":         true,
	"claude_code__Write":        true,
	"claude_code__NotebookEdit": true,
	"claude_code__Bash":         true,
	"git_commit":                true,
	"git_push":                  true,
	"background_build":          true,
}

// pathScopedMutationTools change code only when pointed at the Infinity tree.
var pathScopedMutationTools = map[string]bool{
	"fs_save":  true,
	"fs_edit":  true,
	"bash_run": true,
}

// isSourceMutation reports whether a tool call would change source code:
// always for the coding tools, and for the generic fs/bash tools when any
// string argument points into an Infinity checkout.
func isSourceMutation(tc llm.ToolCall) bool {
	if sourceMutationTools[tc.Name] {
		return true
	}
	if !pathScopedMutationTools[tc.Name] {
		return false
	}
	for _, v := range tc.Input {
		if str, ok := v.(string); ok && strings.Contains(strings.ToLower(str), "infinity") {
			return true
		}
	}
	return false
}

// CodeProposalFiler records a source change the self-heal reflex wanted to
// make but was not allowed to make live. Implemented in serve against
// mem_code_proposals (the same table Voyager's source extractor feeds), so
// the nightly self-improve loop and the Code proposals tab pick it up.
type CodeProposalFiler interface {
	FileSelfHealProposal(ctx context.Context, sessionID, toolName, task string) (string, error)
}

// CodeProposalFilerFunc adapts a func to CodeProposalFiler.
type CodeProposalFilerFunc func(ctx context.Context, sessionID, toolName, task string) (string, error)

// FileSelfHealProposal implements CodeProposalFiler.
func (f CodeProposalFilerFunc) FileSelfHealProposal(ctx context.Context, sessionID, toolName, task string) (string, error) {
	return f(ctx, sessionID, toolName, task)
}

// AttachCodeProposalFiler wires the filer (nil-safe, hot-swappable).
func (l *Loop) AttachCodeProposalFiler(f CodeProposalFiler) {
	if l == nil {
		return
	}
	l.planMu.Lock()
	defer l.planMu.Unlock()
	l.proposalFiler = f
}

func (l *Loop) proposalFilerFn() CodeProposalFiler {
	if l == nil {
		return nil
	}
	l.planMu.RLock()
	defer l.planMu.RUnlock()
	return l.proposalFiler
}

// refuseSelfHealSourceChange files the intended change (best-effort) and
// returns the tool result the model sees instead of running the call.
func (l *Loop) refuseSelfHealSourceChange(ctx context.Context, sessionID string, tc llm.ToolCall) string {
	task := describeToolIntent(tc)
	filed := ""
	if f := l.proposalFilerFn(); f != nil {
		fctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if id, err := f.FileSelfHealProposal(fctx, sessionID, tc.Name, task); err != nil {
			log.Printf("selfheal: file code proposal (%s): %v", tc.Name, err)
			filed = " I could not file it as a code proposal (" + err.Error() + "), so say in one sentence what you believe is broken."
		} else if id != "" {
			filed = " It has been FILED as code proposal " + id + " for the nightly self-improve loop and the Code proposals tab; do not try to patch it now."
		}
	}
	return "BLOCKED (self-heal guard): " + tc.Name + " would change Infinity's own source, and you are in a self-heal pass of a LIVE conversation. " +
		"A heal fixes the CALL, never the codebase, while the boss is talking to you: call the tool correctly, take a different route with the tools you have, " +
		"or tell him in one plain sentence what you could not do." + filed + " Now answer the boss, and honour whatever he last asked for."
}

// describeToolIntent renders the blocked call as a plain brief for the proposal.
func describeToolIntent(tc llm.ToolCall) string {
	if task, ok := tc.Input["task"].(string); ok && strings.TrimSpace(task) != "" {
		return strings.TrimSpace(task)
	}
	var b strings.Builder
	b.WriteString(tc.Name)
	for k, v := range tc.Input {
		s, ok := v.(string)
		if !ok {
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if len(s) > 400 {
			s = s[:400] + "…"
		}
		fmt.Fprintf(&b, "\n%s: %s", k, s)
	}
	return b.String()
}
