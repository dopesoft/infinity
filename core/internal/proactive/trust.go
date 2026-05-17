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
	pool     *pgxpool.Pool
	notifier TrustNotifier
}

func NewTrustStore(p *pgxpool.Pool) *TrustStore { return &TrustStore{pool: p} }

// TrustNotifier is fired (async) after a successful Queue insert so
// pluggable surfaces (Web Push, email, Slack DMs) can poke the boss
// when something needs approval. Keeping the interface here means the
// proactive package doesn't take a build-time dependency on the push
// package - serve.go wires the adapter at boot.
type TrustNotifier interface {
	NotifyTrustQueued(ctx context.Context, c *TrustContract)
}

// SetNotifier registers the trust notifier. Safe to call before or
// after Queue is exercised - nil-safe at the call site.
func (s *TrustStore) SetNotifier(n TrustNotifier) {
	if s == nil {
		return
	}
	s.notifier = n
}

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
	// middleware (e.g. heartbeat/cron) - we set NULL in that case so the
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
		// Async notifier - runs in a goroutine with a detached context so
		// it never blocks the gate's wait loop. We deliberately drop the
		// request context's deadline because push delivery (FCM/APNs over
		// HTTPS) can outlive a typical 5s API timeout.
		if s.notifier != nil {
			contract := *c // capture by value; the goroutine may outlive caller
			go func() {
				bg, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				s.notifier.NotifyTrustQueued(bg, &contract)
			}()
		}
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
	// decision_note is TEXT nullable in the schema; coalesce to '' so the
	// Go scan into `c.DecisionNote string` doesn't fail with "Scan error:
	// converting NULL to string is unsupported" - which silently 500'd the
	// whole list endpoint and showed Studio an empty Trust tab for every
	// pending row ever inserted. (Found by tracing a confirmed insert that
	// never surfaced in the panel.)
	q := `
		SELECT id::text, title, risk_level, source,
		       action_spec, reasoning,
		       COALESCE(array_to_json(cited_memory_ids)::text, '[]'),
		       risk_assessment, preview, status, decided_at,
		       COALESCE(decision_note, ''), created_at
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

// HasRecentApprovalForTool returns true when an approval (or already-
// consumed approval) exists for the (session, tool) pair within `window`.
// This is the deploy-resilient replacement for the gate's old in-memory
// session-approval map - every check hits Postgres, so a core restart
// never loses approvals the boss already granted.
//
// Window mirrors the gate's TTL; the gate passes its configured TTL in.
func (s *TrustStore) HasRecentApprovalForTool(ctx context.Context, sessionID, toolName string, window time.Duration) (bool, error) {
	if s == nil || s.pool == nil || sessionID == "" || toolName == "" {
		return false, nil
	}
	if window <= 0 {
		window = 8 * time.Hour
	}
	var exists bool
	err := s.pool.QueryRow(ctx, `
		SELECT EXISTS (
		    SELECT 1
		      FROM mem_trust_contracts
		     WHERE source = 'claude_code_gate'
		       AND action_spec->>'tool' = $1
		       AND action_spec->>'session_id' = $2
		       AND status IN ('approved', 'consumed')
		       AND COALESCE(decided_at, created_at) > NOW() - $3::interval
		)
	`, toolName, sessionID, window.String()).Scan(&exists)
	return exists, err
}

// LookupForGate returns the status + tool + session for the named
// contract, cheap enough to poll from the gate's wait loop. The fields
// are pulled out of action_spec (where the gate stored them at queue
// time) so a denial returned via the API can be matched back to the
// session that initiated the call.
func (s *TrustStore) LookupForGate(ctx context.Context, contractID string) (status, sessionID, toolName string, err error) {
	if s == nil || s.pool == nil || contractID == "" {
		return "", "", "", nil
	}
	err = s.pool.QueryRow(ctx, `
		SELECT status,
		       COALESCE(action_spec->>'session_id', ''),
		       COALESCE(action_spec->>'tool', '')
		  FROM mem_trust_contracts
		 WHERE id = $1::uuid
	`, contractID).Scan(&status, &sessionID, &toolName)
	return status, sessionID, toolName, err
}

// ConsumeApprovedForTool atomically looks for a recently-approved trust
// contract whose action_spec.tool matches `toolName` and whose
// action_spec.session_id matches `sessionID`, marks it consumed, and
// returns true. The agent loop calls this from the gate so a tool that
// was previously gated + approved by the boss can run on its next
// invocation without a fresh queue → approve → retry cycle.
//
// "Consumed" is encoded by flipping status from "approved" to
// "consumed". The row stays in the table for audit (you can still see
// what got approved-and-run in the Trust tab with status filter "all").
//
// Window: 30 minutes. After that the approval expires and the user has
// to re-confirm. This keeps stale approvals from silently green-lighting
// destructive calls hours later.
func (s *TrustStore) ConsumeApprovedForTool(ctx context.Context, sessionID, toolName string) (bool, error) {
	if s == nil || s.pool == nil || sessionID == "" || toolName == "" {
		return false, nil
	}
	var id string
	err := s.pool.QueryRow(ctx, `
		UPDATE mem_trust_contracts
		   SET status = 'consumed', decided_at = COALESCE(decided_at, NOW())
		 WHERE id = (
		     SELECT id
		       FROM mem_trust_contracts
		      WHERE status = 'approved'
		        AND source = 'claude_code_gate'
		        AND action_spec->>'tool' = $1
		        AND action_spec->>'session_id' = $2
		        AND decided_at > NOW() - INTERVAL '30 minutes'
		      ORDER BY decided_at DESC
		      LIMIT 1
		      FOR UPDATE SKIP LOCKED
		 )
		 RETURNING id::text
	`, toolName, sessionID).Scan(&id)
	if err != nil {
		// pgx.ErrNoRows means no approved contract - that's "not approved",
		// not a failure. Caller treats it as false / no consumption.
		if err.Error() == "no rows in result set" {
			return false, nil
		}
		return false, err
	}
	log.Printf("trust.consume: tool=%s session=%s contract=%s", toolName, sessionID, id)
	return true, nil
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
