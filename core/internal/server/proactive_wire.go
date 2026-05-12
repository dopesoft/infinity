package server

import (
	"context"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/dopesoft/infinity/core/internal/proactive"
)

// registerSession marks a WS connection as active under sessionID and binds
// it to a send function. The heartbeat broadcaster calls send when a finding
// crosses the proactive threshold so the browser tab gets an unprompted
// assistant turn — that's the wire that turns "responds when asked" into
// "speaks first."
//
// Multiple tabs sharing one sessionID is legal; the last registration wins.
// In practice studio uses one tab per active session.
func (s *Server) registerSession(sessionID string, send func(wsServerEvent)) {
	if s == nil || sessionID == "" || send == nil {
		return
	}
	s.activeMu.Lock()
	s.activeSessions[sessionID] = send
	s.activeMu.Unlock()
}

// unregisterSession removes a WS binding. Called when the WS connection
// closes (browser navigated away, network flap, tab closed). Heartbeat
// findings emitted after this point have no live target for that session
// but still land in mem_heartbeat_findings for the next time the boss
// opens Studio.
func (s *Server) unregisterSession(sessionID string, send func(wsServerEvent)) {
	if s == nil || sessionID == "" {
		return
	}
	s.activeMu.Lock()
	if cur, ok := s.activeSessions[sessionID]; ok {
		/* Same-pointer guard so a stale unregister can't evict a fresh
		 * connection that arrived during a reconnect race. */
		if fmt.Sprintf("%p", cur) == fmt.Sprintf("%p", send) {
			delete(s.activeSessions, sessionID)
		}
	}
	s.activeMu.Unlock()
}

// broadcastProactive pushes the same event to every active WS session.
// Heartbeat findings broadcast to all open sessions — there's only one
// boss, so multi-tab fanout is the desired behaviour (whichever tab is
// foregrounded reads it first).
func (s *Server) broadcastProactive(ev wsServerEvent) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	sends := make([]func(wsServerEvent), 0, len(s.activeSessions))
	sids := make([]string, 0, len(s.activeSessions))
	for sid, fn := range s.activeSessions {
		sends = append(sends, fn)
		sids = append(sids, sid)
	}
	s.activeMu.Unlock()
	for i, fn := range sends {
		ev.SessionID = sids[i]
		fn(ev)
	}
}

// onHeartbeatFinding is wired in New() as the heartbeat's per-finding
// callback. We filter aggressively because most findings are diagnostic
// (logged but not noteworthy enough to interrupt). Only kinds the user
// asked us to surface OR findings explicitly pre-approved for autonomous
// surfacing make it through. Filtered findings stay in the DB and surface
// in the Heartbeat tab when the boss looks.
func (s *Server) onHeartbeatFinding(ctx context.Context, f proactive.Finding) {
	if s == nil {
		return
	}
	if !shouldSurfaceFinding(f) {
		return
	}
	text := formatFindingForChat(f)
	if text == "" {
		return
	}
	s.broadcastProactive(wsServerEvent{
		Type:      "proactive_message",
		Text:      text,
		FindingKind: f.Kind,
	})
}

// shouldSurfaceFinding decides whether a finding earns an interruption.
// pre_approved is the explicit "you can speak about this without asking"
// flag set on the finding upstream. kind=surprise / curiosity are the
// designed delight surfaces; kind=security is a safety surface. Other
// kinds (outcome, pattern, self_heal) stay quiet by default to avoid
// nag fatigue — the boss can still see them in the Heartbeat tab.
func shouldSurfaceFinding(f proactive.Finding) bool {
	if f.PreApproved {
		return true
	}
	switch f.Kind {
	case "surprise", "curiosity", "security":
		return true
	}
	return false
}

// formatFindingForChat composes the assistant-style sentence the boss sees
// when the heartbeat decides to speak. Kept tight on purpose — these are
// interruptions and should read like a co-worker tap on the shoulder, not
// a system notification.
func formatFindingForChat(f proactive.Finding) string {
	title := strings.TrimSpace(f.Title)
	detail := strings.TrimSpace(f.Detail)
	if title == "" && detail == "" {
		return ""
	}
	var b strings.Builder
	switch f.Kind {
	case "surprise":
		b.WriteString("Boss — quick idea: ")
	case "curiosity":
		b.WriteString("Boss, quick question: ")
	case "security":
		b.WriteString("⚠️ Heads up: ")
	default:
		b.WriteString("Heads up — ")
	}
	if title != "" {
		b.WriteString(title)
	}
	if detail != "" {
		if title != "" {
			b.WriteString(". ")
		}
		b.WriteString(detail)
	}
	return b.String()
}

