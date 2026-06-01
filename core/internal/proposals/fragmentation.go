package proposals

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// DetectDuplicateClusters finds genuine skill duplicates: active skills whose
// names collapse to the same key after stripping revision suffixes (e.g.
// "x" / "x-update" / "x-v2"). It returns one human-readable line per cluster,
// or nil when the catalog is clean.
//
// It deliberately clusters by NormalizeSkillName ONLY — not the fuzzy
// canonical-intent keyword matcher. That matcher is right for the creation-time
// gate (routing a brand-new proposal into a canonical skill, where a borderline
// match just merges — cheap and reversible), but it is WRONG for auditing
// established skills: it mis-grouped the broad "google-workspace" skill with
// "inbox-triage" on shared keywords ("gmail", "inbox") and nagged the boss
// about a non-duplicate. Name-normalization is deterministic and false-positive
// free. The creation gate remains the semantic defense against differently-named
// clones; this detector is the deterministic backstop for shadow duplicates.
//
// Pure: no DB writes. The caller (proactive.RaiseSkillFragmentation) decides
// whether to raise a question, routing through the cooldown-aware UpsertQuestion
// so a dismissal actually sticks.
func DetectDuplicateClusters(ctx context.Context, pool *pgxpool.Pool) []string {
	if pool == nil {
		return nil
	}
	rows, err := pool.Query(ctx, `SELECT name FROM mem_skills WHERE status='active'`)
	if err != nil {
		return nil
	}
	var names []string
	for rows.Next() {
		var n string
		if scanErr := rows.Scan(&n); scanErr == nil {
			names = append(names, n)
		}
	}
	rows.Close()
	if len(names) < 2 {
		return nil
	}

	clusters := map[string][]string{}
	for _, n := range names {
		k := NormalizeSkillName(n)
		clusters[k] = append(clusters[k], n)
	}

	var lines []string
	for k, members := range clusters {
		if len(members) < 2 {
			continue
		}
		sort.Strings(members)
		lines = append(lines, fmt.Sprintf("%s → %s", k, strings.Join(members, ", ")))
	}
	sort.Strings(lines)
	return lines
}
