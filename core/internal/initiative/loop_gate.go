// Package initiative — runaway-loop guardrail.
//
// The earlier BudgetGate watched USD spend, which was the wrong signal for
// this setup: web_search/http_fetch route through the ChatGPT plan and
// Composio's free tier covers the SaaS calls. The real failure mode is the
// agent stuck repeating the SAME call ten times in a row, or burning
// hundreds of tool calls in a single session with no progress. Both happen
// when a sub-agent flow miswires or when a skill doesn't recognise it's
// gotten its answer.
//
// LoopGate is purely defensive. It keeps an in-memory rolling window of
// recent tool calls per session and trips on two signals:
//
//  1. Same exact call (tool + canonicalised input hash) fired ≥3 times in
//     the last 60s in this session. This is the classic retry loop. Hard
//     block with a clear error so the model sees what happened.
//
//  2. Session-wide tool calls in the last 5 min exceed sessionCallCeiling
//     (default 50). Routes through Trust queue so the boss can approve
//     "keep going" or kill the session. Useful for legitimate but heavy
//     workflows.
//
// No cost queries, no DB reads on the hot path. Eviction runs lazily on
// every Authorize call so the map can't grow unbounded.
package initiative

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/proactive"
)

// Tunables. The defaults are the "feels right" values for a single-user
// agent that occasionally runs multi-tool skills; bump
// repeatWindow/sessionWindow if you legitimately need higher throughput.
const (
	repeatWindow   = 60 * time.Second
	repeatLimit    = 3 // same exact call this many times in repeatWindow → block

	sessionWindow      = 5 * time.Minute
	sessionCallCeiling = 50 // total tool calls in sessionWindow → Trust gate
)

// LoopGate is the agent.ToolGate that catches retry loops + runaway
// sessions. Pluggable on top of the existing GateChain - every tool call
// passes through it AFTER the per-MCP gates have had their say.
//
// LoopGate is the SAFETY NET. The primary defense is the agent itself -
// it sees its own call rate every turn via LoopAwarenessProvider (built
// on top of the same `calls` map) and is expected to self-throttle.
// LoopGate only fires when the agent fails to.
type LoopGate struct {
	trust *proactive.TrustStore

	mu      sync.Mutex
	calls   map[string][]callEntry // key = sessionID, value = recent calls
}

type callEntry struct {
	hash string
	tool string
	at   time.Time
}

func NewLoopGate(trust *proactive.TrustStore) *LoopGate {
	return &LoopGate{
		trust: trust,
		calls: make(map[string][]callEntry),
	}
}

func (g *LoopGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || sessionID == "" {
		return agent.GateDecision{Allow: true}
	}
	now := time.Now()
	hash := callHash(toolName, input)

	g.mu.Lock()
	entries := pruneCalls(g.calls[sessionID], now)
	repeatCount := 0
	for _, e := range entries {
		if e.hash == hash && now.Sub(e.at) <= repeatWindow {
			repeatCount++
		}
	}
	sessionCount := len(entries) // already pruned to sessionWindow
	// Record the new call regardless of decision so the next iteration sees
	// the increased count. Blocked calls still count as "the agent tried"
	// - if it keeps trying, the count keeps climbing and we keep blocking.
	entries = append(entries, callEntry{hash: hash, tool: toolName, at: now})
	g.calls[sessionID] = entries
	g.mu.Unlock()

	if repeatCount >= repeatLimit {
		return agent.GateDecision{
			Allow: false,
			Reason: fmt.Sprintf("loop detected: %s with these exact inputs has fired %d times in the last %s. Stop. Change the input, try a different approach, or ask the boss what to do.",
				toolName, repeatCount+1, repeatWindow),
		}
	}
	if sessionCount+1 > sessionCallCeiling {
		return g.gateThroughTrust(ctx, sessionID, project, toolName, sessionCount+1)
	}
	return agent.GateDecision{Allow: true}
}

func (g *LoopGate) gateThroughTrust(ctx context.Context, sessionID, project, toolName string, count int) agent.GateDecision {
	if g.trust == nil {
		return agent.GateDecision{
			Allow:  false,
			Reason: fmt.Sprintf("session has run %d tool calls in the last %s, suggesting a runaway loop. Trust store not configured; refusing to keep going.", count, sessionWindow),
		}
	}
	title := fmt.Sprintf("Session is busy (%d tool calls in %s) - keep going?", count, sessionWindow)
	rationale := fmt.Sprintf("This session has fired %d tool calls in the last %s. That's above the %d threshold. Approve to keep working (you might be in a legitimate heavy workflow), or deny to stop.",
		count, sessionWindow, sessionCallCeiling)
	id, err := g.trust.Queue(ctx, &proactive.TrustContract{
		Title:     title,
		RiskLevel: "medium",
		Source:    "loop_gate",
		ActionSpec: map[string]any{
			"tool":          toolName,
			"session_id":    sessionID,
			"project":       project,
			"calls_in_window": count,
			"window_seconds": int(sessionWindow.Seconds()),
		},
		Reasoning: rationale,
		Preview:   fmt.Sprintf("Allow %s and keep this session running?", toolName),
	})
	if err != nil || id == "" {
		return agent.GateDecision{
			Allow:  false,
			Reason: fmt.Sprintf("session loop threshold (%d calls / %s) hit and trust queue unavailable; pausing", count, sessionWindow),
		}
	}
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval (session call rate exceeded)",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     5 * time.Minute,
		Preview:         fmt.Sprintf("session ran %d tool calls in %s", count, sessionWindow),
	}
}