// classifyIntentAsync runs IntentFlow on a user message without blocking the
// turn. The decision is recorded for analytics and emitted as a wsServerEvent
// so the Studio Live tab's IntentStream panel can render the per-turn
// classification stream in real time. Fail-closed: any error degrades to a
// silent decision and a warning event — chat itself never stalls on
// classification.
func (s *Server) classifyIntentAsync(ctx context.Context, sessionID, userMsg string, send func(wsServerEvent)) {
	if s == nil || s.intentDet == nil || strings.TrimSpace(userMsg) == "" {
		return
	}
	go func() {
		/* Classify uses an internal Haiku call and parses strict JSON. The
		 * package returns silent on any failure so a classifier outage
		 * never gates the agent loop — chat continues, the IntentStream
		 * panel just shows "silent · classifier unavailable". */
		dec := s.intentDet.Classify(ctx, userMsg, "")
		if s.intentDB != nil {
			_ = s.intentDB.Record(ctx, sessionID, userMsg, dec)
		}
		if send != nil {
			send(wsServerEvent{
				Type:      "intent",
				SessionID: sessionID,
				Intent: &wsIntent{
					Token:      string(dec.Token),
					Confidence: dec.Confidence,
					Reason:     dec.Reason,
					Suggested:  dec.SuggestedAction,
				},
			})
		}
	}()
}

// appendWAL extracts load-bearing fragments from a user message and writes
// them to mem_session_state. Synchronous and fast (regex over the message
// string only — no LLM). Runs before the turn so a corrective phrase
// ("actually, it's Bob not Bill") survives the same turn's compaction.
func (s *Server) appendWAL(ctx context.Context, sessionID, userMsg string) {
	if s == nil || s.wal == nil {
		return
	}
	frags := proactive.Extract(userMsg)
	if len(frags) == 0 {
		return
	}
	_ = s.wal.Append(ctx, sessionID, frags)
}

// captureWorkingBuffer is called after a turn completes. It mirrors the
// last user/assistant pair into mem_working_buffer iff the model's context
// window is past the configured threshold (default 0.6). That way a
// long-running session that's about to compact has a recoverable summary
// for the next turn to reload.
//
// We estimate ctxMax from the model id because the provider interface
// doesn't expose a context window. Anthropic Sonnet/Opus 4.x default to
// 200k, Haiku to 200k; 1M-context variants are detected by suffix. Any
// unknown model falls back to 200k which is the safe minimum.
func (s *Server) captureWorkingBuffer(ctx context.Context, sessionID, userMsg, agentResp string, used int) {
	if s == nil || s.buffer == nil || sessionID == "" || used <= 0 {
		return
	}
	max := estimateContextMax(s.modelForCapture(ctx))
	_ = s.buffer.MaybeCapture(ctx, sessionID, userMsg, agentResp, used, max)
}

// modelForCapture returns the model id used on this turn so context-window
// estimation can pick the right ceiling. Falls back to the loop's provider
// default if no per-turn override is set.
func (s *Server) modelForCapture(ctx context.Context) string {
	if s == nil {
		return ""
	}
	if s.settings != nil {
		if m := s.settings.GetModel(ctx); m != "" {
			return m
		}
	}
	if s.loop != nil {
		if p := s.loop.Provider(); p != nil {
			return p.Model()
		}
	}
	return ""
}

// estimateContextMax maps a model id to its context-window token count.
// Imperfect by design — the working-buffer threshold is a ratio, so a
// missed 1M-context model just means the buffer triggers later than
// ideal. Never under-estimates (would cause spurious captures); always
// returns at least 200k.
func estimateContextMax(model string) int {
	m := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(m, "[1m]") || strings.Contains(m, "-1m"):
		return 1_000_000
	case strings.Contains(m, "gpt-5") || strings.Contains(m, "gpt-4.1"):
		return 1_000_000
	case strings.Contains(m, "gemini") && strings.Contains(m, "pro"):
		return 1_000_000
	default:
		return 200_000
	}
}

// formatRecentContext is a thin helper kept for potential future use:
// IntentFlow's Classify accepts a recentContext string that disambiguates
// short messages. For now the WS handler passes "" — the substrate is
// here so a follow-up can wire the last 2-3 turns without churning every
// call site. Kept deliberately small and import-stable so tests pin it.
func formatRecentContext(_ []intent.Record) string { return "" }

// wsIntent is the on-the-wire shape for the per-turn IntentFlow decision.
// Carried inside wsServerEvent so the WS protocol stays single-typed.
type wsIntent struct {
	Token      string  `json:"token"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason,omitempty"`
	Suggested  string  `json:"suggested_action,omitempty"`
}
