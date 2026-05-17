package proactive

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// WorkingBuffer captures recent user/agent exchanges while the model's context
// window is approaching its compaction limit. The buffer survives compaction
// (it lives in Postgres) so the next session can recover where the prior left
// off.
//
// Activation: when ctx_used / ctx_max ≥ threshold (default 0.6) the buffer
// turns on. First write clears any prior buffer for that session and writes a
// fresh header. Subsequent writes append.
type WorkingBuffer struct {
	pool      *pgxpool.Pool
	threshold float64

	mu sync.Mutex
}

func NewWorkingBuffer(p *pgxpool.Pool, threshold float64) *WorkingBuffer {
	if threshold <= 0 {
		threshold = 0.6
	}
	return &WorkingBuffer{pool: p, threshold: threshold}
}

// MaybeCapture is called after each agent turn with the current context-usage
// ratio. If we're below the threshold, it's a no-op.
func (wb *WorkingBuffer) MaybeCapture(ctx context.Context, sessionID, userMsg, agentResp string, ctxUsed, ctxMax int) error {
	if wb == nil || wb.pool == nil || sessionID == "" || ctxMax <= 0 {
		return nil
	}
	ratio := float64(ctxUsed) / float64(ctxMax)
	if ratio < wb.threshold {
		return nil
	}

	wb.mu.Lock()
	defer wb.mu.Unlock()

	active, err := wb.isActive(ctx, sessionID)
	if err != nil {
		return err
	}

	var entry strings.Builder
	if !active {
		fmt.Fprintf(&entry, "# Working Buffer - session %s\nactivated %s\n\n",
			sessionID, time.Now().UTC().Format(time.RFC3339))
	}
	entry.WriteString("## Human\n")
	entry.WriteString(strings.TrimSpace(userMsg))
	entry.WriteString("\n\n## Agent\n")
	entry.WriteString(strings.TrimSpace(agentResp))
	entry.WriteString("\n\n")

	if !active {
		_, err = wb.pool.Exec(ctx, `
			INSERT INTO mem_working_buffer (session_id, body, threshold, active, updated_at)
			VALUES ($1::uuid, $2, $3, TRUE, NOW())
			ON CONFLICT (session_id) DO UPDATE SET
			  body = EXCLUDED.body,
			  threshold = EXCLUDED.threshold,
			  active = TRUE,
			  updated_at = NOW(),
			  cleared_at = NULL
		`, sessionID, entry.String(), wb.threshold)
		return err
	}

	_, err = wb.pool.Exec(ctx, `
		UPDATE mem_working_buffer
		   SET body = body || $2,
		       updated_at = NOW()
		 WHERE session_id = $1::uuid
	`, sessionID, entry.String())
	return err
}

func (wb *WorkingBuffer) isActive(ctx context.Context, sessionID string) (bool, error) {
	var active bool
	err := wb.pool.QueryRow(ctx,
		`SELECT active FROM mem_working_buffer WHERE session_id = $1::uuid`, sessionID).
		Scan(&active)
	if err != nil {
		return false, nil
	}
	return active, nil
}

// Read returns the current working-buffer body for a session.
func (wb *WorkingBuffer) Read(ctx context.Context, sessionID string) (string, error) {
	if wb == nil || wb.pool == nil || sessionID == "" {
		return "", nil
	}
	var body string
	err := wb.pool.QueryRow(ctx,
		`SELECT body FROM mem_working_buffer WHERE session_id = $1::uuid`, sessionID).Scan(&body)
	if err != nil {
		return "", nil
	}
	return body, nil
}

// Clear marks the buffer inactive after the agent has extracted important
// context into SESSION-STATE.md. The body is retained for diagnostics.
func (wb *WorkingBuffer) Clear(ctx context.Context, sessionID string) error {
	if wb == nil || wb.pool == nil || sessionID == "" {
		return nil
	}
	_, err := wb.pool.Exec(ctx, `
		UPDATE mem_working_buffer
		   SET active = FALSE, cleared_at = NOW()
		 WHERE session_id = $1::uuid
	`, sessionID)
	return err
}
