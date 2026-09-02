package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"github.com/dopesoft/infinity/core/internal/sessions"
	"github.com/dopesoft/infinity/core/internal/turnctx"
	"strings"
	"sync"
	"time"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/dopesoft/infinity/core/internal/extensions"
	"github.com/dopesoft/infinity/core/internal/intent"
	"github.com/dopesoft/infinity/core/internal/llm"
	"github.com/dopesoft/infinity/core/internal/plan"
	"github.com/dopesoft/infinity/core/internal/proactive"
	"github.com/dopesoft/infinity/core/internal/tools"
)

// broadcastFindings tracks fingerprints of findings we've already pushed
// to chat this process. The heartbeat re-evaluates its checklist every
// tick (default every 2 minutes) and re-emits identical findings for any
// condition that hasn't been resolved yet - open curiosity questions,
// active connected_accounts still missing identity, an open pattern, an
// overdue outcome. Without dedup the boss gets the same chat bubble every
// two minutes until the underlying state changes.
//
// Key shape:
//
//	curiosity findings → "cur:<id>"        (existing semantics)
//	everything else    → "fp:<sha256 of Kind|Source|Title>"
//
// Once a fingerprint is seen, identical findings stay silent in chat
// until the process restarts or the underlying condition resolves. The
// finding still lands in mem_heartbeat_findings (the persistence path
// in heartbeat.go is unconditional) so the Heartbeat tab continues to
// show the full history.
//
// In-memory by design: a process restart re-broadcasts at most once per
// open finding, which is acceptable. If we ever need stricter semantics
// the right home is a `last_surfaced_at` column on mem_heartbeat_findings
// keyed by the same fingerprint.
var broadcastFindings sync.Map // map[string]struct{}

// findingFingerprint returns the dedup key for f. Curiosity findings
// keep their existing per-question key; everything else hashes the
// content fields that meaningfully identify the finding so a tick that
// produces the byte-identical "N accounts need identity" surprise twice
// only speaks once.
func findingFingerprint(f proactive.Finding) string {
	if f.CuriosityID != "" {
		return "cur:" + f.CuriosityID
	}
	h := sha256.Sum256([]byte(f.Kind + "|" + f.Source + "|" + f.Title))
	return "fp:" + hex.EncodeToString(h[:12])
}

// registerSession marks a WS connection as active under sessionID and binds
// it to a send function. The heartbeat broadcaster calls send when a finding
// crosses the proactive threshold so the browser tab gets an unprompted
// assistant turn - that's the wire that turns "responds when asked" into
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

// sessionSender returns a send function that, every time it's called, looks
// up the *current* WS binding for sessionID and dispatches there. If the
// session has no active WS (browser navigated away, network flap, tab
// backgrounded on iOS Safari and the socket died), the frame is dropped
// silently - the turn keeps running, persists its output to mem_turns /
// mem_messages on completion, and the client's reconnect path
// (mergeServerRows in useChat.ts) picks the completed turn up.
//
// The key property: the returned closure does NOT capture the send fn
// from the WS handler. A turn launched from a WS that subsequently dies
// will route its remaining frames to whichever WS happens to be bound to
// this session at the moment the frame is emitted - including no WS at
// all, in which case the frame is dropped without stalling the agent.
func (s *Server) sessionSender(sessionID string) func(wsServerEvent) {
	return func(ev wsServerEvent) {
		sessionID = tools.SessionForPublish(sessionID)
		if s == nil || sessionID == "" {
			return
		}
		s.activeMu.Lock()
		send := s.activeSessions[sessionID]
		s.activeMu.Unlock()
		if send == nil {
			return
		}
		ev.SessionID = sessionID
		send(ev)
	}
}

// NotifySession delivers a single proactive message into ONE session's live
// WS, scoped by session id. This is the delivery path the watch poller uses to
// keep an "I'll report back" promise: when a watched run settles, the follow-up
// lands in the originating chat. Drops silently if that session has no live
// socket (the boss navigated away) - the paired push notification covers the
// away case, mirroring how background_build done is delivered.
func (s *Server) NotifySession(sessionID, text string) {
	if s == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(text) == "" {
		return
	}
	s.sessionSender(sessionID)(wsServerEvent{
		Type:        "proactive_message",
		SessionID:   sessionID,
		Text:        text,
		FindingKind: "watch_settled",
	})
}

