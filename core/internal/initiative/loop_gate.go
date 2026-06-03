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

	// shapeLimit catches the counter-varying junk loop the exact-hash guard
	// misses: a model hammering claude_code__Read on /tmp/nonexistent28,
	// /tmp/nonexistent27, … each hashes differently so repeatLimit never trips,
	// but they share one "shape" once digit runs are normalised to '#'. Real
	// distinct calls (hex Gmail ids like 19e8545851a735bf keep their letters)
	// stay unique, so this only fires on genuine incrementing-counter loops.
	shapeLimit = 10 // same digit-normalised shape this many times in repeatWindow → block

	sessionWindow      = 5 * time.Minute
	sessionCallCeiling = 50 // total NON-code-write calls in sessionWindow → Trust gate (chat sessions)

	// graceWindow is how long a boss "keep going" approval keeps the session
	// running before we ask again. Approving grants another full ceiling's
	// worth of calls (see grantGrace) so "Approve" means KEEP GOING — not
	// "run exactly one more call then re-prompt". Without this every call past
	// the ceiling queued a fresh approval card; the boss saw 28 identical
	// "keep going?" prompts for one runaway. The repeat + shape guards still
	// hard-block tight loops inside the grace window, so a true runaway can't
	// abuse the grant.
	graceWindow = 10 * time.Minute

	// codingCallCeiling is the much higher ceiling used once a session is
	// recognised as a coding session (it has a project, or it has fired any
	// code-write tool). Writing a real app is legitimately hundreds of tool
	// calls; the flat chat ceiling was throttling builds mid-flight. The
	// same-call repeat guard (repeatLimit) is the real anti-loop and stays
	// in force for every session regardless. This same ceiling also applies to
	// HEAVY WORKFLOW sessions — ones that have invoked a skill (see
	// heavyWorkflowTools). A skill is a defined multi-step recipe (inbox backfill
	// across 4 mailboxes × 11 days, research sweeps, …) that legitimately needs
	// far more than the 50-call chat budget; pinning skill execution to the
	// bare-chat ceiling is exactly why a "backfill since may 22" died at 50
	// calls. The repeat + shape guards still hard-block real loops at any
	// ceiling, so this only buys headroom for genuine structured work.
	codingCallCeiling = 400
)

// heavyWorkflowTools mark a session as a legitimate heavy workflow (NOT a
// runaway) so the high ceiling applies. Invoking a skill is the signal: the
// agent is following a defined recipe, not spraying ad-hoc calls. Unlike
// codeWriteTools these DO count toward the ceiling (they're real work) — they
// only lift the ceiling, they aren't exempt from it.
var heavyWorkflowTools = map[string]struct{}{
	"skills_invoke": {},
	"skill_run":     {},
}

// isHeavyWorkflowTool reports whether a tool marks the session as a heavy
// workflow (a skill execution), case-insensitively.
func isHeavyWorkflowTool(tool string) bool {
	_, ok := heavyWorkflowTools[strings.ToLower(tool)]
	return ok
}

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

// idempotentMaintenanceTools are self-housekeeping verbs that are SAFE to call
// redundantly: the call just no-ops and tells the agent so. Repeating one is
// never a runaway — it's the model being over-eager about hygiene — so the
// repeat hard-block must NOT fire for them. Without this exemption a model that
// reflexively re-calls compact_context (nudged when its buffer feels heavy) trips
// the loop guard at 3 and the WHOLE TURN dies before the real task finishes —
// the exact inbox-triage failure. They still count toward the session ceiling,
// so a genuine spam-storm is caught there.
var idempotentMaintenanceTools = map[string]struct{}{
	"compact_context": {},
}

func isIdempotentMaintenanceTool(tool string) bool {
	_, ok := idempotentMaintenanceTools[strings.ToLower(tool)]
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
	// grace holds the boss's "keep going" approvals: while grace[session] is
	// active, calls up to its capCount pass without a fresh approval card.
	grace map[string]graceGrant
	// pending dedupes approval cards: at most one open loop_gate contract per
	// session. While set, gateThroughTrust reuses the same contract id instead
	// of stacking a new card for every call past the ceiling.
	pending map[string]string
}

type callEntry struct {
	hash  string
	shape string
	tool  string
	at    time.Time
}

// graceGrant records a boss "keep going" approval for a session: until `until`,
// non-code-write calls up to `capCount` pass without re-queuing an approval.
type graceGrant struct {
	until    time.Time
	capCount int
}

