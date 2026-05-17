package proactive

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Skill self-authoring checklist - the "Jarvis noticed a repeatable
// recipe" + "Jarvis ran a skill but the boss did it better" detectors.
//
// Two scanners that fire every heartbeat tick:
//
//  1. Pattern detector. Group recent PostToolUse observations into
//     rolling 3-tool sequences. When the same signature recurs 3+
//     times in the last 7 days AND no active skill maps to that
//     sequence, emit a 'skill_opportunity' finding so /lab Fix-this
//     surfaces it with an "Approve & fix" path that routes to the
//     propose-skill-from-pattern default skill.
//
//  2. Drift detector. For each recent skill invocation, sample the
//     tool calls that followed in the same session. When the boss
//     steered a different (successful) sequence than the skill's
//     documented steps AND the divergence recurs 2+ times, emit a
//     'skill_drift' finding routing to the evolve-skill-from-deviation
//     skill.
//
// Both detectors are deterministic SQL; no LLM. The cognition lives
// in the two seed skills (migration 037), not here. Dedup uses the
// source_tag lifecycle from migration 034: skill_pattern:<sighash>
// and skill_drift:<skillname>.
//
// Thresholds are intentionally conservative. Cheap to widen later
// once we see real volume; expensive to live with a noisy detector
// surfacing every coincidence.

const (
	skillPatternWindow    = "7 days"
	skillPatternMinHits   = 3
	skillDriftWindow      = "7 days"
	skillDriftMinHits     = 2
	skillScanObservations = 500
)

// SkillAuthoringChecklist returns a Checklist function that runs both
// scanners and emits Findings for any newly-detected opportunity or
// drift. Compose with the other checklists via ComposeChecklists in
// serve.go.
func SkillAuthoringChecklist(pool *pgxpool.Pool) Checklist {
	return func(ctx context.Context, _ *Heartbeat) ([]Finding, error) {
		if pool == nil {
			return nil, nil
		}
		var findings []Finding
		findings = append(findings, scanSkillOpportunities(ctx, pool)...)
		findings = append(findings, scanSkillDrift(ctx, pool)...)
		return findings, nil
	}
}

// scanSkillOpportunities groups recent PostToolUse observations into
// rolling 3-tool sequences per session and counts how often each
// signature appears across sessions. A signature with hits >= threshold
// that isn't already mapped to an active skill earns one
// 'skill_opportunity' finding.
//
// The "isn't already a skill" check is best-effort: we compare against
// the trigger_phrase signatures stored alongside active skills - which
// won't catch every overlap, but does catch the common case where the
// same skill is already installed and the agent just forgot to call it.
func scanSkillOpportunities(ctx context.Context, pool *pgxpool.Pool) []Finding {
	rows, err := pool.Query(ctx, `
		WITH recent AS (
			SELECT
				session_id,
				created_at,
				COALESCE(payload->>'name','') AS tool_name
			  FROM mem_observations
			 WHERE hook_name IN ('PostToolUse','PostToolUseSuccess')
			   AND created_at > NOW() - INTERVAL '`+skillPatternWindow+`'
			   AND COALESCE(payload->>'name','') <> ''
			   AND session_id IS NOT NULL
			 ORDER BY session_id, created_at
			 LIMIT `+itoa(skillScanObservations*4)+`
		),
		windowed AS (
			SELECT
				session_id,
				created_at,
				tool_name,
				LAG(tool_name, 1) OVER (PARTITION BY session_id ORDER BY created_at) AS prev1,
				LAG(tool_name, 2) OVER (PARTITION BY session_id ORDER BY created_at) AS prev2
			  FROM recent
		),
		sigs AS (
			SELECT
				session_id,
				MAX(created_at) AS last_seen,
				prev2 || ' -> ' || prev1 || ' -> ' || tool_name AS signature
			  FROM windowed
			 WHERE prev1 IS NOT NULL AND prev2 IS NOT NULL
			 GROUP BY session_id, signature
		),
		grouped AS (
			SELECT
				signature,
				COUNT(*) AS sessions_hit,
				MAX(last_seen) AS last_seen
			  FROM sigs
			 GROUP BY signature
		)
		SELECT signature, sessions_hit, last_seen
		  FROM grouped
		 WHERE sessions_hit >= $1
		 ORDER BY sessions_hit DESC, last_seen DESC
		 LIMIT 5
	`, skillPatternMinHits)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var (
			signature string
			hits      int
			lastSeen  time.Time
		)
		if err := rows.Scan(&signature, &hits, &lastSeen); err != nil {
			continue
		}
		sigHash := shortHash(signature)
		// Cheap dedup against active skills - if any active skill's name
		// or description text already references the same tool chain
		// verbatim, skip. This is intentionally weak; the boss can
		// always dismiss a duplicate in /lab.
		if mapsToActiveSkill(ctx, pool, signature) {
			continue
		}
		question := fmt.Sprintf("You've run %q %d times. Crystallize it into a skill?", signature, hits)
		rationale := fmt.Sprintf(
			"Pattern signature %q observed across %d sessions in the last %s (most recent %s). No active skill maps to it. Approve to draft a SKILL.md via the propose-skill-from-pattern recipe.",
			signature, hits, skillPatternWindow, lastSeen.UTC().Format(time.RFC3339),
		)
		tag := "skill_pattern:" + sigHash
		ResolveQuestionsBySourceTag(ctx, pool, tag)
		inserted := insertHealingQuestionWithTag(ctx, pool,
			question, rationale, "skill_opportunity", tag, nil, 7)
		if !inserted {
			continue
		}
		out = append(out, Finding{
			Kind:      "skill_opportunity",
			Source:    "skill_opportunity",
			Title:     question,
			Detail:    rationale,
			SourceTag: tag,
		})
	}
	return out
}

