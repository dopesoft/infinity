// Package errs turns a raw failure string into something the boss can act on.
//
// The agent kept surfacing cryptic provider JSON — `openai_oauth: status=401
// body={"error":{"code":"token_revoked"}}` — into mem_runs.error, cron
// last_run_status, and error memories. The boss reads those, not the logs, and
// "status=401" tells him nothing about what broke or what to do. Humanize maps
// the raw string to a Category + plain-language Title / Summary / Impact /
// Action.
//
// This is DETERMINISTIC string classification, not an LLM call (operating rule
// 5: if code can answer, code answers). It reuses llm.IsAuthFailure /
// llm.ReconnectHint for the credential category so there's one source of truth
// for "is this an auth failure" across the loop, the cron path, and the UI.
package errs

import (
	"regexp"
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Category is the machine-readable class of failure, for routing + metrics.
type Category string

const (
	CatAuth         Category = "auth"
	CatQuota        Category = "plan_out_of_usage"
	CatRateLimit    Category = "rate_limit"
	CatPayloadLarge Category = "payload_too_large"
	CatToolNotFound Category = "tool_not_found"
	CatIterationCap Category = "iteration_cap"
	CatDatabase     Category = "database"
	CatTrust        Category = "trust_rejected"
	CatBridge       Category = "bridge_offline"
	CatUnknown      Category = "unknown"
)

// Human is the boss-facing translation of a failure. JSON-tagged so it can be
// stored verbatim in mem_runs.human_error (JSONB) and read by Studio.
type Human struct {
	Category Category `json:"category"`
	Title    string   `json:"title"`   // ≤~60 chars — what failed, in plain words
	Summary  string   `json:"summary"` // one sentence — the root cause
	Impact   string   `json:"impact"`  // how it affects the app / what's now blocked
	Action   string   `json:"action"`  // what the boss can do about it
	Raw      string   `json:"raw"`     // preserved for the "show details" expander
}

// Humanize classifies err and returns a Human. nil → empty Human (caller should
// guard; an empty Category means "no failure").
func Humanize(err error) Human {
	if err == nil {
		return Human{}
	}
	raw := err.Error()
	return HumanizeString(raw)
}

// HumanizeString is the string form, for callers that already hold the message.
func HumanizeString(raw string) Human {
	s := strings.ToLower(raw)
	h := Human{Raw: raw}

	// All copy is FIRST PERSON, the way Jarvis would explain it to the boss,
	// not a status-code dump. The deterministic template gives every surface
	// (dashboard chip, cron status, push) a human, companion read for free; the
	// agent's own in-chat explanation (driven by soul.md) goes deeper. No
	// em-dashes in user-facing strings (the boss hates them).
	switch {
	// Auth/credential. Most specific; reuse the loop's own detector.
	case llm.IsAuthFailure(raw):
		h.Category = CatAuth
		h.Title = "I couldn't reach your model"
		h.Summary = "Its login got turned away (the token looks revoked or expired), so the provider stopped letting me in."
		h.Impact = "I paused what you asked so nothing's lost, but I can't think with that model until it's reconnected."
		h.Action = llm.ReconnectHint(detectProvider(s))

	// Payload-too-large (413). Checked before rate-limit; it's a fetch-shape
	// problem, not a transient one (this is the Gmail 413 the boss hit).
	case contains(s, "413", "payloadtoolarge", "payload too large", "request entity too large", "entity too large"):
		h.Category = CatPayloadLarge
		h.Title = "I tried to grab too much at once"
		h.Summary = "I asked for more data than the provider allows in a single request, so it bounced me back."
		h.Impact = "That step couldn't finish. Usually it's email: too many or too-large messages pulled in one grab."
		h.Action = "I need to page through it in smaller batches instead of all at once. The triage recipe now does that."

	// Plan exhaustion (ChatGPT Plus usage_limit_reached, Claude Max "out of
	// extra usage", Anthropic credits gone). Checked BEFORE rate-limit because
	// it also carries a 429, and it is the opposite of a hiccup: it will not
	// clear on its own until the plan resets, and the boss must hear it in
	// those words (2026-08-26: this read as "brief server hiccup" for hours).
	case contains(s, "usage limit reached", "usage_limit_reached", "out of extra usage", "out of usage", "credit balance"):
		h.Category = CatQuota
		h.Title = "Your plan is out of usage"
		h.Summary = "The model's plan has used up its allowance, so it turned me away. That is the plan's cap, not a fault in your setup."
		h.Impact = "This step waits for the plan to reset; nothing is lost, and I don't switch to a pay-per-token key on my own."
		h.Action = "It clears when the plan resets" + resetClause(raw) + ". If you want it now, pick a different plan-backed model in Settings."

	// Rate-limit / overload / transient upstream (incl. provider-side 5xx
	// server_error). Not the boss's fault, self-heals — and the openai_oauth
	// path now retries these automatically before they ever surface here.
	case contains(s, "429", "rate limit", "rate_limit", "too many requests", "quota", "overloaded", "server_is_overloaded", "server_error", "server had an error", "internal server error", "internal_error", "500", "502", "503", "504", "upstream connect error", "connection termination", "temporarily unavailable", "try again"):
		h.Category = CatRateLimit
		h.Title = "The model provider hit a snag"
		h.Summary = "Your model's provider rate-limited me or had a brief server hiccup on their end. Nothing wrong with your setup or your settings."
		h.Impact = "This run didn't go through, but it clears on its own — and I now retry these automatically a few times before giving up."
		h.Action = "If it keeps happening, it's on the provider's side; switch me to another model in Settings to route around it."

	// Database / schema. Surface the migrations hint for the classic 42P01.
	case contains(s, "sqlstate", "pq:", "pgx", "connection refused", "relation", "does not exist", "violates", "deadlock"):
		h.Category = CatDatabase
		h.Impact = "I couldn't read or write what that step needed, so it stopped."
		if contains(s, "42p01", "does not exist", "relation") {
			h.Title = "I hit a missing database table"
			h.Summary = "A table I expected wasn't there, so the query had nowhere to land."
			h.Action = "Looks like a migration needs to run to catch the schema up to the code."
		} else {
			h.Title = "I hit a database problem"
			h.Summary = "A query failed down at the data layer (a constraint, connection, or data-shape issue)."
			h.Action = "I'll need to look at the failing query. The raw error has the exact code."
		}

	// Coding bridge unreachable (Mac/cloud). The self-heal 404 the boss hit.
	case contains(s, "launch via mac failed", "mac bridge", "bridge offline", "no bridge", "cloudflare access") || (contains(s, "404") && contains(s, "bridge", "mac", "workspace")):
		h.Category = CatBridge
		h.Title = "I couldn't reach the coding workspace"
		h.Summary = "Your Mac didn't answer, so I couldn't run the code step there."
		h.Impact = "Any self-heal or coding work got skipped this run."
		h.Action = "Check the Mac's awake. Otherwise I'll lean on the cloud workspace and keep going."

	// Trust gate. A guarded action is parked for approval.
	case contains(s, "requires trust", "trust approval", "awaiting approval", "blocked by gate", "trust queue"):
		h.Category = CatTrust
		h.Title = "I'm waiting on your okay"
		h.Summary = "A guarded step needs your sign-off before I run it, so I held back rather than push past you."
		h.Impact = "I paused right there; everything before it is done."
		h.Action = "Approve or wave it off in the Trust tab and I'll carry on."

	// Tool/skill missing or not registered.
	case contains(s, "tool not found", "unknown tool", "no such tool", "not registered", "tool unavailable"):
		h.Category = CatToolNotFound
		h.Title = "A tool I needed wasn't loaded"
		h.Summary = "I reached for a capability that isn't wired up in this run."
		h.Impact = "The step that depended on it couldn't run."
		h.Action = "Worth checking that tool or connector is registered and enabled."

	// Iteration cap / timeout / deadline.
	case contains(s, "iteration cap", "max iterations", "context deadline", "deadline exceeded", "timeout", "timed out", "loop limit"):
		h.Category = CatIterationCap
		h.Title = "I hit a safety limit and pulled up"
		h.Summary = "The task ran long enough to trip a step or time guard, so I stopped rather than spin."
		h.Impact = "I may have gotten partway before stopping."
		h.Action = "Narrow it down, or bump the limit if it's genuinely this heavy."

	default:
		h.Category = CatUnknown
		h.Title = "Something went wrong and I stopped"
		h.Summary = firstLine(raw)
		h.Impact = ""
		h.Action = "Open the details for the raw error and I'll dig in."
	}
	return h
}

// detectProvider sniffs the provider out of the raw error so the auth Action
// can name the right reconnect flow. Defaults to "" → ReconnectHint's generic
// "switch the active model" escape hatch.
func detectProvider(s string) string {
	switch {
	case strings.Contains(s, "openai_oauth"), strings.Contains(s, "chatgpt"):
		return "openai_oauth"
	case strings.Contains(s, "anthropic"), strings.Contains(s, "x-api-key"):
		return "anthropic"
	case strings.Contains(s, "google"), strings.Contains(s, "gemini"):
		return "google"
	}
	return ""
}

func contains(s string, subs ...string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		s = s[:i]
	}
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// resetClause pulls the "resets 10:13pm" clause out of a provider message so
// the action line can name the time; empty when the message has none.
func resetClause(raw string) string {
	if m := resetClauseRe.FindString(raw); m != "" {
		return " (" + m + ")"
	}
	return ""
}

var resetClauseRe = regexp.MustCompile(`(?i)resets?\s+(?:at\s+)?(?:[A-Za-z]{3}\s+)?\d{1,2}(?::\d{2})?\s*[ap]m(?:\s*\([^)]+\))?`)
