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
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/proactive"
)

// Tunables. The defaults are the "feels right" values for a single-user
// agent that occasionally runs multi-tool skills; bump
// repeatWindow/sessionWindow if you legitimately need higher throughput.
const (
	repeatWindow = 60 * time.Second
	repeatLimit  = 3 // same exact call this many times in repeatWindow → block

	sessionWindow      = 5 * time.Minute
	sessionCallCeiling = 50 // total NON-code-write calls in sessionWindow → Trust gate (chat sessions)

	// codingCallCeiling is the much higher ceiling used once a session is
	// recognised as a coding session (it has a project, or it has fired any
	// code-write tool). Writing a real app is legitimately hundreds of tool
	// calls; the flat chat ceiling was throttling builds mid-flight. The
	// same-call repeat guard (repeatLimit) is the real anti-loop and stays
	// in force for every session regardless.
	codingCallCeiling = 400
)

// codeWriteTools are the file-mutating verbs that make up the legitimate
// bulk of a build. They do NOT count toward the session ceiling (a 200-file
// scaffold shouldn't trip a runaway-loop guard) and their presence is what
// flags a session as "coding" so the higher ceiling applies. The same-call
// repeat guard still covers them, so a stuck loop re-writing the identical
// file is still caught.
var codeWriteTools = map[string]struct{}{
	"fs_save":                   {},
	"fs_edit":                   {},
	"claude_code__write":        {},
	"claude_code__edit":         {},
	"claude_code__multiedit":    {},
	"claude_code__notebookedit": {},
	"project_create":            {},
	"document_create":           {},
}

// isCodeWriteTool reports whether a tool is a file-mutating code-write verb,
// case-insensitively (the MCP bridge spells them claude_code__Write while the
// native tools are lower_snake). Exempt from the ceiling count.
func isCodeWriteTool(tool string) bool {
	_, ok := codeWriteTools[strings.ToLower(tool)]
	return ok
}

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

	// chatCeiling / codeCeiling are the effective ceilings, env-overridable
	// so a heavy workflow can get headroom without a redeploy:
	//   INFINITY_SESSION_CALL_CEILING  → chat ceiling (default 50)
	//   INFINITY_CODING_CALL_CEILING   → coding ceiling (default 400)
	chatCeiling int
	codeCeiling int

	mu    sync.Mutex
	calls map[string][]callEntry // key = sessionID, value = recent calls
}

type callEntry struct {
	hash string
	tool string
	at   time.Time
}

func NewLoopGate(trust *proactive.TrustStore) *LoopGate {
	return &LoopGate{
		trust:       trust,
		chatCeiling: envInt("INFINITY_SESSION_CALL_CEILING", sessionCallCeiling),
		codeCeiling: envInt("INFINITY_CODING_CALL_CEILING", codingCallCeiling),
		calls:       make(map[string][]callEntry),
	}
}

// envInt reads a positive integer from an env var, falling back to def when
// unset or unparseable. Keeps the ceilings tunable without a redeploy.
func envInt(key string, def int) int {
	if raw := strings.TrimSpace(os.Getenv(key)); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 {
			return n
		}
	}
	return def
}

// effectiveCeiling returns the ceiling that applies to a session given
// whether it has been recognised as coding. Defends against a zero value
// from a partially-constructed gate (tests) by falling back to the consts.
func (g *LoopGate) effectiveCeiling(coding bool) int {
	if coding {
		if g.codeCeiling > 0 {
			return g.codeCeiling
		}
		return codingCallCeiling
	}
	if g.chatCeiling > 0 {
		return g.chatCeiling
	}
	return sessionCallCeiling
}

func (g *LoopGate) Authorize(ctx context.Context, sessionID, project, toolName string, input map[string]any) agent.GateDecision {
	if g == nil || sessionID == "" {
		return agent.GateDecision{Allow: true}
	}
	now := time.Now()
	hash := callHash(toolName, input)
	thisIsCodeWrite := isCodeWriteTool(toolName)

	g.mu.Lock()
	entries := pruneCalls(g.calls[sessionID], now)
	repeatCount := 0
	countedCount := 0 // non-code-write calls in window (the runaway signal)
	coding := project != "" || thisIsCodeWrite
	for _, e := range entries {
		if e.hash == hash && now.Sub(e.at) <= repeatWindow {
			repeatCount++
		}
		if isCodeWriteTool(e.tool) {
			coding = true // a session that has written code is a coding session
		} else {
			countedCount++
		}
	}
	// Record the new call regardless of decision so the next iteration sees
	// the increased count. Blocked calls still count as "the agent tried"
	// - if it keeps trying, the count keeps climbing and we keep blocking.
	entries = append(entries, callEntry{hash: hash, tool: toolName, at: now})
	g.calls[sessionID] = entries
	g.mu.Unlock()

	// Repeat guard is universal — it fires for EVERY tool, code-write
	// included, because re-running the identical call is the real loop. This
	// is unchanged by the coding-aware ceiling below.
	if repeatCount >= repeatLimit {
		return agent.GateDecision{
			Allow: false,
			Reason: fmt.Sprintf("loop detected: %s with these exact inputs has fired %d times in the last %s. Stop. Change the input, try a different approach, or ask the boss what to do.",
				toolName, repeatCount+1, repeatWindow),
		}
	}
	// Session ceiling counts only non-code-write calls, against a ceiling
	// that's much higher for coding sessions. A build firing hundreds of
	// fs_save/edit calls no longer trips "keep going?"; a chat session
	// spraying web_search/http_fetch still does.
	newCounted := countedCount
	if !thisIsCodeWrite {
		newCounted++
	}
	if newCounted > g.effectiveCeiling(coding) {
		return g.gateThroughTrust(ctx, sessionID, project, toolName, newCounted)
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
			"tool":            toolName,
			"session_id":      sessionID,
			"project":         project,
			"calls_in_window": count,
			"window_seconds":  int(sessionWindow.Seconds()),
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
	countedCount := 0 // non-code-write calls — the figure the ceiling tracks
	coding := false
	for _, e := range entries {
		if isCodeWriteTool(e.tool) {
			coding = true
		} else {
			countedCount++
		}
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
	// Report the SAME counted/ceiling the gate enforces, so the agent's
	// self-throttle signal matches the actual trip point. Code-write calls
	// are excluded from the count and lift the session to the coding ceiling.
	return SessionStats{
		CallsInWindow: countedCount,
		WindowSeconds: int(sessionWindow.Seconds()),
		Ceiling:       g.effectiveCeiling(coding),
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
