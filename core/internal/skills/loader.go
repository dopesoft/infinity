package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// LoadFromFS walks a skills root directory, parses every <root>/<name>/SKILL.md,
// and returns the skills it found. Errors on individual skills are returned
// alongside the successful parses so the registry can surface them in /api.
//
// Layout:
//
//	root/
//	  weekly-standup-summary/
//	    SKILL.md
//	    implementation.py    # optional
//	  code-review/
//	    SKILL.md
func LoadFromFS(root string) ([]*Skill, []LoadError, error) {
	info, err := os.Stat(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nil
		}
		return nil, nil, err
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("skills root %q is not a directory", root)
	}

	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, nil, err
	}

	var (
		skills []*Skill
		errs   []LoadError
	)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(root, e.Name())
		mdPath := filepath.Join(skillDir, "SKILL.md")
		s, err := loadSkillFile(mdPath)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			errs = append(errs, LoadError{Path: mdPath, Err: err.Error()})
			continue
		}
		s.Path = skillDir
		s.LoadedAt = time.Now().UTC()
		skills = append(skills, s)
	}

	return skills, errs, nil
}

// LoadError is a non-fatal parse error. We keep iterating so a single broken
// SKILL.md doesn't take down the whole registry.
type LoadError struct {
	Path string `json:"path"`
	Err  string `json:"error"`
}

func loadSkillFile(path string) (*Skill, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	fm, body, err := splitFrontmatter(raw)
	if err != nil {
		return nil, err
	}
	skill, err := skillFromFrontmatter(fm, body)
	if err != nil {
		return nil, err
	}
	skill.Source = SourceManual
	skill.Status = StatusActive
	if impl := findImplementation(filepath.Dir(path)); impl != "" {
		skill.ImplPath = impl
		skill.ImplLanguage = languageFromPath(impl)
	}
	return skill, nil
}

func splitFrontmatter(raw []byte) (Frontmatter, string, error) {
	var fm Frontmatter
	src := string(raw)
	src = strings.TrimPrefix(src, "\ufeff") // strip UTF-8 BOM if present
	if !strings.HasPrefix(src, "---") {
		return fm, "", fmt.Errorf("missing YAML frontmatter (expected leading ---)")
	}
	rest := strings.TrimPrefix(src, "---")
	rest = strings.TrimLeft(rest, "\r\n")
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, "", fmt.Errorf("unterminated YAML frontmatter")
	}
	yamlBlock := rest[:idx]
	body := strings.TrimLeft(rest[idx+len("\n---"):], "\r\n")

	if err := yaml.Unmarshal([]byte(yamlBlock), &fm); err != nil {
		return fm, "", fmt.Errorf("yaml parse: %w", err)
	}
	return fm, body, nil
}

func skillFromFrontmatter(fm Frontmatter, body string) (*Skill, error) {
	if fm.Name == "" {
		return nil, fmt.Errorf("name is required")
	}
	if fm.Version == "" {
		fm.Version = "0.0.1"
	}
	if fm.RiskLevel == "" {
		fm.RiskLevel = RiskLow
	}
	if !fm.RiskLevel.Valid() {
		return nil, fmt.Errorf("invalid risk_level %q", fm.RiskLevel)
	}

	egress, err := normaliseEgress(fm.NetworkEgress)
	if err != nil {
		return nil, err
	}

	return &Skill{
		Name:           fm.Name,
		Version:        fm.Version,
		Description:    fm.Description,
		TriggerPhrases: fm.TriggerPhrases,
		Inputs:         fm.Inputs,
		Outputs:        fm.Outputs,
		RiskLevel:      fm.RiskLevel,
		NetworkEgress:  egress,
		Confidence:     fm.Confidence,
		LastEvolved:    fm.LastEvolved,
		Body:           body,
	}, nil
}

func normaliseEgress(v any) ([]string, error) {
	if v == nil {
		return nil, nil
	}
	switch x := v.(type) {
	case string:
		if strings.EqualFold(x, "none") || x == "" {
			return nil, nil
		}
		return []string{x}, nil
	case []any:
		out := make([]string, 0, len(x))
		for _, item := range x {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("network_egress entry is not a string: %v", item)
			}
			out = append(out, s)
		}
		return out, nil
	case []string:
		return x, nil
	default:
		return nil, fmt.Errorf("network_egress must be 'none' or a string array, got %T", v)
	}
}

func findImplementation(dir string) string {
	candidates := []string{
		"implementation.py",
		"implementation.sh",
		"implementation.js",
		"implementation.ts",
		"main.py",
		"main.sh",
	}
	for _, c := range candidates {
		p := filepath.Join(dir, c)
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func languageFromPath(p string) string {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".py":
		return "python"
	case ".sh":
		return "bash"
	case ".js":
		return "javascript"
	case ".ts":
		return "typescript"
	default:
		return ""
	}
}
