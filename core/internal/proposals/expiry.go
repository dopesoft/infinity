package proposals

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// staleProposalDays is how long an undecided proposal sits in the queue before
// it's auto-rejected. A skill/code proposal the boss hasn't promoted in a month
// is stale signal, not a live to-do — leaving it open just grows the review
// queue into noise (the boss hit 48 open candidates once). Tunable via
// INFINITY_PROPOSAL_TTL_DAYS.
var staleProposalDays = envIntDefault("INFINITY_PROPOSAL_TTL_DAYS", 30)

var expiryLog = log.New(os.Stdout, "proposals: ", log.LstdFlags)

// ExpireStaleProposals auto-rejects skill and code proposals that have sat
// undecided past the TTL. Registered as a memory.RegisterConsolidateHook so it
// runs as part of nightly cognition. Idempotent; nil-safe.
//
// Soft action: status → 'rejected' (reversible, history preserved), never a
// delete. A genuinely valuable proposal that ages out will simply be
// re-proposed by Voyager the next time the pattern recurs.
func ExpireStaleProposals(ctx context.Context, pool *pgxpool.Pool) {
	if pool == nil {
		return
	}
	skillTag, err := pool.Exec(ctx, `
		UPDATE mem_skill_proposals
		SET status = 'rejected', decided_at = NOW()
		WHERE status IN ('candidate','pending','draft')
		  AND created_at < NOW() - make_interval(days => $1)
	`, staleProposalDays)
	if err != nil {
		log.Printf("expire skill proposals: %v", err)
	} else if n := skillTag.RowsAffected(); n > 0 {
		expiryLog.Printf("auto-rejected %d stale skill proposal(s) (>%dd)", n, staleProposalDays)
	}

	codeTag, err := pool.Exec(ctx, `
		UPDATE mem_code_proposals
		SET status = 'rejected', decided_at = NOW(),
		    decision_note = COALESCE(NULLIF(decision_note,''), 'auto-expired: undecided past TTL')
		WHERE status IN ('candidate','pending','draft')
		  AND created_at < NOW() - make_interval(days => $1)
	`, staleProposalDays)
	if err != nil {
		log.Printf("expire code proposals: %v", err)
	} else if n := codeTag.RowsAffected(); n > 0 {
		expiryLog.Printf("auto-rejected %d stale code proposal(s) (>%dd)", n, staleProposalDays)
	}
}

func envIntDefault(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n := 0
	for _, c := range v {
		if c < '0' || c > '9' {
			return def
		}
		n = n*10 + int(c-'0')
	}
	if n <= 0 {
		return def
	}
	return n
}
