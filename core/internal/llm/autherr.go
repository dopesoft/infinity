package llm

import "strings"

// IsAuthFailure reports whether a provider stream error indicates the model
// credential needs re-authentication — a revoked/expired OAuth token or an
// invalid/expired API key — rather than a transient, rate-limit, or model
// error. It is PROVIDER-AGNOSTIC by design: the agent loop uses it to decide
// whether to PARK a turn for re-auth (and tell the boss in chat) instead of
// dumping a raw 401, regardless of which brain is active.
//
// It only ever runs on the LLM provider's own stream error string (never tool
// output), so the matches below are scoped and safe. A false positive is benign:
// the turn parks, the reauth poller probes, and if the credential actually works
// the turn replays immediately — so even a borderline 401 self-heals.
//
// Known phrasings covered:
//   - openai_oauth (ChatGPT): "token_revoked", "invalidated oauth token for
//     user", "status=401", and our own "no refresh_token stored - reconnect
//     ChatGPT".
//   - Anthropic SDK: "authentication_error", "invalid x-api-key", HTTP 401.
//   - Generic: "401 unauthorized", "403 forbidden", "invalid api key".
func IsAuthFailure(errStr string) bool {
	s := strings.ToLower(errStr)
	switch {
	case strings.Contains(s, "token_revoked"),
		strings.Contains(s, "invalidated oauth token"),
		strings.Contains(s, "no refresh_token stored"),
		strings.Contains(s, "reconnect chatgpt"),
		strings.Contains(s, "authentication_error"),
		strings.Contains(s, "invalid_api_key"),
		strings.Contains(s, "invalid x-api-key"),
		strings.Contains(s, "invalid api key"),
		strings.Contains(s, "expired"):
		return true
	}
	// Status-code based 401/403 from the provider HTTP call. Require the
	// provider's status= prefix or an explicit auth phrasing so we don't match
	// an unrelated "401" buried in some other payload.
	if strings.Contains(s, "status=401") || strings.Contains(s, "status=403") ||
		strings.Contains(s, "status code: 401") || strings.Contains(s, "status code: 403") ||
		strings.Contains(s, "401 unauthorized") || strings.Contains(s, "403 forbidden") ||
		strings.Contains(s, "unauthorized") {
		return true
	}
	return false
}

// ReconnectHint returns boss-facing guidance for restoring a provider's
// credential, shown in the chat message when a turn is parked for re-auth. The
// fallback covers any provider we haven't special-cased — switching the active
// model in Settings is always a valid escape hatch.
func ReconnectHint(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "openai_oauth":
		return "Reconnect ChatGPT in Studio → Settings → Models (paste-flow connect), or switch the active model to Anthropic."
	case "anthropic":
		return "Your Anthropic API key was rejected — update the Anthropic credential, or switch the active model in Studio → Settings."
	case "google":
		return "Your Google API key was rejected — update the Google credential, or switch the active model in Studio → Settings."
	default:
		return "Reconnect this model's credential in Studio → Settings → Models, or switch to another model."
	}
}
