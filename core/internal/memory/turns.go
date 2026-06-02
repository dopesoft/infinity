package memory

import (
	"context"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// TurnStore is the persistence side of the LangSmith-style traces feature.
// One row per agent turn lands in mem_turns; per-event rows (observations,
// predictions, trust contracts, skill runs, intent decisions) carry a
// nullable turn_id that joins back here.
//
// All methods are best-effort. The agent loop opens a turn at entry and
// closes it on TaskCompleted; if either insert fails the turn just doesn't
// appear in /logs, but the chat reply is never blocked.
type TurnStore struct {
	pool *pgxpool.Pool
}

func NewTurnStore(pool *pgxpool.Pool) *TurnStore {
	return &TurnStore{pool: pool}
}

// Open inserts a new mem_turns row and returns its id. sessionID must be a
// real UUID (the caller is expected to have already EnsureSession'd it).
// userText may be empty on the resume path - the row is still created so
// every turn has a trace, even ones that didn't start with a fresh prompt.
func (s *TurnStore) Open(ctx context.Context, sessionID, userText, model string) (string, error) {
	if s == nil || s.pool == nil {
		return "", nil
	}
	id := uuid.NewString()
	_, err := s.pool.Exec(ctx, `
		INSERT INTO mem_turns (id, session_id, user_text, model, status)
		VALUES ($1::uuid, $2::uuid, $3, $4, 'in_flight')
	`, id, sessionID, truncateText(userText, 8000), strings.TrimSpace(model))
	if err != nil {
		return "", err
	}
	return id, nil
}

// CloseFields is the set of columns updated when a turn ends. AssistantText
// gets truncated for storage; Summary should already be a short one-liner.
type CloseFields struct {
	AssistantText string
	StopReason    string
	InputTokens   int
	OutputTokens  int
	ToolCallCount int
	// Status is one of: ok | empty | errored | interrupted. The caller
	// computes which based on the run outcome.
	Status  string
	Error   string
	Summary string
}

// Close stamps ended_at + the final outcome fields on a turn row. Idempotent
// at the DB level (UPDATE on PK).
func (s *TurnStore) Close(ctx context.Context, turnID string, f CloseFields) error {
	if s == nil || s.pool == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	status := strings.TrimSpace(f.Status)
	if status == "" {
		status = "ok"
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_turns
		   SET ended_at        = NOW(),
		       assistant_text  = $2,
		       stop_reason     = $3,
		       input_tokens    = $4,
		       output_tokens   = $5,
		       tool_call_count = $6,
		       status          = $7,
		       error           = NULLIF($8, ''),
		       summary         = $9
		 WHERE id = $1::uuid
	`,
		turnID,
		truncateText(f.AssistantText, 8000),
		f.StopReason,
		f.InputTokens,
		f.OutputTokens,
		f.ToolCallCount,
		status,
		f.Error,
		truncateText(f.Summary, 400),
	)
	return err
}

// IncrementToolCalls bumps tool_call_count by 1. Called from the loop on
// every PreToolUse so the in-flight row always reflects the running count.
func (s *TurnStore) IncrementToolCalls(ctx context.Context, turnID string) error {
	if s == nil || s.pool == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE mem_turns SET tool_call_count = tool_call_count + 1 WHERE id = $1::uuid
	`, turnID)
	return err
}

// RecoverStranded marks every turn still flagged in_flight as errored AND
// inserts a synthetic TaskCompleted observation so the chat reload at
// /api/sessions/:id/messages renders an "(interrupted)" assistant turn
// where the silent gap used to be.
//
// Why this is load-bearing:
//   - The agent loop's `closeTurn` runs in a deferred goroutine. If the
//     core process dies mid-turn (deploy, OOM, panic that escapes the
//     recover, host kill), the row is stranded at status='in_flight'
//     forever and the user is stuck staring at their own prompt with no
//     reply, no error, no way to know the agent isn't still thinking.
//   - Studio's chat reload (sessions_messages_api.go) reconstructs the
//     transcript from `UserPromptSubmit + TaskCompleted` observations.
//     Without a TaskCompleted, the transcript ends on the user's prompt
//     and the boss can't tell that anything went wrong — they just see
//     "in flight" forever on the logs page and silence in the chat.
//
// Call this at startup, RIGHT AFTER the migrator and BEFORE the WS
// handlers come online. Infinity is single-process / single-instance —
// any in_flight row visible at boot is definitively stranded; there is
// no concurrent core that might still be working on it.
//
// Returns the number of turns recovered (for the boot log line) and the
// first error encountered (best-effort: a failure on one row doesn't
// abort the sweep).
func (s *TurnStore) RecoverStranded(ctx context.Context) (int, error) {
	if s == nil || s.pool == nil {
		return 0, nil
	}
	// Synthetic text the chat reload renders as an assistant turn. The
	// text is intentionally short + actionable: the boss should be able
	// to read it once and know to retry the prompt.
	const recoveryText = "(Jarvis was interrupted mid-turn — the core restarted. Your prompt was received but the reply never completed. Please try again.)"
	const recoveryError = "core restarted while turn was in flight"

	// CTE pattern: close every in_flight row in one statement, returning
	// the (session_id, user_id, turn_id) tuples we then fan back out as
	// synthetic observation inserts. Atomic + single round trip for the
	// status flip; observation inserts go in a follow-up loop because
	// mem_observations has FTS / embedding columns that don't compose
	// well into a single INSERT … SELECT.
	//
	// ended_at = the timestamp of the LAST observation we recorded for the
	// turn — that's the last moment we know the agent was alive. Falling
	// back to NOW() (the obvious choice) lies: a turn started at 20:01,
	// crashed at 20:05, and recovered at 22:30 would report a 149-minute
	// "latency" when the agent was actually only working for 4 minutes
	// before it died. The boss's complaint about "103 minutes" was
	// exactly this lie. If a turn has no observations at all (extremely
	// rare — even the UserPromptSubmit fires before the loop starts),
	// fall back to started_at + 1s so the row still gets a finite latency.
	rows, err := s.pool.Query(ctx, `
		WITH stranded AS (
		    SELECT id, session_id, user_id, started_at
		      FROM mem_turns
		     WHERE status = 'in_flight'
		     FOR UPDATE SKIP LOCKED
		),
		last_obs AS (
		    SELECT turn_id, MAX(created_at) AS ts
		      FROM mem_observations
		     WHERE turn_id IN (SELECT id FROM stranded)
		     GROUP BY turn_id
		),
		closed AS (
		    UPDATE mem_turns t
		       SET status   = 'errored',
		           ended_at = COALESCE(
		                          t.ended_at,
		                          (SELECT ts FROM last_obs WHERE turn_id = t.id),
		                          t.started_at + INTERVAL '1 second'
		                      ),
		           error    = COALESCE(NULLIF(t.error, ''), $1),
		           summary  = COALESCE(NULLIF(t.summary, ''), $2),
		           stop_reason = COALESCE(NULLIF(t.stop_reason, ''), 'recovered')
		      FROM stranded
		     WHERE t.id = stranded.id
		 RETURNING t.id::text, t.session_id::text, COALESCE(t.user_id::text, '') AS user_id
		)
		SELECT id, session_id, user_id FROM closed
	`, recoveryError, "(interrupted)")
	if err != nil {
		return 0, fmt.Errorf("recover stranded turns: %w", err)
	}
	defer rows.Close()

	type stranded struct{ id, sessionID, userID string }
	var batch []stranded
	for rows.Next() {
		var s stranded
		if err := rows.Scan(&s.id, &s.sessionID, &s.userID); err != nil {
			return 0, fmt.Errorf("scan stranded: %w", err)
		}
		batch = append(batch, s)
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	if len(batch) == 0 {
		return 0, nil
	}

	// Emit the synthetic TaskCompleted observation per row. user_id is
	// nullable on mem_observations so the empty-string → NULL coercion
	// is safe; setting it when known keeps RLS-scoped reads correct.
	var firstErr error
	for _, st := range batch {
		obsID := uuid.NewString()
		var userArg any
		if st.userID != "" {
			userArg = st.userID
		}
		if _, err := s.pool.Exec(ctx, `
			INSERT INTO mem_observations
			    (id, session_id, hook_name, payload, raw_text, fts_doc, importance, turn_id, user_id)
			VALUES (
			    $1::uuid, $2::uuid, 'TaskCompleted',
			    jsonb_build_object('recovered', true, 'reason', $3::text),
			    $4::text,
			    to_tsvector(COALESCE(current_setting('infinity.search_config', true), 'english')::regconfig, left($4::text, 200000)),
			    1, $5::uuid, NULLIF($6::text, '')::uuid
			)
		`, obsID, st.sessionID, recoveryError, recoveryText, st.id, st.userID); err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("insert recovery observation for turn %s: %w", st.id, err)
			}
			continue
		}
		_ = userArg
	}
	return len(batch), firstErr
}

