// Package config exposes embedded configuration that ships with the binary.
//
// scaffold_skills.go bundles the default project-scaffolding skills (Next.js,
// Vite, static HTML, iOS SwiftUI, Capacitor) so the binary can plant them on
// the boss's $INFINITY_SKILLS_ROOT at boot. The on-disk loader continues to
// own everything after that — these embeds are just seed material so a fresh
// install isn't missing the building blocks the agent advertises.
package config

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed skills/scaffold-nextjs/SKILL.md
//go:embed skills/scaffold-vite-react/SKILL.md
//go:embed skills/scaffold-static-html/SKILL.md
//go:embed skills/scaffold-ios-swift/SKILL.md
//go:embed skills/scaffold-capacitor/SKILL.md
var scaffoldSkillsFS embed.FS

// MaterializeScaffoldSkills writes the embedded scaffold-* skills into
// `<skillsRoot>/<name>/SKILL.md`, but only when a particular SKILL.md is
// missing on disk. Never overwrites — if the boss (or a Voyager-evolved
// version) has touched the file, we leave it alone.
//
// Returns the names that were written. A non-fatal error is returned only
// when the root directory itself can't be created; per-file write errors
// are returned in `written` alongside any successes (with the failure
// recorded in the error chain via fmt.Errorf wrapping).
func MaterializeScaffoldSkills(skillsRoot string) ([]string, error) {
	if skillsRoot == "" {
		return nil, fmt.Errorf("skillsRoot required")
	}
	if err := os.MkdirAll(skillsRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create skills root: %w", err)
	}

	var written []string
	err := fs.WalkDir(scaffoldSkillsFS, "skills", func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		// path looks like "skills/scaffold-nextjs/SKILL.md".
		rel := path[len("skills/"):]
		dstPath := filepath.Join(skillsRoot, rel)
		if _, statErr := os.Stat(dstPath); statErr == nil {
			// File exists — preserve whatever's there.
			return nil
		}
		if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(dstPath), err)
		}
		data, err := scaffoldSkillsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("read embed %s: %w", path, err)
		}
		if err := os.WriteFile(dstPath, data, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", dstPath, err)
		}
		written = append(written, filepath.Dir(rel))
		return nil
	})
	if err != nil {
		return written, err
	}
	return written, nil
}
