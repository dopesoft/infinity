// Package compass is the boss's authored north-star: a small set of free-text
// sections (mission, goals, challenges, principles, fronts) the boss edits in
// Studio, injected into every turn by Provider. Unlike the boss profile
// (inferred from observations) or Honcho (a peer model), the Compass is what
// the boss DECLARES matters, in his own words — the highest-signal context
// there is. The agent reads it; it does not write it (v1).
package compass

import (
	"context"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Sections is the canonical ordered set the Studio editor seeds. Free-form in
// the DB, but these five are the shape the boss fills in.
var Sections = []string{"mission", "goals", "challenges", "principles", "fronts"}

// SectionLabel maps a section key to a human label for the system-prompt block.
var SectionLabel = map[string]string{
	"mission":    "Mission",
	"goals":      "Goals",
	"challenges": "Challenges",
	"principles": "Principles",
	"fronts":     "Active fronts",
}

// Section is one authored block of the Compass.
type Section struct {
	Section  string `json:"section"`
	Content  string `json:"content"`
	Position int    `json:"position"`
}

// Store is the thin persistence layer over mem_compass.
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// List returns every section ordered by position. Empty when unset.
func (s *Store) List(ctx context.Context) ([]Section, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT section, content, position
		  FROM mem_compass
		 ORDER BY position ASC, section ASC
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Section
	for rows.Next() {
		var sec Section
		if err := rows.Scan(&sec.Section, &sec.Content, &sec.Position); err != nil {
			continue
		}
		out = append(out, sec)
	}
	return out, rows.Err()
}

// Upsert writes one section's content (and position). An empty content clears
// the section but keeps the row, so the editor's ordering survives.
func (s *Store) Upsert(ctx context.Context, section, content string, position int) error {
	if s == nil || s.pool == nil {
		return nil
	}
	section = strings.ToLower(strings.TrimSpace(section))
	if section == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_compass (section, content, position, created_at, updated_at)
		VALUES ($1, $2, $3, NOW(), NOW())
		ON CONFLICT (section) DO UPDATE
		   SET content = EXCLUDED.content,
		       position = EXCLUDED.position,
		       updated_at = NOW()
	`, section, strings.TrimSpace(content), position)
	return err
}
