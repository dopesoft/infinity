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
	// Don't even track degenerate triplets. A reusable workflow is a sequence
	// of DISTINCT, meaningful tools — not the agent doing ordinary mechanics
	// (read a file, run bash, edit a file). Same-tool-thrice (bash→bash→bash)
	// and all-primitive combos (claude_code__*/fs_* file & shell ops) are
	// normal work, not a named capability. Minting "triplet_*" proposals for
	// them just floods the candidate queue with noise the boss has to reject
	// (46 such junk candidates accumulated by 2026-06-01). Skip them entirely.
	if !tripletWorthTracking(last3[0].name, last3[1].name, last3[2].name) {
		return nil
	}
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

// primitiveTripletTools are the raw file/shell/read mechanics the agent runs
// constantly as part of ordinary work. A triplet made up only of these is not
// a reusable named workflow, so it must never become a skill proposal.
var primitiveTripletTools = map[string]bool{
	"claude_code__bash": true, "claude_code__read": true, "claude_code__edit": true,
	"claude_code__write": true, "claude_code__ls": true, "claude_code__glob": true,
	"claude_code__grep": true,
	"fs_read": true, "fs_ls": true, "fs_write": true, "fs_edit": true,
	"fs_glob": true, "fs_grep": true,
	"bash_run": true, "read": true, "edit": true, "write": true, "bash": true,
}

// tripletWorthTracking rejects degenerate triplets: all-identical (e.g.
// bash→bash→bash) or all-primitive (only file/shell/read ops). Anything with a
// meaningful, distinct tool in it still flows through to discovery.
func tripletWorthTracking(a, b, c string) bool {
	la, lb, lc := strings.ToLower(a), strings.ToLower(b), strings.ToLower(c)
	if la == lb && lb == lc {
		return false
	}
	if primitiveTripletTools[la] && primitiveTripletTools[lb] && primitiveTripletTools[lc] {
		return false
	}
	return true
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
		  (name, description, reasoning, skill_md, risk_level, importance, importance_reason, status)
		VALUES ($1, $2, $3, '', 'low', 70, 'Repeated tool triplet; review for reusable workflow automation value.', 'candidate')
		ON CONFLICT DO NOTHING
	`, name, desc, reasoning)

	// Activate mem_patterns: the heartbeat's "open patterns" check reads this
	// table but nothing wrote it. Record the detected recurrence (bumping
	// occurrences on repeat) with the skill we'd suggest, so the agent's
	// pattern-noticing becomes visible and actionable instead of a dead query.
	_, _ = m.pool.Exec(ctx, `
		INSERT INTO mem_patterns (description, occurrences, suggested_skill, last_seen_at, status)
		VALUES ($1, $2, $3, NOW(), 'open')
		ON CONFLICT (description) DO UPDATE SET
		  occurrences   = mem_patterns.occurrences + 1,
		  suggested_skill = EXCLUDED.suggested_skill,
		  last_seen_at  = NOW(),
		  status        = 'open'
	`, desc, tripletMinHits, name)
}
