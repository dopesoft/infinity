package finish

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/dopesoft/infinity/core/internal/surface"
	"github.com/google/uuid"
)

// The continuation loop must never become a model turn a minute into the
// boss's chat.
//
// What happened (2026-09-02, 05:45 to 06:00): one stranded coding run, the
// poller ticking every sixty seconds, and each tick replaying a brief INTO THE
// BOSS'S LIVE CONVERSATION, whose context was about 900K tokens. Each of those
// was a full Opus turn to answer in under 120 tokens. Over the week the
// "[Automatic check, no one asked for this]" turns were 42 turns and 28% of the
// Claude Max plan. Half of them were "the job DID finish and its result was
// never reported", which is a notice, not a decision.
//
// This file drives the real tick loop against an in-memory copy of the run
// rows for the same fifteen minutes and pins four things: at most three
// briefs, none of them into the parent chat, a real gap between them, and a
// finished job producing one card and zero turns.

// ─── the in-memory rows ──────────────────────────────────────────────────────

type fakeRun struct {
	id, kind, label, status string
	meta                    map[string]string
	startedAt, endedAt      time.Time // endedAt zero = NULL
	summary                 string
}

type fakeSession struct{ parent, name string }

// fakeRows is runRows over a slice, applying the same eligibility rules
// claimSQL and ClaimSettle encode. The SQL itself is pinned by string tests
// (TestClaimSQL_* below and in nudge_loop_test.go); this is the behaviour the
// loop has on top of it.
type fakeRows struct {
	now      func() time.Time
	runs     []*fakeRun
	sessions map[string]fakeSession
	plans    map[string]string
}

const stamp = "2006-01-02T15:04:05Z"

func (f *fakeRows) at(key string, r *fakeRun) (time.Time, bool) {
	t, err := time.Parse(stamp, r.meta[key])
	return t, err == nil
}

func (f *fakeRows) intMeta(key string, r *fakeRun) int {
	n := 0
	fmt.Sscanf(r.meta[key], "%d", &n)
	return n
}

func (f *fakeRows) lastActivity(r *fakeRun) time.Time {
	if t, ok := f.at("activity_at", r); ok {
		return t
	}
	return r.startedAt
}

func (f *fakeRows) MarkStalled(context.Context, time.Duration) (int, error) { return 0, nil }

func (f *fakeRows) ClaimContinue(_ context.Context, q claimParams) (*stranded, error) {
	now := f.now()
	var pick *fakeRun
	for _, r := range f.runs {
		if r.kind != codingKinds[0] || r.status == "running" || r.meta["stopped_reason"] == "" || r.endedAt.IsZero() {
			continue
		}
		if !r.endedAt.Before(now.Add(-q.settleGrace)) || !r.endedAt.After(now.Add(-q.lookback)) {
			continue
		}
		if f.intMeta("finish_passes", r) >= q.maxPasses {
			continue
		}
		if last, ok := f.at("finish_last_at", r); ok && !last.Before(now.Add(-q.backoff)) {
			continue
		}
		if r.meta["session_id"] == "" || r.meta["repo"] == "" {
			continue
		}
		if q.requireSettled && r.meta["engine"] == "claude_code" && r.meta["settle_last_at"] == "" {
			continue
		}
		superseded := false
		for _, o := range f.runs {
			if o.kind != codingKinds[0] || o.meta["session_id"] != r.meta["session_id"] {
				continue
			}
			if o.status == "running" || (!o.endedAt.IsZero() && o.endedAt.After(r.endedAt)) {
				superseded = true
			}
		}
		if superseded {
			continue
		}
		if pick == nil || r.endedAt.Before(pick.endedAt) {
			pick = r
		}
	}
	if pick == nil {
		return nil, nil
	}
	pick.meta["finish_passes"] = fmt.Sprint(f.intMeta("finish_passes", pick) + 1)
	pick.meta["finish_last_at"] = now.UTC().Format(stamp)
	if pick.meta["finish_session_id"] == "" {
		pick.meta["finish_session_id"] = uuid.NewString()
	}
	return &stranded{
		runID: pick.id, label: pick.label, sessionID: pick.meta["session_id"], repo: pick.meta["repo"],
		claudeSes: pick.meta["claude_session_id"], reason: pick.meta["stopped_reason"], summary: pick.summary,
		lastFile: pick.meta["currentFile"], pass: f.intMeta("finish_passes", pick),
		startedAt: pick.startedAt, endedAt: pick.endedAt, finishSes: pick.meta["finish_session_id"],
	}, nil
}

