package dashboard

import "time"

// Liveness — the ONE place that decides whether a work item is really live.
//
// WHY THIS FILE EXISTS
//
// Six different producers put items on the Agent Work board: crons, generic
// runs, sentinels, workflows, plans and mandates. Every one of them decided
// "running" for itself, on its own reading of its own table's status column,
// and each therefore had to independently remember that a status column is a
// claim rather than a fact — that a row says 'active' because something set it
// to 'active', and nothing on earth guarantees anything set it back.
//
// Four of the six did not remember. On 2026-08-29 the board showed, all of it
// under "Running" or "Awaiting you":
//
//   - four plans titled "/workspace/infinity", 'active' for three days, with
//     no session id at all, orphaned when a process died mid-build
//   - ten plans 'paused', the oldest quiet since 8 June
//   - four mandates 'open', one of them untouched for twenty seven days
//
// Every one of them had a status column that was technically accurate and a
// meaning that was a lie. And the reason it was not caught is structural: with
// the rule living in six places, a fix in one place looks like a fix, and the
// other five keep lying. That is the same failure as a mechanic living in a
// skill's prose instead of in a tool — it works until someone forgets, and
// nobody can tell when they have.
//
// So the rule lives here, once, and runs over every item from every producer
// at the single seam where the board is assembled. A new producer added
// tomorrow is covered without knowing this file exists, which is the only
// property that actually makes it robust.
//
// THE RULE
//
// A status column is a claim. Time is the evidence. When the two disagree, the
// evidence wins, because the failure mode we are defending against is exactly
// the one where the bookkeeping that would have corrected the claim is the
// thing that died.
//
// Three kinds of aliveness, and a producer declares which one it has:
//
//	in flight    a job with a run behind it. Alive only while something is
//	             still touching it, so it must show recent evidence.
//	a question   a decision addressed to the boss. It waits as long as he
//	             takes; its age says nothing, so it never goes stale.
//	armed        a watcher with nothing in flight. Continuously ready, never
//	             "running", and time cannot judge it either way.
//
// Anything in flight with no recent evidence is demoted out of the live
// columns and told the truth about itself. If it went quiet before today it
// leaves the board entirely, because the board is what is happening now and a
// job that died on Tuesday is history.

// liveWindow is how long an in-flight item may go untouched before the board
// stops calling it live.
//
// The bound comes from the runtime rather than from taste: a cron agent turn
// runs under a 30 minute context budget, so anything genuinely working updates
// well inside that. 45 minutes gives the slowest legitimate step half again as
// long as the longest one before we call it quiet.
const liveWindow = 45 * time.Minute

// liveColumns are the two that assert something is happening. Only these can
// lie in the way this file exists to stop; "queued" and "done" make no claim
// about the present.
func isLiveColumn(c string) bool { return c == "running" || c == "awaiting" }

// lastSignal is the most recent moment we have EVIDENCE this item was alive.
//
// FinishedAt is preferred because every producer that has one sets it from the
// row's updated_at, which is what actually moves when work happens. StartedAt
// is the fallback for producers that only know when a thing began. An item
// with neither cannot be judged by time at all, and says so.
func lastSignal(w WorkItem) (time.Time, bool) {
	if w.FinishedAt != nil && !w.FinishedAt.IsZero() {
		return *w.FinishedAt, true
	}
	if w.StartedAt != nil && !w.StartedAt.IsZero() {
		return *w.StartedAt, true
	}
	return time.Time{}, false
}

// hasGoneQuiet reports whether an item claiming to be live has stopped
// producing evidence that it is.
//
// The two exemptions are not special cases, they are the other two kinds of
// aliveness: a question is not stale because nobody answered it yet, and an
// armed watcher is not stale because it was never in flight to begin with.
func hasGoneQuiet(w WorkItem, now time.Time) bool {
	if !isLiveColumn(w.Column) || w.AwaitsDecision || w.Armed {
		return false
	}
	at, ok := lastSignal(w)
	if !ok {
		// No evidence either way. Demoting on no evidence would invent a
		// failure, so it stays as it is - and the fix for a producer that
		// cannot say when its work last moved is to make it say so, not to
		// guess here.
		return false
	}
	return at.Before(now.Add(-liveWindow))
}

// applyLiveness is the chokepoint. Every work item from every producer passes
// through it before the board sees it.
//
// Items that went quiet today are demoted to "done" and told the truth about
// themselves. Items that went quiet before today are dropped: the board is the
// present tense.
func applyLiveness(items []WorkItem, now time.Time) []WorkItem {
	startOfDay := now.UTC().Truncate(24 * time.Hour)
	out := items[:0:0]
	for _, w := range items {
		if !hasGoneQuiet(w, now) {
			out = append(out, w)
			continue
		}
		at, _ := lastSignal(w)
		if at.Before(startOfDay) {
			continue // history, not status
		}
		w.Column = "done"
		w.Subtitle = quietSubtitle(w.Kind)
		out = append(out, w)
	}
	return out
}

// quietSubtitle says what actually happened, in the boss's language, without
// claiming more than we know. We know it stopped producing evidence; we do not
// know that it failed, and calling it "failed" would be its own small lie.
func quietSubtitle(kind string) string {
	if kind == "mandate" {
		// A mandate is a standing definition of done, not a run. It did not
		// stop; nothing advanced it.
		return "nothing has moved on this"
	}
	return "stopped without finishing"
}
