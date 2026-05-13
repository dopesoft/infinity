package llm

import (
	"fmt"
	"os"
	"strings"
)

func FromEnv() (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	if provider == "" {
		provider = "anthropic"
	}
	model := os.Getenv("LLM_MODEL")

	switch provider {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for provider=anthropic")
		}
		return NewAnthropic(key, model), nil
	case "openai":
		return NewOpenAI(os.Getenv("OPENAI_API_KEY"), model), nil
	case "openai_oauth":
		// OAuth-backed provider needs a Postgres pool for token storage,
		// which isn't available at this construction point. The serve
		// command resolves this by calling NewOpenAIOAuth(store, model)
		// directly once the pool is up. Returning ErrNotImplemented
		// here keeps boot from crashing on env-only paths (e.g. the
		// migrate/consolidate commands).
		return nil, fmt.Errorf("LLM_PROVIDER=openai_oauth requires a database pool; constructed by serve cmd after pool init")
	case "google":
		return NewGoogle(os.Getenv("GOOGLE_API_KEY"), model), nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER=%q", provider)
	}
}

// IsOpenAIOAuth returns true when the env asks for the OAuth-backed OpenAI
// provider. Serve uses this after the Postgres pool is up to construct the
// real provider with token storage attached.
func IsOpenAIOAuth() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("LLM_PROVIDER")), "openai_oauth")
}