// TurnRow is one mem_turns row plus the session display name. Used by the
// /logs list view and trace_* agent tools.
type TurnRow struct {
	ID            string
	SessionID     string
	SessionName   string
	// SessionKind is the origin of the session this turn belongs to:
	// 'chat' (default), 'cron', 'heartbeat', etc. Lets /logs mark a cron run
	// as a cron instead of rendering it chat-style. OriginLabel is the
	// human label for non-chat origins (e.g. the cron name "inbox-triage").
	SessionKind   string
	OriginLabel   string
	UserText      string
	AssistantText string
	Model         string
	Status        string
	StopReason    string
	Summary       string
	Error         string
	StartedAt     string
	EndedAt       string
	InputTokens   int
	OutputTokens  int
	ToolCallCount int
	LatencyMS     int64
}

// List returns the most recent N turns, optionally filtered by session and/or
// status. Ordered by started_at DESC.
func (s *TurnStore) List(ctx context.Context, sessionID, status string, limit int) ([]TurnRow, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args := []any{limit}
	where := []string{"1=1"}
	if sid := strings.TrimSpace(sessionID); sid != "" {
		args = append(args, sid)
		where = append(where, "t.session_id = $"+itoa(len(args))+"::uuid")
	}
	if st := strings.TrimSpace(status); st != "" {
		args = append(args, st)
		where = append(where, "t.status = $"+itoa(len(args)))
	}
	q := `
		SELECT t.id, t.session_id, COALESCE(s.name, ''), t.user_text, t.assistant_text,
		       t.model, t.status, t.stop_reason, t.summary, COALESCE(t.error, ''),
		       t.started_at, COALESCE(t.ended_at, t.started_at),
		       t.input_tokens, t.output_tokens, t.tool_call_count,
		       EXTRACT(EPOCH FROM (COALESCE(t.ended_at, NOW()) - t.started_at)) * 1000,
		       COALESCE(s.kind, 'chat'),
		       COALESCE(s.origin_ref->>'cron_name', s.origin_ref->>'sentinel_name', '')
		FROM mem_turns t
		LEFT JOIN mem_sessions s ON s.id = t.session_id
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY t.started_at DESC
		LIMIT $1`
	rows, err := s.pool.Query(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TurnRow{}
	for rows.Next() {
		var r TurnRow
		var startedAt, endedAt interface{ Format(string) string }
		_ = startedAt
		_ = endedAt
		var sa, ea pgtimeWrap
		var latency float64
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.SessionName, &r.UserText, &r.AssistantText,
			&r.Model, &r.Status, &r.StopReason, &r.Summary, &r.Error,
			&sa.t, &ea.t,
			&r.InputTokens, &r.OutputTokens, &r.ToolCallCount,
			&latency,
			&r.SessionKind, &r.OriginLabel,
		); err != nil {
			return nil, err
		}
		r.StartedAt = sa.iso()
		r.EndedAt = ea.iso()
		r.LatencyMS = int64(latency)
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get returns a single turn row by id. Returns ErrNoRows when not found.
func (s *TurnStore) Get(ctx context.Context, turnID string) (TurnRow, error) {
	var r TurnRow
	if s == nil || s.pool == nil {
		return r, pgx.ErrNoRows
	}
	var sa, ea pgtimeWrap
	var latency float64
	err := s.pool.QueryRow(ctx, `
		SELECT t.id, t.session_id, COALESCE(s.name, ''), t.user_text, t.assistant_text,
		       t.model, t.status, t.stop_reason, t.summary, COALESCE(t.error, ''),
		       t.started_at, COALESCE(t.ended_at, t.started_at),
		       t.input_tokens, t.output_tokens, t.tool_call_count,
		       EXTRACT(EPOCH FROM (COALESCE(t.ended_at, NOW()) - t.started_at)) * 1000,
		       COALESCE(s.kind, 'chat'),
		       COALESCE(s.origin_ref->>'cron_name', s.origin_ref->>'sentinel_name', '')
		FROM mem_turns t
		LEFT JOIN mem_sessions s ON s.id = t.session_id
		WHERE t.id = $1::uuid
	`, turnID).Scan(
		&r.ID, &r.SessionID, &r.SessionName, &r.UserText, &r.AssistantText,
		&r.Model, &r.Status, &r.StopReason, &r.Summary, &r.Error,
		&sa.t, &ea.t,
		&r.InputTokens, &r.OutputTokens, &r.ToolCallCount,
		&latency,
		&r.SessionKind, &r.OriginLabel,
	)
	if err != nil {
		return r, err
	}
	r.StartedAt = sa.iso()
	r.EndedAt = ea.iso()
	r.LatencyMS = int64(latency)
	return r, nil
}

// TraceEvent is one row in the merged chronological timeline a /logs detail
// page renders. Sourced from mem_observations + mem_predictions +
// mem_trust_contracts joined on turn_id.
type TraceEvent struct {
	ID         string         `json:"id"`
	Kind       string         `json:"kind"`
	Source     string         `json:"source"`
	Timestamp  string         `json:"timestamp"`
	HookName   string         `json:"hook_name,omitempty"`
	ToolName   string         `json:"tool_name,omitempty"`
	ToolCallID string         `json:"tool_call_id,omitempty"`
	Input      string         `json:"input,omitempty"`
	Output     string         `json:"output,omitempty"`
	Expected   string         `json:"expected,omitempty"`
	Actual     string         `json:"actual,omitempty"`
	Error      string         `json:"error,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	RawText    string         `json:"raw_text,omitempty"`
	Surprise   *float64       `json:"surprise,omitempty"`
	Payload    map[string]any `json:"payload,omitempty"`
}

// Events returns every per-event row tied to a turn, ordered by timestamp.
// observations are the spine; predictions + trust contracts annotate.
func (s *TurnStore) Events(ctx context.Context, turnID string) ([]TraceEvent, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	out := []TraceEvent{}

	// Observations.
	rows, err := s.pool.Query(ctx, `
		SELECT id, hook_name, COALESCE(raw_text, ''), COALESCE(payload::text, '{}'), created_at
		FROM mem_observations
		WHERE turn_id = $1::uuid
		ORDER BY created_at ASC
	`, turnID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var e TraceEvent
		var payloadJSON string
		var ts pgtimeWrap
		if err := rows.Scan(&e.ID, &e.HookName, &e.RawText, &payloadJSON, &ts.t); err != nil {
			rows.Close()
			return nil, err
		}
		e.Source = "observation"
		e.Kind = obsKind(e.HookName)
		e.Timestamp = ts.iso()
		e.Payload = decodePayload(payloadJSON)
		hydrateFromPayload(&e)
		out = append(out, e)
	}
	rows.Close()

	// Predictions.
	rows, err = s.pool.Query(ctx, `
		SELECT id, tool_call_id, tool_name, COALESCE(expected, ''), COALESCE(actual, ''),
		       surprise_score, created_at
		FROM mem_predictions
		WHERE turn_id = $1::uuid
		ORDER BY created_at ASC
	`, turnID)
	if err == nil {
		for rows.Next() {
			var e TraceEvent
			var surprise *float64
			var ts pgtimeWrap
			if err := rows.Scan(&e.ID, &e.ToolCallID, &e.ToolName, &e.Expected, &e.Actual, &surprise, &ts.t); err != nil {
				rows.Close()
				return nil, err
			}
			e.Source = "prediction"
			e.Kind = "prediction"
			e.Timestamp = ts.iso()
			e.Surprise = surprise
			out = append(out, e)
		}
		rows.Close()
	}

	// Trust contracts (gated tool calls). Schema uses `title` + `reasoning` +
	// `status`; the trace UI surfaces them as the gate event's title / reason.
	rows, err = s.pool.Query(ctx, `
		SELECT id, COALESCE(title, ''), COALESCE(reasoning, ''), COALESCE(status, ''), created_at
		FROM mem_trust_contracts
		WHERE turn_id = $1::uuid
		ORDER BY created_at ASC
	`, turnID)
	if err == nil {
		for rows.Next() {
			var e TraceEvent
			var status string
			var ts pgtimeWrap
			if err := rows.Scan(&e.ID, &e.ToolName, &e.Reason, &status, &ts.t); err != nil {
				rows.Close()
				return nil, err
			}
			e.Source = "trust_contract"
			e.Kind = "gate"
			e.Timestamp = ts.iso()
			e.Payload = map[string]any{"status": status}
			out = append(out, e)
		}
		rows.Close()
	}

	// Stable chronological order. Empty timestamps sort last but observations
	// always carry one so this is just a defensive guard.
	sortByTimestamp(out)
	return out, nil
}

// Search runs a fuzzy match over mem_turns.user_text + summary + the joined
// session name. Used by the traces_search agent tool. limit caps at 50.
func (s *TurnStore) Search(ctx context.Context, query string, limit int) ([]TurnRow, error) {
	if s == nil || s.pool == nil {
		return nil, nil
	}
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, nil
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.pool.Query(ctx, `
		SELECT t.id, t.session_id, COALESCE(s.name, ''), t.user_text, t.assistant_text,
		       t.model, t.status, t.stop_reason, t.summary, COALESCE(t.error, ''),
		       t.started_at, COALESCE(t.ended_at, t.started_at),
		       t.input_tokens, t.output_tokens, t.tool_call_count,
		       EXTRACT(EPOCH FROM (COALESCE(t.ended_at, NOW()) - t.started_at)) * 1000
		FROM mem_turns t
		LEFT JOIN mem_sessions s ON s.id = t.session_id
		WHERE t.user_text ILIKE '%' || $1 || '%'
		   OR t.assistant_text ILIKE '%' || $1 || '%'
		   OR t.summary ILIKE '%' || $1 || '%'
		   OR COALESCE(s.name, '') ILIKE '%' || $1 || '%'
		ORDER BY t.started_at DESC
		LIMIT $2
	`, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TurnRow{}
	for rows.Next() {
		var r TurnRow
		var sa, ea pgtimeWrap
		var latency float64
		if err := rows.Scan(
			&r.ID, &r.SessionID, &r.SessionName, &r.UserText, &r.AssistantText,
			&r.Model, &r.Status, &r.StopReason, &r.Summary, &r.Error,
			&sa.t, &ea.t,
			&r.InputTokens, &r.OutputTokens, &r.ToolCallCount,
			&latency,
		); err != nil {
			return nil, err
		}
		r.StartedAt = sa.iso()
		r.EndedAt = ea.iso()
		r.LatencyMS = int64(latency)
		out = append(out, r)
	}
	return out, rows.Err()
}
