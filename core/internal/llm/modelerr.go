package llm

import "strings"

// IsUnsupportedModel reports whether a provider error means "this brain will
// not serve THIS MODEL", as opposed to a transient failure, a spent plan, or a
// bad credential. It is provider-agnostic, and it exists because that
// distinction changes what a caller should do: retrying the same provider is
// pointless, and punishing the request is wrong. Move to a different brain.
//
// THE FAILURE THAT PRODUCED IT. Session titling ran against the ChatGPT plan
// with a model that a ChatGPT account refuses outright:
//
//	openai_oauth: status=400 body={"detail":"The 'codex-mini-latest' model is
//	not supported when using Codex with a ChatGPT account."}
//
// That is not a quota error, so the sweep counted it against the session's
// three-attempt budget and, after three passes, condemned the conversation to
// read "New Conversation" forever - permanently, over a configuration problem
// that was fixed minutes later. Every session opened for two days went the
// same way. A misconfiguration must never be able to permanently mark a
// perfectly nameable thing as unnameable.
//
// Deliberately narrow: it requires BOTH a model-shaped subject and a
// refusal-shaped verb, so a model whose OUTPUT happens to discuss unsupported
// models can never match. A false positive costs one retry against another
// brain; a false negative costs the boss a permanent hex slug.
func IsUnsupportedModel(errStr string) bool {
	s := strings.ToLower(errStr)
	if !strings.Contains(s, "model") {
		return false
	}
	switch {
	case strings.Contains(s, "is not supported"),
		strings.Contains(s, "not supported when using"),
		strings.Contains(s, "does not exist"),
		strings.Contains(s, "unknown model"),
		strings.Contains(s, "unsupported model"),
		strings.Contains(s, "invalid model"),
		strings.Contains(s, "model_not_found"),
		strings.Contains(s, "does not have access to model"),
		strings.Contains(s, "no access to model"):
		return true
	}
	return false
}
