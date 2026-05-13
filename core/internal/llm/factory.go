package llm

import (
	"fmt"
	"os"
	"sort"
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

// Registry holds every provider whose credentials are available at boot,
// keyed by canonical id ("anthropic" / "openai" / "openai_oauth" / "google").
// The Settings PUT for provider looks up the requested id here and swaps
// the agent loop's active provider via Loop.SetProvider. Providers without
// available credentials are simply absent from the map — the UI surfaces
// "not configured" instead of letting a switch silently fail at first
// turn.
//
// Token storage is shared across registry rebuilds: the OAuth provider's
// store is the same pool-backed instance every time, so flipping vendors
// in Settings never wipes mem_provider_tokens.
type Registry struct {
	providers map[string]Provider
}

func NewRegistry() *Registry { return &Registry{providers: map[string]Provider{}} }

func (r *Registry) Register(p Provider) {
	if p == nil {
		return
	}
	r.providers[p.Name()] = p
}

func (r *Registry) Get(name string) (Provider, bool) {
	p, ok := r.providers[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// Available returns the sorted list of provider ids the registry knows
// about. Studio uses this to gray out vendor options whose credentials
// aren't wired (e.g. ANTHROPIC_API_KEY missing → anthropic absent).
func (r *Registry) Available() []string {
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// BuildRegistry constructs every provider whose credentials are present
// in the environment. Pass a non-nil OAuthStore to enable the
// openai_oauth provider. Boot prints which ones registered.
func BuildRegistry(oauthStore *OAuthStore) *Registry {
	reg := NewRegistry()
	model := os.Getenv("LLM_MODEL")
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		reg.Register(NewAnthropic(key, model))
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		reg.Register(NewOpenAI(key, model))
	}
	if oauthStore != nil {
		reg.Register(NewOpenAIOAuth(oauthStore, model))
	}
	if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		reg.Register(NewGoogle(key, model))
	}
	return reg
}
