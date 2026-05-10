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
	case "google":
		return NewGoogle(os.Getenv("GOOGLE_API_KEY"), model), nil
	default:
		return nil, fmt.Errorf("unknown LLM_PROVIDER=%q", provider)
	}
}
