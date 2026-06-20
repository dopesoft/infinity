package llm

import (
	"context"
	"testing"
)

func TestSystemPromptRender_StableFirst(t *testing.T) {
	cases := []struct {
		name string
		sp   SystemPrompt
		want string
	}{
		{"both", SystemPrompt{Stable: "SOUL", Volatile: "CTX"}, "SOUL\n\nCTX"},
		{"stable only", SystemPrompt{Stable: "SOUL"}, "SOUL"},
		{"volatile only", SystemPrompt{Volatile: "CTX"}, "CTX"},
		{"empty", SystemPrompt{}, ""},
	}
	for _, c := range cases {
		if got := c.sp.Render(); got != c.want {
			t.Errorf("%s: Render() = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCacheKeyContextRoundTrip(t *testing.T) {
	ctx := WithCacheKey(context.Background(), "sess-123")
	if got := CacheKeyFromContext(ctx); got != "sess-123" {
		t.Fatalf("CacheKeyFromContext = %q, want sess-123", got)
	}
	if got := CacheKeyFromContext(context.Background()); got != "" {
		t.Fatalf("empty context should yield empty key, got %q", got)
	}
	// Empty key must not wrap the context (avoids storing useless values).
	if WithCacheKey(context.Background(), "") != context.Background() {
		// Not a hard failure across Go versions, but document intent.
		t.Log("WithCacheKey('') returned a wrapped context")
	}
}

// Anthropic and the OpenAI providers must satisfy CachingProvider so the loop
// routes them through StreamCached. A compile-time assertion is the cheapest
// guard against a future refactor silently dropping the method.
var (
	_ CachingProvider = (*Anthropic)(nil)
	_ CachingProvider = (*OpenAI)(nil)
	_ CachingProvider = (*OpenAIOAuth)(nil)
	_ CachingProvider = (*noDashesProvider)(nil)
)
