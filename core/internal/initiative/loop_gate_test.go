package initiative

import (
	"context"
	"fmt"
	"testing"
)

// newTestGate builds a LoopGate with explicit ceilings and no trust store, so
// the pure in-memory guards (repeat, shape, grace) can be exercised without a DB.
func newTestGate(chatCeiling int) *LoopGate {
	g := NewLoopGate(nil)
	g.chatCeiling = chatCeiling
	return g
}

func authorize(g *LoopGate, sess, tool string, input map[string]any) bool {
	return g.Authorize(context.Background(), sess, "", tool, input).Allow
}

// The counter-varying junk loop (/tmp/nonexistent28, 27, 26 …) is the exact
// failure that drowned the boss in approval cards: each input hashes
// differently so the exact-repeat guard never trips. The shape guard MUST catch
// it — otherwise a confused model burns the whole session ceiling on garbage.
func TestShapeGuardBlocksCounterLoop(t *testing.T) {
	g := newTestGate(1000) // high ceiling so only the shape guard can trip
	sess := "s-counter"
	blockedAt := 0
	for i := 1; i <= shapeLimit+2; i++ {
		allow := authorize(g, sess, "claude_code__Read", map[string]any{
			"file_path": fmt.Sprintf("/tmp/nonexistent%d", i),
		})
		if !allow && blockedAt == 0 {
			blockedAt = i
		}
	}
	if blockedAt == 0 {
		t.Fatal("counter-varying loop was never blocked — shape guard inert")
	}
	if blockedAt > shapeLimit+1 {
		t.Fatalf("shape guard tripped too late at call %d (limit %d)", blockedAt, shapeLimit)
	}
}

// Distinct, non-counter inputs (e.g. real Gmail hex message ids) must NOT trip
// the shape guard — collapsing only digit runs keeps letter-bearing ids unique.
// If this regresses, legitimate batch email reads would get falsely killed.
func TestShapeGuardSparesDistinctHexIDs(t *testing.T) {
	g := newTestGate(1000)
	sess := "s-hex"
	ids := []string{
		"19e8545851a735bf", "19e850f29502537a", "19e84d1bc7e0aa31",
		"19e83fae21b9cc40", "19e8390df1a2bb55", "19e82bbc04d7ee66",
		"19e81f7a9c3d2200", "19e8130b5e6f8a99", "19e807cd4b1e7733",
		"19e7fb2a8d0c5511", "19e7ef91726b4cdd", "19e7e3007f3a9e22",
	}
	for _, id := range ids {
		if !authorize(g, sess, "composio__GMAIL_FETCH_MESSAGE_BY_MESSAGE_ID", map[string]any{"message_id": id}) {
			t.Fatalf("distinct hex id %s was wrongly blocked as a loop", id)
		}
	}
}

// The exact-repeat guard (same call ≥ repeatLimit times) must still fire — it's
// the classic stuck retry and predates the shape guard.
func TestExactRepeatGuard(t *testing.T) {
	g := newTestGate(1000)
	sess := "s-repeat"
	in := map[string]any{"q": "same"}
	for i := 0; i < repeatLimit; i++ {
		if !authorize(g, sess, "web_search", in) {
			t.Fatalf("call %d blocked before repeat limit", i)
		}
	}
	if authorize(g, sess, "web_search", in) {
		t.Fatal("identical call past repeatLimit was not blocked")
	}
}

// "Approve" must mean KEEP GOING: after a grace grant the session runs up to
// another full ceiling's worth of calls without a fresh card. The regression
// this guards: every call past the ceiling re-queued an approval, so the boss
// had to tap Approve once per call.
func TestGraceGrantMeansKeepGoing(t *testing.T) {
	ceiling := 5
	g := newTestGate(ceiling)
	sess := "s-grace"
	// Burn up to the ceiling with distinct calls (no shape/repeat trip).
	for i := 0; i < ceiling; i++ {
		if !authorize(g, sess, "read_email", map[string]any{"id": fmt.Sprintf("id-%c", 'a'+i)}) {
			t.Fatalf("call %d blocked below ceiling", i)
		}
	}
	// Next call is over the ceiling → with no trust store it is held (not allowed).
	if authorize(g, sess, "read_email", map[string]any{"id": "over"}) {
		t.Fatal("call over ceiling should not pass without approval/grace")
	}
	// Boss approves → grant grace; subsequent over-ceiling calls now pass.
	g.grantGrace(sess)
	for i := 0; i < ceiling; i++ {
		if !authorize(g, sess, "read_email", map[string]any{"id": fmt.Sprintf("g-%c", 'a'+i)}) {
			t.Fatalf("post-grace call %d was blocked — Approve did not mean keep going", i)
		}
	}
}

// A session that has invoked a skill is a heavy workflow and must get the high
// ceiling — not the 50-call chat budget. This is the exact regression behind
// "why is it so tight that it cant do what i need": a "backfill since may 22"
// runs a multi-step skill across mailboxes and legitimately needs >50 calls.
func TestSkillSessionGetsHeavyCeiling(t *testing.T) {
	g := newTestGate(50) // bare chat ceiling
	sess := "s-skill"
	// One skill invocation flips the session to heavy-workflow.
	if !authorize(g, sess, "skills_invoke", map[string]any{"name": "inbox-triage"}) {
		t.Fatal("skills_invoke itself was blocked")
	}
	// Now fire 120 distinct non-code calls — well past the chat ceiling of 50,
	// but under the heavy ceiling. None should be gated.
	for i := 0; i < 120; i++ {
		if !authorize(g, sess, "composio__GMAIL_LIST_MESSAGES", map[string]any{"page": fmt.Sprintf("p%c%c", 'a'+i/26, 'a'+i%26)}) {
			t.Fatalf("heavy-workflow call %d was gated below the heavy ceiling", i)
		}
	}
	if got := g.StatsFor(sess).Ceiling; got != codingCallCeiling {
		t.Fatalf("StatsFor ceiling = %d, want heavy ceiling %d", got, codingCallCeiling)
	}
}

// deploy_status / deploy_status_refresh take zero arguments, so every call
// hashes identically at BOTH the exact-hash level (repeat guard) AND the
// shape level (shape guard). The post-deploy-verify skill polls these in a
// push → wait → check loop; blocking ends the turn before verify completes.
// Both guards must let them through freely.
//
// The original commit 183ad50 only exempted the repeat guard (repeatLimit=3).
// The shape guard (shapeLimit=10) had no corresponding exemption, so call 11+
// would still be blocked. This test covers shapeLimit*2+1 calls to prove both
// guards are bypassed.
func TestIdempotentDeployStatusToolsBypassBothGuards(t *testing.T) {
	g := newTestGate(1000)
	sess := "s-deploy"
	for _, tool := range []string{"deploy_status", "deploy_status_refresh"} {
		for i := 0; i < shapeLimit*2+1; i++ {
			if !authorize(g, sess, tool, nil) {
				t.Fatalf("%s call %d wrongly blocked (repeat guard trips at %d, shape guard at %d)",
					tool, i+1, repeatLimit, shapeLimit)
			}
		}
	}
}

// Denial clears grace + pending so a stale grant can't silently keep a denied
// session running.
func TestStopSessionClearsGrace(t *testing.T) {
	g := newTestGate(1)
	sess := "s-stop"
	g.grantGrace(sess)
	g.stopSession(sess)
	g.mu.Lock()
	_, hasGrace := g.grace[sess]
	_, hasPending := g.pending[sess]
	g.mu.Unlock()
	if hasGrace || hasPending {
		t.Fatal("stopSession left grace/pending state behind")
	}
}
