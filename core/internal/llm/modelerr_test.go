package llm

import "testing"

func TestIsUnsupportedModel(t *testing.T) {
	yes := []string{
		// The verbatim production error, from mem_sessions.metadata.name_error.
		`openai_oauth: status=400 body={"detail":"The 'codex-mini-latest' model is not supported when using Codex with a ChatGPT account."}`,
		`error: the model "gpt-9" does not exist`,
		`{"error":{"code":"model_not_found","message":"The model does not exist"}}`,
		"unknown model: sonnet-99",
		"your account does not have access to model o5-preview",
	}
	for _, s := range yes {
		if !IsUnsupportedModel(s) {
			t.Errorf("expected unsupported-model for %q", s)
		}
	}
	no := []string{
		"",
		"status=429 rate limit exceeded",
		"you have exhausted your plan usage",
		"status=401 invalid x-api-key",
		"context deadline exceeded",
		// A refusal shape with no model subject must not match.
		"this operation is not supported",
		// A model's own prose about models must not match: no refusal verb.
		"The model considered several options and picked one.",
	}
	for _, s := range no {
		if IsUnsupportedModel(s) {
			t.Errorf("did NOT expect unsupported-model for %q", s)
		}
	}
}
