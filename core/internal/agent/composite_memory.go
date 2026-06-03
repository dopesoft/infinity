package agent

import (
	"context"
	"strings"
)

// CompositeMemory chains multiple MemoryProviders. Each is asked for a
// system-prompt prefix; non-empty results are concatenated separated by
// blank lines, in the order providers were supplied.
//
// Used to fold Honcho's peer representation alongside Infinity's
// triple-stream RRF retrieval without making either layer aware of the
// other.
type CompositeMemory struct {
	providers []MemoryProvider
}

// NewCompositeMemory returns a provider that calls each child sequentially.
// Nil children are dropped so callers can build the slice with optional
// providers without a check at every site.
func NewCompositeMemory(providers ...MemoryProvider) *CompositeMemory {
	out := make([]MemoryProvider, 0, len(providers))
	for _, p := range providers {
		if p != nil {
			out = append(out, p)
		}
	}
	return &CompositeMemory{providers: out}
}

func (c *CompositeMemory) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if c == nil || len(c.providers) == 0 {
		return "", nil
	}
	var b strings.Builder
	for _, p := range c.providers {
		s, err := p.BuildSystemPrefix(ctx, sessionID, query)
		if err != nil {
			// One provider's failure must not silence the others. Continue.
			continue
		}
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(s)
	}
	return b.String(), nil
}

// GatedProvider is a query-adaptive decorator: it only consults its inner
// provider when a deterministic predicate says the query is relevant, returning
// "" (silent) otherwise so the rest of the chain still runs. This is the
// context-intelligence seam - it lets the memory chain scale to many providers
// without every one paying a query + tokens on every turn. The predicate must
// be deterministic (no LLM in the routing path); the model is for judgment, not
// for deciding which substrate to read.
type GatedProvider struct {
	inner    MemoryProvider
	relevant func(query string) bool
}

// NewGatedProvider wraps inner so it is consulted only when relevant(query) is
// true. A nil inner or nil predicate degrades to a passthrough so callers can
// wrap unconditionally.
func NewGatedProvider(inner MemoryProvider, relevant func(query string) bool) *GatedProvider {
	return &GatedProvider{inner: inner, relevant: relevant}
}

func (g *GatedProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if g == nil || g.inner == nil {
		return "", nil
	}
	if g.relevant != nil && !g.relevant(query) {
		return "", nil
	}
	return g.inner.BuildSystemPrefix(ctx, sessionID, query)
}

// trivialQueryWords are the whole-message openers that carry no task signal -
// greetings, acknowledgements, pleasantries. When a turn's query is just one of
// these (or empty / very short), the heavier behavioral providers (proven
// lessons, cross-session reflection chains) have nothing to contribute and only
// cost tokens, so SubstantiveQuery gates them out.
var trivialQueryWords = map[string]bool{
	"": true, "hi": true, "hey": true, "hello": true, "yo": true, "sup": true,
	"thanks": true, "thank you": true, "thx": true, "ty": true, "ok": true,
	"okay": true, "k": true, "cool": true, "nice": true, "great": true,
	"got it": true, "gotcha": true, "yes": true, "no": true, "yep": true,
	"nope": true, "yeah": true, "sure": true, "perfect": true, "good": true,
	"morning": true, "good morning": true, "good night": true, "gn": true,
	"bye": true, "later": true, "lol": true, "haha": true,
}

// SubstantiveQuery is the default relevance predicate for behavioral providers:
// true unless the turn is a bare greeting / acknowledgement / very short filler.
// Conservative on purpose - anything with real content passes, so we only skip
// the cases where injecting lessons/reflections is pure waste.
func SubstantiveQuery(query string) bool {
	q := strings.ToLower(strings.TrimSpace(query))
	q = strings.Trim(q, ".!?,; ")
	if q == "" {
		return false
	}
	if trivialQueryWords[q] {
		return false
	}
	// A handful of words with no trivial-only opener is substantive; a single
	// short token that isn't a known trivial word is ambiguous - treat <= 2
	// chars as filler, longer as substantive.
	if !strings.Contains(q, " ") && len(q) <= 2 {
		return false
	}
	return true
}
