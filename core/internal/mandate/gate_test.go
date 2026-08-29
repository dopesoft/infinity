package mandate

import (
	"strings"
	"testing"
	"time"
)

// The done-gate is what stops "done" being a vibe, so these tests encode the
// ways it has to refuse — not the strings it returns.

// A mandate promising nothing proves nothing. Letting an empty one close is
// how a green tick gets earned by opening a contract and never writing it.
func TestBlockers_AMandateWithNoCriteriaIsNotDone(t *testing.T) {
	b := Blockers(&Mandate{Title: "ship it", Status: StatusOpen})
	if len(b) == 0 {
		t.Fatal("a mandate with no criteria must never be closeable")
	}
	if !strings.Contains(strings.Join(b, " "), "no acceptance criteria") {
		t.Fatalf("the refusal must say why: %v", b)
	}
}

func TestBlockers_ListsEveryUnprovenCriterion(t *testing.T) {
	m := &Mandate{
		Status: StatusOpen,
		Criteria: []Criterion{
			{ID: "c1", Text: "go build ./... exits 0", Status: CritPass},
			{ID: "c2", Text: "migrations applied to the live database", Status: CritPending},
			{ID: "c3", Text: "committed with a clean tree", Status: CritFail},
		},
	}
	b := strings.Join(Blockers(m), " | ")
	if strings.Contains(b, "go build") {
		t.Fatalf("a passing criterion is not a blocker: %s", b)
	}
	if !strings.Contains(b, "migrations applied") || !strings.Contains(b, "clean tree") {
		t.Fatalf("every unproven criterion must be named: %s", b)
	}
	if !strings.Contains(b, "(failing)") {
		t.Fatalf("a criterion that was CHECKED and failed must read differently from one never checked: %s", b)
	}
}

// High-stakes work can't be waved through by its own author even when every
// criterion is ticked — that is the whole point of the crosscheck.
func TestBlockers_HighStakesNeedsAnIndependentPass(t *testing.T) {
	m := &Mandate{
		Status:     StatusOpen,
		HighStakes: true,
		Criteria:   []Criterion{{ID: "c1", Text: "sent", Status: CritPass}},
	}
	if b := Blockers(m); len(b) != 1 || !strings.Contains(b[0], "mandate_verify") {
		t.Fatalf("high-stakes with no verification must block: %v", b)
	}
	now := time.Now()
	m.VerifiedAt = &now
	if b := Blockers(m); len(b) != 0 {
		t.Fatalf("a verified high-stakes mandate with all criteria passing is done: %v", b)
	}
}

// An already-closed mandate is not re-litigated; Close returns it as-is.
func TestBlockers_DoneIsDone(t *testing.T) {
	if b := Blockers(&Mandate{Status: StatusDone}); len(b) != 0 {
		t.Fatalf("a closed mandate has no blockers: %v", b)
	}
}

// The migration check must fire on the claim that migrations are APPLIED, and
// must not hijack an unrelated criterion that merely mentions a database or
// the act of writing one.
func TestIsMigrationCriterion_MatchesTheProofNotTheAuthoring(t *testing.T) {
	yes := []string{
		"migrations applied to the live database",
		"migration 194 is applied in prod",
		"infinity migrate run, schema up to date",
		"no pending migrations",
		"schema_migrations contains 194",
		"the migration is deployed",
	}
	for _, s := range yes {
		if !isMigrationCriterion(s) {
			t.Fatalf("%q claims the migrations are applied", s)
		}
	}
	no := []string{
		"write a migration for the new column",
		"the database query returns 200",
		"go build ./... exits 0",
		"committed with a clean tree",
		// Authoring is not proof: a written-but-unapplied migration is the
		// exact state that stranded 011-014, so this must NOT count as the
		// applied claim.
		"add a migration file under core/db/migrations",
	}
	for _, s := range no {
		if isMigrationCriterion(s) {
			t.Fatalf("%q is not a claim that migrations are applied", s)
		}
	}
}

// A nil store must not pretend to gate. Every one of these paths is reached in
// chat-only / migrate-only processes where no pool exists.
func TestGate_NilSafe(t *testing.T) {
	var s *Store
	if err := s.CheckStepDone(t.Context(), "step-1"); err != nil {
		t.Fatalf("no store means no gate, not an error: %v", err)
	}
	if got := s.UnprovenMigrations(t.Context(), &Mandate{}); got != "" {
		t.Fatalf("no store cannot claim drift: %q", got)
	}
	m, err := s.ForStep(t.Context(), "step-1")
	if m != nil || err != nil {
		t.Fatalf("no store returns nothing, cleanly: %v %v", m, err)
	}
}

// A step with no mandate is the overwhelmingly common case and must stay
// completely unaffected — a gate that blocks ordinary steps would be worse
// than no gate at all.
func TestGate_AStepWithNoMandateIsNotGated(t *testing.T) {
	var s *Store
	if err := s.CheckStepDone(t.Context(), ""); err != nil {
		t.Fatalf("an unbound step passes freely: %v", err)
	}
}
