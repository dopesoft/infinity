package tools

import "testing"

// THE VERBATIM STRING. This is what Claude Code printed on the boss's Mac on
// 2026-09-01, copied out of the core logs, and it is the whole reason this
// test exists: looksLikeUsageCap did not match it.
//
// "hit your limit" is not a substring of "hit your session limit", and "usage
// limit" never appears. So a spent plan read as an ordinary error: the quota
// ledger stayed unmarked, code_agent relaunched `claude -p` on a plan that
// could not run it, and the continuation poller woke Jarvis about it once a
// minute. Eight coding jobs died in about a minute each that evening, the
// pursuit he asked for was never built, and nothing on his screen ever said
// why. His words: "he obviously did things ... but it's not fucking visible".
//
// A detector is only worth what it matches in production, so the production
// text is pinned here rather than a paraphrase of it.
const bossSessionLimitCopy = "Claude stopped without finishing: You've hit your session limit · resets 8:20pm (America/Chicago)"

func TestLooksLikeUsageCap_MatchesWhatClaudeActuallyPrints(t *testing.T) {
	if !looksLikeUsageCap(bossSessionLimitCopy) {
		t.Fatalf("the message that cost the boss an evening must read as a spent plan:\n  %s", bossSessionLimitCopy)
	}
}

// Why: the cap is worded several ways depending on which limit was hit and
// which plan he is on, and every one of them means the same thing - do not
// launch, tell him when it comes back. Matching only the phrasing we happened
// to see first is what broke this.
func TestLooksLikeUsageCap_CoversTheOtherWordings(t *testing.T) {
	spent := []string{
		"You've hit your session limit · resets 8:20pm (America/Chicago)",
		"You've hit your usage limit. Upgrade to continue.",
		"Claude AI usage limit reached|1787800427",
		"You are out of extra usage credits",
		"You're out of usage for now",
		"5-hour limit reached ∙ resets 3am",
		"rate limit exceeded",
	}
	for _, m := range spent {
		if !looksLikeUsageCap(m) {
			t.Errorf("must read as a spent plan: %q", m)
		}
	}
}

// Why: the other half of the contract. A hold stops all coding work until the
// reset, so a false positive is expensive - an ordinary compile error or a
// failed test must never convince Infinity that his subscription is gone.
func TestLooksLikeUsageCap_DoesNotFireOnOrdinaryFailures(t *testing.T) {
	fine := []string{
		"go build failed: undefined: fooBar",
		"the repo has uncommitted changes",
		"connection reset by peer",
		"I hit a snag reading the file",
		"could not find the migration",
		"",
	}
	for _, m := range fine {
		if looksLikeUsageCap(m) {
			t.Errorf("an ordinary failure must NOT hold every coding job: %q", m)
		}
	}
}