func (g *LoopGate) WaitForDecision(ctx context.Context, contractID string, timeout time.Duration) (bool, string) {
	if g == nil || g.trust == nil || contractID == "" {
		return false, "trust store not configured"
	}
	if timeout <= 0 {
		timeout = 5 * time.Minute
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	tick := time.NewTicker(1 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-waitCtx.Done():
			return false, "timed out waiting for loop-gate approval (" + timeout.String() + ")"
		case <-tick.C:
			status, sessionID, toolName, err := g.trust.LookupForGate(waitCtx, contractID)
			if err != nil {
				continue
			}
			switch status {
			case "approved":
				_, _ = g.trust.ConsumeApprovedForTool(waitCtx, sessionID, toolName)
				return true, ""
			case "denied":
				return false, "denied by the boss (looked like a loop)"
			case "snoozed":
				return false, "snoozed by the boss"
			case "consumed":
				return true, ""
			default:
				continue
			}
		}
	}
}

// pruneCalls drops entries older than the larger of the two windows so the
// per-session slice stays bounded. Cheap O(n) walk; n stays tiny because
// the ceiling itself caps the working set.
func pruneCalls(entries []callEntry, now time.Time) []callEntry {
	cutoff := now.Add(-sessionWindow)
	// Entries are append-ordered so we can binary-search the first one we
	// keep. Slice is rarely big enough to need that - linear is fine.
	idx := 0
	for ; idx < len(entries); idx++ {
		if !entries[idx].at.Before(cutoff) {
			break
		}
	}
	if idx == 0 {
		return entries
	}
	return append(entries[:0:0], entries[idx:]...)
}

// SessionStats summarises this session's recent tool-call activity for
// the awareness provider. Top hash carries the most-repeated call so the
// agent can see what it has been hammering.
type SessionStats struct {
	CallsInWindow int
	WindowSeconds int
	Ceiling       int
	TopHashCount  int
	TopHashTool   string
	RepeatLimit   int
}

// StatsFor reads the current rolling stats for sessionID. Cheap, safe to
// call every turn from the system-prefix path. Returns a zero value when
// the session has no recorded calls yet.
func (g *LoopGate) StatsFor(sessionID string) SessionStats {
	if g == nil || sessionID == "" {
		return SessionStats{WindowSeconds: int(sessionWindow.Seconds()), Ceiling: sessionCallCeiling, RepeatLimit: repeatLimit}
	}
	now := time.Now()
	g.mu.Lock()
	entries := pruneCalls(g.calls[sessionID], now)
	g.calls[sessionID] = entries
	// Most-repeated (hash → tool, count) inside the repeat window.
	tally := make(map[string]int, len(entries))
	toolForHash := make(map[string]string, len(entries))
	for _, e := range entries {
		if now.Sub(e.at) > repeatWindow {
			continue
		}
		tally[e.hash]++
		toolForHash[e.hash] = e.tool
	}
	topHash, topCount := "", 0
	for h, n := range tally {
		if n > topCount {
			topHash = h
			topCount = n
		}
	}
	g.mu.Unlock()
	return SessionStats{
		CallsInWindow: len(entries),
		WindowSeconds: int(sessionWindow.Seconds()),
		Ceiling:       sessionCallCeiling,
		TopHashCount:  topCount,
		TopHashTool:   toolForHash[topHash],
		RepeatLimit:   repeatLimit,
	}
}

// callHash collapses (tool, input) into a stable identity so two calls
// with the same arguments hash the same way even when Go's map iteration
// order shuffles. JSON marshalling with sorted keys is the standard
// canonicalisation; we don't bother with a full RFC 8785 jcs because the
// signal is already noisy.
func callHash(tool string, input map[string]any) string {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0})
	if len(input) > 0 {
		keys := make([]string, 0, len(input))
		for k := range input {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			h.Write([]byte(k))
			h.Write([]byte{0})
			b, _ := json.Marshal(input[k])
			h.Write(b)
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}
