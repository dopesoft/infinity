package surface

import (
	"testing"
	"time"
)

// The inbox silts up when informational cards never expire. applyDefaultTTL is
// the mechanic that prevents it, and each carve-out below protects something
// the boss would notice losing — so this table is the contract, not a nicety.
func TestApplyDefaultTTL(t *testing.T) {
	explicit := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		item    Item
		wantTTL bool
		why     string
	}{
		{
			name:    "agent FYI gets a TTL",
			item:    Item{Surface: "insights", Kind: "finding"},
			wantTTL: true,
			why:     "action-less agent notes are what accumulate forever",
		},
		{
			name:    "health alert gets a TTL",
			item:    Item{Surface: "health", Kind: "alert", ExternalID: "ext-yt-dlp"},
			wantTTL: true,
			why:     "re-upsert under the same external_id refreshes it while the condition lasts",
		},
		{
			name:    "producer's explicit expiry wins",
			item:    Item{Surface: "runs", Kind: "run_outcome", ExpiresAt: &explicit},
			wantTTL: true,
			why:     "the 36h cron/self-heal TTL must not be overwritten with 72h",
		},
		{
			name:    "a card with actions never expires",
			item:    Item{Surface: "routines", Kind: "skill_proposal", Actions: []Action{{ID: "dismiss", Label: "Not a routine"}}},
			wantTTL: false,
			why:     "it awaits a decision; expiring it decides on the boss's behalf",
		},
		{
			name:    "follow-up mail never expires",
			item:    Item{Surface: "followups", Kind: "email"},
			wantTTL: false,
			why:     "nothing automated resolves the boss's inbox",
		},
		{
			name:    "system surface never expires",
			item:    Item{Surface: "system", Kind: "insight"},
			wantTTL: false,
			why:     "Activity reads status='open'; a TTL would erase his history, not tidy it",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			it := tc.item
			applyDefaultTTL(&it)
			if got := it.ExpiresAt != nil; got != tc.wantTTL {
				t.Fatalf("ExpiresAt set = %v, want %v — %s", got, tc.wantTTL, tc.why)
			}
		})
	}

	// The producer's own value must survive verbatim, not just be non-nil.
	it := Item{Surface: "runs", ExpiresAt: &explicit}
	applyDefaultTTL(&it)
	if !it.ExpiresAt.Equal(explicit) {
		t.Fatalf("explicit expiry overwritten: got %v, want %v", it.ExpiresAt, explicit)
	}
}

func TestApplyDefaultTTLNilSafe(t *testing.T) {
	applyDefaultTTL(nil) // must not panic — Upsert calls this before its nil check pays off
}
