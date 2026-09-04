package finish

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/surface"
)

// Settling a coding job from its OWN transcript, before anything else acts on
// the row.
//
// THE FAILURE. 2026-08-29: a 47-minute Claude Code build finished
// successfully, wrote a full report, and Infinity told the boss it had failed.
// Two separate defects, both fixed here:
//
//  1. Nothing re-read a finished transcript. The poll loop reads it while
//     someone is watching; when the window closed, the report sat on the Mac
//     and no code ever opened it again. The run stayed "still working", the
//     plan step stayed `blocked`, and the work read as lost.
//  2. A dead `running` row blocked recovery. Coding kinds are exempt from the
//     blanket reaper (correctly — a wall clock cannot tell a 50-minute build
//     from a dead one), so a row whose process was gone kept claiming to be
//     live, which held the one-job-per-conversation guard shut AND held this
//     poller's own "never while newer coding work is live" check shut.
//
// The answer to both is one read-only probe of the job's own files, asking
// three questions: are they there, is the process alive, did Claude write its
// terminal result. Deterministic Go, no cognition (Rule #1b). What follows a
// finish is a NOTICE, not a decision, so it is a card in the boss's inbox and
// not a model turn: on 2026-09-02 half of the machine-started turns eating
// his Claude plan were this exact "it DID finish, I have already corrected
// the run" sentence, replayed into a 900K-token chat once a minute.

// Verdict is what a job's own files say about it. Mirrors
// tools.ClaudeJobVerdict; declared here so this package keeps its no-bridge,
// no-network testability.
type Verdict struct {
	Found   bool
	Alive   bool
	Done    bool
	IsError bool
	Report  string
	Files   []string
	Err     string
}

// Looked reports whether the probe actually ran. An unreachable Mac must never
// read as "the job left nothing behind".
func (v Verdict) Looked() bool { return v.Err == "" }

// Transcript reads a coding job's own files off the bridge that ran it.
type Transcript interface {
	Read(ctx context.Context, sessionID, repo, runID string) Verdict
}

// candidate is one run worth probing.
type candidate struct {
	runID     string
	label     string
	sessionID string
	repo      string
	claudeSes string
	status    string
	reason    string
	startedAt time.Time
}

// settleTriesSQL counts CONSECUTIVE FAILED probes, guarded like the other
// numeric meta reads because Postgres has no TRY_CAST.
const settleTriesSQL = `CASE WHEN meta->>'settle_tries' ~ '^[0-9]+$'
                             THEN (meta->>'settle_tries')::int ELSE 0 END`

// SettleOne probes at most one coding run per tick and settles it from its own
// transcript. Returns true when it changed something.
//
// One per tick, like ContinueOne: each probe is a bridge round trip, and there
// is never a hurry — the row has already stopped moving.
func (p *Poller) SettleOne(ctx context.Context) (bool, error) {
	if p == nil || p.rows == nil || p.transcript == nil {
		return false, nil
	}
	c, err := p.rows.ClaimSettle(ctx, p.settleParams())
	if err != nil || c == nil {
		return false, err
	}
	v := p.transcript.Read(ctx, c.sessionID, c.repo, c.runID)
	if !v.Looked() {
		// Could not look. The try counter already went up at claim time, so a
		// Mac that stays asleep stops being probed rather than being probed
		// forever, and the row is left exactly as it was.
		return false, fmt.Errorf("probe run %s: %s", c.runID, v.Err)
	}
	// The probe worked, so the failure counter starts again from zero: a long
	// job that is simply still going must never exhaust its attempts and
	// become unsettleable.
	p.note(ctx, c.runID, "settle_tries", "0")

	switch {
	case v.Done:
		return true, p.closeCompleted(ctx, *c, v)
	case v.Alive:
		// Still working. Nothing to do, and deliberately nothing said: the row
		// already carries its own progress line.
		return false, nil
	case c.status == "running" && !v.Found:
		// No process and no files. This row cannot be describing live work,
		// and while it claims to be, it blocks every path behind it.
		return true, p.closeDead(ctx, *c)
	}
	return false, nil
}

func (p *Poller) settleParams() settleParams {
	return settleParams{
		lookback:    p.lookback,
		maxTries:    p.maxSettleTries,
		each:        p.settleEach,
		stallAfter:  p.stallAfter,
		settleGrace: p.settleGrace,
	}
}

