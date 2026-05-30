package initiative

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// LocalTimeProvider implements agent.MemoryProvider and injects the boss's
// current local time + timezone every turn so Jarvis answers in the boss's
// frame of reference (e.g. "9:30pm" / "Thu 5 Jun 7am CT") instead of UTC
// ISO timestamps the boss can't read at a glance.
//
// Why a provider and not a hardwired loop edit:
//
//   - The agent should reason about time, not have it filtered by Go code.
//     Telling the model "it's currently 4:32pm CDT on Thu 5 Jun 2026" once
//     per turn is the same pattern as the goals / loop / project providers.
//   - One env var (INFINITY_USER_TIMEZONE) controls it. Default is
//     America/Chicago because the boss lives on CST/CDT; override if he
//     travels. Bad IANA names fall back to UTC silently rather than
//     crashing the prompt build.
//
// The block also carries a one-line nudge ("Speak about time in this
// frame…") so soul.md's general "use local time" rule has a concrete
// anchor every turn instead of relying on the agent to remember what TZ
// the boss is in.
type LocalTimeProvider struct {
	loc *time.Location
}

// NewLocalTimeProvider builds the provider from INFINITY_USER_TIMEZONE
// (IANA name like "America/Chicago"). Returns a provider with loc=UTC
// when unset/invalid so the agent at least sees something authoritative.
func NewLocalTimeProvider() *LocalTimeProvider {
	tz := strings.TrimSpace(os.Getenv("INFINITY_USER_TIMEZONE"))
	if tz == "" {
		tz = "America/Chicago"
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}
	return &LocalTimeProvider{loc: loc}
}

var _ agent.MemoryProvider = (*LocalTimeProvider)(nil)

func (p *LocalTimeProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.loc == nil {
		return "", nil
	}
	now := time.Now().In(p.loc)
	// e.g. "Thu 5 Jun 2026, 4:32pm CDT (America/Chicago)"
	abbrev, _ := now.Zone()
	pretty := strings.ToLower(now.Format("3:04pm"))
	stamp := fmt.Sprintf("%s, %s %s",
		now.Format("Mon 2 Jan 2006"), pretty, abbrev)

	var b strings.Builder
	b.WriteString("<current_time>\n")
	b.WriteString("Speak about time in the boss's local frame, NOT UTC and NOT ISO timestamps. Use casual short forms like \"9:30pm\", \"tomorrow 7am\", \"Thu 5 Jun 8am CT\".\n")
	fmt.Fprintf(&b, "now=%s\n", stamp)
	fmt.Fprintf(&b, "timezone=%s (%s)\n", p.loc.String(), abbrev)
	b.WriteString("</current_time>")
	return b.String(), nil
}
