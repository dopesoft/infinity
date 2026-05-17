package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

type Store struct {
	pool *pgxpool.Pool
}

func NewStore(pool *pgxpool.Pool) *Store {
	return &Store{pool: pool}
}

// EnsureSession upserts a session row and returns the resolved id.
func (s *Store) EnsureSession(ctx context.Context, id, project string) (string, error) {
	if id == "" {
		id = uuid.NewString()
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_sessions (id, project, started_at)
		VALUES ($1, $2, NOW())
		ON CONFLICT (id) DO UPDATE SET project = COALESCE(EXCLUDED.project, mem_sessions.project)
	`, id, nullable(project))
	if err != nil {
		return "", err
	}
	return id, nil
}

func (s *Store) EndSession(ctx context.Context, id string) error {
	_, err := s.pool.Exec(ctx, `UPDATE mem_sessions SET ended_at = NOW() WHERE id = $1`, id)
	return err
}

type ObservationInput struct {
	SessionID  string
	HookName   string
	Payload    map[string]any
	RawText    string
	Embedding  []float32
	Importance int
	// TurnID groups the observation under a /logs trace row. Optional -
	// rows that predate the mem_turns migration or come from hook paths
	// without a turn context (steered messages, voice turns) leave it
	// empty and join their session via the (session_id, created_at)
	// fallback the trace API uses.
	TurnID string
}

func (s *Store) InsertObservation(ctx context.Context, in ObservationInput) (string, error) {
	if in.SessionID == "" || in.HookName == "" {
		return "", errors.New("session_id and hook_name required")
	}
	id := uuid.NewString()
	payloadJSON, _ := json.Marshal(in.Payload)
	importance := in.Importance
	if importance == 0 {
		importance = 5
	}

	var emb any
	if len(in.Embedding) > 0 {
		emb = pgvector.NewVector(in.Embedding)
	}

	var turnArg any
	if t := strings.TrimSpace(in.TurnID); t != "" {
		turnArg = t
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_observations (id, session_id, hook_name, payload, raw_text, embedding, fts_doc, importance, turn_id)
		VALUES ($1, $2, $3, $4::jsonb, $5, $6,
		        to_tsvector(COALESCE(current_setting('infinity.search_config', true), 'english')::regconfig, COALESCE($5, '')),
		        $7, NULLIF($8::text, '')::uuid)
	`, id, in.SessionID, in.HookName, string(payloadJSON), nullable(in.RawText), emb, importance, turnArg)
	if err != nil {
		return "", fmt.Errorf("insert observation: %w", err)
	}
	return id, nil
}

func (s *Store) RecentObservations(ctx context.Context, limit int) ([]Observation, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id, session_id, hook_name, COALESCE(raw_text, ''), importance, created_at
		FROM mem_observations
		ORDER BY created_at DESC
		LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Observation{}
	for rows.Next() {
		var o Observation
		if err := rows.Scan(&o.ID, &o.SessionID, &o.HookName, &o.RawText, &o.Importance, &o.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

func (s *Store) Counts(ctx context.Context) (map[string]int, error) {
	out := map[string]int{}
	queries := map[string]string{
		"observations": `SELECT COUNT(*) FROM mem_observations`,
		"memories":     `SELECT COUNT(*) FROM mem_memories WHERE status = 'active'`,
		"graph_nodes":  `SELECT COUNT(*) FROM mem_graph_nodes`,
		"graph_edges":  `SELECT COUNT(*) FROM mem_graph_edges`,
		"stale":        `SELECT COUNT(*) FROM mem_graph_nodes WHERE stale_flag = TRUE`,
		"sessions":     `SELECT COUNT(*) FROM mem_sessions`,
	}
	for k, q := range queries {
		var n int
		if err := s.pool.QueryRow(ctx, q).Scan(&n); err != nil {
			return nil, err
		}
		out[k] = n
	}
	return out, nil
}

func (s *Store) GetMemory(ctx context.Context, id string) (*Memory, error) {
	row := s.pool.QueryRow(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(content, ''), tier, version,
		       superseded_by, status, strength, importance, COALESCE(project, ''),
		       forget_after, created_at, updated_at, last_accessed_at
		FROM mem_memories WHERE id = $1
	`, id)
	var m Memory
	var sb pgxNullableString
	var fa *time.Time
	if err := row.Scan(&m.ID, &m.Title, &m.Content, &m.Tier, &m.Version,
		&sb, &m.Status, &m.Strength, &m.Importance, &m.Project,
		&fa, &m.CreatedAt, &m.UpdatedAt, &m.LastAccessedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if sb.Valid {
		m.SupersededBy = &sb.String
	}
	if fa != nil {
		m.ForgetAfter = fa
	}
	return &m, nil
}

type pgxNullableString struct {
	Valid  bool
	String string
}

func (n *pgxNullableString) Scan(src any) error {
	if src == nil {
		n.Valid = false
		return nil
	}
	switch v := src.(type) {
	case string:
		n.String = v
		n.Valid = v != ""
	case []byte:
		n.String = string(v)
		n.Valid = len(v) > 0
	default:
		return fmt.Errorf("nullable string: unsupported type %T", src)
	}
	return nil
}

func nullable(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}
