package server

import (
	"strings"
	"unicode"
)

// session_labels.go — what a session is CALLED in the boss's list, and who made
// it. Both are derived at read time from data the row already carries, so the
// 573 sessions already in the database get a readable name with no backfill and
// no LLM call.
//
// Two separate problems this fixes, both reported 2026-08-25:
//
//  1. "my sessions list is a bunch of coded letters (ae3ndhc)". Studio falls
//     back to the first 8 characters of the uuid when `name` is empty. The
//     auto-namer fills `name` from the first exchange, but only for a session
//     that HAS an exchange and only after that turn ends, so anything younger
//     than its first completed turn shows as hex.
//
//  2. "crons that make sessions never get a title". Two different populations
//     hide behind that. Cron jobs that run an AGENT turn are titled normally
//     (71 of 85 in prod). Cron jobs that run DETERMINISTIC Go — inbox triage
//     being the big one — open a session as a container for the run and its
//     plan and never hold a conversation at all: 0 observations, no user
//     message, nothing for a summarizer to read. 488 of those exist. They were
//     never going to get an LLM title, because there is no text to title.
//
// The answer to both is the same: a title is only worth an LLM when there IS a
// conversation. Otherwise it is a fact we already know — which cron ran, which
// dashboard card was opened for discussion — and a fact should be rendered from
// the data, deterministically, not guessed by a model.

// sessionJobIsListableSQL excludes sessions belonging to a scheduled job that
// declares its sessions aren't worth listing (mem_crons.show_sessions = false).
// Expects a mem_sessions row aliased `s` in scope.
//
// The boss's rule for the Automated tab: a report he might want to talk about,
// yes; a chore whose result already lives somewhere else, no. Inbox triage is
// the example he gave — its output IS the Follow-ups card, so the session
// behind it is bookkeeping he has to scroll past.
//
// Expressed as a JOIN on the job's own declaration rather than a name check in
// Go: which jobs are chores is the boss's call and changes over time, so it
// lives in data he (or Jarvis, via the registered mem_act verbs) can flip
// without a deploy. A session with no cron behind it is listable by default —
// NOT EXISTS, so nothing is hidden by accident.
const sessionJobIsListableSQL = `NOT EXISTS (
	SELECT 1 FROM mem_crons c
	 WHERE c.id::text = s.origin_ref->>'cron_id'
	   AND COALESCE(c.show_sessions, TRUE) = FALSE
)`

// sessionOriginLabel turns a session's kind + origin_ref into a short phrase
// naming what produced it ("Inbox triage", "Nightly self-improve", "Heartbeat").
// Empty for a session the boss started himself: his own chats need no badge.
func sessionOriginLabel(kind string, origin map[string]any) string {
	switch strings.TrimSpace(strings.ToLower(kind)) {
	case "", "user":
		return ""
	case "cron":
		if n := humanizeSlug(strFromAny(origin, "cron_name")); n != "" {
			return n
		}
		return "Scheduled run"
	case "workflow":
		if n := humanizeSlug(strFromAny(origin, "workflow_name", "name")); n != "" {
			return n
		}
		return "Workflow"
	case "heartbeat":
		return "Heartbeat"
	case "voyager":
		return "Skill learning"
	case "sentinel":
		return "Sentinel watch"
	case "skill_test":
		if n := humanizeSlug(strFromAny(origin, "skill", "skill_name")); n != "" {
			return n + " check"
		}
		return "Skill check"
	case "backfill":
		return "Backfill"
	default:
		return humanizeSlug(kind)
	}
}

// seedOriginLabel names where a "Discuss with Jarvis" session came from. These
// stay under Mine (the boss opened them himself, by tapping a card) but he
// asked to be able to tell them apart from a chat he started cold, so the row
// says which surface put him there.
func seedOriginLabel(seed map[string]any) string {
	if seed == nil {
		return ""
	}
	switch strings.ToLower(strFromAny(seed, "kind")) {
	case "approval":
		return "From an approval"
	case "surface":
		return "From a surfaced item"
	case "work":
		return "From the work board"
	case "memory":
		return "From a memory"
	case "activity":
		return "From activity"
	case "":
		return ""
	default:
		return "From the dashboard"
	}
}

