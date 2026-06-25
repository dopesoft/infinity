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
const dedupSystem = `You are a deduplication gate for a skill library. Decide whether a PROPOSED skill performs essentially the SAME CONCRETE TASK as one already in the catalog — the same core job, producing the same kind of result. Be conservative and PRECISE: match ONLY when the proposal would do the same work as the catalog skill. Do NOT match on shared THEME or domain — "resume a stalled coding run" is NOT a duplicate of "nightly self-improve", and "re-anchor on a plan before updating" is NOT a duplicate of "plan and verify", even though they touch the same area. A false match silently relabels a new skill as an update to an unrelated one and misleads the operator. When in doubt, answer NONE. Reply with EXACTLY the catalog skill name to merge into, or the single word NONE. No other text.`

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

	// Tier 2: canonical-capability intent match. Some capabilities the system
	// relies on (email triage being the scarred example) kept re-spawning under
	// fresh names - gmail-triage, triage_emails_to_dashboard, scheduled-gmail-
	// followup-triage - because the LLM judge (tier 3) is deliberately
	// conservative and the names don't normalize-match. A proposal that is
	// clearly "that capability again" by deterministic keyword signature routes
	// into the canonical skill regardless, so the capability stays ONE skill
	// that gets improved instead of cloned. Generic + data-driven: add a new
	// canonicalIntent entry, not a new code branch.
	hay := strings.ToLower(name + " " + desc)
	for _, ci := range canonicalIntents {
		if !ci.matches(hay) {
			continue
		}
		for _, s := range catalog { // only route to the canonical if it's actually active
			if s.name == ci.canonical {
				return ci.canonical
			}
		}
	}

	// Tier 3: conservative LLM judge over the catalog.
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
	// Empty model → the drafter uses the boss's active model.
	ans, err := drafter.Draft(ctx, "", dedupSystem, b.String(), 20)
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

// canonicalIntent is a deterministic "this is capability X again" signature.
// A proposal matches when its lowercased name+description contains at least
// one token from EVERY group in `allOf` (each group is an OR of synonyms).
// Requiring a hit in every group keeps the match tight - a triage proposal
// must name BOTH the triage intent AND the email domain, so unrelated email
// skills (e.g. an "email newsletter summarizer") don't get swept in.
type canonicalIntent struct {
	canonical string     // active skill all matches route into
	allOf     [][]string // AND of (OR groups); every group must hit
}

func (ci canonicalIntent) matches(hay string) bool {
	for _, group := range ci.allOf {
		hit := false
		for _, kw := range group {
			if strings.Contains(hay, kw) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return len(ci.allOf) > 0
}

// canonicalIntents is the registry of capabilities that must stay ONE skill.
// This is the data, not code: extend it when a new core capability starts
// re-spawning clones. inbox-triage is here because email triage fragmented
// into 8 competing skills and a clone dismissed days of real follow-ups.
var canonicalIntents = []canonicalIntent{
	{
		canonical: "inbox-triage",
		allOf: [][]string{
			{"triage", "inbox", "follow-up", "followup", "follow up", "sweep"},
			{"email", "gmail", "inbox", "mail", "message"},
		},
	},
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