// scanSkillDrift looks for installed skills whose recent invocations
// were followed by a divergent (successful) tool sequence in the same
// session, and groups those divergences. When the same divergence
// recurs >= threshold times, emit one 'skill_drift' finding per skill.
//
// "Successful" here means the divergent run did NOT produce any
// PostToolUseFailure observations within the same session-window.
// "Divergent" is approximated by comparing the post-invocation tool
// signature against the skill's documented body via a substring check:
// if the actual signature isn't a substring of the body, that's drift.
// Cheap and good enough for a first pass.
func scanSkillDrift(ctx context.Context, pool *pgxpool.Pool) []Finding {
	rows, err := pool.Query(ctx, `
		WITH invocations AS (
			SELECT
				session_id,
				created_at,
				COALESCE(payload->>'name','') AS skill_name
			  FROM mem_observations
			 WHERE hook_name = 'SkillInvoked'
			   AND created_at > NOW() - INTERVAL '`+skillDriftWindow+`'
			   AND COALESCE(payload->>'name','') <> ''
			   AND session_id IS NOT NULL
		),
		followups AS (
			SELECT
				inv.skill_name,
				inv.session_id,
				inv.created_at AS inv_at,
				array_agg(COALESCE(obs.payload->>'name','') ORDER BY obs.created_at) AS tools
			  FROM invocations inv
			  JOIN mem_observations obs
			    ON obs.session_id = inv.session_id
			   AND obs.hook_name IN ('PostToolUse','PostToolUseSuccess')
			   AND obs.created_at > inv.created_at
			   AND obs.created_at < inv.created_at + INTERVAL '15 minutes'
			   AND COALESCE(obs.payload->>'name','') <> ''
			 GROUP BY inv.skill_name, inv.session_id, inv.created_at
		),
		failed AS (
			SELECT DISTINCT
				inv.skill_name,
				inv.session_id,
				inv.created_at AS inv_at
			  FROM invocations inv
			  JOIN mem_observations obs
			    ON obs.session_id = inv.session_id
			   AND obs.hook_name = 'PostToolUseFailure'
			   AND obs.created_at > inv.created_at
			   AND obs.created_at < inv.created_at + INTERVAL '15 minutes'
		),
		clean AS (
			SELECT f.skill_name, f.session_id, f.inv_at, f.tools
			  FROM followups f
			 LEFT JOIN failed x USING (skill_name, session_id, inv_at)
			 WHERE x.skill_name IS NULL
		),
		grouped AS (
			SELECT
				skill_name,
				COUNT(*) AS hits,
				MAX(inv_at) AS last_seen,
				(ARRAY_AGG(array_to_string(tools, ' -> ') ORDER BY inv_at DESC))[1] AS sample_path
			  FROM clean
			 GROUP BY skill_name
		)
		SELECT skill_name, hits, last_seen, COALESCE(sample_path,'')
		  FROM grouped
		 WHERE hits >= $1
		 ORDER BY hits DESC, last_seen DESC
		 LIMIT 5
	`, skillDriftMinHits)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var out []Finding
	for rows.Next() {
		var (
			skillName, samplePath string
			hits                  int
			lastSeen              time.Time
		)
		if err := rows.Scan(&skillName, &hits, &lastSeen, &samplePath); err != nil {
			continue
		}
		body := loadActiveSkillBody(ctx, pool, skillName)
		if body == "" {
			continue
		}
		if pathIsCoveredByBody(samplePath, body) {
			continue
		}
		question := fmt.Sprintf("Skill %q drifted - boss steered a different (successful) path. Evolve it?", skillName)
		rationale := fmt.Sprintf(
			"Skill %q ran %d times with successful divergent tool paths (most recent %s). Sample path: %s. Approve to draft an updated SKILL.md via the evolve-skill-from-deviation recipe.",
			skillName, hits, lastSeen.UTC().Format(time.RFC3339), samplePath,
		)
		tag := "skill_drift:" + skillName
		ResolveQuestionsBySourceTag(ctx, pool, tag)
		inserted := insertHealingQuestionWithTag(ctx, pool,
			question, rationale, "skill_drift", tag, nil, 7)
		if !inserted {
			continue
		}
		out = append(out, Finding{
			Kind:      "skill_drift",
			Source:    "skill_drift",
			Title:     question,
			Detail:    rationale,
			SourceTag: tag,
		})
	}
	return out
}