// broadcastProactive pushes the same event to every active WS session.
// Heartbeat findings broadcast to all open sessions - there's only one
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

// broadcastAll pushes an already-scoped WS event to every active browser
// connection without rewriting SessionID. Use this for non-chat work streams
// such as surface actions, where Studio filters by the run's synthetic
// session id from the event payload.
// registerBroadcast binds a WS connection to the broadcast fan-out for its
// whole lifetime, chatting or not.
//
// This is deliberately NOT registerSession. A session binding only exists once
// the boss sends a message, which means the dashboard, the one screen where he
// watches a live call, was never a broadcast target at all.
func (s *Server) registerBroadcast(connID string, send func(wsServerEvent)) {
	if s == nil || connID == "" || send == nil {
		return
	}
	s.activeMu.Lock()
	s.broadcastConns[connID] = send
	s.activeMu.Unlock()
}

// unregisterBroadcast drops a closed connection from the fan-out.
func (s *Server) unregisterBroadcast(connID string) {
	if s == nil || connID == "" {
		return
	}
	s.activeMu.Lock()
	delete(s.broadcastConns, connID)
	s.activeMu.Unlock()
}

// broadcastAll pushes an event to EVERY open tab: the live phone transcript,
// browser takeover state, anything not addressed to one chat session.
func (s *Server) broadcastAll(ev wsServerEvent) {
	if s == nil {
		return
	}
	s.activeMu.Lock()
	sends := make([]func(wsServerEvent), 0, len(s.broadcastConns))
	for _, fn := range s.broadcastConns {
		sends = append(sends, fn)
	}
	s.activeMu.Unlock()
	for _, fn := range sends {
		fn(ev)
	}
}

// BroadcastSkillPromoted surfaces a Voyager-auto-promoted skill as a
// chat bubble in every active session. Wired from serve.go alongside
// the procedural-memory write-through so the boss sees:
//
//	🤖 skill learned
//	I just created a skill called "create_habit_pursuit" - when you
//	ask me to set up another habit like this, I'll know exactly what
//	to do.
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
		tail = desc + " - next time it comes up, I'll know what to do."
	}
	text := "**Skill learned: `" + name + "`**\n\n" +
		"I just taught myself a new skill from how this work went. " + tail
	s.broadcastProactive(wsServerEvent{
		Type:        "proactive_message",
		Text:        text,
		FindingKind: "skill_promoted",
	})
}

// BroadcastRoutineProposed surfaces a routine miner detection as a chat
// bubble in the originating session. The miner only calls this when a
// brand-new cluster crosses the threshold from a fresh prompt — never on
// the nightly sweep, which lands its findings on surface='routines' so
// the boss reviews them on the dashboard at a moment of his choosing.
//
// Renders through the same proactive_message path as heartbeat findings;
// finding_kind="routine_proposed" tells Studio to use the routine icon
// and label.
func (s *Server) BroadcastRoutineProposed(sessionID, name, markdown string) {
	if s == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	name = strings.TrimSpace(name)
	markdown = strings.TrimSpace(markdown)
	if sessionID == "" || markdown == "" {
		return
	}
	s.sessionSender(sessionID)(wsServerEvent{
		Type:        "proactive_message",
		SessionID:   sessionID,
		Text:        markdown,
		FindingKind: "routine_proposed",
	})
}

