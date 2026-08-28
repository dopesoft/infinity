package agent

import (
	"regexp"
	"strings"
)

// Stop intent (Rule #1b mechanic behind "talking must never kill work").
//
// 2026-08-28: a coding job died every time the boss typed ANYTHING while it
// ran. executeInterruptible fired on the ARRIVAL of a steer and never read
// its text, so "how's it going?" and "stop building" were the same order, and
// a 14-minute build was thrown away by a question about it.
//
// This is the deterministic reader that tells those apart. Keyword matching,
// never an LLM call: a job's life must not depend on a classifier round-trip
// that can time out, cost money, or be dropped by a model that "forgot" the
// instruction. Two tiers, biased hard toward NOT stopping:
//
//   - Tier A: unambiguous phrases anywhere in the message ("stop building",
//     "don't build", "cancel the job", "nevermind").
//   - Tier B: the message IS the command - every word is a stop verb, a
//     filler, or a word for the work itself ("stop", "ok stop please",
//     "cancel that"). "cancel the 3pm meeting" is NOT a stop, because
//     "3pm"/"meeting" are neither.
//
// The asymmetry is deliberate. A missed stop costs the boss one press of the
// Stop button. A false stop throws away real work he was waiting on.

// stopNegated is a stop VERB under a negation - "don't stop", "no need to
// cancel". Checked before everything else, so "don't stop building" can never
// read as "stop building".
var stopNegated = regexp.MustCompile(`\b(do ?n'?t|dont|do not|never|no need to) (stop|cancel|abort|halt|quit|kill)\b`)

// stopKeepGoing names the messages that mean "carry on". Checked AFTER the
// stop phrases, so "don't continue" still reads as a stop.
var stopKeepGoing = regexp.MustCompile(`\b(` +
	`keep (going|building|working|at it)|carry on|continue|no rush|take your time|finish (it|up)` +
	`)\b`)

// stopPhrases are the unambiguous stop orders: a stop verb with the work as
// its object, or a self-standing "call it off".
var stopPhrases = regexp.MustCompile(`\b(` +
	`stop (the |that |this )?(build|building|coding|code|job|run|running|task|work|working|it|that|this|everything|all)|` +
	`stop (now|immediately|please)|please stop|just stop|` +
	`(do ?n'?t|dont|do not|not to|no more) (build|building|code|coding|start|continue|proceed)|` +
	`cancel (the |that |this )?(build|building|job|run|task|coding|work|everything|it|that|this)|` +
	`kill (it|that|the (build|job|run|task|process))|` +
	`abort|halt|` +
	`never ?mind|forget it|call it off|stand down|belay that|` +
	`scrap (it|that|the build)|abandon (it|that|the build)|back out of (it|that)` +
	`)\b`)

// stopVerbs are the bare commands for Tier B.
var stopVerbs = map[string]bool{
	"stop": true, "cancel": true, "halt": true, "abort": true,
	"quit": true, "nevermind": true, "stopit": true,
}

// stopFiller is every OTHER word a bare stop order is allowed to contain:
// politeness, address, urgency, and the words for the work itself. A word
// outside this set means the message is about something else, so it is not a
// stop order (this is what keeps "cancel the 3pm meeting" alive).
var stopFiller = map[string]bool{
	"ok": true, "okay": true, "k": true, "please": true, "pls": true,
	"hey": true, "yo": true, "jarvis": true, "wait": true, "just": true,
	"actually": true, "no": true, "now": true, "right": true, "immediately": true,
	"everything": true, "it": true, "that": true, "this": true, "all": true,
	"the": true, "build": true, "building": true, "job": true, "run": true,
	"running": true, "task": true, "coding": true, "code": true, "work": true,
	"working": true, "man": true, "dude": true, "bro": true, "and": true,
	"seriously": true, "whoa": true, "woah": true, "hold": true, "up": true,
	"off": true, "everything's": true, "for": true, "a": true, "sec": true,
	"second": true, "minute": true, "min": true, "asap": true, "damn": true,
	"fucking": true, "fuck": true, "shit": true, "i": true, "said": true,
	"to": true, "your": true, "you": true, "self": true, "everythin": true,
}

// stopMaxBareWords bounds Tier B: a long message is prose, not a command,
// even when every word happens to be in the filler set.
const stopMaxBareWords = 8

// isStopIntent reports whether a mid-run message from the boss is an EXPLICIT
// order to stop the work - the only thing that kills a running job. Anything
// else (a question, an addition, praise) detaches instead: the job keeps
// going and he is answered now.
func isStopIntent(text string) bool {
	s := normalizeStopText(text)
	if s == "" {
		return false
	}
	// Order matters: a negated stop verb ("don't stop") is never a stop; an
	// explicit stop phrase ("don't continue", "stop building") always is; a
	// keep-going marker only then gets to veto the bare-command tier.
	if stopNegated.MatchString(s) {
		return false
	}
	if stopPhrases.MatchString(s) {
		return true
	}
	if stopKeepGoing.MatchString(s) {
		return false
	}
	return isBareStopCommand(s)
}

// anyStopIntent reports whether ANY of the messages consumed in one
// interruption is a stop order. "how's it going" followed by "actually stop"
// is a stop.
func anyStopIntent(steers []Steer) bool {
	for _, s := range steers {
		if isStopIntent(s.Text) {
			return true
		}
	}
	return false
}

// normalizeStopText lowercases, straightens curly apostrophes (a curly ’ is
// how a phone keyboard writes "don't", and matching only the straight form is
// the classic silent no-op), and reduces every other punctuation mark to a
// space so word boundaries are real.
func normalizeStopText(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer("’", "'", "‘", "'", "ʼ", "'").Replace(s)
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '\'':
			b.WriteRune(r)
		default:
			b.WriteByte(' ')
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// isBareStopCommand is Tier B: the whole message is the order.
func isBareStopCommand(s string) bool {
	words := strings.Fields(s)
	if len(words) == 0 || len(words) > stopMaxBareWords {
		return false
	}
	found := false
	for _, w := range words {
		w = strings.Trim(w, "'")
		switch {
		case stopVerbs[w]:
			found = true
		case stopFiller[w]:
		default:
			// A word that is neither a stop verb nor filler means this
			// message is about something else.
			return false
		}
	}
	return found
}
