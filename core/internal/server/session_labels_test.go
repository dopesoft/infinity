package server

import (
	"strings"
	"testing"
)

// The boss's report, verbatim: "my sessions list isn't a bunch of coded letters
// (ae3ndhc)". Every branch here exists to keep a hex slug off the screen.
func TestSessionFallbackTitle(t *testing.T) {
	cases := []struct {
		name    string
		kind    string
		origin  map[string]any
		firstMs string
		want    string
	}{
		{
			name:   "a scheduled run is named after its cron, not titled by a model",
			kind:   "cron",
			origin: map[string]any{"cron_name": "inbox-triage"},
			want:   "Inbox triage",
		},
		{
			name:   "every separator becomes a space, predictably",
			kind:   "cron",
			origin: map[string]any{"cron_name": "nightly-self-improve"},
			want:   "Nightly self improve",
		},
		{
			name: "a cron container with no origin still says what it is",
			kind: "cron",
			want: "Scheduled run",
		},
		{
			name:    "an untitled chat borrows the boss's own opening words",
			kind:    "user",
			firstMs: "why do our cron sessions never get a title like a normal session",
			want:    "why do our cron sessions never get a…",
		},
		{
			name:    "the opener is cut at its first sentence when that's shorter",
			kind:    "user",
			firstMs: "Fix the sessions list. It is unreadable.",
			want:    "Fix the sessions list",
		},
		{
			name:    "a seeded Discuss opener uses its first real line, not the markdown",
			kind:    "user",
			firstMs: "## Context\nThe Supabase security alert needs a decision",
			want:    "Context",
		},
		{
			name: "nothing to go on returns empty so the caller decides",
			kind: "user",
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sessionFallbackTitle(c.kind, c.origin, c.firstMs); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// The badge that tells the boss a row was machinery, not him.
func TestSessionOriginLabel(t *testing.T) {
	if got := sessionOriginLabel("user", nil); got != "" {
		t.Errorf("the boss's own chats carry no origin badge, got %q", got)
	}
	if got := sessionOriginLabel("heartbeat", nil); got != "Heartbeat" {
		t.Errorf("got %q", got)
	}
	if got := sessionOriginLabel("workflow", map[string]any{"workflow_name": "content_autopilot"}); got != "Content autopilot" {
		t.Errorf("got %q", got)
	}
	// An unknown future kind must still render as words, never as a raw slug
	// or an empty badge that makes it look like the boss's own chat.
	if got := sessionOriginLabel("some_new_engine", nil); got != "Some new engine" {
		t.Errorf("got %q", got)
	}
}

// The switcher's contract. A typo must be an error, not a silent wrong answer:
// quietly falling back to 'user' would show the boss his own chats while the
// Automated tab looked empty.
func TestSessionKindFilter(t *testing.T) {
	for _, in := range []string{"", "user", "mine"} {
		if sql, ok := sessionKindFilter(in); !ok || sql != `COALESCE(s.kind, 'user') = 'user'` {
			t.Errorf("%q should select the boss's own chats, got %q ok=%v", in, sql, ok)
		}
	}
	for _, in := range []string{"automated", "AUTO", " agent "} {
		if sql, ok := sessionKindFilter(in); !ok || sql != `COALESCE(s.kind, 'user') <> 'user'` {
			t.Errorf("%q should select machinery, got %q ok=%v", in, sql, ok)
		}
	}
	if sql, ok := sessionKindFilter("all"); !ok || sql != "TRUE" {
		t.Errorf("all should select both, got %q ok=%v", sql, ok)
	}
	if _, ok := sessionKindFilter("crons"); ok {
		t.Error("an unrecognised filter must be rejected, not silently defaulted")
	}
}

// A job name full of acronyms must not read as a mumble ("Weekly ai agent
// advancement brief" is the boss's actual weekly cron).
func TestHumanizeSlugAcronyms(t *testing.T) {
	if got := humanizeSlug("weekly-ai-agent-advancement-brief"); got != "Weekly AI agent advancement brief" {
		t.Errorf("got %q", got)
	}
	if got := humanizeSlug("api_health_check"); got != "API health check" {
		t.Errorf("got %q", got)
	}
	// A word that merely contains an acronym is left alone.
	if got := humanizeSlug("aid-station-sync"); got != "Aid station sync" {
		t.Errorf("got %q", got)
	}
}

// "Discuss with Jarvis" sessions stay under Mine (the boss opened them) but
// have to be tellable apart from a chat he started cold.
func TestSeedOriginLabel(t *testing.T) {
	cases := map[string]string{
		"approval": "From an approval",
		"surface":  "From a surfaced item",
		"work":     "From the work board",
		"memory":   "From a memory",
		"activity": "From activity",
		"":         "",
	}
	for kind, want := range cases {
		var seed map[string]any
		if kind != "" {
			seed = map[string]any{"kind": kind}
		}
		if got := seedOriginLabel(seed); got != want {
			t.Errorf("seedOriginLabel(%q) = %q, want %q", kind, got, want)
		}
	}
	// A seed kind we haven't met still says it came from the dashboard rather
	// than looking like a cold chat.
	if got := seedOriginLabel(map[string]any{"kind": "something_new"}); got != "From the dashboard" {
		t.Errorf("got %q", got)
	}
}

// The boss's rule for the Automated tab, encoded: a job declares whether its
// sessions are worth listing, and the ABSENCE of a declaration means visible.
// A predicate that hid sessions by default would silently swallow every future
// cron's history, which is the opposite failure of the one being fixed.
func TestSessionJobIsListableSQLShape(t *testing.T) {
	// Guard the two properties the query depends on. If either identifier is
	// renamed, the filter would silently stop matching and the chores would
	// quietly come back into his list.
	for _, needle := range []string{
		"mem_crons",                 // the job table carrying the declaration
		"origin_ref->>'cron_id'",    // how a session links back to its job
		"COALESCE(c.show_sessions,", // absent/NULL must read as visible
		"NOT EXISTS",                // no cron behind it → listable
	} {
		if !strings.Contains(sessionJobIsListableSQL, needle) {
			t.Errorf("the listable predicate no longer contains %q:\n%s", needle, sessionJobIsListableSQL)
		}
	}
}
