package server

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/dopesoft/infinity/core/internal/proactive"
)

// broadcastFindings tracks curiosity IDs we've already pushed to chat
// this process. The heartbeat keeps re-listing every OPEN curiosity
// question on every tick, so without this guard the same finding would
// surface as a duplicate card every interval. The first approve/dismiss
// flips mem_curiosity_questions.status away from 'open' and the question
// stops appearing in future ticks naturally; this map keeps the
// intervening ticks from spamming a second card.
//
// In-memory by design: a process restart re-broadcasts at most once per
// open question, which is acceptable. If we ever need stricter semantics
// the right home is a `last_surfaced_at` column on mem_curiosity_questions.
var broadcastFindings sync.Map // map[curiosityID]struct{}

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

// BroadcastSkillPromoted surfaces a Voyager-auto-promoted skill as a
// chat bubble in every active session. Wired from serve.go alongside
// the procedural-memory write-through so the boss sees:
//
//   🤖 skill learned
//   I just created a skill called "create_habit_pursuit" — when you
//   ask me to set up another habit like this, I'll know exactly what
//   to do.
//
// Renders through the same proactive_message path as heartbeat
// findings; finding_kind="skill_promoted" tells Studio to swap in the
// robot icon and "skill learned" label.
func (s *Server) BroadcastSkillPromoted(name, description string) {
	if s == nil {
		return
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	description = strings.TrimSpace(description)
	tail := "I'll know exactly what to do."
	if description != "" {
		// Trim trailing punctuation so we can chain cleanly into "when…".
		desc := strings.TrimRight(description, " .!?")
		tail = desc + " — next time it comes up, I'll know what to do."
	}
	text := "**Skill learned: `" + name + "`**\n\n" +
		"I just taught myself a new skill from how this work went. " + tail
	s.broadcastProactive(wsServerEvent{
		Type:        "proactive_message",
		Text:        text,
		FindingKind: "skill_promoted",
	})
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
	// Drop duplicates: a curiosity question that's already shown up in
	// chat this process stays on screen until the boss decides — surfacing
	// a second card on the next tick is pure noise. Findings without a
	// curiosity_id (security / pre-approved surfaces) fall through to the
	// broadcast unchanged.
	if f.CuriosityID != "" {
		if _, already := broadcastFindings.LoadOrStore(f.CuriosityID, struct{}{}); already {
			return
		}
	}
	text := formatFindingForChat(f)
	if text == "" {
		return
	}
	s.broadcastProactive(wsServerEvent{
		Type:        "proactive_message",
		Text:        text,
		FindingKind: f.Kind,
		CuriosityID: f.CuriosityID,
	})
}

// shouldSurfaceFinding decides whether a finding earns an interruption.
// pre_approved is the explicit "you can speak about this without asking"
// flag set on the finding upstream. We still gate pre-approved findings
// by kind because some are meant for dashboard / heartbeat visibility,
// not live chat interruption. kind=surprise / curiosity are the designed
// delight surfaces; kind=security is a safety surface. Other kinds
// (outcome, pattern, self_heal) stay quiet by default to avoid nag
// fatigue — the boss can still see them in the Heartbeat tab.
func shouldSurfaceFinding(f proactive.Finding) bool {
	switch f.Kind {
	case "surprise", "curiosity", "security":
		return true
	}
	return false
}

// formatFindingForChat composes the Markdown the boss sees when the
// heartbeat decides to speak. The chat surface renders Markdown, so this
// uses real structure — a header, a one-line "why this surfaced" framing,
// the actual ask set off as a quote, and supporting detail with any
// code/JSON pushed into fenced blocks. The old single-run-on-paragraph
// form was unreadable the moment a finding carried a JSON payload.
func formatFindingForChat(f proactive.Finding) string {
	title := strings.TrimSpace(f.Title)
	detail := strings.TrimSpace(f.Detail)
	if title == "" && detail == "" {
		return ""
	}

	var b strings.Builder

	// Header + a one-line "why" so the boss isn't left guessing what the
	// agent is actually flagging or why it spoke up.
	header, why := findingFraming(f)
	b.WriteString(header)
	b.WriteString("\n\n")
	if why != "" {
		b.WriteString(why)
		b.WriteString("\n\n")
	}

	// The ask itself — set off as a blockquote so it's visually distinct
	// from the framing above and the supporting data below.
	if title != "" {
		switch f.Kind {
		case "curiosity":
			b.WriteString("**My question**\n")
		case "surprise":
			b.WriteString("**The idea**\n")
		default:
			b.WriteString("**What I noticed**\n")
		}
		b.WriteString("> ")
		b.WriteString(strings.ReplaceAll(title, "\n", "\n> "))
		b.WriteString("\n")
	}

	// Supporting detail — labelled sections, with code/JSON fenced so it
	// never bleeds into the prose as an unreadable run-on.
	if detail != "" {
		b.WriteString("\n")
		b.WriteString(formatFindingDetail(detail))
	}

	return strings.TrimSpace(b.String())
}

// findingFraming returns the bold header line and a one-sentence
// explanation of why a finding surfaced. The curiosity branch keys off
// Finding.Source so "a prediction missed" vs "two memories disagree" read
// accurately instead of a vague catch-all.
func findingFraming(f proactive.Finding) (header, why string) {
	switch f.Kind {
	case "surprise":
		return "💡 **Heartbeat — an idea for you**",
			"Something I noticed while reviewing recent activity that might be worth acting on."
	case "security":
		return "⚠️ **Heartbeat — security heads-up**",
			"This looked security-relevant, so I'm surfacing it now rather than waiting for you to ask."
	case "curiosity":
		switch f.Source {
		case "high_surprise":
			return "🔭 **Heartbeat — a prediction of mine missed**",
				"I predicted how a tool would behave and the result came back noticeably different. I'd like your read before I change how I use it."
		case "contradiction":
			return "🔭 **Heartbeat — two memories disagree**",
				"I'm holding two memories that contradict each other and can't tell which is right on my own."
		case "uncovered_mention":
			return "🔭 **Heartbeat — a gap I noticed**",
				"You've referenced this several times but I haven't captured anything durable about it yet."
		default:
			return "🔭 **Heartbeat — a question for you**",
				"Something didn't line up while I was reviewing recent activity and I'd like your call on it."
		}
	default:
		return "**Heartbeat — heads-up**", ""
	}
}

// formatFindingDetail turns a finding's raw detail string into readable
// Markdown. Lines shaped "label: value" become bold-labelled sections;
// any value that looks like code/JSON is pushed into a fenced block so it
// reads as data, not prose. Anything that isn't label-shaped is kept as a
// plain paragraph.
func formatFindingDetail(detail string) string {
	var b strings.Builder
	for _, ln := range strings.Split(detail, "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		label, value, ok := splitLabeled(ln)
		if !ok {
			if b.Len() > 0 {
				b.WriteString("\n")
			}
			b.WriteString(ln)
			b.WriteString("\n")
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString("**")
		b.WriteString(prettyLabel(label))
		b.WriteString("**\n")
		if looksLikeCode(value) {
			lang := ""
			if looksLikeJSON(value) {
				lang = "json"
			}
			b.WriteString("```")
			b.WriteString(lang)
			b.WriteString("\n")
			b.WriteString(value)
			b.WriteString("\n```\n")
		} else {
			b.WriteString(value)
			b.WriteString("\n")
		}
	}
	return strings.TrimSpace(b.String())
}

// splitLabeled splits "label: value" when the label is a single short
// token (so prose sentences that merely contain a colon aren't mangled).
func splitLabeled(line string) (label, value string, ok bool) {
	i := strings.Index(line, ":")
	if i <= 0 || i > 24 {
		return "", "", false
	}
	label = strings.TrimSpace(line[:i])
	value = strings.TrimSpace(line[i+1:])
	if label == "" || value == "" || strings.ContainsAny(label, " \t") {
		return "", "", false
	}
	return label, value, true
}

// prettyLabel maps the known machine labels to boss-facing phrasing and
// Title-cases anything else.
func prettyLabel(label string) string {
	switch strings.ToLower(label) {
	case "expected":
		return "What I expected"
	case "actual":
		return "What actually came back"
	default:
		if label == "" {
			return label
		}
		return strings.ToUpper(label[:1]) + label[1:]
	}
}

// looksLikeJSON is a cheap structural sniff — enough to pick a fence
// language, not a validator.
func looksLikeJSON(value string) bool {
	v := strings.TrimSpace(value)
	return strings.HasPrefix(v, "{") || strings.HasPrefix(v, "[")
}

// looksLikeCode decides whether a value should go in a fenced block
// rather than inline prose. JSON-ish payloads and obviously machine
// strings (braces, angle brackets) qualify; a plain sentence does not.
func looksLikeCode(value string) bool {
	v := strings.TrimSpace(value)
	if v == "" {
		return false
	}
	if looksLikeJSON(v) {
		return true
	}
	if strings.HasPrefix(v, "<") {
		return true
	}
	// Dense punctuation that reads as a payload, not a sentence.
	return strings.Count(v, "\"") >= 2 && strings.ContainsAny(v, "{}[]")
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