// BroadcastBackgroundProgress surfaces a live progress update for a
// background_build run into the parent chat session. The payload carries
// the run id so Studio can bind it to mem_runs and render a real progress
// card instead of appending a fresh bubble on every update.
func (s *Server) BroadcastBackgroundProgress(p agent.BackgroundProgress) {
	if s == nil {
		return
	}
	if strings.TrimSpace(p.ParentSession) == "" {
		return
	}
	text := strings.TrimSpace(p.Label)
	if text == "" {
		text = "working"
	}
	ev := wsServerEvent{
		Type:           "proactive_message",
		SessionID:      p.ParentSession,
		Text:           text,
		FindingKind:    "background_build_progress",
		RunID:          p.RunID,
		ProgressStep:   p.Step,
		ProgressAction: strings.TrimSpace(p.Action),
		ProgressDetail: strings.TrimSpace(p.Detail),
		ProgressTask:   strings.TrimSpace(p.Task),
	}
	if p.Progress != nil {
		v := *p.Progress
		ev.Progress = &v
	}
	s.sessionSender(p.ParentSession)(ev)
}

// BroadcastBackgroundDone surfaces the completion of a background_build
// run as a chat bubble in the ORIGINATING session. Wired from serve.go as
// the BackgroundAgent.OnDone callback (alongside a push notification, so
// the boss learns even with no tab open). Renders through the same
// proactive_message path as heartbeat findings / skill-promoted; the
// finding_kind drives the icon + label Studio shows.
func (s *Server) BroadcastBackgroundDone(sessionID, task, summary, errMsg string) {
	if s == nil {
		return
	}
	sessionID = tools.SessionForPublish(strings.TrimSpace(sessionID))
	if sessionID == "" {
		return
	}
	task = strings.TrimSpace(task)
	summary = strings.TrimSpace(summary)
	errMsg = strings.TrimSpace(errMsg)

	var text string
	if errMsg != "" {
		text = "**Background build failed**"
		if task != "" {
			text += "\n\n_" + task + "_"
		}
		text += "\n\n" + errMsg
		if summary != "" {
			text += "\n\n" + summary
		}
	} else {
		header := "**Background build complete**"
		if task != "" {
			header += "\n\n_" + task + "_"
		}
		text = header
		if summary != "" {
			text += "\n\n" + summary
		}
	}

	s.sessionSender(sessionID)(wsServerEvent{
		Type:        "proactive_message",
		SessionID:   sessionID,
		Text:        text,
		FindingKind: "background_build",
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
	// Drop duplicates: every finding gets a stable fingerprint so a
	// heartbeat checklist that keeps surfacing the same condition (open
	// curiosity question, unresolved connector identity, overdue outcome)
	// stays silent in chat after the first surfacing. The DB-side history
	// continues to populate so the Heartbeat tab shows every tick.
	fp := findingFingerprint(f)
	if _, already := broadcastFindings.LoadOrStore(fp, struct{}{}); already {
		return
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

// ResumeFromExtensionAuth is wired as extensions.Manager.OnAuthComplete. When a
// cli tool's sign-in is verified via the /check probe (the inline auth card),
// it broadcasts the SAME "tool is ready + resume_intent" turn the heartbeat
// ExtensionAuthChecklist would have - so the agent picks up automatically the
// instant the boss finishes, with no "ok, I'm signed in" message. The /check
// path completes auth itself, so the heartbeat would otherwise never see it as
// pending and the resume would be lost; this closes that gap.
func (s *Server) ResumeFromExtensionAuth(ctx context.Context, ext *extensions.Extension) {
	if s == nil || ext == nil {
		return
	}
	detail := fmt.Sprintf(
		"The %q tool is installed and authenticated in the cloud workspace - run it via bash_run (source %s first so it uses the saved credentials).",
		ext.Name, extensions.EnvFilePath)
	if ext.ResumeIntent != "" {
		detail += "\nResume what you set it up for: " + ext.ResumeIntent
	}
	s.onHeartbeatFinding(ctx, proactive.Finding{
		Kind:        "outcome",
		Title:       fmt.Sprintf("%s is authenticated and ready", ext.Name),
		Detail:      detail,
		PreApproved: true,
		Source:      "extension_auth",
		SourceTag:   "extension_auth:" + ext.Name,
	})
}

// shouldSurfaceFinding decides whether a finding earns an interruption.
// pre_approved is the explicit "you can speak about this without asking"
// flag set on the finding upstream. We still gate pre-approved findings
// by kind because some are meant for dashboard / heartbeat visibility,
// not live chat interruption. kind=surprise / curiosity are the designed
// delight surfaces; kind=security is a safety surface. Other kinds
// (outcome, pattern, self_heal) stay quiet by default to avoid nag
// fatigue - the boss can still see them in the Heartbeat tab.
func shouldSurfaceFinding(f proactive.Finding) bool {
	switch f.Kind {
	case "security":
		return true
	case "surprise", "curiosity":
		return f.PreApproved
	}
	return false
}

// formatFindingForChat composes the Markdown the boss sees when the
// heartbeat decides to speak. The chat surface renders Markdown, so this
// uses real structure - a header, a one-line "why this surfaced" framing,
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

	// The ask itself - set off as a blockquote so it's visually distinct
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

	// Supporting detail - labelled sections, with code/JSON fenced so it
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
		return "💡 **Heartbeat - an idea for you**",
			"Something I noticed while reviewing recent activity that might be worth acting on."
	case "security":
		return "⚠️ **Heartbeat - security heads-up**",
			"This looked security-relevant, so I'm surfacing it now rather than waiting for you to ask."
	case "curiosity":
		switch f.Source {
		case "high_surprise":
			return "🔭 **Heartbeat - a prediction of mine missed**",
				"I predicted how a tool would behave and the result came back noticeably different. I'd like your read before I change how I use it."
		case "contradiction":
			return "🔭 **Heartbeat - two memories disagree**",
				"I'm holding two memories that contradict each other and can't tell which is right on my own."
		case "uncovered_mention":
			return "🔭 **Heartbeat - a gap I noticed**",
				"You've referenced this several times but I haven't captured anything durable about it yet."
		default:
			return "🔭 **Heartbeat - a question for you**",
				"Something didn't line up while I was reviewing recent activity and I'd like your call on it."
		}
	default:
		return "**Heartbeat - heads-up**", ""
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

// looksLikeJSON is a cheap structural sniff - enough to pick a fence
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
// silent decision and a warning event - chat itself never stalls on
// classification.
// recent is the memoized recent-context builder for this message (see
// recentContextFn); nil is treated as "no context".
func (s *Server) classifyIntentAsync(ctx context.Context, sessionID, userMsg string, send func(wsServerEvent), stance *turnctx.StanceHolder, recent func(context.Context) string) {
	if s == nil || s.intentDet == nil || strings.TrimSpace(userMsg) == "" {
		// No classifier: release the stance immediately so the loop's first
		// work-tool call never waits on a reading that will not come.
		stance.Set(turnctx.StanceUnknown, "classifier unavailable")
		return
	}
	go func() {
		/* Classify uses the active brain and parses strict JSON. The
		 * package returns silent on any failure so a classifier outage
		 * never gates the agent loop - chat continues, the IntentStream
		 * panel just shows "silent · classifier unavailable". */
		dec := s.intentDet.Classify(ctx, userMsg, recentContext(ctx, recent))
		// The stance is the consent fact the loop enforces (agent/consent.go):
		// discuss holds work tools, work / unclear / unknown do not.
		stance.Set(turnctx.ParseStance(dec.Stance), dec.Reason)
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
					Stance:     string(turnctx.ParseStance(dec.Stance)),
				},
			})
		}
	}()
}

