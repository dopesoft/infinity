package worldmodel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store is the persistence boundary for the world model — entities, the
// edges between them, and the agent's own goals.
type Store struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
}

func NewStore(pool *pgxpool.Pool, logger *slog.Logger) *Store {
	if logger == nil {
		logger = slog.Default()
	}
	return &Store{pool: pool, logger: logger}
}

// ── entities ──────────────────────────────────────────────────────────────

const entityCols = `id::text, kind, name, aliases, attributes, summary, status,
	salience, last_seen_at, created_at, updated_at`

// UpsertEntity creates or updates an entity by (kind, name). last_seen_at
// is bumped on every upsert — observing an entity keeps it fresh.
func (s *Store) UpsertEntity(ctx context.Context, e *Entity) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("worldmodel: no pool")
	}
	e.Kind = strings.TrimSpace(e.Kind)
	e.Name = strings.TrimSpace(e.Name)
	if e.Kind == "" || e.Name == "" {
		return "", errors.New("worldmodel: entity kind and name are required")
	}
	if e.Status == "" {
		e.Status = "active"
	}
	if e.Salience == 0 {
		e.Salience = 50
	}
	if e.Salience < 0 {
		e.Salience = 0
	}
	if e.Salience > 100 {
		e.Salience = 100
	}
	if e.Aliases == nil {
		e.Aliases = []string{}
	}
	if e.Attributes == nil {
		e.Attributes = map[string]any{}
	}
	aliasesJSON, _ := json.Marshal(e.Aliases)
	attrsJSON, _ := json.Marshal(e.Attributes)

	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO mem_entities
		  (kind, name, aliases, attributes, summary, status, salience, last_seen_at)
		VALUES ($1, $2, $3::jsonb, $4::jsonb, $5, $6, $7, NOW())
		ON CONFLICT (kind, name) DO UPDATE SET
		  aliases      = EXCLUDED.aliases,
		  attributes   = mem_entities.attributes || EXCLUDED.attributes,
		  summary      = CASE WHEN EXCLUDED.summary <> '' THEN EXCLUDED.summary ELSE mem_entities.summary END,
		  status       = EXCLUDED.status,
		  salience     = EXCLUDED.salience,
		  last_seen_at = NOW(),
		  updated_at   = NOW()
		RETURNING id::text
	`, e.Kind, e.Name, string(aliasesJSON), string(attrsJSON), e.Summary, e.Status, e.Salience).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("worldmodel: upsert entity: %w", err)
	}
	e.ID = id
	return id, nil
}

// GetEntity returns an entity (by name or id) with its resolved links.
// Returns (nil, nil) when it doesn't exist.
func (s *Store) GetEntity(ctx context.Context, nameOrID string) (*Entity, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("worldmodel: no pool")
	}
	id, err := s.resolveEntityID(ctx, nameOrID)
	if err != nil {
		return nil, err
	}
	if id == "" {
		return nil, nil
	}
	ent, err := scanEntity(s.pool.QueryRow(ctx,
		`SELECT `+entityCols+` FROM mem_entities WHERE id = $1::uuid`, id))
	if err != nil {
		return nil, err
	}
	links, err := s.entityLinks(ctx, id)
	if err != nil {
		return nil, err
	}
	ent.Links = links
	return ent, nil
}

// SearchEntities finds entities by free text over name/summary, optionally
// filtered by kind, ranked by salience.
func (s *Store) SearchEntities(ctx context.Context, query, kind string, limit int) ([]*Entity, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	q := strings.TrimSpace(query)
	rows, err := s.pool.Query(ctx, `
		SELECT `+entityCols+`
		  FROM mem_entities
		 WHERE status <> 'archived'
		   AND ($1 = '' OR kind = $1)
		   AND ($2 = '' OR name ILIKE '%' || $2 || '%' OR summary ILIKE '%' || $2 || '%')
		 ORDER BY salience DESC, last_seen_at DESC
		 LIMIT $3
	`, strings.TrimSpace(kind), q, limit)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: search entities: %w", err)
	}
	defer rows.Close()
	var out []*Entity
	for rows.Next() {
		ent, err := scanEntity(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ent)
	}
	return out, rows.Err()
}

// LinkEntities creates a typed edge between two entities (resolved by name
// or id). Idempotent on (from, to, relation).
func (s *Store) LinkEntities(ctx context.Context, fromRef, toRef, relation, note string) error {
	if s == nil || s.pool == nil {
		return errors.New("worldmodel: no pool")
	}
	relation = strings.TrimSpace(relation)
	if relation == "" {
		return errors.New("worldmodel: relation is required")
	}
	fromID, err := s.resolveEntityID(ctx, fromRef)
	if err != nil {
		return err
	}
	toID, err := s.resolveEntityID(ctx, toRef)
	if err != nil {
		return err
	}
	if fromID == "" || toID == "" {
		return fmt.Errorf("worldmodel: both entities must exist before linking (from=%q to=%q)", fromRef, toRef)
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO mem_entity_links (from_id, to_id, relation, note)
		VALUES ($1::uuid, $2::uuid, $3, $4)
		ON CONFLICT (from_id, to_id, relation) DO UPDATE SET note = EXCLUDED.note
	`, fromID, toID, relation, note)
	if err != nil {
		return fmt.Errorf("worldmodel: link: %w", err)
	}
	return nil
}

