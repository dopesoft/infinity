package dashboard

import (
	"testing"
	"time"
)

// now is fixed at mid-afternoon so "quiet before today" and "quiet today" are
// both reachable without the test depending on when it runs.
var testNow = time.Date(2026, 8, 29, 15, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time {
	t := testNow.Add(d)
	return &t
}

// These are the actual rows the board was lying about on 2026-08-29, one per
// producer. They are here as a set rather than as a plans test because the
// point is that ONE rule covers all six producers: a fix that only knows about
// plans is the shape of bug this file exists to end.
func TestLivenessDemotesEveryProducersCorpse(t *testing.T) {
	cases := []struct {
		name string
		in   WorkItem
		// "" means the item must be dropped from the board entirely.
		wantColumn   string
		wantSubtitle string
	}{
		{
			// The four "/workspace/infinity" rows: plan status 'active', no
			// session id, orphaned when the build died three days earlier.
			// Nothing existed that could ever have corrected them.
			name: "plan active for three days is not running",
			in: WorkItem{
				Kind: "plan", Column: "running", Subtitle: "next: build",
				StartedAt: at(-4 * 24 * time.Hour), FinishedAt: at(-3 * 24 * time.Hour),
			},
			wantColumn: "",
		},
		{
			// Ten of these sat in the needs-you lane. "Paused at checkpoint"
			// reads as though he is the thing holding it up. He was not.
			name: "plan paused since June is not waiting on him",
			in: WorkItem{
				Kind: "plan", Column: "awaiting", Subtitle: "paused at checkpoint",
				FinishedAt: at(-82 * 24 * time.Hour),
			},
			wantColumn: "",
		},
		{
			// The producer my first pass missed entirely. Four open mandates
			// in Running, the oldest untouched for twenty seven days.
			name: "mandate open and untouched for weeks is not running",
			in: WorkItem{
				Kind: "mandate", Column: "running", Subtitle: "2/5 verified",
				FinishedAt: at(-27 * 24 * time.Hour),
			},
			wantColumn: "",
		},
		{
			// Same corpse, but it died this morning. It stays on the board,
			// because today is what the board is about - it just stops
			// claiming to be in flight.
			name: "work that went quiet earlier today lands in done, told truthfully",
			in: WorkItem{
				Kind: "plan", Column: "running", Subtitle: "next: build",
				FinishedAt: at(-3 * time.Hour),
			},
			wantColumn: "done", wantSubtitle: "stopped without finishing",
		},
		{
			// A mandate did not "stop" - it is a standing definition of done
			// that nothing advanced. Saying "stopped without finishing" there
			// would be its own small lie.
			name: "a quiet mandate is described as unmoved, not as stopped",
			in: WorkItem{
				Kind: "mandate", Column: "running", Subtitle: "2/5 verified",
				FinishedAt: at(-3 * time.Hour),
			},
			wantColumn: "done", wantSubtitle: "nothing has moved on this",
		},

		// ── things the guard must NOT touch ───────────────────────────────
		{
			name: "genuinely running work is left alone",
			in: WorkItem{
				Kind: "cron_run", Column: "running", Subtitle: "running · via cron",
				StartedAt: at(-2 * time.Minute),
			},
			wantColumn: "running", wantSubtitle: "running · via cron",
		},
		{
			// The slow side of the boundary. A step already past any
			// legitimate cron budget still gets its full window.
			name: "in flight at 44 minutes is still running",
			in: WorkItem{
				Kind: "plan", Column: "running", FinishedAt: at(-44 * time.Minute),
			},
			wantColumn: "running",
		},
		{
			name: "in flight at 46 minutes has gone quiet",
			in: WorkItem{
				Kind: "plan", Column: "running", FinishedAt: at(-46 * time.Minute),
			},
			wantColumn: "done", wantSubtitle: "stopped without finishing",
		},
		{
			// The carve-out that matters most. Ageing out a decision he never
			// made is the same crime as the phantom Running row, pointed at
			// something he actually cares about.
			name: "a pending permission never goes stale",
			in: WorkItem{
				Kind: "trust", Column: "awaiting", Subtitle: "needs your okay",
				AwaitsDecision: true, StartedAt: at(-30 * 24 * time.Hour),
			},
			wantColumn: "awaiting", wantSubtitle: "needs your okay",
		},
		{
			name: "an old code proposal is still a live question",
			in: WorkItem{
				Kind: "code_proposal", Column: "awaiting", AwaitsDecision: true,
				StartedAt: at(-60 * 24 * time.Hour),
			},
			wantColumn: "awaiting",
		},
		{
			name: "an unanswered plan proposal is still a live question",
			in: WorkItem{
				Kind: "plan", Column: "awaiting", Subtitle: "proposed, waiting for your go",
				AwaitsDecision: true, FinishedAt: at(-9 * 24 * time.Hour),
			},
			wantColumn: "awaiting",
		},
		{
			// A watcher has no run behind it, so it was never in flight and
			// time has no opinion about it either way.
			name: "an armed sentinel is not judged by time",
			in: WorkItem{
				Kind: "sentinel", Column: "running", Subtitle: "watching", Armed: true,
			},
			wantColumn: "running", wantSubtitle: "watching",
		},
		{
			// Demoting on no evidence would invent a failure. The fix for a
			// producer that cannot say when its work last moved is to make it
			// say so, not to guess.
			name: "no timestamp at all is left alone rather than guessed at",
			in: WorkItem{
				Kind: "workflow", Column: "running", Subtitle: "running · step 2",
			},
			wantColumn: "running", wantSubtitle: "running · step 2",
		},
		{
			// "queued" and "done" make no claim about the present, so there is
			// nothing here for this guard to correct.
			name: "a queued item is never demoted however old",
			in: WorkItem{
				Kind: "cron_run", Column: "queued", Subtitle: "due 3:00am",
				StartedAt: at(-40 * 24 * time.Hour),
			},
			wantColumn: "queued", wantSubtitle: "due 3:00am",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.ID = "x"
			got := applyLiveness([]WorkItem{tc.in}, testNow)

			if tc.wantColumn == "" {
				if len(got) != 0 {
					t.Fatalf("want dropped from the board, got column %q subtitle %q",
						got[0].Column, got[0].Subtitle)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("want kept on the board, got %d items", len(got))
			}
			if got[0].Column != tc.wantColumn {
				t.Fatalf("column = %q, want %q", got[0].Column, tc.wantColumn)
			}
			if tc.wantSubtitle != "" && got[0].Subtitle != tc.wantSubtitle {
				t.Fatalf("subtitle = %q, want %q", got[0].Subtitle, tc.wantSubtitle)
			}
		})
	}
}

// The guard has to hold for producers nobody has written yet. A new source
// that forgets about liveness entirely still cannot put a corpse in Running,
// because it does not get a vote - it passes through the same seam.
func TestLivenessCoversAProducerThatKnowsNothingAboutIt(t *testing.T) {
	naive := WorkItem{
		ID: "new-1", Kind: "some_future_kind", Column: "running",
		Subtitle: "running", FinishedAt: at(-2 * time.Hour),
	}
	got := applyLiveness([]WorkItem{naive}, testNow)
	if len(got) != 1 || got[0].Column != "done" {
		t.Fatalf("a new producer must be covered by construction, got %+v", got)
	}
}

// Order is how the board reads. The guard rewrites items in place and drops
// some; it must never shuffle the survivors.
func TestLivenessPreservesOrderOfSurvivors(t *testing.T) {
	in := []WorkItem{
		{ID: "a", Column: "running", StartedAt: at(-1 * time.Minute)},
		{ID: "dead", Column: "running", FinishedAt: at(-9 * 24 * time.Hour)},
		{ID: "b", Column: "queued"},
		{ID: "c", Column: "awaiting", AwaitsDecision: true},
	}
	got := applyLiveness(in, testNow)
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %d items, want %d", len(got), len(want))
	}
	for i, id := range want {
		if got[i].ID != id {
			t.Fatalf("position %d = %q, want %q", i, got[i].ID, id)
		}
	}
}