func (f *fakeRows) ClaimSettle(_ context.Context, q settleParams) (*candidate, error) {
	now := f.now()
	var pick *fakeRun
	for _, r := range f.runs {
		if r.kind != codingKinds[0] || r.meta["engine"] != "claude_code" || r.meta["repo"] == "" {
			continue
		}
		if !r.startedAt.After(now.Add(-q.lookback)) || f.intMeta("settle_tries", r) >= q.maxTries {
			continue
		}
		if last, ok := f.at("settle_last_at", r); ok && !last.Before(now.Add(-q.each)) {
			continue
		}
		quiet := r.status == "running" && f.lastActivity(r).Before(now.Add(-q.stallAfter))
		closed := r.status != "running" && r.meta["stopped_reason"] != "" && !r.endedAt.IsZero() &&
			r.endedAt.Before(now.Add(-q.settleGrace))
		if !quiet && !closed {
			continue
		}
		if pick == nil || r.startedAt.Before(pick.startedAt) {
			pick = r
		}
	}
	if pick == nil {
		return nil, nil
	}
	pick.meta["settle_tries"] = fmt.Sprint(f.intMeta("settle_tries", pick) + 1)
	pick.meta["settle_last_at"] = now.UTC().Format(stamp)
	return &candidate{
		runID: pick.id, label: pick.label, sessionID: pick.meta["session_id"], repo: pick.meta["repo"],
		claudeSes: pick.meta["claude_session_id"], status: pick.status, reason: pick.meta["stopped_reason"],
		startedAt: pick.startedAt,
	}, nil
}

func (f *fakeRows) find(id string) *fakeRun {
	for _, r := range f.runs {
		if r.id == id {
			return r
		}
	}
	return nil
}

func (f *fakeRows) Note(_ context.Context, runID, key, value string) error {
	if r := f.find(runID); r != nil {
		r.meta[key] = value
	}
	return nil
}

func (f *fakeRows) Spend(_ context.Context, runID string, maxPasses int) error {
	return f.Note(context.Background(), runID, "finish_passes", fmt.Sprint(maxPasses))
}

func (f *fakeRows) CloseCompleted(_ context.Context, runID, status, summary string) error {
	r := f.find(runID)
	r.status = status
	if r.endedAt.IsZero() {
		r.endedAt = f.now()
	}
	r.summary = summary
	delete(r.meta, "stopped_reason")
	delete(r.meta, "stalled_since")
	r.meta["finish_outcome"] = "completed"
	r.meta["settled_at"] = f.now().UTC().Format(stamp)
	return nil
}

func (f *fakeRows) CloseDead(_ context.Context, runID, summary string) error {
	r := f.find(runID)
	if r.status != "running" {
		return nil
	}
	r.status = "ok"
	r.endedAt = f.now()
	if r.summary == "" {
		r.summary = summary
	}
	r.meta["stopped_reason"] = "still_working"
	r.meta["finish_outcome"] = "reaped"
	return nil
}

func (f *fakeRows) EnsureContinuationSession(_ context.Context, sessionID, parent, name, _ string) error {
	if _, ok := f.sessions[sessionID]; !ok {
		f.sessions[sessionID] = fakeSession{parent: parent, name: name}
	}
	return nil
}

func (f *fakeRows) ActivePlanID(_ context.Context, sessionID string) (string, error) {
	return f.plans[sessionID], nil
}

// ─── the collaborators ───────────────────────────────────────────────────────

type replayCall struct {
	session, text string
	at            time.Time
}

type recordingReplayer struct {
	now   func() time.Time
	calls []replayCall
}

func (r *recordingReplayer) Replay(_ context.Context, sessionID, userText, _ string) (string, error) {
	r.calls = append(r.calls, replayCall{session: sessionID, text: userText, at: r.now()})
	return "Resuming it now.", nil
}

// movingEvidence reports a different HEAD on every look, so a pass always
// reads as "the repo moved" and the pass budget, not the settle-on-no-change
// rule, is what ends the loop. That is the shape of the 2026-09-02 morning.
type movingEvidence struct{ looks int }

func (e *movingEvidence) Gather(_ context.Context, _, repo string) Report {
	e.looks++
	return Report{Repo: repo, Branch: "main", Head: fmt.Sprintf("head%d", e.looks), Dirty: []string{"a.go"}}
}

type fixedTranscript struct{ v Verdict }

func (t fixedTranscript) Read(context.Context, string, string, string) Verdict { return t.v }

type recordingSurfacer struct{ items []*surface.Item }

func (s *recordingSurfacer) Upsert(_ context.Context, it *surface.Item) (string, error) {
	cp := *it
	s.items = append(s.items, &cp)
	return it.ExternalID, nil
}

// ─── the scenario ────────────────────────────────────────────────────────────

const (
	parentChat = "22222222-2222-4222-8222-222222222222"
	strandedID = "33333333-3333-4333-8333-333333333333"
)

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