func (s *Store) entityLinks(ctx context.Context, id string) ([]LinkView, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT 'out' AS dir, l.relation, e.id::text, e.name, e.kind, l.note
		  FROM mem_entity_links l JOIN mem_entities e ON e.id = l.to_id
		 WHERE l.from_id = $1::uuid
		UNION ALL
		SELECT 'in' AS dir, l.relation, e.id::text, e.name, e.kind, l.note
		  FROM mem_entity_links l JOIN mem_entities e ON e.id = l.from_id
		 WHERE l.to_id = $1::uuid
		 ORDER BY dir, relation
	`, id)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: entity links: %w", err)
	}
	defer rows.Close()
	var out []LinkView
	for rows.Next() {
		var lv LinkView
		if err := rows.Scan(&lv.Direction, &lv.Relation, &lv.OtherID, &lv.OtherName, &lv.OtherKind, &lv.Note); err != nil {
			return nil, err
		}
		out = append(out, lv)
	}
	return out, rows.Err()
}

// resolveEntityID maps a name or id to an entity id. Returns "" when no
// entity matches (not an error — callers decide).
func (s *Store) resolveEntityID(ctx context.Context, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", errors.New("worldmodel: empty entity reference")
	}
	var id string
	var err error
	if looksLikeUUID(ref) {
		err = s.pool.QueryRow(ctx, `SELECT id::text FROM mem_entities WHERE id = $1::uuid`, ref).Scan(&id)
	} else {
		err = s.pool.QueryRow(ctx,
			`SELECT id::text FROM mem_entities WHERE lower(name) = lower($1) ORDER BY salience DESC LIMIT 1`, ref).Scan(&id)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// ── goals ─────────────────────────────────────────────────────────────────

const goalCols = `id::text, title, description, status, priority, plan, progress,
	blocker, COALESCE(entity_id::text,''), due_at, last_progress_at, created_at, updated_at`

// UpsertGoal creates a new goal or, when g.ID is set, replaces it.
func (s *Store) UpsertGoal(ctx context.Context, g *Goal) (string, error) {
	if s == nil || s.pool == nil {
		return "", errors.New("worldmodel: no pool")
	}
	if strings.TrimSpace(g.Title) == "" {
		return "", errors.New("worldmodel: goal title is required")
	}
	if g.Status == "" {
		g.Status = "active"
	}
	if g.Priority == "" {
		g.Priority = "med"
	}
	if g.Plan == nil {
		g.Plan = []PlanItem{}
	}
	planJSON, _ := json.Marshal(g.Plan)
	var entityID *string
	if g.EntityID != "" {
		entityID = &g.EntityID
	}

	if g.ID == "" {
		var id string
		err := s.pool.QueryRow(ctx, `
			INSERT INTO mem_agent_goals
			  (title, description, status, priority, plan, progress, blocker, entity_id, due_at)
			VALUES ($1,$2,$3,$4,$5::jsonb,$6,$7,$8,$9)
			RETURNING id::text
		`, g.Title, g.Description, g.Status, g.Priority, string(planJSON),
			g.Progress, g.Blocker, entityID, g.DueAt).Scan(&id)
		if err != nil {
			return "", fmt.Errorf("worldmodel: insert goal: %w", err)
		}
		g.ID = id
		return id, nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_agent_goals SET
		  title=$2, description=$3, status=$4, priority=$5, plan=$6::jsonb,
		  progress=$7, blocker=$8, entity_id=$9, due_at=$10, updated_at=NOW()
		WHERE id = $1::uuid
	`, g.ID, g.Title, g.Description, g.Status, g.Priority, string(planJSON),
		g.Progress, g.Blocker, entityID, g.DueAt)
	if err != nil {
		return "", fmt.Errorf("worldmodel: update goal: %w", err)
	}
	return g.ID, nil
}

