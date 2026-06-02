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
	"strings"

	"github.com/dopesoft/infinity/core/internal/llm"
)

// Category is the machine-readable class of failure, for routing + metrics.
type Category string

const (
	CatAuth         Category = "auth"
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

	switch {
	// Auth/credential — most specific; reuse the loop's own detector.
	case llm.IsAuthFailure(raw):
		h.Category = CatAuth
		h.Title = "Your model token needs reconnecting"
		h.Summary = "The active model's credential was rejected (revoked or expired token / invalid key)."
		h.Impact = "This run — and any scheduled runs on this model — can't proceed until you reconnect it."
		h.Action = llm.ReconnectHint(detectProvider(s))

	// Payload-too-large (413) — checked before rate-limit; it's a fetch-shape
	// problem, not a transient one (this is the Gmail 413 the boss hit).
	case contains(s, "413", "payloadtoolarge", "payload too large", "request entity too large", "entity too large"):
		h.Category = CatPayloadLarge
		h.Title = "Too much data requested at once"
		h.Summary = "A fetch pulled more than the provider allows in one request and was rejected."
		h.Impact = "The step that needed that data couldn't complete (e.g. Gmail returned too many / too-large messages)."
		h.Action = "The skill needs to paginate and fetch leaner batches — no full payloads on the first pass."

	// Rate-limit / overload / transient upstream — not the boss's fault, self-heals.
	case contains(s, "429", "rate limit", "rate_limit", "too many requests", "quota", "overloaded", "server_is_overloaded", "503", "upstream connect error", "connection termination", "temporarily unavailable"):
		h.Category = CatRateLimit
		h.Title = "Model provider was overloaded"
		h.Summary = "The provider was rate-limited or temporarily unavailable — a transient outage, not a config problem."
		h.Impact = "This run failed but nothing is broken on your side; it usually clears on its own."
		h.Action = "Retry shortly, or switch the active model in Studio → Settings if it persists."

	// Database / schema — surface the migrations hint for the classic 42P01.
	case contains(s, "sqlstate", "pq:", "pgx", "connection refused", "relation", "does not exist", "violates", "deadlock"):
		h.Category = CatDatabase
		h.Title = "Database error"
		if contains(s, "42p01", "does not exist", "relation") {
			h.Summary = "A database table or column the code expects is missing."
			h.Action = "Run the migrator (`infinity migrate`) — schema is behind the code."
		} else {
			h.Summary = "A database query failed (constraint, connection, or data-shape issue)."
			h.Action = "Check the DB / the failing query; see raw error for the SQLSTATE."
		}
		h.Impact = "The action couldn't read or write its data, so it stopped."

	// Coding bridge unreachable (Mac/cloud) — the self-heal 404 the boss hit.
	case contains(s, "launch via mac failed", "mac bridge", "bridge offline", "no bridge", "cloudflare access") || (contains(s, "404") && contains(s, "bridge", "mac", "workspace")):
		h.Category = CatBridge
		h.Title = "Coding workspace was unreachable"
		h.Summary = "The Mac bridge (or cloud workspace) couldn't be reached, so a code action couldn't run."
		h.Impact = "Self-heal / coding work was skipped this run."
		h.Action = "Check the Mac bridge is up; otherwise the cloud workspace should take over automatically."

	// Trust gate — a guarded action is parked for approval.
	case contains(s, "requires trust", "trust approval", "awaiting approval", "blocked by gate", "trust queue"):
		h.Category = CatTrust
		h.Title = "Waiting on your approval"
		h.Summary = "A guarded action was held back pending sign-off."
		h.Impact = "The run paused on a step that needs you."
		h.Action = "Approve or reject it in the Trust tab."

	// Tool/skill missing or not registered.
	case contains(s, "tool not found", "unknown tool", "no such tool", "not registered", "tool unavailable"):
		h.Category = CatToolNotFound
		h.Title = "A required tool wasn't available"
		h.Summary = "The agent tried to use a capability that isn't wired up or loaded."
		h.Impact = "The step needing that tool couldn't run."
		h.Action = "Check the tool/connector is registered and enabled."

	// Iteration cap / timeout / deadline.
	case contains(s, "iteration cap", "max iterations", "context deadline", "deadline exceeded", "timeout", "timed out", "loop limit"):
		h.Category = CatIterationCap
		h.Title = "Run hit a time or step limit"
		h.Summary = "The work ran into a safety limit (too many steps or too long) and stopped."
		h.Impact = "It may have done partial work before stopping."
		h.Action = "Narrow the task, or raise the limit if this is legitimately heavy work."

	default:
		h.Category = CatUnknown
		h.Title = "Run failed"
		h.Summary = firstLine(raw)
		h.Impact = ""
		h.Action = "Open the details for the raw error."
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
