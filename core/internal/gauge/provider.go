package gauge

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Provider implements agent.MemoryProvider, folding the session's most recent
// Gauge read back into the turn as a one-line <effort> hint so multi-turn work
// calibrates (a deep read nudges the agent to plan + verify + open a Mandate).
// Because the Gauge runs async, this hint lands on the turns AFTER the one that
// was sized — which is exactly when it's useful, on the follow-through of a long
// task. Advisory only: it never caps how hard the agent may think.
//
// Silent for glance/standard reads and when there's no read — only a deep read
// is worth a line in the prompt.
type Provider struct {
	store *Store
}

func NewProvider(pool *pgxpool.Pool) *Provider {
	return &Provider{store: NewStore(pool)}
}

func (p *Provider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.store == nil || strings.TrimSpace(sessionID) == "" {
		return "", nil
	}
	r, ok := p.store.Latest(ctx, sessionID)
	if !ok || r.Tier != TierDeep {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("<effort>\n")
	b.WriteString("This task was sized as DEEP: real work where being wrong is costly or the path is long. Don't rush it — plan, verify your result, and open a Mandate so 'done' is provable. ")
	if strings.TrimSpace(r.Reason) != "" {
		b.WriteString("(why: " + strings.TrimSpace(r.Reason) + ")")
	}
	b.WriteString("\n</effort>")
	return b.String(), nil
}
