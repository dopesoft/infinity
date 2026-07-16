package voyager

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"github.com/dopesoft/infinity/core/internal/eval"
	"github.com/google/uuid"
)

// Skill verification harness.
//
// The boss's rule: "HOW DO WE BUILD A SKILL AND THEN WE DONT ENSURE IT CAN BRING
// BACK DATA. IF THE FUCKIN SKILL CANT BE VERIFIED THEN THE TEST CLEANS ITSELF UP."
//
// Until now a skill was promoted on a regex over its SKILL.md (it never ran).
// This harness actually RUNS the recipe — read-only, in an ephemeral session —
// asserts it returned real data, records the outcome to mem_evals, and then
// DELETES everything the test wrote (the only sanctioned hard delete, scoped
// strictly to the synthetic session). A skill that can't prove data is NOT
// promoted; its test footprint is gone.
//
// Rule #1: the verification SCENARIO is skill-authored DATA (the verify_contract
// column); this harness is generic Go.

// verifyContract is the skill-authored proof spec (mem_skills.verify_contract).
type verifyContract struct {
	// Scenario is the read-only task to exercise the skill's data path. Empty →
	// a generic "exercise your core data path once."
	Scenario string `json:"scenario"`
	// Assert, when set, is a substring that MUST appear in the result for a pass.
	// Empty → the default heuristic (ran, reported a non-empty/non-zero result).
	Assert string `json:"assert"`
	// MinArtifacts, when >0, requires at least N observations written under the
	// test session (a skill that genuinely touched data leaves a trail).
	MinArtifacts int `json:"min_artifacts"`
}

// VerifySkill loads the active SKILL.md + verify_contract for a named skill and
// runs the gate. Used on-demand (API / manual re-verify of an active skill).
func (m *Manager) VerifySkill(ctx context.Context, skillName string) (bool, string, error) {
	if m == nil || m.pool == nil {
		return false, "no pool", nil
	}
	var skillMD string
	err := m.pool.QueryRow(ctx, `
		SELECT v.skill_md
		  FROM mem_skill_active a
		  JOIN mem_skill_versions v
		    ON v.skill_name = a.skill_name AND v.version = a.active_version
		 WHERE a.skill_name = $1
	`, skillName).Scan(&skillMD)
	if err != nil {
		return false, "active version not found: " + err.Error(), err
	}
	return m.VerifySkillMD(ctx, skillName, skillMD, m.loadVerifyContract(ctx, skillName))
}

// VerifySkillMD runs a given recipe (skillMD) against a contract in an ephemeral,
// read-only test session, asserts it returned data, records the eval, and cleans
// up. The recipe is handed to the agent DIRECTLY (not via skills_invoke) so an
// unpromoted proposal can be verified before it's ever registered.
func (m *Manager) VerifySkillMD(ctx context.Context, skillName, skillMD string, contract verifyContract) (bool, string, error) {
	if m == nil || m.pool == nil {
		return false, "no pool", nil
	}
	if m.runTurn == nil {
		// No runner wired (e.g. unit test, loop-less deploy). Can't prove data;
		// signal "unverified" without blocking — the caller decides.
		return false, "no verification runner wired", nil
	}

	testSession := uuid.NewString()
	m.markTestSession(ctx, testSession, skillName)
	defer m.cleanupTestSession(ctx, testSession) // always clean, pass or fail

	out, runErr := m.runTurn(ctx, testSession, buildVerifyPrompt(skillName, skillMD, contract))
	artifacts := m.countTestArtifacts(ctx, testSession)
	passed, notes := assessVerification(contract, out, artifacts, runErr)

	outcome := eval.OutcomeFailure
	if passed {
		outcome = eval.OutcomeSuccess
	}
	if m.evals != nil {
		_ = m.evals.Record(ctx, &eval.Eval{
			SubjectKind: "skill",
			SubjectName: skillName,
			Outcome:     outcome,
			Notes:       notes,
			Source:      "verify_harness",
		})
	}
	return passed, notes, nil
}

// loadVerifyContract reads mem_skills.verify_contract; zero value when absent.
func (m *Manager) loadVerifyContract(ctx context.Context, skillName string) verifyContract {
	var c verifyContract
	var raw []byte
	if err := m.pool.QueryRow(ctx, `
		SELECT COALESCE(verify_contract, '{}'::jsonb) FROM mem_skills WHERE name = $1
	`, skillName).Scan(&raw); err != nil {
		return c
	}
	_ = json.Unmarshal(raw, &c)
	return c
}

