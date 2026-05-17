package llm

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// ModelForVendor resolves the model id to hand to a provider constructor.
// Priority: per-vendor env (LLM_MODEL_ANTHROPIC / _OPENAI / _OPENAI_OAUTH /
// _GOOGLE) → generic LLM_MODEL if its prefix matches the vendor's family →
// empty string (provider falls back to its built-in default).
//
// Why: a single LLM_MODEL env used to be blasted at every provider in
// BuildRegistry, which meant an Anthropic model id like
// "claude-sonnet-4-5-20250929" got stuffed into the openai_oauth provider's
// model field. First inference call would crash or get silently routed to
// gpt-5. Family-match guards against that and lets one env serve every
// vendor whose id happens to match.
func ModelForVendor(vendor string) string {
	if v := strings.TrimSpace(os.Getenv("LLM_MODEL_" + strings.ToUpper(vendor))); v != "" {
		return v
	}
	generic := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if generic == "" {
		return ""
	}
	lower := strings.ToLower(generic)
	switch vendor {
	case "anthropic":
		if strings.HasPrefix(lower, "claude-") {
			return generic
		}
	case "openai", "openai_oauth":
		// OpenAI ships gpt-* and o*-series (o1, o3, o4-mini, etc).
		if strings.HasPrefix(lower, "gpt-") ||
			strings.HasPrefix(lower, "o1") ||
			strings.HasPrefix(lower, "o3") ||
			strings.HasPrefix(lower, "o4") {
			return generic
		}
	case "google":
		if strings.HasPrefix(lower, "gemini-") {
			return generic
		}
	}
	return ""
}

func FromEnv() (Provider, error) {
	provider := strings.ToLower(strings.TrimSpace(os.Getenv("LLM_PROVIDER")))
	if provider == "" {
		provider = "anthropic"
	}

	// All return paths route through fromEnvProvider so the universal
	// em/en-dash sanitizer is applied to the boot provider too. Any
	// helper that fetches the bare provider via FromEnv (instead of
	// going through Registry.Register) still gets the sanitizer.
	p, err := fromEnvProvider(provider)
	if p != nil {
		p = WrapNoDashes(p)
	}
	return p, err
}

func fromEnvProvider(provider string) (Provider, error) {
	switch provider {
	case "anthropic":
		key := os.Getenv("ANTHROPIC_API_KEY")
		if key == "" {
			return nil, fmt.Errorf("ANTHROPIC_API_KEY is required for provider=anthropic")
		}
		return NewAnthropic(key, ModelForVendor("anthropic")), nil
	case "openai":
		return NewOpenAI(os.Getenv("OPENAI_API_KEY"), ModelForVendor("openai")), nil
	case "openai_oauth":
		// OAuth-backed provider needs a Postgres pool for token storage,
		// which isn't available at this construction point. The serve
		// command resolves this by calling NewOpenAIOAuth(store, model)
		// directly once the pool is up. Returning ErrNotImplemented
		// here keeps boot from crashing on env-only paths (e.g. the
		// migrate/consolidate commands).
		return nil, fmt.Errorf("LLM_PROVIDER=openai_oauth requires a database pool; constructed by serve cmd after pool init")
	case "google":
		return NewGoogle(os.Getenv("GOOGLE_API_KEY"), ModelForVendor("google")), nil
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
// available credentials are simply absent from the map - the UI surfaces
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
	// Universal em/en-dash sanitizer. Every provider gets wrapped so
	// any helper-LLM call (summarizer, critic, namer, code-proposal
	// generator, compaction summary, etc.) AND the main agent loop's
	// streamed text are both scrubbed at the LLM boundary. See
	// sanitize.go for the policy rationale.
	r.providers[p.Name()] = WrapNoDashes(p)
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
	if key := os.Getenv("ANTHROPIC_API_KEY"); key != "" {
		reg.Register(NewAnthropic(key, ModelForVendor("anthropic")))
	}
	if key := os.Getenv("OPENAI_API_KEY"); key != "" {
		reg.Register(NewOpenAI(key, ModelForVendor("openai")))
	}
	if oauthStore != nil {
		reg.Register(NewOpenAIOAuth(oauthStore, ModelForVendor("openai_oauth")))
	}
	if key := os.Getenv("GOOGLE_API_KEY"); key != "" {
		reg.Register(NewGoogle(key, ModelForVendor("google")))
	}
	return reg
}
