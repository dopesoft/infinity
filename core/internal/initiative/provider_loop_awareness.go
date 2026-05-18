package initiative

import (
	"context"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/agent"
)

// LoopAwarenessProvider implements agent.MemoryProvider and surfaces the
// session's own tool-call rate every turn so the agent can self-throttle
// BEFORE the LoopGate hard-blocks. This is the primary line of defense -
// the gate is the safety net.
//
// Two states show up in the prompt:
//
//   - Below ~50% of the ceiling: nothing emitted. No prompt bloat.
//   - At or above ~50%: a <call_rate> block tells the agent how close it
//     is to the session ceiling and how many times its most-recent call
//     has repeated. The agent is expected to consolidate, batch, or stop.
//
// The provider depends on a *LoopGate (interface-narrowed) so the gate is
// the single source of truth for "what has this session been doing." No
// DB reads on the hot path.
type LoopAwarenessProvider struct {
	gate loopStatsSource
}

// loopStatsSource is the narrow interface the provider needs from the
// gate. Lets tests fake it without standing up a full gate.
type loopStatsSource interface {
	StatsFor(sessionID string) SessionStats
}

func NewLoopAwarenessProvider(gate *LoopGate) *LoopAwarenessProvider {
	if gate == nil {
		return nil
	}
	return &LoopAwarenessProvider{gate: gate}
}

var _ agent.MemoryProvider = (*LoopAwarenessProvider)(nil)

func (p *LoopAwarenessProvider) BuildSystemPrefix(ctx context.Context, sessionID, query string) (string, error) {
	if p == nil || p.gate == nil || sessionID == "" {
		return "", nil
	}
	stats := p.gate.StatsFor(sessionID)
	if stats.Ceiling <= 0 {
		return "", nil
	}
	// Quiet until we cross ~50% of either limit. Below that the block
	// would just be noise on healthy sessions.
	callsHalf := stats.Ceiling / 2
	if callsHalf < 1 {
		callsHalf = 1
	}
	hashHalf := stats.RepeatLimit / 2
	if hashHalf < 1 {
		hashHalf = 1
	}
	if stats.CallsInWindow < callsHalf && stats.TopHashCount < hashHalf {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("<call_rate>\n")
	b.WriteString("You're tracking your own tool-call rate. Self-throttle BEFORE you hit the gate: consolidate, batch, or stop if the answer hasn't changed.\n")
	fmt.Fprintf(&b, "calls_in_%ds=%d/%d (session ceiling - approval required above this)\n",
		stats.WindowSeconds, stats.CallsInWindow, stats.Ceiling)
	if stats.TopHashCount > 0 {
		tool := stats.TopHashTool
		if tool == "" {
			tool = "(same call)"
		}
		fmt.Fprintf(&b, "most_repeated_in_60s=%s × %d (hard-blocked at %d - change inputs or stop)\n",
			tool, stats.TopHashCount, stats.RepeatLimit)
	}
	b.WriteString("</call_rate>")
	return b.String(), nil
}
