package memory

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Auditor struct {
	pool *pgxpool.Pool
}

func NewAuditor(pool *pgxpool.Pool) *Auditor { return &Auditor{pool: pool} }

func (a *Auditor) Log(ctx context.Context, op, table, targetID, actor string, diff map[string]any) error {
	if a == nil || a.pool == nil {
		return nil
	}
	id := uuid.NewString()
	diffJSON, _ := json.Marshal(diff)
	_, err := a.pool.Exec(ctx, `
		INSERT INTO mem_audit (id, operation, target_table, target_id, actor, diff)
		VALUES ($1, $2, $3, $4, $5, $6::jsonb)
	`, id, op, table, targetID, actor, string(diffJSON))
	return err
}