// closeCompleted turns a run that actually finished into one that SAYS it
// finished, and then tells the boss with a card in his inbox.
//
// The row is corrected first and the card second, deliberately: if the card
// fails, the truth is still on the board rather than depending on a second
// thing also working. Once corrected, the row has no stopped_reason and a
// terminal status, so neither claim can pick it up again: a finished job is
// told exactly once.
//
// A card and not a model turn, because nothing here is a decision. The job is
// done, its report is in hand, the run row already says so. What is left is
// telling him, and a turn spent telling him costs a full pass over his chat's
// context (28% of a week's Claude plan, 2026-09-02) to produce the same
// sentence this writes for free.
func (p *Poller) closeCompleted(ctx context.Context, c candidate, v Verdict) error {
	status := "ok"
	if v.IsError {
		status = "error"
	}
	summary := strings.TrimSpace(v.Report)
	if summary == "" {
		summary = "It finished, but it wrote no closing summary. " + filesLine(v.Files)
	}
	if err := p.rows.CloseCompleted(ctx, c.runID, status, clip(summary, 8000)); err != nil {
		return fmt.Errorf("close completed run %s: %w", c.runID, err)
	}
	infoLog.Printf("run %s actually finished (%s) — corrected the row, telling the boss from chat %s",
		c.runID, status, c.sessionID)

	if p.surfacer == nil {
		// Never silently: the board is right but the boss was not told, and
		// a finished job he was last told had failed is exactly the thing he
		// needs to hear.
		log.Printf("finish: run %s finished but no inbox writer is wired, so the boss was not told (label %q, chat %s)",
			c.runID, c.label, c.sessionID)
		p.note(ctx, c.runID, "finish_error", "finished, but no inbox writer was wired to tell the boss")
		return nil
	}

	planID := ""
	if id, err := p.rows.ActivePlanID(ctx, c.sessionID); err != nil {
		log.Printf("finish: reading the active plan for session %s: %v", c.sessionID, err)
	} else {
		planID = id
	}
	item := buildCompletedItem(c, v, planID)
	if _, err := p.surfacer.Upsert(ctx, item); err != nil {
		p.note(ctx, c.runID, "finish_error", err.Error())
		return fmt.Errorf("surface finished run %s: %w", c.runID, err)
	}
	p.note(ctx, c.runID, "finish_outcome", "surfaced")
	return nil
}

// buildCompletedItem is the inbox card for a job that finished unwatched.
// One card per run (ExternalID keyed on the run id), so a second probe of the
// same run could only ever refresh it, never stack another.
func buildCompletedItem(c candidate, v Verdict, planID string) *surface.Item {
	title, body := buildCompletedNotice(c, v, planID)
	imp := 30
	if v.IsError {
		imp = 70
	}
	reason := firstLineOf(strings.TrimSpace(v.Report))
	if reason == "" {
		reason = "It finished, but wrote no closing summary."
	}
	meta := map[string]any{
		"run_id":     c.runID,
		"session_id": c.sessionID,
		"repo":       c.repo,
		"status":     "ok",
	}
	if v.IsError {
		meta["status"] = "error"
	}
	if c.claudeSes != "" {
		meta["claude_session_id"] = c.claudeSes
	}
	if len(v.Files) > 0 {
		meta["files"] = v.Files
	}
	if planID != "" {
		meta["plan_id"] = planID
	}
	item := &surface.Item{
		Surface:          "runs",
		Kind:             "run_outcome",
		Source:           "coding-job",
		ExternalID:       "build-finished:" + c.runID,
		Title:            title,
		Subtitle:         repoName(c.repo),
		Body:             body,
		Importance:       &imp,
		ImportanceReason: clip(reason, 200),
		Metadata:         meta,
		Status:           surface.StatusOpen,
	}
	// A clean finish is information, not a chore: it self-clears like a
	// cron's did-work card. A reported failure stays until he has seen it.
	if !v.IsError {
		exp := time.Now().UTC().Add(36 * time.Hour)
		item.ExpiresAt = &exp
	}
	return item
}

// closeDead closes a `running` row whose job is provably gone: no process, no
// files. It closes 'ok' + still_working, matching how every other interruption
// is recorded — the job was never stopped and never failed, we simply have no
// verdict — and that stamp is what makes ContinueOne pick it up next tick.
func (p *Poller) closeDead(ctx context.Context, c candidate) error {
	summary := "I lost track of this one. Its process is gone from the Mac and it left nothing behind, " +
		"so I have no result for it — it was not stopped and it did not fail. Anything it wrote is still " +
		"uncommitted in " + firstNonEmpty(c.repo, "that repo") + "."
	if err := p.rows.CloseDead(ctx, c.runID, summary); err != nil {
		return fmt.Errorf("close dead run %s: %w", c.runID, err)
	}
	infoLog.Printf("run %s had no process and no files left — closed it so the work behind it can move", c.runID)
	return nil
}

// filesLine names what the transcript showed it touching, for a job that
// finished without writing its own summary.
func filesLine(files []string) string {
	if len(files) == 0 {
		return "Check git_status in that repo for what it changed."
	}
	return "It touched " + strings.Join(clipList(files, 8), ", ") + "."
}

// repoName is the last path segment of a repo path, which is what the boss
// calls it ("infinity", not "/Users/kai/Dev/infinity").
func repoName(repo string) string {
	repo = strings.TrimRight(strings.TrimSpace(repo), "/")
	if i := strings.LastIndex(repo, "/"); i >= 0 {
		return repo[i+1:]
	}
	return repo
}

// firstLineOf is the opening line of a report, for the card's one-line why.
func firstLineOf(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}
