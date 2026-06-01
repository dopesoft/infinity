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
)

// PredictionStore records the agent's pre-tool-call expectations and the
// post-tool-call deltas. JEPA's predict-then-act discipline applied to LLM
// tools: before non-trivial calls, generate a one-sentence prediction; after
// the call, score how surprising the actual result was. High-surprise rows
// become curriculum signals for Voyager.
//
// The store is best-effort: prediction writes happen async and never block
// the agent loop. A missing prediction row on PostToolUse is fine - the
// resolver no-ops and moves on.
type PredictionStore struct {
	pool *pgxpool.Pool
}

func NewPredictionStore(pool *pgxpool.Pool) *PredictionStore {
	return &PredictionStore{pool: pool}
}

// Record writes a prediction *before* the tool fires. toolCallID must be the
// same ID later passed to Resolve. Returns the row id (uuid) for caller
// instrumentation; empty when the store is disabled or input invalid.
func (p *PredictionStore) Record(ctx context.Context, sessionID, toolCallID, toolName, expected string, input map[string]any) (string, error) {
	return p.RecordWithTurn(ctx, sessionID, "", toolCallID, toolName, expected, input)
}

// RecordWithTurn is Record but with an explicit turn_id so the /logs trace
// view can show predictions as paired pre/post events alongside their
// triggering tool call. turnID may be empty - that path matches the old
// Record behavior exactly.
func (p *PredictionStore) RecordWithTurn(ctx context.Context, sessionID, turnID, toolCallID, toolName, expected string, input map[string]any) (string, error) {
	if p == nil || p.pool == nil {
		return "", nil
	}
	if strings.TrimSpace(toolCallID) == "" || strings.TrimSpace(toolName) == "" {
		return "", errors.New("tool_call_id and tool_name required")
	}
	if strings.TrimSpace(expected) == "" {
		return "", nil // skip - no prediction worth recording
	}
	inputJSON, _ := json.Marshal(input)
	id := uuid.NewString()
	var sessionArg any
	if s := strings.TrimSpace(sessionID); s != "" {
		sessionArg = s
	}
	var turnArg any
	if t := strings.TrimSpace(turnID); t != "" {
		turnArg = t
	}
	_, err := p.pool.Exec(ctx, `
		INSERT INTO mem_predictions
		  (id, session_id, tool_call_id, tool_name, tool_input, expected, turn_id)
		VALUES ($1::uuid, NULLIF($2::text, '')::uuid, $3, $4, $5::jsonb, $6,
		        NULLIF($7::text, '')::uuid)
	`, id, sessionArg, toolCallID, toolName, string(inputJSON), expected, turnArg)
	if err != nil {
		return "", err
	}
	return id, nil
}

// Resolve closes a prediction by attaching the actual result + surprise
// score. Idempotent: if a prediction is already resolved we leave it alone.
// surprise is 0..1 (0 = perfectly predicted, 1 = totally unexpected).
func (p *PredictionStore) Resolve(ctx context.Context, toolCallID, actual string, matched bool, surprise float64) error {
	if p == nil || p.pool == nil {
		return nil
	}
	if strings.TrimSpace(toolCallID) == "" {
		return nil
	}
	if surprise < 0 {
		surprise = 0
	}
	if surprise > 1 {
		surprise = 1
	}
	_, err := p.pool.Exec(ctx, `
		UPDATE mem_predictions
		   SET actual = $2,
		       matched = $3,
		       surprise_score = $4,
		       resolved_at = NOW()
		 WHERE tool_call_id = $1
		   AND resolved_at IS NULL
	`, toolCallID, truncateText(actual, 4000), matched, surprise)
	return err
}