// mapsToActiveSkill is a cheap "does any active skill already describe
// this tool chain" check. Used by the pattern detector to avoid
// surfacing opportunities for skills the boss already installed but
// the agent forgot to invoke. Substring match on the active skill body
// is intentionally weak - false positives mean we miss a proposal (the
// boss can re-run the heartbeat later), false negatives mean a
// duplicate the boss dismisses in /lab.
func mapsToActiveSkill(ctx context.Context, pool *pgxpool.Pool, signature string) bool {
	if pool == nil {
		return false
	}
	var hit int
	_ = pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM mem_skill_versions v
		 JOIN mem_skill_active a ON a.skill_name = v.skill_name AND a.active_version = v.version
		WHERE position($1 in v.skill_md) > 0
	`, signature).Scan(&hit)
	return hit > 0
}

// loadActiveSkillBody returns the active SKILL.md body for the given
// skill name, or empty string when the skill isn't installed.
func loadActiveSkillBody(ctx context.Context, pool *pgxpool.Pool, name string) string {
	if pool == nil || name == "" {
		return ""
	}
	var body string
	_ = pool.QueryRow(ctx, `
		SELECT v.skill_md
		  FROM mem_skill_versions v
		  JOIN mem_skill_active a ON a.skill_name = v.skill_name AND a.active_version = v.version
		 WHERE v.skill_name = $1
		 LIMIT 1
	`, name).Scan(&body)
	return body
}

// pathIsCoveredByBody returns true when the body text already mentions
// every tool in the sampled path in order. Approximate; good enough to
// suppress an obvious non-drift where the agent followed the body to
// the letter and the heartbeat tripped on coincidence.
func pathIsCoveredByBody(path, body string) bool {
	if path == "" || body == "" {
		return false
	}
	if len(path) > 0 && len(body) > 0 && len(path) < len(body) {
		// Cheap substring containment.
		return containsCI(body, path)
	}
	return false
}

func containsCI(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	if len(needle) > len(haystack) {
		return false
	}
	// Case-insensitive contains via lowering both - small strings, fine.
	return indexCI(haystack, needle) >= 0
}

func indexCI(haystack, needle string) int {
	h := toLower(haystack)
	n := toLower(needle)
	for i := 0; i+len(n) <= len(h); i++ {
		if h[i:i+len(n)] == n {
			return i
		}
	}
	return -1
}

func toLower(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		b[i] = c
	}
	return string(b)
}

func shortHash(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:6])
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
}
