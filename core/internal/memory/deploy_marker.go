package memory

import (
	"context"
	"log"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
)

// deployLog routes the success/lifecycle line to stdout so Railway tags it
// severity:info (stderr → severity:error).
var deployLog = log.New(os.Stdout, "deploy-marker: ", log.LstdFlags)

// RecordDeploymentOnBoot stamps the deployment timeline. When the running
// commit (RAILWAY_GIT_COMMIT_SHA) differs from the last recorded one, it inserts
// a mem_deployments row. From that moment, the commit_sha DEFAULT on
// mem_memories / mem_observations (migration 096) auto-stamps every new row with
// this code version — so the Memory tab can show "from before your latest
// deploy" on anything observed under older code.
//
// This is the deploy-awareness substrate behind the boss's complaint that
// failure memories "fire without knowing about my updates." Deterministic Go,
// nil-safe, best-effort: a DB hiccup at boot never blocks startup.
//
// Returns (changed, sha): changed=true when a NEW deployment row was written.
func RecordDeploymentOnBoot(ctx context.Context, pool *pgxpool.Pool) (bool, string) {
	if pool == nil {
		return false, ""
	}
	sha := os.Getenv("RAILWAY_GIT_COMMIT_SHA")
	if sha == "" {
		// Local/dev or non-Railway runtime — no commit to stamp. Skip silently;
		// the column default just yields NULL, which the UI treats as "old".
		return false, ""
	}

	var last string
	_ = pool.QueryRow(ctx, `
		SELECT commit_sha FROM mem_deployments ORDER BY deployed_at DESC LIMIT 1
	`).Scan(&last)
	if last == sha {
		return false, sha // already recorded this commit
	}

	if _, err := pool.Exec(ctx, `
		INSERT INTO mem_deployments (commit_sha) VALUES ($1)
	`, sha); err != nil {
		log.Printf("deploy-marker: record %s: %v", short(sha), err)
		return false, sha
	}

	if last == "" {
		deployLog.Printf("recorded first deployment %s", short(sha))
	} else {
		deployLog.Printf("new deployment %s (was %s) — memories from prior code now flag as pre-deploy", short(sha), short(last))
	}
	return true, sha
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}