// GetGoal returns one goal, or (nil, nil) when it doesn't exist.
func (s *Store) GetGoal(ctx context.Context, id string) (*Goal, error) {
	if s == nil || s.pool == nil {
		return nil, errors.New("worldmodel: no pool")
	}
	g, err := scanGoal(s.pool.QueryRow(ctx, `SELECT `+goalCols+` FROM mem_agent_goals WHERE id = $1::uuid`, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return g, err
}

// ListGoals returns goals, optionally filtered by status, priority-ordered.
func (s *Store) ListGoals(ctx context.Context, status string, limit int) ([]*Goal, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+goalCols+`
		  FROM mem_agent_goals
		 WHERE $1 = '' OR status = $1
		 ORDER BY
		   CASE status WHEN 'active' THEN 0 WHEN 'blocked' THEN 1 WHEN 'done' THEN 2 ELSE 3 END,
		   CASE priority WHEN 'high' THEN 0 WHEN 'med' THEN 1 ELSE 2 END,
		   due_at NULLS LAST
		 LIMIT $2
	`, strings.TrimSpace(status), limit)
	if err != nil {
		return nil, fmt.Errorf("worldmodel: list goals: %w", err)
	}
	defer rows.Close()
	var out []*Goal
	for rows.Next() {
		g, err := scanGoal(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// UpdateGoal applies a partial patch. A non-nil ProgressAppend is appended
// to the running narrative and bumps last_progress_at — that timestamp is
// what the autonomous-pursuit heartbeat watches.
func (s *Store) UpdateGoal(ctx context.Context, id string, p GoalPatch) error {
	if s == nil || s.pool == nil {
		return errors.New("worldmodel: no pool")
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("worldmodel: goal id is required")
	}
	var planJSON *string
	if p.Plan != nil {
		b, _ := json.Marshal(*p.Plan)
		s := string(b)
		planJSON = &s
	}
	ct, err := s.pool.Exec(ctx, `
		UPDATE mem_agent_goals SET
		  status   = COALESCE($2, status),
		  priority = COALESCE($3, priority),
		  plan     = COALESCE($4::jsonb, plan),
		  blocker  = COALESCE($5, blocker),
		  due_at   = COALESCE($6, due_at),
		  progress = CASE WHEN $7::text IS NOT NULL AND $7 <> ''
		                  THEN (CASE WHEN progress = '' THEN '' ELSE progress || E'\n' END) || $7
		                  ELSE progress END,
		  last_progress_at = CASE WHEN $7::text IS NOT NULL AND $7 <> '' THEN NOW() ELSE last_progress_at END,
		  updated_at = NOW()
		WHERE id = $1::uuid
	`, id, p.Status, p.Priority, planJSON, p.Blocker, p.DueAt, p.ProgressAppend)
	if err != nil {
		return fmt.Errorf("worldmodel: update goal: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("worldmodel: no goal with id %s", id)
	}
	return nil
}

// ── scan helpers ──────────────────────────────────────────────────────────

func scanEntity(row pgx.Row) (*Entity, error) {
	var (
		e         Entity
		aliasRaw  []byte
		attrsRaw  []byte
		salience  int16
	)
	if err := row.Scan(
		&e.ID, &e.Kind, &e.Name, &aliasRaw, &attrsRaw, &e.Summary, &e.Status,
		&salience, &e.LastSeenAt, &e.CreatedAt, &e.UpdatedAt,
	); err != nil {
		return nil, err
	}
	e.Salience = int(salience)
	if len(aliasRaw) > 0 {
		_ = json.Unmarshal(aliasRaw, &e.Aliases)
	}
	if e.Aliases == nil {
		e.Aliases = []string{}
	}
	if len(attrsRaw) > 0 {
		_ = json.Unmarshal(attrsRaw, &e.Attributes)
	}
	if e.Attributes == nil {
		e.Attributes = map[string]any{}
	}
	return &e, nil
}

func scanGoal(row pgx.Row) (*Goal, error) {
	var (
		g       Goal
		planRaw []byte
	)
	if err := row.Scan(
		&g.ID, &g.Title, &g.Description, &g.Status, &g.Priority, &planRaw,
		&g.Progress, &g.Blocker, &g.EntityID, &g.DueAt, &g.LastProgressAt,
		&g.CreatedAt, &g.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if len(planRaw) > 0 {
		_ = json.Unmarshal(planRaw, &g.Plan)
	}
	if g.Plan == nil {
		g.Plan = []PlanItem{}
	}
	return &g, nil
}

func looksLikeUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, r := range s {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			isHex := (r >= '0' && r <= '9') || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
			if !isHex {
				return false
			}
		}
	}
	return true
}

var _ = time.Now
