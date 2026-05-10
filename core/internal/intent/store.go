package intent

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Store persists IntentFlow decisions for the Studio Live tab's intent stream
// panel and for analytics ("user rejected >30% of fast interventions over 7
// days → auto-raise threshold").
type Store struct {
	pool *pgxpool.Pool
}

func NewStore(p *pgxpool.Pool) *Store { return &Store{pool: p} }

func (s *Store) Record(ctx context.Context, sessionID, userMsg string, d Decision) error {
	if s == nil || s.pool == nil {
		return nil
	}
	var sid *string
	if sessionID != "" {
		if _, err := uuid.Parse(sessionID); err == nil {
			sid = &sessionID
		}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_intent_decisions (session_id, user_msg, token, confidence, reason, suggested)
		VALUES ($1::uuid, $2, $3, $4, $5, $6)
	`, sid, userMsg, string(d.Token), d.Confidence, d.Reason, d.SuggestedAction)
	return err
}

type Record struct {
	ID         string    `json:"id"`
	SessionID  string    `json:"session_id,omitempty"`
	UserMsg    string    `json:"user_msg"`
	Token      Token     `json:"token"`
	Confidence float64   `json:"confidence"`
	Reason     string    `json:"reason"`
	Suggested  string    `json:"suggested_action,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) Recent(ctx context.Context, limit int) ([]Record, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, COALESCE(session_id::text, ''), user_msg, token, confidence,
		       reason, suggested, created_at
		  FROM mem_intent_decisions
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Record
	for rows.Next() {
		var r Record
		if err := rows.Scan(&r.ID, &r.SessionID, &r.UserMsg, &r.Token, &r.Confidence,
			&r.Reason, &r.Suggested, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
