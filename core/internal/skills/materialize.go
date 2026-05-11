package skills

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// MaterializeActiveSkills syncs Postgres → on-disk skill files at boot.
//
// Why: Voyager auto-evolved skills are persisted to mem_skill_versions
// (durable), then derived to ./skills/<name>/SKILL.md on disk (ephemeral
// in Railway's container filesystem). After a redeploy the disk is fresh
// from git — auto-evolved skills would be silently lost. This function
// runs once during boot, walks mem_skill_active joined with the active
// version's body, and writes any rows whose disk file is missing or
// out-of-date.
//
// Skills committed to the repo (in ./skills/ already) win the on-disk
// race naturally — we only WRITE if the file is missing or its content
// drifted from the DB row, and we never delete files.
//
// Safe to call on every boot. Idempotent. Failures don't block the
// registry — we just log and continue with whatever's on disk.
func MaterializeActiveSkills(ctx context.Context, pool *pgxpool.Pool, skillsRoot string) (written int, err error) {
	if pool == nil {
		return 0, nil
	}
	root := strings.TrimRight(skillsRoot, "/")
	if root == "" {
		root = "./skills"
	}

	rows, err := pool.Query(ctx, `
		SELECT s.name,
		       COALESCE(v.skill_md, '')
		  FROM mem_skill_active a
		  JOIN mem_skills s         ON s.name        = a.skill_name
		  JOIN mem_skill_versions v ON v.skill_name  = a.skill_name
		                            AND v.version    = a.active_version
		 WHERE s.status = 'active'
	`)
	if err != nil {
		return 0, fmt.Errorf("query active skills: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, body string
		if err := rows.Scan(&name, &body); err != nil {
			log.Printf("materialize: scan: %v", err)
			continue
		}
		if name == "" || body == "" {
			continue
		}
		dir := root + "/" + safeFilename(name)
		path := dir + "/SKILL.md"

		// Compare current disk content; only write when missing or drifted.
		// This preserves any local hand-edits on a dev machine that hasn't
		// been pushed back to the DB yet — DB wins only when something
		// changed in the DB.
		existing, readErr := os.ReadFile(path)
		if readErr == nil && string(existing) == body {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("materialize: mkdir %s: %v", dir, err)
			continue
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			log.Printf("materialize: write %s: %v", path, err)
			continue
		}
		written++
		log.Printf("materialize: wrote %s (%d bytes)", path, len(body))
	}
	return written, rows.Err()
}

// safeFilename mirrors voyager.safeName so disk filenames stay consistent
// regardless of which caller created them. Kept here (not imported) to
// avoid a dependency cycle.
func safeFilename(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == ' ', r == '-', r == '_':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}
