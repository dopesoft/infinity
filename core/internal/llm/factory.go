package llm

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
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
	if ModelFamilyMatches(vendor, generic) {
		return generic
	}
	return ""
}

// ModelFamilyMatches reports whether a model id belongs to a vendor's family
// (claude-* → anthropic, gpt-*/o*-series → openai + openai_oauth, gemini-* →
// google). Used to keep a vendor-specific id from being sent to a different
// vendor, both at boot (LLM_MODEL) and on standby failover.
func ModelFamilyMatches(vendor, model string) bool {
	lower := strings.ToLower(strings.TrimSpace(model))
	if lower == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(vendor)) {
	case "anthropic":
		return strings.HasPrefix(lower, "claude-")
	case "openai", "openai_oauth":
		// OpenAI ships gpt-* and o*-series (o1, o3, o4-mini, etc).
		return strings.HasPrefix(lower, "gpt-") ||
			strings.HasPrefix(lower, "o1") ||
			strings.HasPrefix(lower, "o3") ||
			strings.HasPrefix(lower, "o4")
	case "google":
		return strings.HasPrefix(lower, "gemini-")
	case "deepseek":
		return strings.HasPrefix(lower, "deepseek")
	}
	return false
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
	case "deepseek":
		return NewDeepSeek(os.Getenv("DEEPSEEK_API_KEY"), ModelForVendor("deepseek")), nil
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
	// mu guards providers. The map is no longer boot-only: pasting a key in
	// Settings registers a vendor while turns are in flight, so reads and
	// writes genuinely race.
	mu        sync.RWMutex
	providers map[string]Provider
}

func NewRegistry() *Registry { return &Registry{providers: map[string]Provider{}} }

func (r *Registry) Register(p Provider) {
	if p == nil {
		return
	}
	// A stub provider never enters the registry. Everything downstream reads
	// registry membership as "this brain can answer": Settings enables the
	// vendor row, the picker lets it be selected, failover may route a spent
	// plan to it. Registering something whose every Stream call returns
	// ErrNotImplemented would make all three of those statements false at
	// once. This is the single chokepoint every path registers through
	// (BuildRegistry, the provider-keys save, boot), so gating here covers
	// them all by construction.
	if !Implemented(p) {
		fmt.Fprintf(os.Stderr,
			"llm: refusing to register %q - the provider is a stub and cannot answer a turn\n", p.Name())
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	// Universal em/en-dash sanitizer. Every provider gets wrapped so
	// any helper-LLM call (summarizer, critic, namer, code-proposal
	// generator, compaction summary, etc.) AND the main agent loop's
	// streamed text are both scrubbed at the LLM boundary. See
	// sanitize.go for the policy rationale.
	r.providers[p.Name()] = WrapNoDashes(p)
}

// Get returns the provider by id, wrapped in the plan-quota failover (see
// failover.go): every consumer that resolves a brain through the registry
// (the agent loop via Settings, activeModelProvider for every auxiliary
// call) gets standby routing from this one seam.
func (r *Registry) Get(name string) (Provider, bool) {
	r.mu.RLock()
	p, ok := r.providers[strings.ToLower(strings.TrimSpace(name))]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return WrapFailover(p, r), true
}

// lookup returns the RAW registered provider (no failover wrapper) under the
// registry lock. The standby picker needs the unwrapped instance and must not
// touch the map directly - it is written at runtime now that keys can be
// pasted mid-session.
func (r *Registry) lookup(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[strings.ToLower(strings.TrimSpace(name))]
	return p, ok
}

// Unregister drops a provider, used when its stored key is removed. The
// vendor picker goes back to "not configured" on the next poll rather than
// offering a brain whose credential is gone.
func (r *Registry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.providers, strings.ToLower(strings.TrimSpace(name)))
}

// FirstHealthy returns the first provider that is registered AND not
// currently held out for a spent plan, trying `prefer` in order before
// falling back to any healthy registrant.
//
// This is for small housekeeping calls (session titles and the like) that
// must never be the reason a feature stops working. The chat brain is the
// boss's explicit choice and is left alone; a background title is not worth
// failing over a quota, and pinning one to a plan that runs out is how
// session naming silently died on 2026-08-30.
//
// Returns the RAW provider (no failover wrapper): the caller is already
// choosing among healthy providers, so wrapping would only add a second,
// redundant layer of the same decision.
func (r *Registry) FirstHealthy(prefer ...string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, name := range prefer {
		name = strings.ToLower(strings.TrimSpace(name))
		if name == "" {
			continue
		}
		p, ok := r.providers[name]
		if !ok || p == nil {
			continue
		}
		if _, _, spent := Exhausted(name); spent {
			continue
		}
		return p, true
	}
	// Deterministic fallback order so two calls a second apart don't land on
	// different brains for no reason.
	names := make([]string, 0, len(r.providers))
	for k := range r.providers {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, _, spent := Exhausted(name); spent {
			continue
		}
		if p := r.providers[name]; p != nil {
			return p, true
		}
	}
	return nil, false
}

