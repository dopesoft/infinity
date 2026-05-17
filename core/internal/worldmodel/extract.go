package worldmodel

import (
	"context"
	"regexp"
	"strings"
)

var (
	emailRe      = regexp.MustCompile(`\b[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}\b`)
	repoRe       = regexp.MustCompile(`\b[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+\b`)
	urlHostRe    = regexp.MustCompile(`https?://([^/\s]+)`)
	commitmentRe = regexp.MustCompile(`(?i)\b(todo|need to|follow up|deadline|due|renew|waiting on|blocked by)\b`)
)

type ExtractReport struct {
	Scanned  int `json:"scanned"`
	Upserted int `json:"upserted"`
}

// ExtractFromRecentObservations keeps the world model warm without hardcoding
// a vertical. It recognizes durable anchors (emails, repos, hosts, open-loop
// phrases) from recent memory text and writes them through the same generic
// entity contract the agent uses.
func (s *Store) ExtractFromRecentObservations(ctx context.Context, limit int) (ExtractReport, error) {
	if s == nil || s.pool == nil {
		return ExtractReport{}, nil
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.pool.Query(ctx, `
		SELECT COALESCE(raw_text, '')
		  FROM mem_observations
		 WHERE raw_text IS NOT NULL AND raw_text <> ''
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return ExtractReport{}, err
	}
	defer rows.Close()
	report := ExtractReport{}
	seen := map[string]bool{}
	for rows.Next() {
		var text string
		if err := rows.Scan(&text); err != nil {
			return report, err
		}
		report.Scanned++
		for _, email := range emailRe.FindAllString(text, -1) {
			key := "person:" + strings.ToLower(email)
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, err := s.UpsertEntity(ctx, &Entity{
				Kind: "person", Name: email, Summary: "Observed email identity.",
				Attributes: map[string]any{"email": email, "source": "worldmodel_extract"}, Salience: 55,
			}); err == nil {
				report.Upserted++
			}
		}
		for _, repo := range repoRe.FindAllString(text, -1) {
			if strings.Contains(repo, "//") {
				continue
			}
			key := "project:" + strings.ToLower(repo)
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, err := s.UpsertEntity(ctx, &Entity{
				Kind: "project", Name: repo, Summary: "Observed repository or project reference.",
				Attributes: map[string]any{"repo": repo, "source": "worldmodel_extract"}, Salience: 60,
			}); err == nil {
				report.Upserted++
			}
		}
		for _, match := range urlHostRe.FindAllStringSubmatch(text, -1) {
			if len(match) < 2 {
				continue
			}
			host := strings.TrimPrefix(strings.ToLower(match[1]), "www.")
			key := "account:" + host
			if seen[key] {
				continue
			}
			seen[key] = true
			if _, err := s.UpsertEntity(ctx, &Entity{
				Kind: "account", Name: host, Summary: "Observed external service or domain.",
				Attributes: map[string]any{"domain": host, "source": "worldmodel_extract"}, Salience: 45,
			}); err == nil {
				report.Upserted++
			}
		}
		if commitmentRe.MatchString(text) {
			name := clipEntityName(text)
			key := "commitment:" + strings.ToLower(name)
			if !seen[key] {
				seen[key] = true
				if _, err := s.UpsertEntity(ctx, &Entity{
					Kind: "commitment", Name: name, Summary: text,
					Attributes: map[string]any{"source": "worldmodel_extract"}, Salience: 70,
				}); err == nil {
					report.Upserted++
				}
			}
		}
	}
	return report, rows.Err()
}

func clipEntityName(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) <= 80 {
		return s
	}
	return s[:80]
}
