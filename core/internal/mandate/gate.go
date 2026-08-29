package mandate

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/dopesoft/infinity/core/db"
	"github.com/jackc/pgx/v5"
)

// The DONE-GATE, and the plan step it now also guards.
//
// mem_mandates has always had a real contract behind "done": binary criteria,
// evidence per criterion, and Close refusing while anything is unproven. What
// it did NOT have was any connection to the thing the boss actually watches —
// the plan step. plan_update(status='done') would flip a step green on the
// model's word while an open mandate beside it sat with three criteria still
// pending, because the only gate on a step was verify_required, which most
// steps never set.
//
// Migration 194 couples them. This file is the shared check: Close and the
// plan-step gate ask the SAME function whether a mandate is done, so there is
// one definition of the word and not two that can drift apart.
//
// It also proves what Go can prove. "Migrations applied" is one of the four
// proofs the boss requires of a finished coding task, and it is a fact about
// the live database, not an opinion — so a criterion about migrations is
// checked against `schema_migrations` here and CANNOT be ticked off while
// there is drift, whatever the model believes. Everything else stays a
// judgment the model makes with evidence, which is the correct division.

// Blockers returns the reasons this mandate cannot close yet, in the order a
// person would want to read them. Empty means it is genuinely done.
//
// A mandate with NO criteria is blocked: a contract that promises nothing
// proves nothing, and letting one close was how "done" stayed a vibe.
func Blockers(m *Mandate) []string {
	if m == nil {
		return []string{"there is no mandate to check"}
	}
	if m.Status == StatusDone {
		return nil
	}
	var out []string
	if len(m.Criteria) == 0 {
		out = append(out, "it has no acceptance criteria at all, so there is nothing proving it is finished")
	}
	for _, c := range m.FailingCriteria() {
		out = append(out, c)
	}
	if m.HighStakes && m.VerifiedAt == nil {
		out = append(out, "it is high-stakes and no independent verification has passed yet (mandate_verify)")
	}
	return out
}

// migrationCriterion matches a criterion whose subject is the schema being
// migrated. Deliberately narrow: it must mention the act, not merely the word
// "database", so an unrelated criterion is never hijacked by this check.
func isMigrationCriterion(text string) bool {
	t := strings.ToLower(text)
	if !strings.Contains(t, "migrat") && !strings.Contains(t, "schema_migrations") {
		return false
	}
	// "write a migration" is authoring; "migrations applied" is the proof.
	for _, verb := range []string{"appl", "run", "live", "prod", "up to date", "up-to-date", "current", "pending", "deployed"} {
		if strings.Contains(t, verb) {
			return true
		}
	}
	return strings.Contains(t, "schema_migrations")
}

// UnprovenMigrations returns a blocker when the mandate claims migrations are
// applied but the live database disagrees.
//
// This is the one criterion the model is not trusted on, because it is the one
// with a track record: 011 through 014 sat unapplied in production for weeks
// while everything reported fine, and a prior session asserted they were live
// without checking. A probe that cannot run is reported as unknown and blocks
// too — "I could not look" must never pass as "verified".
func (s *Store) UnprovenMigrations(ctx context.Context, m *Mandate) string {
	if s == nil || s.pool == nil || m == nil {
		return ""
	}
	claimed := false
	for _, c := range m.Criteria {
		if c.Status == CritPass && isMigrationCriterion(c.Text) {
			claimed = true
			break
		}
	}
	if !claimed {
		return ""
	}
	pending, err := db.Pending(ctx, s.pool)
	if err != nil {
		return fmt.Sprintf("a criterion says the migrations are applied, and I could not verify that against the database (%v) — "+
			"run `infinity migrate` and confirm its output before closing this", err)
	}
	if len(pending) == 0 {
		return ""
	}
	return fmt.Sprintf("a criterion says the migrations are applied, but %d are still pending on the live database (%s) — "+
		"run `infinity migrate` and confirm it prints `apply` for each, then tick that criterion again",
		len(pending), strings.Join(clipNames(pending, 5), ", "))
}

func clipNames(names []string, n int) []string {
	if len(names) <= n {
		return names
	}
	out := append([]string{}, names[:n]...)
	return append(out, fmt.Sprintf("…and %d more", len(names)-n))
}

// ForStep loads the OPEN mandate guarding a plan step, or nil when the step
// has none (the overwhelmingly common case, and not an error).
func (s *Store) ForStep(ctx context.Context, stepID string) (*Mandate, error) {
	if s == nil || s.pool == nil || strings.TrimSpace(stepID) == "" {
		return nil, nil
	}
	m, err := scanOne(s.pool.QueryRow(ctx, selectCols+`
		 WHERE step_id = $1::uuid AND status IN ('open','verifying')
		 ORDER BY updated_at DESC
		 LIMIT 1`, stepID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return m, err
}

// LinkPlanStep binds a mandate to the plan step it defines "done" for, so the
// step inherits the mandate's gate.
func (s *Store) LinkPlanStep(ctx context.Context, mandateID, planID, stepID string) error {
	if s == nil || s.pool == nil {
		return errors.New("mandate store not configured")
	}
	var planArg, stepArg any
	if strings.TrimSpace(planID) != "" {
		planArg = planID
	}
	if strings.TrimSpace(stepID) != "" {
		stepArg = stepID
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_mandates
		   SET plan_id = COALESCE($2::uuid, plan_id),
		       step_id = COALESCE($3::uuid, step_id),
		       updated_at = NOW()
		 WHERE id = $1::uuid
	`, mandateID, planArg, stepArg)
	return err
}

// CheckStepDone is the plan-step done-gate. It returns nil when the step is
// free to be marked done — including when no mandate guards it, which is most
// steps — and an explaining error when it is not.
//
// Implements the tools.StepDoneGate seam, so plan_update consults it without
// the plan tools knowing mandates exist.
func (s *Store) CheckStepDone(ctx context.Context, stepID string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	m, err := s.ForStep(ctx, stepID)
	if err != nil {
		// Failing to READ the gate must not silently open it: a step guarded
		// by a contract we cannot load is a step whose state is unknown.
		return fmt.Errorf("I couldn't check this step's definition of done (%v), so I'm not marking it done on a guess — retry in a moment", err)
	}
	if m == nil {
		return nil
	}
	blockers := Blockers(m)
	if drift := s.UnprovenMigrations(ctx, m); drift != "" {
		blockers = append(blockers, drift)
	}
	if len(blockers) == 0 {
		return nil
	}
	return fmt.Errorf("this step isn't done yet — %q still needs: %s. Satisfy each one and record the proof with mandate_check (id %s); "+
		"if one of them turns out not to apply, say so to the boss rather than ticking it",
		m.Title, strings.Join(blockers, "; "), m.ID)
}
