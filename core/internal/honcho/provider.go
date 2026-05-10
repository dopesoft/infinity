package honcho

import (
	"context"
	"strings"
	"time"
)

// MemoryProvider implements agent.MemoryProvider by folding the Honcho peer
// representation into the system prompt under "About the boss (Honcho dialectic):".
// It is intentionally tiny — fact retrieval continues to come from Infinity's
// own memory.Searcher; Honcho only contributes the *who* layer.
type MemoryProvider struct {
	client *Client
	ttl    time.Duration
}

func NewMemoryProvider(c *Client) *MemoryProvider {
	return &MemoryProvider{client: c, ttl: 60 * time.Second}
}

// BuildSystemPrefix returns the peer representation block. It never errors
// upstream — a Honcho outage degrades to "no peer context" rather than
// failing the user's turn.
func (p *MemoryProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || !p.client.Enabled() {
		return "", nil
	}
	rep, err := p.client.Representation(ctx, p.ttl)
	if err != nil || strings.TrimSpace(rep) == "" {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("About the boss (Honcho dialectic — peer representation):\n")
	b.WriteString(strings.TrimSpace(rep))
	b.WriteString("\n")
	return b.String(), nil
}
