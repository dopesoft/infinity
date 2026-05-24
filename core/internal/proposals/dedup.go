package proposals

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// dedupSystem is the conservative duplicate-detection judge. It runs at most
// once per skill-create attempt (a rare action), only when a drafter is wired.
// Substrate cognition - a gate, not an assembled capability.
const dedupSystem = `You are a deduplication gate for a skill library. Decide whether a PROPOSED skill is essentially the SAME capability as one already in the catalog - same core job, not merely related. Be conservative: only match when promoting the proposal as a new skill would create a redundant near-duplicate. Reply with EXACTLY the catalog skill name to merge into, or the single word NONE. No other text.`

// FindDuplicateSkill returns the name of an active skill that a proposed new
// skill duplicates, or "" when it is genuinely new. It is the single
// dedup-before-create gate every skill-create path should consult so the
// catalog stops accumulating near-duplicates ("gmail-triage",
// "load-then-sweep-gmail", "inbox-triage-update", …) for the same capability.
// When a match is found the caller should route the proposal through
// UpsertCandidate with ParentSkill set, so it merges into that skill's one
// draft instead of spawning a new row.
//
// Two tiers: a free deterministic name-normalization check (catches "x" vs
// "x-update"/"x-v2"), then a conservative LLM judge over the catalog when a
// drafter is available. nil-safe.
func FindDuplicateSkill(ctx context.Context, pool *pgxpool.Pool, drafter Drafter, name, desc string) string {
	if pool == nil || strings.TrimSpace(name) == "" {
		return ""
	}
	type sk struct{ name, desc string }
	rows, err := pool.Query(ctx, `SELECT name, COALESCE(description,'') FROM mem_skills WHERE status='active'`)
	if err != nil {
		return ""
	}
	var catalog []sk
	for rows.Next() {
		var s sk
		if err := rows.Scan(&s.name, &s.desc); err == nil {
			catalog = append(catalog, s)
		}
	}
	rows.Close()
	if len(catalog) == 0 {
		return ""
	}

	// Tier 1: deterministic name match after normalizing revision suffixes.
	pn := NormalizeSkillName(name)
	for _, s := range catalog {
		if NormalizeSkillName(s.name) == pn {
			return s.name
		}
	}

	// Tier 2: conservative LLM judge over the catalog.
	if drafter == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("CATALOG:\n")
	for _, s := range catalog {
		d := s.desc
		if len(d) > 140 {
			d = d[:140]
		}
		fmt.Fprintf(&b, "- %s: %s\n", s.name, d)
	}
	fmt.Fprintf(&b, "\nPROPOSED:\n%s: %s\n\nWhich catalog skill (if any) does PROPOSED duplicate? Reply with the exact name or NONE.", name, desc)
	ans, err := drafter.Draft(ctx, "claude-haiku-4-5-20251001", dedupSystem, b.String(), 20)
	if err != nil {
		return ""
	}
	ans = strings.TrimSpace(strings.Trim(strings.TrimSpace(ans), "\"'`.,"))
	if ans == "" || strings.EqualFold(ans, "none") {
		return ""
	}
	for _, s := range catalog {
		if strings.EqualFold(s.name, ans) {
			return s.name
		}
	}
	return ""
}

// NormalizeSkillName lowercases, unifies separators, and strips trailing
// revision/variant suffixes so "gmail-triage" and "gmail-triage-update" (or
// "-v2", "-new", "-2") collapse to the same key.
func NormalizeSkillName(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = strings.ReplaceAll(n, "_", "-")
	for {
		trimmed := n
		for _, suf := range []string{"-update", "-updated", "-new", "-v2", "-v3", "-2", "-3", "-copy", "-final"} {
			trimmed = strings.TrimSuffix(trimmed, suf)
		}
		if trimmed == n {
			break
		}
		n = trimmed
	}
	return n
}
