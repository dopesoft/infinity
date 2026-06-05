package mandate

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Provider implements agent.MemoryProvider, injecting the session's OPEN
// mandate into every turn — its title, summary, and the criteria still unchecked
// — so the agent stays anchored to the definition of done across turns and
// restarts (mirrors memory.PlanProvider). Silent when the session has no open
// mandate.
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
	m, err := p.store.GetOpenForSession(ctx, sessionID)
	if err != nil || m == nil {
		return "", nil
	}

	var b strings.Builder
	b.WriteString("<mandate>\n")
	b.WriteString("Your definition of done for this task. You are NOT finished until every criterion below is checked off as passed (with evidence). Mark each with mandate_check the moment it's actually true; never tick one that isn't. When all pass, mandate_close — it will refuse if any criterion is still open. ")
	if m.HighStakes {
		b.WriteString("This is HIGH-STAKES: run mandate_verify (a fresh, independent pass audits your result) before you can close it. ")
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Mandate: %s (id=%s)\n", m.Title, m.ID)
	if strings.TrimSpace(m.Summary) != "" {
		fmt.Fprintf(&b, "  %s\n", trimLine(m.Summary, 240))
	}
	passed := 0
	for _, c := range m.Criteria {
		marker := "[ ]"
		switch c.Status {
		case CritPass:
			marker = "[x]"
			passed++
		case CritFail:
			marker = "[✗]"
		}
		line := fmt.Sprintf("  %s %s (id=%s)", marker, trimLine(c.Text, 160), c.ID)
		if c.Status == CritPass && strings.TrimSpace(c.Evidence) != "" {
			line += fmt.Sprintf(" — %s", trimLine(c.Evidence, 120))
		}
		b.WriteString(line + "\n")
	}
	fmt.Fprintf(&b, "Progress: %d/%d criteria passed.\n", passed, len(m.Criteria))
	b.WriteString("</mandate>")
	return b.String(), nil
}

func trimLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}