// ── classifier recent context ────────────────────────────────────────────
//
// Both classifiers take a `recentContext` argument whose own doc says it
// "should be the last few user/assistant turns concatenated". It was hard-coded
// to "" at every call site, so the stance classifier read every message cold.
// That is why "please continue the build and finish up" landed as `discuss`:
// the classifier's definition of `work` includes "an approval of something
// already proposed", and with no context there was nothing on record as
// proposed or underway. The consent gate then refused plan_update for the rest
// of the turn.
//
// It is deliberately CHEAP and BOUNDED: the last few turns the model itself is
// already looking at (the agent loop's in-memory session, no query at all),
// falling back to the same mem_observations rows hydrateLoopSession reads when
// the process was restarted; plus one line naming the plan this conversation
// already has open. Never the whole session.
const (
	// recentContextTurns is how many trailing user/assistant messages ride along.
	recentContextTurns = 6
	// recentContextChars clips each one; the classifier needs the shape of the
	// exchange, not its content.
	recentContextChars = 400
)

// recentContextFn returns a memoized, lazy builder for this message's recent
// context. Lazy so nothing runs on the WS read loop (both classifiers call it
// from their own goroutine), memoized so the intent and gauge classifiers
// share one gather instead of doing it twice.
func (s *Server) recentContextFn(sessionID string) func(context.Context) string {
	var (
		once sync.Once
		val  string
	)
	return func(ctx context.Context) string {
		once.Do(func() { val = s.recentContextFor(ctx, sessionID) })
		return val
	}
}

