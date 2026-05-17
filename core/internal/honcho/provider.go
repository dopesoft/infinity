package honcho

import (
	"context"
	"strings"
	"time"
)

// fastRepTimeout bounds how long the synchronous BuildSystemPrefix path
// will wait for Honcho's /context endpoint. Honcho can be slow (it does
// embedding work synchronously on the read path under load), so we keep
// the on-turn deadline tight - better to skip the peer-rep injection
// than to make every chat turn feel laggy. The underlying client's
// 60-second TTL cache handles the steady-state case; we only pay this
// timeout on the first call after a cache miss.
const fastRepTimeout = 2 * time.Second

// MemoryProvider implements agent.MemoryProvider by folding the Honcho peer
// representation into the system prompt under "About the boss (Honcho dialectic):".
// It is intentionally tiny - fact retrieval continues to come from Infinity's
// own memory.Searcher; Honcho only contributes the *who* layer.
type MemoryProvider struct {
	client *Client
	ttl    time.Duration
}

func NewMemoryProvider(c *Client) *MemoryProvider {
	return &MemoryProvider{client: c, ttl: 60 * time.Second}
}

// BuildSystemPrefix returns the peer representation block. It never errors
// upstream - a Honcho outage (or just a slow Honcho) degrades to "no peer
// context" rather than blocking the user's turn. The fastRepTimeout caps
// the synchronous wait; cached values bypass the network entirely.
func (p *MemoryProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || !p.client.Enabled() {
		return "", nil
	}
	// Tight ctx for the synchronous read. If Honcho doesn't return in
	// time we simply omit the peer-rep block this turn - Infinity's own
	// RRF retrieval still runs and the agent still has the boss's facts.
	repCtx, cancel := context.WithTimeout(ctx, fastRepTimeout)
	defer cancel()
	rep, err := p.client.Representation(repCtx, p.ttl)
	if err != nil || strings.TrimSpace(rep) == "" {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("About the boss (Honcho dialectic - peer representation):\n")
	b.WriteString(strings.TrimSpace(rep))
	b.WriteString("\n")
	return b.String(), nil
}
