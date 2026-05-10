package memory

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ListOpts struct {
	Tier    string
	Project string
	Status  string
	Limit   int
}

// ListMemories returns active memories filtered by tier/project. Used by the
// Studio Memory tab when no search query is active.
func ListMemories(ctx context.Context, pool *pgxpool.Pool, opts ListOpts) ([]Memory, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	status := opts.Status
	if status == "" {
		status = "active"
	}

	rows, err := pool.Query(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(content, ''), tier, version,
		       superseded_by, status, strength, importance, COALESCE(project, ''),
		       forget_after, created_at, updated_at, last_accessed_at
		FROM mem_memories
		WHERE status = $1
		  AND ($2 = '' OR tier = $2)
		  AND ($3 = '' OR project = $3)
		ORDER BY last_accessed_at DESC, importance DESC
		LIMIT $4
	`, status, opts.Tier, opts.Project, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Memory{}
	for rows.Next() {
		var m Memory
		var sb pgxNullableString
		if err := rows.Scan(&m.ID, &m.Title, &m.Content, &m.Tier, &m.Version,
			&sb, &m.Status, &m.Strength, &m.Importance, &m.Project,
			&m.ForgetAfter, &m.CreatedAt, &m.UpdatedAt, &m.LastAccessedAt); err != nil {
			return nil, err
		}
		if sb.Valid {
			m.SupersededBy = &sb.String
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