// sessionFallbackTitle is what the list shows when `name` is empty. Order of
// preference, most specific first:
//
//  1. what the session is FOR (the cron / workflow that opened it)
//  2. the opening line of the conversation, trimmed to a title's length
//  3. nothing — the caller keeps its own last-resort rendering
//
// Never the uuid. A hex slug tells the boss nothing about which conversation he
// is looking at, which is the entire complaint.
func sessionFallbackTitle(kind string, origin map[string]any, firstMessage string) string {
	if label := sessionOriginLabel(kind, origin); label != "" {
		return label
	}
	return titleFromMessage(firstMessage)
}

// titleFromMessage makes a title out of the boss's opening line: first sentence
// or first ~8 words, whichever is shorter. Deliberately his own words, not a
// paraphrase — an untitled chat should read like the thing he actually said, and
// it costs nothing while the real auto-namer is still drafting.
func titleFromMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return ""
	}
	// A seeded "Discuss with Jarvis" opener arrives as a context block; take
	// its first non-empty line rather than the whole payload.
	for _, line := range strings.Split(msg, "\n") {
		line = strings.TrimSpace(line)
		// Skip markdown scaffolding a seeded opener starts with.
		line = strings.TrimLeft(line, "#>*-` ")
		if line != "" {
			msg = line
			break
		}
	}
	if i := strings.IndexAny(msg, ".!?\n"); i > 12 {
		msg = msg[:i]
	}
	words := strings.Fields(msg)
	if len(words) == 0 {
		return ""
	}
	if len(words) > 8 {
		words = words[:8]
		return strings.Join(words, " ") + "…"
	}
	return strings.Join(words, " ")
}

// humanizeSlug turns a machine name into a readable one: "inbox-triage" →
// "Inbox triage", "nightly-self-improve" → "Nightly self improve". Every
// separator becomes a space, with no attempt to guess which hyphens were meant
// as words: a rule the boss can predict beats one that is right slightly more
// often, and the result still matches the cron he sees on the Cron tab.
func humanizeSlug(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.NewReplacer("_", " ", "-", " ", ".", " ").Replace(s)
	words := strings.Fields(s)
	for i, w := range words {
		// Acronyms read as a mumble when lowercased ("Weekly ai agent brief").
		if up, ok := knownAcronyms[strings.ToLower(w)]; ok {
			words[i] = up
		}
	}
	out := strings.Join(words, " ")
	r := []rune(out)
	if len(r) > 0 {
		r[0] = unicode.ToUpper(r[0])
	}
	out = string(r)
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

// knownAcronyms are the ones that actually appear in this system's job names.
// Deliberately a short explicit list rather than a heuristic: a heuristic that
// uppercases any two-letter word would mangle more names than it fixes.
var knownAcronyms = map[string]string{
	"ai": "AI", "api": "API", "ui": "UI", "ux": "UX", "url": "URL",
	"db": "DB", "llm": "LLM", "mcp": "MCP", "qa": "QA", "seo": "SEO",
	"pr": "PR", "id": "ID", "gepa": "GEPA", "pdf": "PDF", "csv": "CSV",
}

func strFromAny(m map[string]any, keys ...string) string {
	if m == nil {
		return ""
	}
	for _, k := range keys {
		if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// sessionKindFilter maps the list's tab to the SQL predicate it needs.
// "user" (default) is the boss's own chats. "automated" is everything the
// machinery opened for itself. "all" is both.
func sessionKindFilter(param string) (sql string, ok bool) {
	switch strings.TrimSpace(strings.ToLower(param)) {
	case "", "user", "mine":
		return `COALESCE(s.kind, 'user') = 'user'`, true
	case "automated", "auto", "agent":
		return `COALESCE(s.kind, 'user') <> 'user'`, true
	case "all":
		return `TRUE`, true
	default:
		return "", false
	}
}
