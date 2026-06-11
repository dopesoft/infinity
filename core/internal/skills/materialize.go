package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// infoLog writes to stdout so Railway's log shipper tags these lines
// with severity=info instead of the severity=error it stamps on
// anything that comes out of stderr (which is where the stdlib `log`
// package writes by default). Reserve the default `log.Printf` for
// genuine failures.
var infoLog = log.New(os.Stdout, "", log.LstdFlags)

// MaterializeActiveSkills syncs Postgres → on-disk skill files at boot.
//
// Why: Voyager auto-evolved skills are persisted to mem_skill_versions
// (durable), then derived to ./skills/<name>/SKILL.md on disk (ephemeral
// in Railway's container filesystem). After a redeploy the disk is fresh
// from git - auto-evolved skills would be silently lost. This function
// runs once during boot, walks mem_skill_active joined with the active
// version's body, and writes any rows whose disk file is missing or
// out-of-date.
//
// Skills committed to the repo (in ./skills/ already) win the on-disk
// race naturally - we only WRITE if the file is missing or its content
// drifted from the DB row, and we never delete files.
//
// Safe to call on every boot. Idempotent. Failures don't block the
// registry - we just log and continue with whatever's on disk.
func MaterializeActiveSkills(ctx context.Context, pool *pgxpool.Pool, skillsRoot string) (written int, err error) {
	if pool == nil {
		return 0, nil
	}
	root := strings.TrimRight(skillsRoot, "/")
	if root == "" {
		root = "./skills"
	}

	// Pull the metadata columns alongside the version body so we can
	// reconstruct a valid YAML frontmatter when the stored skill_md is
	// frontmatter-less - which happens whenever GEPA's optimizer
	// rewrites a skill body without re-emitting the header. The loader
	// is strict about frontmatter, so without this stitching the
	// auto-evolved versions get rejected on next boot.
	rows, err := pool.Query(ctx, `
		SELECT s.name,
		       COALESCE(v.skill_md, ''),
		       COALESCE(s.description, ''),
		       COALESCE(s.risk_level, 'low'),
		       COALESCE(s.trigger_phrases, '[]'::jsonb)::text,
		       COALESCE(a.active_version, ''),
		       COALESCE(v.confidence, 0)
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
		var name, body, description, riskLevel, triggerJSON, version string
		var confidence float64
		if err := rows.Scan(&name, &body, &description, &riskLevel, &triggerJSON, &version, &confidence); err != nil {
			log.Printf("materialize: scan: %v", err)
			continue
		}
		if name == "" || body == "" {
			continue
		}

		// A body whose own frontmatter declares a DIFFERENT skill name would
		// be loaded into the registry under THAT name, silently vaporizing
		// this skill (2026-06-08: a recovery recipe promoted onto
		// nightly-self-improve's pointer made skills_invoke return "unknown
		// skill"). A corrupt active pointer is a real error: skip the row and
		// say so on stderr instead of poisoning the registry directory.
		if fmName := FrontmatterName(body); fmName != "" && safeFilename(fmName) != safeFilename(name) {
			log.Printf("materialize: %s: active version %s body declares name %q; pointer is corrupt, skipping (repoint mem_skill_active)", name, version, fmName)
			continue
		}

		// If the stored body lacks YAML frontmatter, synthesize one from
		// the canonical metadata columns and prepend. Loader rejects any
		// SKILL.md without a leading `---`, so this is what unblocks
		// auto-evolved scaffolds whose optimizer dropped the header.
		// Strip a UTF-8 BOM first since the loader does the same.
		final := body
		if !strings.HasPrefix(strings.TrimPrefix(body, "\ufeff"), "---") {
			final = synthesizeFrontmatter(name, version, description, riskLevel, triggerJSON, confidence) + body
		}

		dir := root + "/" + safeFilename(name)
		path := dir + "/SKILL.md"

		// Compare current disk content; only write when missing or drifted.
		// This preserves any local hand-edits on a dev machine that hasn't
		// been pushed back to the DB yet - DB wins only when something
		// changed in the DB.
		existing, readErr := os.ReadFile(path)
		if readErr == nil && string(existing) == final {
			continue
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("materialize: mkdir %s: %v", dir, err)
			continue
		}
		if err := os.WriteFile(path, []byte(final), 0o644); err != nil {
			log.Printf("materialize: write %s: %v", path, err)
			continue
		}
		written++
		infoLog.Printf("materialize: wrote %s (%d bytes)", path, len(final))
	}
	return written, rows.Err()
}

// FrontmatterName returns the `name:` a SKILL.md body declares in its YAML
// frontmatter, or "" when the body has no parseable frontmatter. This is the
// identity the loader will key the registry on, so writers (materialize,
// voyager promote) use it to detect bodies that belong to a different skill.
func FrontmatterName(body string) string {
	fm, _, err := splitFrontmatter([]byte(body))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(fm.Name)
}

// healThrottle bounds how often GetFresh may rescan: a skill whose stored
// frontmatter version legitimately differs from its active pointer would
// otherwise turn every invoke into a materialize + full reload.
const healThrottle = 30 * time.Second

// GetFresh returns the named skill, self-healing a stale registry first. A
// migration or a Voyager promotion can activate or repoint a skill in
// Postgres while this process is running, but skills only materialize to
// disk at boot - so the in-memory index misses until the next redeploy (the
// "unknown skill: nightly-self-improve" failure). On a miss, or when the
// loaded version differs from mem_skill_active.active_version, this
// re-materializes active skills, reloads, and retries once. Falls back to
// plain Get when no store/pool is attached.
func (r *Registry) GetFresh(ctx context.Context, name string) (*Skill, bool) {
	s, ok := r.Get(name)
	r.mu.RLock()
	store := r.store
	healed := r.healedAt
	r.mu.RUnlock()
	if store == nil || store.Pool() == nil {
		return s, ok
	}
	if ok && !r.versionStale(ctx, store.Pool(), name, s.Version) {
		return s, ok
	}
	if time.Since(healed) < healThrottle {
		return s, ok
	}
	r.mu.Lock()
	r.healedAt = time.Now()
	r.mu.Unlock()
	if _, err := MaterializeActiveSkills(ctx, store.Pool(), r.root); err != nil {
		log.Printf("skills: self-heal materialize: %v", err)
	}
	if _, err := r.Reload(ctx); err != nil {
		log.Printf("skills: self-heal reload: %v", err)
		return s, ok
	}
	if fresh, fok := r.Get(name); fok {
		if !ok || fresh.Version != s.Version {
			infoLog.Printf("skills: self-heal recovered %q (version %s)", name, fresh.Version)
		}
		return fresh, true
	}
	return s, ok
}

// versionStale reports whether the loaded skill's version no longer matches
// the DB's active pointer - the signal that something repointed the skill
// after this process last materialized it.
func (r *Registry) versionStale(ctx context.Context, pool *pgxpool.Pool, name, loaded string) bool {
	var active string
	err := pool.QueryRow(ctx,
		`SELECT COALESCE(active_version, '') FROM mem_skill_active WHERE skill_name = $1`,
		name).Scan(&active)
	return err == nil && active != "" && active != loaded
}

// synthesizeFrontmatter rebuilds a minimal-but-loader-valid YAML header
// from the canonical metadata in mem_skills. Used when the stored
// skill_md body is frontmatter-less (GEPA optimizer output, manual
// inserts that forgot the header, legacy rows). Keeps the same field
// shape as skills.Frontmatter so the loader's strict YAML parser is
// happy on the next boot.
//
// triggerJSON arrives as the raw JSONB text from the DB. We pass it
// through unchanged when it's a non-empty array, otherwise emit `[]`.
func synthesizeFrontmatter(name, version, description, riskLevel, triggerJSON string, confidence float64) string {
	var triggers []string
	if triggerJSON != "" {
		_ = json.Unmarshal([]byte(triggerJSON), &triggers)
	}
	if version == "" {
		version = "1.0.0"
	}
	if riskLevel == "" {
		riskLevel = "low"
	}

	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("name: ")
	b.WriteString(yamlEscape(name))
	b.WriteString("\n")
	b.WriteString("version: \"")
	b.WriteString(strings.ReplaceAll(version, "\"", "'"))
	b.WriteString("\"\n")
	b.WriteString("description: ")
	b.WriteString(yamlEscape(description))
	b.WriteString("\n")
	b.WriteString("risk_level: ")
	b.WriteString(yamlEscape(riskLevel))
	b.WriteString("\n")
	if len(triggers) > 0 {
		b.WriteString("trigger_phrases:\n")
		for _, t := range triggers {
			b.WriteString("  - ")
			b.WriteString(yamlEscape(t))
			b.WriteString("\n")
		}
	} else {
		b.WriteString("trigger_phrases: []\n")
	}
	if confidence > 0 {
		fmt.Fprintf(&b, "confidence: %.3f\n", confidence)
	}
	b.WriteString("---\n\n")
	return b.String()
}

// yamlEscape quotes a string when it contains characters that would
// break naive YAML scalar parsing (colons, leading/trailing whitespace,
// special markers, etc.). Single-line strings without those characters
// pass through unquoted for readability.
func yamlEscape(s string) string {
	if s == "" {
		return "\"\""
	}
	needsQuote := false
	for _, r := range s {
		if r == ':' || r == '#' || r == '\n' || r == '"' || r == '\'' || r == '\\' || r == '\t' {
			needsQuote = true
			break
		}
	}
	if !needsQuote && (s != strings.TrimSpace(s) || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "?") || strings.HasPrefix(s, "&") || strings.HasPrefix(s, "*") || strings.HasPrefix(s, "[") || strings.HasPrefix(s, "{")) {
		needsQuote = true
	}
	if !needsQuote {
		return s
	}
	escaped := strings.ReplaceAll(s, "\\", "\\\\")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")
	escaped = strings.ReplaceAll(escaped, "\n", "\\n")
	return "\"" + escaped + "\""
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