// Available returns the sorted list of provider ids the registry knows
// about. Studio uses this to gray out vendor options whose credentials
// aren't wired (e.g. ANTHROPIC_API_KEY missing → anthropic absent).
func (r *Registry) Available() []string {
	r.mu.RLock()
	out := make([]string, 0, len(r.providers))
	for k := range r.providers {
		out = append(out, k)
	}
	r.mu.RUnlock()
	sort.Strings(out)
	return out
}

// KeyableVendors is the set of vendors whose credential is a plain API key
// the boss can paste into Studio, in picker order. Each entry names the env
// var that serves as the deploy-time fallback and the constructor that turns
// a key into a brain - so adding a vendor is one row here, not a new branch
// in the registry, the HTTP layer and the settings API.
//
// openai_oauth is deliberately absent: it is a subscription connected by the
// OAuth paste flow, not an API key.
var KeyableVendors = []KeyableVendor{
	{ID: "anthropic", Env: "ANTHROPIC_API_KEY", New: func(key, model string) Provider { return NewAnthropic(key, model) }},
	{ID: "openai", Env: "OPENAI_API_KEY", New: func(key, model string) Provider { return NewOpenAI(key, model) }},
	{ID: "google", Env: "GOOGLE_API_KEY", New: func(key, model string) Provider { return NewGoogle(key, model) }},
	{ID: "deepseek", Env: "DEEPSEEK_API_KEY", New: func(key, model string) Provider { return NewDeepSeek(key, model) }},
}

// KeyableVendor describes one API-key vendor generically.
type KeyableVendor struct {
	ID  string
	Env string
	New func(apiKey, model string) Provider
}

// FindKeyableVendor returns the descriptor for a vendor id.
func FindKeyableVendor(id string) (KeyableVendor, bool) {
	id = strings.ToLower(strings.TrimSpace(id))
	for _, v := range KeyableVendors {
		if v.ID == id {
			return v, true
		}
	}
	return KeyableVendor{}, false
}

// ResolveKey returns the credential for a vendor plus where it came from:
// "ui" for a key pasted into Settings (mem_provider_keys), "env" for the
// deploy-time variable, "" when there is none. The store WINS over the env -
// a key typed in the UI is the boss's most recent explicit instruction.
//
// A store error is returned rather than swallowed: "the DB was unreachable"
// must never quietly render as "no key configured", which would take a
// working brain off the picker with no explanation.
func ResolveKey(ctx context.Context, keys *KeyStore, v KeyableVendor) (key, source string, err error) {
	stored, ok, err := keys.Get(ctx, v.ID)
	if err != nil {
		return "", "", err
	}
	if ok {
		return stored, "ui", nil
	}
	if envKey := strings.TrimSpace(os.Getenv(v.Env)); envKey != "" {
		return envKey, "env", nil
	}
	return "", "", nil
}

// BuildRegistry constructs every provider whose credentials are available -
// pasted into Studio (mem_provider_keys) or set in the environment. Pass a
// non-nil OAuthStore to enable the openai_oauth provider, and a non-nil
// KeyStore to pick up UI-pasted keys. Boot prints which ones registered.
func BuildRegistry(oauthStore *OAuthStore, keys *KeyStore) *Registry {
	reg := NewRegistry()
	ctx := context.Background()
	for _, v := range KeyableVendors {
		key, _, err := ResolveKey(ctx, keys, v)
		if err != nil {
			// Loud, not silent: a lookup failure means the registry may be
			// missing a brain the boss configured, and he needs to know that
			// rather than wonder why the picker shrank.
			fmt.Fprintf(os.Stderr, "llm: provider key lookup failed for %s: %v\n", v.ID, err)
			continue
		}
		if key == "" {
			continue
		}
		reg.Register(v.New(key, ModelForVendor(v.ID)))
	}
	if oauthStore != nil {
		reg.Register(NewOpenAIOAuth(oauthStore, ModelForVendor("openai_oauth")))
	}
	return reg
}