// recentContext is the nil-safe way to call a builder returned above.
func recentContext(ctx context.Context, fn func(context.Context) string) string {
	if fn == nil {
		return ""
	}
	return fn(ctx)
}

// recentContextFor assembles the block. Nil-safe at every step: a server with
// no loop and no pool yields "", which is exactly the old behaviour.
func (s *Server) recentContextFor(ctx context.Context, sessionID string) string {
	if s == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	msgs := s.recentTurns(sessionID)
	if len(msgs) == 0 {
		msgs = s.recentTurnsFromStore(ctx, sessionID)
	}
	return buildRecentContext(msgs, s.openPlanNote(ctx, sessionID))
}

// recentTurns reads the tail of the live in-memory conversation straight from
// the agent loop — no query, and it is exactly what the model sees. It looks
// the session up by scanning Sessions() rather than calling
// GetOrCreateSession, which would MINT a session (and fire SessionStart)
// before the turn has even begun.
func (s *Server) recentTurns(sessionID string) []llm.Message {
	if s == nil || s.loop == nil {
		return nil
	}
	for _, sess := range s.loop.Sessions() {
		if sess == nil || sess.ID != sessionID {
			continue
		}
		return tailConversation(sess.Snapshot(), recentContextTurns)
	}
	return nil
}