// strandedRun is the run as it looked at 05:45: a Claude Code job that closed
// without a verdict five minutes ago, from the boss's live chat.
func strandedRun(now time.Time) *fakeRun {
	return &fakeRun{
		id: strandedID, kind: codingKinds[0], status: "ok",
		label: "Claude Code: redesign the coach conversation",
		meta: map[string]string{
			"stopped_reason":    "still_working",
			"session_id":        parentChat,
			"repo":              "/Users/kai/Dev/infinity",
			"engine":            "claude_code",
			"claude_session_id": "f4fcf5d1-dffc-407e-bf64-9330f8b4b329",
		},
		startedAt: now.Add(-40 * time.Minute),
		endedAt:   now.Add(-5 * time.Minute),
	}
}

func newQuietLoop(t *testing.T, verdict Verdict, surfacer Surfacer) (*Poller, *clock, *fakeRows, *recordingReplayer) {
	t.Helper()
	c := &clock{t: time.Date(2026, 9, 2, 5, 45, 0, 0, time.UTC)}
	rows := &fakeRows{
		now:      c.now,
		runs:     []*fakeRun{strandedRun(c.t)},
		sessions: map[string]fakeSession{},
		plans:    map[string]string{parentChat: "44444444-4444-4444-8444-444444444444"},
	}
	rep := &recordingReplayer{now: c.now}
	p := &Poller{
		rows:       rows,
		replayer:   rep,
		evidence:   &movingEvidence{},
		transcript: fixedTranscript{v: verdict},
		surfacer:   surfacer,
		// The production numbers, verbatim from NewPoller.
		interval: 60 * time.Second, settleGrace: 2 * time.Minute, lookback: 6 * time.Hour,
		stallAfter: 12 * time.Minute, maxPasses: 3, settleEach: 3 * time.Minute, maxSettleTries: 4,
	}
	return p, c, rows, rep
}

// fifteenMinutes ticks the loop the way the ticker did that morning: once a
// minute, for fifteen minutes.
func fifteenMinutes(p *Poller, c *clock) {
	for i := 0; i < 15; i++ {
		p.tick(context.Background())
		c.t = c.t.Add(time.Minute)
	}
}

// Why: this is the 05:45-06:00 shape exactly. The rules that make it bearable
// are all mechanics (Rule #1b), so they are all pinned here.
func TestQuietLoop_AStrandedRunIsBriefedThreeTimesAtMostInItsOwnSession(t *testing.T) {
	inbox := &recordingSurfacer{}
	p, c, rows, rep := newQuietLoop(t, Verdict{Found: true}, inbox)

	fifteenMinutes(p, c)

	// At most three briefs, and the run's budget is spent, not merely paused.
	if got := len(rep.calls); got != 3 {
		t.Fatalf("fifteen ticks must produce exactly the pass budget (3) of briefs, got %d", got)
	}
	if rows.find(strandedID).meta["finish_passes"] != "3" {
		t.Fatalf("the run's pass budget must be spent on the row, got %q", rows.find(strandedID).meta["finish_passes"])
	}

	// Never into the parent chat, always into one side session.
	side := rep.calls[0].session
	if side == parentChat {
		t.Fatal("the brief went into the boss's live chat: that is the 900K-token turn a minute")
	}
	if !isChatSession(side) {
		t.Fatalf("the side session must be a real uuid chat session, got %q", side)
	}
	for i, call := range rep.calls {
		if call.session != side {
			t.Fatalf("pass %d went to %s, pass 1 went to %s: the passes must share one session", i+1, call.session, side)
		}
	}
	if rows.find(strandedID).meta["finish_session_id"] != side {
		t.Fatal("the side session id must be remembered on the run row so a restart reuses it")
	}

	// The side session exists, is labelled for a person, and knows its parent.
	ses, ok := rows.sessions[side]
	if !ok {
		t.Fatal("the side session was never opened; the replay would have landed in a session with no row")
	}
	if ses.parent != parentChat {
		t.Fatalf("the side session must trace back to the chat the job came from, got parent %q", ses.parent)
	}
	if ses.name != "Continuing: redesign the coach conversation" {
		t.Fatalf("the session is named for the boss, without the engine prefix, got %q", ses.name)
	}

	// A real gap between briefs: settleEach, not the next sixty-second tick.
	sort.Slice(rep.calls, func(i, j int) bool { return rep.calls[i].at.Before(rep.calls[j].at) })
	for i := 1; i < len(rep.calls); i++ {
		if gap := rep.calls[i].at.Sub(rep.calls[i-1].at); gap < p.settleEach {
			t.Fatalf("brief %d came %s after brief %d; the minimum is %s", i+1, gap, i, p.settleEach)
		}
	}

	// The brief still says where the job came from and which plan it belongs
	// to, because the side session has no context of its own.
	brief := rep.calls[0].text
	if !strings.Contains(brief, parentChat) {
		t.Fatalf("the brief must name the parent conversation:\n%s", brief)
	}
	if !strings.Contains(brief, "44444444-4444-4444-8444-444444444444") || !strings.Contains(brief, "plan_get") {
		t.Fatalf("the brief must hand over the plan id and the tool that reads it:\n%s", brief)
	}

	// And nothing reached the inbox: a stranded job is a decision, not a notice.
	if len(inbox.items) != 0 {
		t.Fatalf("a stranded run is briefed, not surfaced; got %d card(s)", len(inbox.items))
	}
}

