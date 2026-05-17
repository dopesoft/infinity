package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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

// SurpriseFor compares an expectation to an actual result and returns a
// 0..1 surprise score. Cheap heuristic: tokenize, compute jaccard on lowered
// words. This is intentionally not LLM-driven - we want post-hoc scoring
// to be free. The score is rough but consistent, and high values correctly
// flag "the tool returned something nothing like what we predicted."
func SurpriseFor(expected, actual string) (matched bool, surprise float64) {
	e := strings.ToLower(strings.TrimSpace(expected))
	a := strings.ToLower(strings.TrimSpace(actual))
	if e == "" || a == "" {
		return false, 0
	}
	// Hard match on common signals first.
	if strings.HasPrefix(a, "error:") || strings.HasPrefix(a, "blocked:") {
		// Did we predict an error? Then it's not surprising.
		if strings.Contains(e, "error") || strings.Contains(e, "fail") || strings.Contains(e, "block") {
			return true, 0.2
		}
		return false, 0.9
	}
	eTokens := tokenSet(e)
	aTokens := tokenSet(a)
	if len(eTokens) == 0 || len(aTokens) == 0 {
		return false, 0.5
	}
	inter := 0
	for t := range eTokens {
		if _, ok := aTokens[t]; ok {
			inter++
		}
	}
	union := len(eTokens) + len(aTokens) - inter
	if union == 0 {
		return false, 0.5
	}
	jaccard := float64(inter) / float64(union)
	// Jaccard ≥ 0.3 is "matched-ish"; surprise inversely tracks similarity.
	matched = jaccard >= 0.3
	surprise = 1.0 - jaccard
	return matched, surprise
}

func tokenSet(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, raw := range strings.FieldsFunc(s, func(r rune) bool {
		return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9')
	}) {
		if len(raw) < 3 {
			continue
		}
		out[raw] = struct{}{}
	}
	return out
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + fmt.Sprintf("…[+%d chars]", len(s)-n)
}
