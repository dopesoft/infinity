package voyager

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/hooks"
)

// Real-time skill-discovery hook.
//
// Hermes only inspects sessions at SessionEnd. Infinity additionally watches
// every PostToolUse: per-session sliding window of recent tool names, then a
// global counter of consecutive triplets. When the same triplet shows up
// across enough sessions in a sliding window, that's a procedure crystallizing
// in front of us - we propose it as a candidate immediately, before the
// session even ends.
//
// Tunables:
//
//	windowSize       - how many recent tools per session we remember (default 8)
//	tripletMinHits   - how many session-distinct firings before we propose (default 3)
//	tripletWindowMin - how long a triplet's hit count is valid (default 60min)
//
// The proposal it writes is light - just a hint with the triplet and counts.
// SessionEnd extraction is still the place that produces a real SKILL.md.

const (
	windowSize       = 8
	tripletMinHits   = 3
	tripletWindowMin = 60
)

// OnPostToolUse is the hook handler. Wire as:
//
//	pipeline.RegisterFunc("voyager.discover", m.OnPostToolUse, hooks.PostToolUse)
func (m *Manager) OnPostToolUse(ctx context.Context, ev hooks.Event) error {
	if !m.Enabled() {
		return nil
	}
	name := ""
	if ev.Payload != nil {
		if v, ok := ev.Payload["name"].(string); ok {
			name = v
		}
	}
	if name == "" || ev.SessionID == "" {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Slide the per-session window.
	w := m.sessionWindows[ev.SessionID]
	w = append(w, toolEvent{name: name, at: time.Now()})
	if len(w) > windowSize {
		w = w[len(w)-windowSize:]
	}
	m.sessionWindows[ev.SessionID] = w

	// Need at least 3 events to form a triplet.
	if len(w) < 3 {
		return nil
	}
	last3 := w[len(w)-3:]
	key := tripletKey(last3[0].name, last3[1].name, last3[2].name)

	tc, ok := m.tripletCounters[key]
	if !ok {
		tc = &tripletCounter{
			tools:    [3]string{last3[0].name, last3[1].name, last3[2].name},
			first:    time.Now(),
			sessions: map[string]struct{}{},
		}
		m.tripletCounters[key] = tc
	}

	// Decay: if the window's gone stale, reset and start over.
	if time.Since(tc.first) > time.Duration(tripletWindowMin)*time.Minute {
		tc.first = time.Now()
		tc.hits = 0
		tc.sessions = map[string]struct{}{}
	}

	if _, seen := tc.sessions[ev.SessionID]; !seen {
		tc.sessions[ev.SessionID] = struct{}{}
		tc.hits++
	}
	tc.lastHit = time.Now()

	if tc.hits >= tripletMinHits {
		// Reset hit counter so we don't re-propose every event after threshold.
		tc.hits = 0
		tc.sessions = map[string]struct{}{}
		go m.recordTripletProposal(tc.tools)
	}
	return nil
}

func tripletKey(a, b, c string) string {
	return strings.ToLower(a) + "|" + strings.ToLower(b) + "|" + strings.ToLower(c)
}

func (m *Manager) recordTripletProposal(tools [3]string) {
	if m == nil || m.pool == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	name := fmt.Sprintf("triplet_%s_%s_%s", safeName(tools[0]), safeName(tools[1]), safeName(tools[2]))
	desc := fmt.Sprintf("Repeated tool sequence: %s → %s → %s", tools[0], tools[1], tools[2])
	reasoning := fmt.Sprintf("Discovery hook fired: this triplet appeared in %d separate sessions within %dmin.",
		tripletMinHits, tripletWindowMin)

	// Idempotent on (name) so repeat detections only update timestamps.
	_, _ = m.pool.Exec(ctx, `
		INSERT INTO mem_skill_proposals
		  (name, description, reasoning, skill_md, risk_level, status)
		VALUES ($1, $2, $3, '', 'low', 'candidate')
		ON CONFLICT DO NOTHING
	`, name, desc, reasoning)
}