func NewLoopGate(trust *proactive.TrustStore) *LoopGate {
	return &LoopGate{
		trust:       trust,
		chatCeiling: envInt("INFINITY_SESSION_CALL_CEILING", sessionCallCeiling),
		codeCeiling: envInt("INFINITY_CODING_CALL_CEILING", codingCallCeiling),
		calls:       make(map[string][]callEntry),
		grace:       make(map[string]graceGrant),
		pending:     make(map[string]string),
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
	shape := shapeHash(toolName, input)
	thisIsCodeWrite := isCodeWriteTool(toolName)

	g.mu.Lock()
	entries := pruneCalls(g.calls[sessionID], now)
	repeatCount := 0
	shapeCount := 0  // digit-normalised matches (the counter-varying loop signal)
	countedCount := 0 // non-code-write calls in window (the runaway signal)
	// "coding" here means "heavy workflow" — a session that gets the high
	// ceiling because it's writing code OR executing a skill (a defined recipe).
	coding := project != "" || thisIsCodeWrite || isHeavyWorkflowTool(toolName)
	for _, e := range entries {
		if e.hash == hash && now.Sub(e.at) <= repeatWindow {
			repeatCount++
		}
		if e.shape == shape && now.Sub(e.at) <= repeatWindow {
			shapeCount++
		}
		if isCodeWriteTool(e.tool) {
			coding = true // a session that has written code is a coding session
		} else {
			countedCount++
		}
		if isHeavyWorkflowTool(e.tool) {
			coding = true // a session running a skill is a heavy workflow
		}
	}
	// Record the new call regardless of decision so the next iteration sees
	// the increased count. Blocked calls still count as "the agent tried"
	// - if it keeps trying, the count keeps climbing and we keep blocking.
	entries = append(entries, callEntry{hash: hash, shape: shape, tool: toolName, at: now})
	g.calls[sessionID] = entries
	graceActive := false
	graceCap := 0
	if gr, ok := g.grace[sessionID]; ok && now.Before(gr.until) {
		graceActive = true
		graceCap = gr.capCount
	}
	g.mu.Unlock()

	// Repeat guard is universal — it fires for EVERY tool, code-write
	// included, because re-running the identical call is the real loop. This
	// is unchanged by the coding-aware ceiling below. The ONE exception is
	// idempotent maintenance verbs (compact_context): repeating one is a
	// harmless no-op, not a runaway, so blocking it — which ends the turn —
	// would be worse than letting it fall through to its own no-op response.
	if repeatCount >= repeatLimit && !isIdempotentMaintenanceTool(toolName) {
		return agent.GateDecision{
			Allow: false,
			Reason: fmt.Sprintf("loop detected: %s with these exact inputs has fired %d times in the last %s. Stop. Change the input, try a different approach, or ask the boss what to do.",
				toolName, repeatCount+1, repeatWindow),
		}
	}
	// Shape guard catches the counter-varying loop the exact-hash guard can't:
	// the same tool fired with inputs that differ only by an incrementing number
	// (/tmp/nonexistent28, 27, 26 …). Code-write tools are exempt — a scaffold
	// legitimately writes file1.go, file2.go, … in sequence; the exact-repeat
	// guard still covers a stuck rewrite of the identical file.
	if !thisIsCodeWrite && shapeCount >= shapeLimit {
		return agent.GateDecision{
			Allow: false,
			Reason: fmt.Sprintf("loop detected: %s has fired %d times in the last %s with inputs that differ only by a counter — this is a runaway, not progress. Stop and change approach, or ask the boss.",
				toolName, shapeCount+1, repeatWindow),
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
		// A live "keep going" approval lets the session run up to its granted
		// cap before we ask again — so Approve means KEEP GOING, not "one more
		// call". The repeat + shape guards above still hard-block tight loops
		// inside the grace window, so this can't green-light a true runaway.
		if graceActive && newCounted <= graceCap {
			return agent.GateDecision{Allow: true}
		}
		return g.gateThroughTrust(ctx, sessionID, project, toolName, newCounted, coding)
	}
	return agent.GateDecision{Allow: true}
}

func (g *LoopGate) gateThroughTrust(ctx context.Context, sessionID, project, toolName string, count int, coding bool) agent.GateDecision {
	if g.trust == nil {
		return agent.GateDecision{
			Allow:  false,
			Reason: fmt.Sprintf("session has run %d tool calls in the last %s, suggesting a runaway loop. Trust store not configured; refusing to keep going.", count, sessionWindow),
		}
	}
	// Dedupe: at most one open "keep going?" card per session. If one is
	// already pending, reuse it instead of stacking a fresh card for every
	// call past the ceiling (the boss was drowning in 28 identical prompts).
	g.mu.Lock()
	existing := g.pending[sessionID]
	g.mu.Unlock()
	if existing != "" {
		if status, _, _, err := g.trust.LookupForGate(ctx, existing); err == nil && status == "pending" {
			return agent.GateDecision{
				Allow:           false,
				Reason:          "awaiting boss approval (session call rate exceeded)",
				ContractID:      existing,
				WaitForApproval: true,
				WaitTimeout:     5 * time.Minute,
				Preview:         fmt.Sprintf("session ran %d tool calls in %s", count, sessionWindow),
			}
		}
		// Stale (decided/expired) — drop it so we can queue a fresh one.
		g.mu.Lock()
		delete(g.pending, sessionID)
		g.mu.Unlock()
	}
	ceiling := g.effectiveCeiling(coding)
	title := fmt.Sprintf("Session is busy (%d tool calls in %s) - keep going?", count, sessionWindow)
	rationale := fmt.Sprintf("This session has fired %d tool calls in the last %s. That's above the %d threshold. Approve and it KEEPS GOING for the next %s (another ~%d calls) without re-asking; deny to stop.",
		count, sessionWindow, ceiling, graceWindow, ceiling)
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
	g.mu.Lock()
	g.pending[sessionID] = id
	g.mu.Unlock()
	return agent.GateDecision{
		Allow:           false,
		Reason:          "awaiting boss approval (session call rate exceeded)",
		ContractID:      id,
		WaitForApproval: true,
		WaitTimeout:     5 * time.Minute,
		Preview:         fmt.Sprintf("session ran %d tool calls in %s", count, sessionWindow),
	}
}

// grantGrace records a boss "keep going" approval: the session may run up to
// another full ceiling's worth of non-code-write calls over the next
// graceWindow before we ask again. Called when a loop_gate contract is
// approved. Also clears the pending card so the next over-ceiling call (once
// the grace is used up) can queue a fresh prompt.
func (g *LoopGate) grantGrace(sessionID string) {
	if g == nil || sessionID == "" {
		return
	}
	now := time.Now()
	g.mu.Lock()
	defer g.mu.Unlock()
	entries := pruneCalls(g.calls[sessionID], now)
	g.calls[sessionID] = entries
	counted, coding := 0, false
	for _, e := range entries {
		if isCodeWriteTool(e.tool) {
			coding = true
		} else {
			counted++
		}
		if isHeavyWorkflowTool(e.tool) {
			coding = true
		}
	}
	g.grace[sessionID] = graceGrant{
		until:    now.Add(graceWindow),
		capCount: counted + g.effectiveCeiling(coding),
	}
	delete(g.pending, sessionID)
}

// stopSession clears any pending card and grace for a session — called when the
// boss denies "keep going" so the session doesn't keep a stale grant around.
func (g *LoopGate) stopSession(sessionID string) {
	if g == nil || sessionID == "" {
		return
	}
	g.mu.Lock()
	delete(g.pending, sessionID)
	delete(g.grace, sessionID)
	g.mu.Unlock()
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
				// KEEP GOING: grant the session another full budget so the next
				// over-ceiling call passes without a fresh card.
				g.grantGrace(sessionID)
				return true, ""
			case "denied":
				g.stopSession(sessionID)
				return false, "denied by the boss (looked like a loop)"
			case "snoozed":
				g.stopSession(sessionID)
				return false, "snoozed by the boss"
			case "consumed":
				g.grantGrace(sessionID)
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
		if isHeavyWorkflowTool(e.tool) {
			coding = true // skill execution → heavy-workflow ceiling
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

// shapeHash is callHash with every run of digits inside a string value
// collapsed to a single '#'. It gives counter-varying calls a shared identity
// — /tmp/nonexistent28 and /tmp/nonexistent27 both become /tmp/nonexistent# —
// so a model incrementing a number to dodge the exact-repeat guard still trips
// the shape guard. Distinct calls keep their identity: hex ids like
// 19e8545851a735bf retain their letters, so different ids hash differently.
func shapeHash(tool string, input map[string]any) string {
	h := sha256.New()
	h.Write([]byte(tool))
	h.Write([]byte{0, 's'})
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
			h.Write([]byte(collapseDigits(string(b))))
			h.Write([]byte{0})
		}
	}
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// collapseDigits replaces every maximal run of ASCII digits with a single '#'.
func collapseDigits(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	inRun := false
	for i := 0; i < len(s); i++ {
		if s[i] >= '0' && s[i] <= '9' {
			if !inRun {
				b.WriteByte('#')
				inRun = true
			}
			continue
		}
		inRun = false
		b.WriteByte(s[i])
	}
	return b.String()
}
