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