// markTestSession pre-stamps the ephemeral session as a container so it stays
// out of the boss's sessions list (which shows kind='user' only). This MUST run
// before runTurn: the turn's own memory.Store.OpenSession upserts the same id,
// and its ON CONFLICT deliberately doesn't touch kind, so whichever row lands
// first decides. Losing that race means a phantom blank session in his history
// for the length of the verify, so a failure here is logged, never swallowed —
// 'skill_test' was missing from mem_sessions_kind_check until migration 184 and
// this Exec had failed silently on every run since it was written.
func (m *Manager) markTestSession(ctx context.Context, sessionID, skillName string) {
	if _, err := m.pool.Exec(ctx, `
		INSERT INTO mem_sessions (id, kind, origin_ref, started_at)
		VALUES ($1::uuid, 'skill_test', $2::jsonb, NOW())
		ON CONFLICT (id) DO NOTHING
	`, sessionID, fmt.Sprintf(`{"kind":"skill_test","skill":%q}`, skillName)); err != nil {
		log.Printf("voyager: mark test session for skill %q: %v (verification will surface as a blank session)", skillName, err)
	}
}

func (m *Manager) countTestArtifacts(ctx context.Context, sessionID string) int {
	var n int
	_ = m.pool.QueryRow(ctx, `
		SELECT count(*) FROM mem_observations WHERE session_id = $1
	`, sessionID).Scan(&n)
	return n
}

// cleanupTestSession is the ONLY sanctioned hard delete in the codebase, scoped
// strictly to the synthetic verification session UUID. It removes the test's
// entire footprint so verification never pollutes the boss's data. (Surfaces /
// followups are not deleted here because verification scenarios are read-only by
// contract and never write them — see buildVerifyPrompt.)
func (m *Manager) cleanupTestSession(ctx context.Context, sessionID string) {
	for _, tbl := range []string{"mem_observations", "mem_turns", "mem_skill_runs"} {
		_, _ = m.pool.Exec(ctx, fmt.Sprintf(`DELETE FROM %s WHERE session_id = $1`, tbl), sessionID)
	}
	_, _ = m.pool.Exec(ctx, `DELETE FROM mem_sessions WHERE id = $1::uuid`, sessionID)
}

const verifyResultMarker = "VERIFY_RESULT:"

func buildVerifyPrompt(skillName, skillMD string, c verifyContract) string {
	scenario := strings.TrimSpace(c.Scenario)
	if scenario == "" {
		scenario = "Exercise this skill's core data path exactly once and show what comes back."
	}
	var b strings.Builder
	b.WriteString("You are running a VERIFICATION TEST of the skill \"")
	b.WriteString(skillName)
	b.WriteString("\" in an isolated sandbox. Your ONLY job is to prove the skill can actually return real data.\n\n")
	b.WriteString("STRICT RULES — this is read-only:\n")
	b.WriteString("- Do NOT surface anything to the dashboard, do NOT create or resolve followups, do NOT draft or send any email/message, do NOT write memories, do NOT commit or push code, do NOT modify or delete anything.\n")
	b.WriteString("- ONLY fetch / list / compute / classify to demonstrate the data path works.\n")
	b.WriteString("- Keep it small (one lean batch is enough).\n\n")
	b.WriteString("SCENARIO:\n")
	b.WriteString(scenario)
	b.WriteString("\n\nSKILL RECIPE TO EXECUTE:\n")
	b.WriteString(skillMD)
	b.WriteString("\n\nWhen done, end your reply with EXACTLY one final line:\n")
	b.WriteString(verifyResultMarker)
	b.WriteString(" <one sentence stating the concrete data that came back, including a count> — or, if no data came back or you were blocked, state plainly that it FAILED and why.\n")
	return b.String()
}

// assessVerification decides pass/fail from the run error, the agent's reported
// result line, and the artifact count. Default heuristic when no explicit Assert
// is given: the run succeeded, reported a VERIFY_RESULT, and that result doesn't
// read as a failure / empty / zero.
func assessVerification(c verifyContract, out string, artifacts int, runErr error) (bool, string) {
	if runErr != nil {
		return false, "run errored: " + runErr.Error()
	}
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return false, "no output from verification run"
	}
	if c.MinArtifacts > 0 && artifacts < c.MinArtifacts {
		return false, fmt.Sprintf("only %d data artifact(s), need ≥%d", artifacts, c.MinArtifacts)
	}

	lower := strings.ToLower(trimmed)

	// Explicit assertion: the result MUST contain the skill-authored substring.
	if a := strings.TrimSpace(c.Assert); a != "" {
		if strings.Contains(lower, strings.ToLower(a)) {
			return true, "matched assert: " + a
		}
		return false, "assert substring not found: " + a
	}

	// Default heuristic: pull the VERIFY_RESULT line and judge it.
	result := lower
	if i := strings.LastIndex(lower, strings.ToLower(verifyResultMarker)); i >= 0 {
		result = strings.TrimSpace(lower[i+len(verifyResultMarker):])
	}
	if result == "" {
		return false, "no VERIFY_RESULT reported"
	}
	for _, bad := range []string{"failed", "no data", "could not", "couldn't", "cannot", "can't", "unable", "blocked", "error", "empty", "nothing", "0 ", " zero"} {
		if strings.Contains(result, bad) {
			return false, "verification reported a failure: " + clip(result, 160)
		}
	}
	return true, "verified data return: " + clip(result, 160)
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