// recentTurnsFromStore is the after-a-restart fallback: the same
// mem_observations rows hydrateLoopSession replays, newest few only. Without
// it the first message after a Core restart or a fresh tab would classify cold
// again — which is the exact failure this fixes, so it cannot depend on
// process-local state.
func (s *Server) recentTurnsFromStore(ctx context.Context, sessionID string) []llm.Message {
	if s == nil || s.pool == nil {
		return nil
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	rows, err := s.pool.Query(cctx, `
		SELECT hook_name, COALESCE(raw_text, '')
		  FROM mem_observations
		 WHERE session_id = $1
		   AND hook_name IN (`+sessions.HydrationHooksSQL+`)
		 ORDER BY created_at DESC
		 LIMIT $2
	`, sessionID, recentContextTurns)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []llm.Message
	for rows.Next() {
		var hook, text string
		if err := rows.Scan(&hook, &text); err != nil {
			return nil
		}
		if strings.TrimSpace(text) == "" {
			continue
		}
		role := llm.RoleAssistant
		if hook == "UserPromptSubmit" || hook == "DashboardSeed" || hook == sessions.AgentSelfPromptHook {
			role = llm.RoleUser
		}
		// Query is newest-first; prepend so the block reads oldest -> newest.
		out = append([]llm.Message{{Role: role, Content: text}}, out...)
	}
	return out
}

// openPlanNote names the plan this conversation already has open. This is the
// single fact the classifier was missing when the boss said "continue": that
// something is already underway and already approved, which makes "carry on"
// an approval rather than a musing. Silent when there is no plan.
func (s *Server) openPlanNote(ctx context.Context, sessionID string) string {
	if s == nil || s.pool == nil {
		return ""
	}
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	store := plan.NewStore(s.pool)
	p, err := store.GetActiveBySession(cctx, sessionID)
	if err != nil {
		return ""
	}
	if p == nil {
		// No live plan — but an un-approved PROPOSAL is equally load-bearing:
		// "go ahead" against one is the classifier's own definition of work.
		if p, err = store.GetProposedBySession(cctx, sessionID); err != nil || p == nil {
			return ""
		}
		return fmt.Sprintf("Already on the table in this conversation: the proposed plan %q (%d steps), which the boss has NOT approved yet.",
			p.Title, len(p.Steps))
	}
	done := 0
	next := ""
	for _, st := range p.Steps {
		switch st.Status {
		case "done", "skipped":
			done++
		default:
			if next == "" {
				next = st.Title
			}
		}
	}
	line := fmt.Sprintf("Already underway in this conversation: the plan %q, which the boss approved. Status %s, %d of %d steps finished.",
		p.Title, p.Status, done, len(p.Steps))
	if next != "" {
		line += fmt.Sprintf(" The next unfinished step is %q.", clipForContext(next, 120))
	}
	return line
}

// tailConversation keeps the last n user/assistant messages, oldest first.
// Tool traffic is dropped: it is noise for an intent read and it is where the
// bulk of the tokens live.
func tailConversation(msgs []llm.Message, n int) []llm.Message {
	if n <= 0 {
		return nil
	}
	var kept []llm.Message
	for _, m := range msgs {
		if m.Role != llm.RoleUser && m.Role != llm.RoleAssistant {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		kept = append(kept, m)
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return kept
}

// buildRecentContext renders the block. Pure so the shape is testable without
// a loop, a pool, or a classifier.
func buildRecentContext(msgs []llm.Message, planNote string) string {
	var b strings.Builder
	for _, m := range msgs {
		who := "Boss"
		if m.Role == llm.RoleAssistant {
			who = "Jarvis"
		}
		b.WriteString(who + ": " + clipForContext(collapseWhitespace(m.Content), recentContextChars) + "\n")
	}
	if strings.TrimSpace(planNote) != "" {
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(strings.TrimSpace(planNote) + "\n")
	}
	return strings.TrimSpace(b.String())
}

// collapseWhitespace flattens a message to one line so a long fenced reply
// can't dominate the block.
func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// clipForContext truncates on a rune boundary.
func clipForContext(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// wsGauge is the per-turn effort sizing emitted on a type="gauge" frame.
type wsGauge struct {
	Tier   string `json:"tier"`
	Reason string `json:"reason,omitempty"`
}

// classifyGaugeAsync sizes a user message's effort (glance/standard/deep)
// without blocking the turn — same async, fail-open discipline as the intent
// classifier (the boss is latency-sensitive; sizing must never stall chat). The
// read is persisted (the durable record + the session-scoped GaugeProvider's
// source) and emitted as a `gauge` frame for the Studio chip. Gated to
// substantive turns so a greeting doesn't burn a model call.
func (s *Server) classifyGaugeAsync(ctx context.Context, sessionID, userMsg string, send func(wsServerEvent), recent func(context.Context) string) {
	if s == nil || s.gaugeDet == nil || strings.TrimSpace(userMsg) == "" {
		return
	}
	if !agent.SubstantiveQuery(userMsg) {
		return
	}
	go func() {
		read := s.gaugeDet.Classify(ctx, userMsg, recentContext(ctx, recent))
		if s.gaugeDB != nil {
			s.gaugeDB.Record(ctx, sessionID, userMsg, read)
		}
		if send != nil {
			send(wsServerEvent{
				Type:      "gauge",
				SessionID: sessionID,
				Gauge:     &wsGauge{Tier: string(read.Tier), Reason: read.Reason},
			})
		}
	}()
}

// appendWAL extracts load-bearing fragments from a user message and writes
// them to mem_session_state. Synchronous and fast (regex over the message
// string only - no LLM). Runs before the turn so a corrective phrase
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
// Imperfect by design - the working-buffer threshold is a ratio, so a
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
// short messages. For now the WS handler passes "" - the substrate is
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
	// Stance is the consent read-back Studio shows above the composer:
	// "discuss" (talking it through) or "work" (a work order / approval).
	Stance string `json:"stance,omitempty"`
}