// Why: half of the 2026-09-02 turns were "the job DID finish and was never
// reported", replayed into the chat once a minute. That is a notice. It is one
// card, attributed to the chat the job came from, and zero model turns.
func TestQuietLoop_AFinishedRunIsOneCardAndNoTurn(t *testing.T) {
	inbox := &recordingSurfacer{}
	p, c, rows, rep := newQuietLoop(t, Verdict{
		Found: true, Done: true,
		Report: "Done. Rewrote the coach panel and the four call sites; go build and pnpm test both clean.",
		Files:  []string{"studio/components/CoachConversation.tsx"},
	}, inbox)

	fifteenMinutes(p, c)

	if len(rep.calls) != 0 {
		t.Fatalf("a finished job must never wake the model; got %d replay(s), first into %s", len(rep.calls), rep.calls[0].session)
	}
	if len(inbox.items) != 1 {
		t.Fatalf("fifteen ticks must produce exactly one card, got %d", len(inbox.items))
	}
	card := inbox.items[0]
	if card.Surface != "runs" {
		t.Fatalf("the card must land in 'Surfaced by Jarvis' (a non-system surface), got %q", card.Surface)
	}
	if card.Metadata["session_id"] != parentChat || card.Metadata["run_id"] != strandedID {
		t.Fatalf("the card must be attributed to the run and the chat it came from, got %v", card.Metadata)
	}
	if !strings.HasPrefix(card.Title, "Build finished: ") {
		t.Fatalf("the title is the plain fact, got %q", card.Title)
	}
	if !strings.Contains(card.Body, "Rewrote the coach panel") {
		t.Fatalf("the body carries the job's own report:\n%s", card.Body)
	}
	if card.ExternalID != "build-finished:"+strandedID {
		t.Fatalf("one card per run, keyed on the run id, got %q", card.ExternalID)
	}

	run := rows.find(strandedID)
	if run.status != "ok" || run.meta["stopped_reason"] != "" || run.meta["finish_outcome"] != "surfaced" {
		t.Fatalf("the row must be corrected and marked surfaced, got status=%q meta=%v", run.status, run.meta)
	}
}

// Why: with no inbox writer the notice cannot be delivered, and that must be
// said, not swallowed. The board is still corrected, and the model is still
// not woken for it.
func TestQuietLoop_NoInboxWriterIsSaidNotSwallowed(t *testing.T) {
	p, c, rows, rep := newQuietLoop(t, Verdict{Found: true, Done: true, Report: "Done."}, nil)

	fifteenMinutes(p, c)

	if len(rep.calls) != 0 {
		t.Fatalf("no surfacer is not a reason to fall back to a model turn; got %d replay(s)", len(rep.calls))
	}
	run := rows.find(strandedID)
	if run.meta["finish_outcome"] != "completed" {
		t.Fatalf("the row must still be corrected, got %v", run.meta)
	}
	if !strings.Contains(run.meta["finish_error"], "no inbox writer") {
		t.Fatalf("the undelivered notice must be recorded on the row, got %v", run.meta)
	}
}

// Why: the two rules that make the loop quiet live in the SQL, so the SQL is
// pinned the same way nudge_loop_test.go pins the queue rule.
func TestClaimSQL_BacksOffAfterABriefAndMintsTheSideSession(t *testing.T) {
	sql := claimSQL()
	if !strings.Contains(sql, "(r.meta->>'finish_last_at')::timestamptz < NOW() - $6::interval") {
		t.Fatal("without the backoff clause a briefed run is claimed again on the next sixty-second tick")
	}
	if !strings.Contains(sql, "'finish_session_id', COALESCE(NULLIF(meta->>'finish_session_id',''), uuid_generate_v4()::text)") {
		t.Fatal("the side session must be minted in the claim itself, once per run, or passes stop sharing context")
	}
	if !strings.Contains(sql, "COALESCE(meta->>'finish_session_id','')") {
		t.Fatal("the claim must hand the side session id back, or the replay has nowhere to go but the parent chat")
	}
}
