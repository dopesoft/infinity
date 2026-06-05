package compass

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Provider implements agent.MemoryProvider, injecting the boss's authored
// Compass into every turn so Jarvis reasons framed by what the boss has said
// actually matters — not just what we've inferred about him. Registered early
// (right after local time, before the searcher) so authored purpose sits at the
// top of the system prompt.
//
// Returns "" when the boss hasn't written a Compass yet, per the provider
// contract that empty substrate must not bloat the prompt.
type Provider struct {
	store *Store
}

func NewProvider(pool *pgxpool.Pool) *Provider {
	return &Provider{store: NewStore(pool)}
}

func (p *Provider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.store == nil {
		return "", nil
	}
	secs, err := p.store.List(ctx)
	if err != nil {
		return "", nil
	}

	var b strings.Builder
	count := 0
	for _, sec := range secs {
		content := strings.TrimSpace(sec.Content)
		if content == "" {
			continue
		}
		if count == 0 {
			b.WriteString("<compass>\n")
			b.WriteString("The boss's own mission and priorities, in his words (he authored this — it is not inferred). Let it frame what matters and how you operate; never contradict it. When the current task touches one of these, act in service of it.\n")
		}
		label := SectionLabel[sec.Section]
		if label == "" {
			label = sec.Section
		}
		fmt.Fprintf(&b, "## %s\n%s\n", label, content)
		count++
	}
	if count == 0 {
		return "", nil
	}
	b.WriteString("</compass>")
	return strings.TrimRight(b.String(), "\n"), nil
}