// SweepStale closes predictions left unresolved past olderThan - they were
// recorded at PreToolUse but their PostToolUse hook never fired (the session
// died mid-call, the loop dropped the post event, etc). Without this they
// accumulate forever as orphaned "open" rows. Mirrors TurnStore.RecoverStranded
// and runs.RecoverStranded: call it at boot (single-instance core ⇒ anything
// unresolved and older than the window is definitively abandoned). They are
// resolved with surprise=0 / matched=false and a marker actual, so they are
// closed but never mined as high-surprise. Returns the number swept.
func (p *PredictionStore) SweepStale(ctx context.Context, olderThan time.Duration) (int, error) {
	if p == nil || p.pool == nil {
		return 0, nil
	}
	if olderThan <= 0 {
		olderThan = time.Hour
	}
	cutoff := time.Now().Add(-olderThan)
	tag, err := p.pool.Exec(ctx, `
		UPDATE mem_predictions
		   SET actual = COALESCE(NULLIF(actual, ''), '(unresolved: tool post-hook never fired)'),
		       matched = false,
		       surprise_score = 0,
		       resolved_at = NOW()
		 WHERE resolved_at IS NULL
		   AND created_at < $1
	`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("sweep stale predictions: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// ExpectedFor returns the prediction sentence recorded at PreToolUse for a
// given tool_call_id, so the resolver can score surprise against what we
// actually predicted rather than re-deriving it. Returns "" when no open
// prediction exists. Indexed by idx_predictions_call.
func (p *PredictionStore) ExpectedFor(ctx context.Context, toolCallID string) (string, error) {
	if p == nil || p.pool == nil || strings.TrimSpace(toolCallID) == "" {
		return "", nil
	}
	var expected string
	err := p.pool.QueryRow(ctx, `
		SELECT COALESCE(expected, '')
		  FROM mem_predictions
		 WHERE tool_call_id = $1
		   AND resolved_at IS NULL
		 ORDER BY created_at DESC
		 LIMIT 1
	`, toolCallID).Scan(&expected)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return expected, nil
}

// HighSurprise returns the most recent high-surprise predictions, used by
// the curiosity scanner + Studio Memory tab. threshold defaults to 0.7.
func (p *PredictionStore) HighSurprise(ctx context.Context, threshold float64, limit int) ([]PredictionRow, error) {
	if p == nil || p.pool == nil {
		return nil, nil
	}
	if threshold <= 0 {
		threshold = 0.7
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := p.pool.Query(ctx, `
		SELECT id::text, COALESCE(session_id::text, ''), tool_call_id, tool_name,
		       expected, COALESCE(actual, ''), COALESCE(matched, false),
		       COALESCE(surprise_score, 0), created_at,
		       COALESCE(resolved_at, created_at)
		  FROM mem_predictions
		 WHERE surprise_score >= $1
		 ORDER BY surprise_score DESC, created_at DESC
		 LIMIT $2
	`, threshold, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PredictionRow
	for rows.Next() {
		var r PredictionRow
		if err := rows.Scan(&r.ID, &r.SessionID, &r.ToolCallID, &r.ToolName,
			&r.Expected, &r.Actual, &r.Matched, &r.SurpriseScore,
			&r.CreatedAt, &r.ResolvedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PredictionRow is the wire shape returned by HighSurprise.
type PredictionRow struct {
	ID            string    `json:"id"`
	SessionID     string    `json:"session_id,omitempty"`
	ToolCallID    string    `json:"tool_call_id"`
	ToolName      string    `json:"tool_name"`
	Expected      string    `json:"expected"`
	Actual        string    `json:"actual,omitempty"`
	Matched       bool      `json:"matched"`
	SurpriseScore float64   `json:"surprise_score"`
	CreatedAt     time.Time `json:"created_at"`
	ResolvedAt    time.Time `json:"resolved_at,omitempty"`
}

// OutcomeClass buckets a tool result into one of three coarse outcomes. This
// is what surprise is actually scored against - whether the call succeeded,
// came back empty, or errored - NOT the literal bytes.
//
// The previous implementation tokenized the prediction sentence and the raw
// result and scored 1 - jaccard(words). An English prediction ("expect X to
// return a usable result") shares almost no tokens with a JSON blob, so every
// successful call scored ~1.0 surprise. That made the metric meaningless and
// poisoned every downstream consumer: Gym training-example mining (which pulls
// "high-surprise" rows) and the curiosity high_surprise detector were both
// selecting essentially at random. Outcome-class comparison fixes that: a
// successful JSON-returning call scores low, a genuine error scores high.
type OutcomeClass string

const (
	OutcomeOK    OutcomeClass = "ok"
	OutcomeEmpty OutcomeClass = "empty"
	OutcomeError OutcomeClass = "error"
)

// errorSignals / emptySignals are matched case-insensitively as substrings of
// the actual result. Kept deliberately tight to avoid mislabeling legitimate
// content as an error.
var errorSignals = []string{
	`"error":`, `"ok":false`, `"success":false`, `"status":"error"`,
	"exit status 1", "nonzero exit", "non-zero exit", "panic:",
	"traceback (most recent", "unauthorized", "forbidden",
	"permission denied", "context deadline exceeded", "sqlstate",
	"401 invalid", "invalid api key",
}

var emptySignals = []string{
	`"count":0`, `"items":[]`, `"results":[]`, `"matches":[]`, `"rows":[]`,
	`"data":[]`, "no results", "not found", "0 results",
}

// ClassifyActual buckets a tool's actual output into ok / empty / error.
func ClassifyActual(actual string) OutcomeClass {
	a := strings.ToLower(strings.TrimSpace(actual))
	if a == "" {
		return OutcomeEmpty
	}
	if strings.HasPrefix(a, "error:") || strings.HasPrefix(a, "blocked:") ||
		strings.HasPrefix(a, "failed") {
		return OutcomeError
	}
	// Scan signals only in the result ENVELOPE (head+tail), not the body.
	// A tool's status/error wrapper lives at the edges (the agent loop
	// prefixes errors); the multi-KB body is CONTENT. A fetched email or a
	// compacted summary that merely contains "unauthorized" / "not found" /
	// "error" as content must NOT be misread as a failed call - that
	// misclassification produced surprise 0.9 and the un-dismissable
	// high-surprise curiosity spam (compact_context 564x, composio fetch
	// 599x/499x). Anchoring to the envelope errs toward OutcomeOK, the safe
	// direction for these data/internal tools.
	env := outcomeEnvelope(a)
	for _, sig := range errorSignals {
		if strings.Contains(env, sig) {
			return OutcomeError
		}
	}
	if a == "{}" || a == "[]" || a == "null" {
		return OutcomeEmpty
	}
	for _, sig := range emptySignals {
		if strings.Contains(env, sig) {
			return OutcomeEmpty
		}
	}
	return OutcomeOK
}

// outcomeEnvelopeChars is how many leading/trailing chars of a tool result we
// scan for status/error signals. Wrappers live at the edges; bodies don't.
const outcomeEnvelopeChars = 200

// outcomeEnvelope returns the head + tail of an (already-lowered) result so
// signal scanning ignores arbitrary body content. Short results are returned
// whole. A signal split across the head/tail join simply won't match, which
// is the conservative (treat-as-OK) direction.
func outcomeEnvelope(a string) string {
	if len(a) <= 2*outcomeEnvelopeChars {
		return a
	}
	return a[:outcomeEnvelopeChars] + "\n" + a[len(a)-outcomeEnvelopeChars:]
}

// expectedClass reads the prediction sentence and decides whether it
// anticipated success or failure. Our heuristic predictions are all
// success-flavored ("succeed", "non-empty", "2xx", "exit 0"); a drafted
// prediction can explicitly anticipate an error.
func expectedClass(expected string) OutcomeClass {
	e := strings.ToLower(expected)
	if strings.Contains(e, "error") || strings.Contains(e, "fail") ||
		strings.Contains(e, "block") || strings.Contains(e, "denied") ||
		strings.Contains(e, "non-zero") || strings.Contains(e, "empty result") {
		return OutcomeError
	}
	return OutcomeOK
}

// SurpriseFor compares an expectation to an actual result and returns a
// 0..1 surprise score (0 = perfectly predicted, 1 = totally unexpected) plus
// whether the outcome class matched the prediction. Deterministic and free -
// no LLM call. High surprise now correctly flags "we expected success and hit
// an error/empty", which is exactly the curriculum signal we want.
func SurpriseFor(expected, actual string) (matched bool, surprise float64) {
	if strings.TrimSpace(actual) == "" {
		return false, 0.5
	}
	exp := expectedClass(expected)
	act := ClassifyActual(actual)
	switch {
	case exp == act && act == OutcomeError:
		return true, 0.2 // correctly predicted a failure - low surprise, still informative
	case exp == act:
		return true, 0.1 // predicted success, got success (or empty==empty)
	case exp == OutcomeOK && act == OutcomeEmpty:
		return false, 0.5 // expected a usable result, got nothing back
	case exp == OutcomeOK && act == OutcomeError:
		return false, 0.9 // expected success, hit an error - the signal we care about
	case exp == OutcomeError && act == OutcomeOK:
		return false, 0.7 // braced for failure, it worked - worth noticing
	case exp == OutcomeError && act == OutcomeEmpty:
		return false, 0.45
	default:
		return false, 0.5
	}
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("…[+%d chars]", len(s)-n)
}
