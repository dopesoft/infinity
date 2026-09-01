package sessions

import (
	"context"
	"errors"
	"fmt"

	"github.com/dopesoft/infinity/core/internal/agent"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// UsagePersistence is the agent.UsageStore implementation backed by the
// `last_input_tokens / last_output_tokens / total_input_tokens / total_output_tokens`
// columns added to mem_sessions in migration 013. The prompt-cache split lives
// on mem_turns (migration 151), so hydration also reads the latest turn's cache
// counters when that table/columns exist.
//
// Before 013 the loop kept these counters in process memory; Studio's
// context meter then showed 0% on any session that survived a Railway
// container rotation. This struct closes that gap: Hydrate runs on first
// session-faulting, Save runs (async) after every successful turn.
//
// All operations are best-effort. Failures are surfaced via the returned
// error so the agent can log them, but they never block a user turn.
type UsagePersistence struct {
	pool *pgxpool.Pool
}

func NewUsagePersistence(pool *pgxpool.Pool) *UsagePersistence {
	return &UsagePersistence{pool: pool}
}

// Hydrate returns the persisted counters for the given session. A missing
// row is not an error - it just means this session has never recorded a
// turn yet (returns a zero snapshot + nil).
func (u *UsagePersistence) Hydrate(ctx context.Context, sessionID string) (agent.UsageSnapshot, error) {
	if u == nil || u.pool == nil {
		return agent.UsageSnapshot{}, nil
	}
	if sessionID == "" {
		return agent.UsageSnapshot{}, fmt.Errorf("usage hydrate: empty session id")
	}
	var snap agent.UsageSnapshot
	err := u.pool.QueryRow(ctx, `
		SELECT s.last_input_tokens, s.last_output_tokens,
		       s.total_input_tokens, s.total_output_tokens,
		       COALESCE(t.cache_read_tokens, 0),
		       COALESCE(t.cache_write_tokens, 0),
		       COALESCE(t.model, '')
		FROM mem_sessions s
		LEFT JOIN LATERAL (
			SELECT cache_read_tokens, cache_write_tokens, model
			  FROM mem_turns
			 WHERE session_id = s.id
			   AND ended_at IS NOT NULL
			 ORDER BY ended_at DESC
			 LIMIT 1
		) t ON true
		WHERE s.id = $1::uuid
	`, sessionID).Scan(
		&snap.LastInputTokens,
		&snap.LastOutputTokens,
		&snap.TotalInputTokens,
		&snap.TotalOutputTokens,
		&snap.LastCacheReadTokens,
		&snap.LastCacheWriteTokens,
		// Which brain took the reading. Carried so a restart doesn't turn a
		// measurement from one model into a claim about another.
		&snap.LastMeasuredModel,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return agent.UsageSnapshot{}, nil
		}
		return agent.UsageSnapshot{}, err
	}
	return snap, nil
}

// Save upserts the counters onto the session row. We use INSERT ... ON
// CONFLICT so a session that hasn't been seeded into mem_sessions yet
// (e.g. a fresh chat that never hit the project-rename path) still gets
// its usage tracked. The conflict target is the primary key.
func (u *UsagePersistence) Save(ctx context.Context, sessionID string, snap agent.UsageSnapshot) error {
	if u == nil || u.pool == nil {
		return nil
	}
	if sessionID == "" {
		return fmt.Errorf("usage save: empty session id")
	}
	_, err := u.pool.Exec(ctx, `
		INSERT INTO mem_sessions
		    (id, last_input_tokens, last_output_tokens,
		     total_input_tokens, total_output_tokens, usage_updated_at)
		VALUES ($1::uuid, $2, $3, $4, $5, NOW())
		ON CONFLICT (id) DO UPDATE SET
		    last_input_tokens   = EXCLUDED.last_input_tokens,
		    last_output_tokens  = EXCLUDED.last_output_tokens,
		    total_input_tokens  = EXCLUDED.total_input_tokens,
		    total_output_tokens = EXCLUDED.total_output_tokens,
		    usage_updated_at    = EXCLUDED.usage_updated_at
	`,
		sessionID,
		snap.LastInputTokens,
		snap.LastOutputTokens,
		snap.TotalInputTokens,
		snap.TotalOutputTokens,
	)
	return err
}
