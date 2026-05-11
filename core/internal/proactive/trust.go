package proactive

import (
	"context"
	"encoding/json"
	"log"
	"time"

	"github.com/dopesoft/infinity/core/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TrustContract is the approval queue surface. Anything the agent wants to do
// that isn't pre-approved (auto-evolved skills, mutating side effects, etc.)
// queues here for the user to approve / deny / snooze.
type TrustContract struct {
	ID             string         `json:"id"`
	Title          string         `json:"title"`
	RiskLevel      string         `json:"risk_level"`
	Source         string         `json:"source"`
	ActionSpec     map[string]any `json:"action_spec"`
	Reasoning      string         `json:"reasoning"`
	CitedMemoryIDs []string       `json:"cited_memory_ids"`
	RiskAssessment map[string]any `json:"risk_assessment"`
	Preview        string         `json:"preview"`
	Status         string         `json:"status"`
	DecidedAt      *time.Time     `json:"decided_at,omitempty"`
	DecisionNote   string         `json:"decision_note,omitempty"`
	CreatedAt      time.Time      `json:"created_at"`
}

type TrustStore struct {
	pool *pgxpool.Pool
}

func NewTrustStore(p *pgxpool.Pool) *TrustStore { return &TrustStore{pool: p} }

func (s *TrustStore) Queue(ctx context.Context, c *TrustContract) (string, error) {
	if s == nil || s.pool == nil {
		return "", nil
	}
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	if c.Status == "" {
		c.Status = "pending"
	}
	action, _ := json.Marshal(c.ActionSpec)
	risk, _ := json.Marshal(c.RiskAssessment)
	cited := c.CitedMemoryIDs
	if cited == nil {
		cited = []string{}
	}

	// Pull the authenticated user from ctx so the row is owned by them.
	// auth.UserID returns "" when the request didn't pass through the auth
	// middleware (e.g. heartbeat/cron) — we set NULL in that case so the
	// existing single-user fallback (rows with NULL get claimed by the
	// owner at first login) keeps working.
	userID := auth.UserID(ctx)
	var userIDArg any
	if userID == "" {
		userIDArg = nil
	} else {
		userIDArg = userID
	}

	tag, err := s.pool.Exec(ctx, `
		INSERT INTO mem_trust_contracts
		  (id, title, risk_level, source, action_spec, reasoning, cited_memory_ids,
		   risk_assessment, preview, status, user_id)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6, $7::uuid[], $8::jsonb, $9, $10, $11::uuid)
	`, c.ID, c.Title, c.RiskLevel, c.Source, action, c.Reasoning, cited, risk, c.Preview, c.Status, userIDArg)
	// Loud logging because this row appearing (or not) is the entire UX:
	// the Studio Trust queue and the gated tool card both depend on it.
	if err != nil {
		log.Printf("trust.queue: INSERT failed id=%s source=%s user_id=%v err=%v", c.ID, c.Source, userIDArg, err)
	} else {
		log.Printf("trust.queue: INSERT ok id=%s source=%s status=%s user_id=%v rows=%d",
			c.ID, c.Source, c.Status, userIDArg, tag.RowsAffected())
	}
	return c.ID, err
}

func (s *TrustStore) List(ctx context.Context, status string, limit int) ([]TrustContract, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{limit}
	q := `
		SELECT id::text, title, risk_level, source,
		       action_spec, reasoning,
		       COALESCE(array_to_json(cited_memory_ids)::text, '[]'),
		       risk_assessment, preview, status, decided_at, decision_note, created_at
		  FROM mem_trust_contracts`
	if status != "" && status != "all" {
		q += ` WHERE status = $2`
		args = append(args, status)
	}
	q += ` ORDER BY created_at DESC LIMIT $1`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []TrustContract
	for rows.Next() {
		var c TrustContract
		var actionRaw, riskRaw, citedRaw []byte
		if err := rows.Scan(&c.ID, &c.Title, &c.RiskLevel, &c.Source,
			&actionRaw, &c.Reasoning, &citedRaw, &riskRaw, &c.Preview, &c.Status,
			&c.DecidedAt, &c.DecisionNote, &c.CreatedAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(actionRaw, &c.ActionSpec)
		_ = json.Unmarshal(riskRaw, &c.RiskAssessment)
		_ = json.Unmarshal(citedRaw, &c.CitedMemoryIDs)
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *TrustStore) Decide(ctx context.Context, id, decision, note string) error {
	if s == nil || s.pool == nil {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_trust_contracts
		   SET status = $2, decided_at = NOW(), decision_note = $3
		 WHERE id = $1::uuid
	`, id, decision, note)
	return err
}
