package proposals

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DetectSkillFragmentation is the nightly backstop for the "one flexible skill
// per capability" rule. The creation-time gates (FindDuplicateSkill, consulted
// by both skill_create and the Voyager extractor) prevent NEW duplicates, but
// anything that slipped in before the gates existed — or via a path not yet
// routed through them — should still be caught instead of compounding the way
// email triage fragmented into ~17 skills before the 2026-06 sweep.
//
// It groups active skills into capability domains (canonical-intent match when
// one applies, else the revision-normalized name) and, for any domain holding
// more than one active skill, raises ONE idempotent curiosity question in the
// /lab "Fix this" lane so the cluster gets reviewed and merged. It does NOT
// auto-merge — collapsing skills is a judgment call the boss signs off on.
//
// Registered as a memory.RegisterConsolidateHook so it runs as the last stage
// of nightly cognition. Pool-only; nil-safe.
func DetectSkillFragmentation(ctx context.Context, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	rows, err := pool.Query(ctx, `SELECT name, COALESCE(description,'') FROM mem_skills WHERE status='active'`)
	if err != nil {
		return
	}
	type sk struct{ name, desc string }
	var catalog []sk
	for rows.Next() {
		var s sk
		if scanErr := rows.Scan(&s.name, &s.desc); scanErr == nil {
			catalog = append(catalog, s)
		}
	}
	rows.Close()
	if len(catalog) < 2 {
		resolveFragmentationQuestion(ctx, pool)
		return
	}

	// domainKey: the canonical intent a skill matches, else its normalized name.
	domainOf := func(s sk) string {
		hay := strings.ToLower(s.name + " " + s.desc)
		for _, ci := range canonicalIntents {
			if ci.matches(hay) {
				return "intent:" + ci.canonical
			}
		}
		return "name:" + NormalizeSkillName(s.name)
	}

	clusters := map[string][]string{}
	for _, s := range catalog {
		k := domainOf(s)
		clusters[k] = append(clusters[k], s.name)
	}

	var dupLines []string
	for k, names := range clusters {
		if len(names) < 2 {
			continue
		}
		sort.Strings(names)
		dupLines = append(dupLines, fmt.Sprintf("%s → %s", strings.TrimPrefix(strings.TrimPrefix(k, "intent:"), "name:"), strings.Join(names, ", ")))
	}

	if len(dupLines) == 0 {
		resolveFragmentationQuestion(ctx, pool)
		return
	}
	sort.Strings(dupLines)

	question := fmt.Sprintf("%d skill domain(s) have duplicate skills — collapse each into one flexible recipe", len(dupLines))
	rationale := "These active skills cover the same capability and should be ONE parameterized skill, not siblings. " +
		"Merge each cluster into its canonical skill (skill_optimize / promote one body, archive the rest) so the capability stays single and improvable:\n" +
		strings.Join(dupLines, "\n")

	_, _ = pool.Exec(ctx, `
		INSERT INTO mem_curiosity_questions
		  (question, rationale, source_kind, source_ids, importance, status,
		   source_tag, occurrences_count, evidence_log, last_seen_at)
		VALUES ($1, $2, 'skill_fragmentation', '{}'::uuid[], 75, 'open',
		        'skill_fragmentation', 1, jsonb_build_array(jsonb_build_object('at', NOW()::text, 'clusters', $3::text)), NOW())
		ON CONFLICT (source_tag) WHERE status='open' AND source_tag IS NOT NULL AND source_tag <> ''
		DO UPDATE SET
		  question      = EXCLUDED.question,
		  rationale     = EXCLUDED.rationale,
		  last_seen_at  = NOW(),
		  importance    = GREATEST(mem_curiosity_questions.importance, EXCLUDED.importance)
	`, question, rationale, strings.Join(dupLines, "; "))
}

// resolveFragmentationQuestion closes any open fragmentation finding once the
// catalog is clean, so a stale "duplicates exist" row doesn't linger after a
// merge.
func resolveFragmentationQuestion(ctx context.Context, pool *pgxpool.Pool) {
	_, _ = pool.Exec(ctx, `
		UPDATE mem_curiosity_questions
		SET status='resolved', resolved_at=NOW()
		WHERE source_tag='skill_fragmentation' AND status='open'
	`)
}
