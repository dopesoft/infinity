package memory

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/dopesoft/infinity/core/internal/embed"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"
)

// Reflector is the metacognition layer Infinity was missing. The compressor
// converts observations into facts; the reflector converts *sessions* into
// critiques + lessons. Pattern: Generative Agents (Park et al., 2023) +
// Multi-Agent Reflexion (MAR, arXiv 2512.20845) - separate "critic" persona,
// fresh LLM call so the actor doesn't get to grade its own homework.
//
// Output lands in mem_reflections. Each row carries a quality_score (the
// critic's read of the session) and an array of structured lessons. Lessons
// that score above a confidence threshold ALSO write to mem_lessons so the
// existing search machinery picks them up.
type Reflector struct {
	pool     *pgxpool.Pool
	embedder embed.Embedder
	llm      Critic
}

// Critic is the minimal LLM dependency. Implementations live in the llm
// package; declared here so the memory package doesn't import llm directly
// (mirrors the Summarizer interface pattern used by the compressor).
type Critic interface {
	CritiqueSession(ctx context.Context, transcript string) (ReflectionResult, error)
}

// ReflectionResult is what the critic returns. Strict JSON shape - the LLM
// must hit this contract or we drop the row. quality_score is 0..1; lessons
// are short imperative sentences with self-assessed confidence.
type ReflectionResult struct {
	Critique     string   `json:"critique"`
	QualityScore float64  `json:"quality_score"`
	Lessons      []Lesson `json:"lessons"`
	Kind         string   `json:"kind"` // session_critique | error_postmortem | self_consistency
	// Fix, when present on a low-quality reflection, is queued as a
	// mem_code_proposals candidate for the self-improve loop (see persist).
	Fix *CriticFix `json:"fix,omitempty"`
}

type Lesson struct {
	Text       string  `json:"text"`
	Confidence float64 `json:"confidence"`
}

// CriticFix mirrors llm.CriticFix — the queued repair a reflection identified.
type CriticFix struct {
	TargetHint string `json:"target_hint"`
	Title      string `json:"title"`
	Change     string `json:"change"`
	Risk       string `json:"risk"`
}

func NewReflector(pool *pgxpool.Pool, embedder embed.Embedder, critic Critic) *Reflector {
	if embedder == nil {
		embedder = embed.NewStub()
	}
	return &Reflector{pool: pool, embedder: embedder, llm: critic}
}

// ReflectOnSession pulls the transcript of a single session, asks the critic
// for a structured judgment, and persists the result. Idempotent - re-running
// for a session that already has a reflection is a no-op unless force=true.
func (r *Reflector) ReflectOnSession(ctx context.Context, sessionID string, force bool) (string, error) {
	if r == nil || r.pool == nil || r.llm == nil {
		return "", errors.New("reflector not configured")
	}
	if strings.TrimSpace(sessionID) == "" {
		return "", errors.New("session id required")
	}

	if !force {
		var existing string
		err := r.pool.QueryRow(ctx, `
			SELECT id::text FROM mem_reflections
			 WHERE session_id = $1::uuid
			 ORDER BY created_at DESC LIMIT 1
		`, sessionID).Scan(&existing)
		if err == nil && existing != "" {
			return existing, nil
		}
	}

	transcript, err := r.buildTranscript(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(transcript) == "" {
		return "", nil
	}

	result, err := r.llm.CritiqueSession(ctx, transcript)
	if err != nil {
		return "", fmt.Errorf("critique: %w", err)
	}
	if strings.TrimSpace(result.Critique) == "" {
		return "", nil
	}
	return r.persist(ctx, sessionID, result)
}

// ReflectRecent runs reflection over every session ended in the last `window`
// duration that doesn't yet have a reflection. Used by the `infinity reflect`
// CLI command and as the nightly cron action.
func (r *Reflector) ReflectRecent(ctx context.Context, window time.Duration, limit int) (int, error) {
	if r == nil || r.pool == nil {
		return 0, errors.New("reflector not configured")
	}
	if limit <= 0 {
		limit = 20
	}
	rows, err := r.pool.Query(ctx, `
		SELECT s.id::text
		  FROM mem_sessions s
		  LEFT JOIN mem_reflections rf ON rf.session_id = s.id
		 WHERE s.started_at > NOW() - $1::interval
		   AND rf.id IS NULL
		 ORDER BY s.started_at DESC
		 LIMIT $2
	`, fmt.Sprintf("%d seconds", int(window.Seconds())), limit)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return 0, err
		}
		ids = append(ids, id)
	}
	rows.Close()

	done := 0
	for _, id := range ids {
		ictx, cancel := context.WithTimeout(ctx, 60*time.Second)
		_, err := r.ReflectOnSession(ictx, id, false)
		cancel()
		if err != nil {
			fmt.Printf("[reflector] %s: %v\n", id, err)
			continue
		}
		done++
	}
	return done, nil
}

// buildTranscript pulls the observation stream for a session and renders a
// compact transcript suitable for the critic. We cap at 60 obs and 12k chars
// to keep the call cheap - the critic doesn't need every keystroke, it needs
// the shape of the work.
func (r *Reflector) buildTranscript(ctx context.Context, sessionID string) (string, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT hook_name, COALESCE(raw_text, ''), payload, created_at
		  FROM mem_observations
		 WHERE session_id = $1::uuid
		 ORDER BY created_at ASC
		 LIMIT 60
	`, sessionID)
	if err != nil {
		return "", err
	}
	defer rows.Close()

	var b strings.Builder
	for rows.Next() {
		var hook, raw string
		var payload []byte
		var at time.Time
		if err := rows.Scan(&hook, &raw, &payload, &at); err != nil {
			return "", err
		}
		text := strings.TrimSpace(raw)
		if text == "" {
			continue
		}
		switch hook {
		case "UserPromptSubmit":
			fmt.Fprintf(&b, "USER: %s\n", clipText(text, 500))
		case "TaskCompleted":
			fmt.Fprintf(&b, "ASSISTANT: %s\n", clipText(text, 800))
		case "PreToolUse":
			fmt.Fprintf(&b, "→ tool: %s\n", clipText(text, 200))
		case "PostToolUseFailure":
			fmt.Fprintf(&b, "✗ tool error: %s\n", clipText(text, 300))
		case "PostToolUse":
			fmt.Fprintf(&b, "← tool ok: %s\n", clipText(text, 200))
		}
		if b.Len() > 12000 {
			b.WriteString("\n[transcript truncated]")
			break
		}
	}
	return b.String(), rows.Err()
}

func (r *Reflector) persist(ctx context.Context, sessionID string, res ReflectionResult) (string, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	lessonsJSON, _ := json.Marshal(res.Lessons)
	kind := strings.TrimSpace(res.Kind)
	if kind == "" {
		kind = "session_critique"
	}
	quality := res.QualityScore
	if quality < 0 {
		quality = 0
	}
	if quality > 1 {
		quality = 1
	}

	emb, embErr := r.embedder.Embed(ctx, res.Critique)
	var embArg any
	if embErr == nil && emb != nil {
		embArg = pgvector.NewVector(emb)
	}

	id := uuid.NewString()
	if _, err := tx.Exec(ctx, `
		INSERT INTO mem_reflections
		  (id, session_id, kind, critique, lessons, quality_score, importance, embedding)
		VALUES ($1::uuid, $2::uuid, $3, $4, $5::jsonb, $6, $7, $8)
	`, id, sessionID, kind, res.Critique, string(lessonsJSON),
		quality, importanceFromQuality(quality), embArg); err != nil {
		return "", fmt.Errorf("insert reflection: %w", err)
	}

	// Promote only STRONG lessons, and never a duplicate. mem_lessons had no
	// dedup and a low (0.6) bar, so it bloated to 294 redundant rows ("mint the
	// batch_id" said six ways). Raise the bar to 0.75 and skip a lesson whose
	// text already exists (case-insensitive). Paraphrase-dups still slip, but the
	// forget loop's lesson pruning (low-confidence + never-reinforced + stale)
	// sweeps those over time, so the archive can't grow without bound again.
	for _, l := range res.Lessons {
		text := strings.TrimSpace(l.Text)
		if text == "" || l.Confidence < 0.75 {
			continue
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO mem_lessons (lesson_text, confidence)
			SELECT $1, $2
			 WHERE NOT EXISTS (
			   SELECT 1 FROM mem_lessons WHERE lower(lesson_text) = lower($1)
			 )
		`, text, l.Confidence); err != nil {
			// Don't fail the whole reflection over a lesson - log and move on.
			fmt.Printf("[reflector] persist lesson: %v\n", err)
		}
	}

	// Noticing → fixing bridge. When the critic identified a concrete,
	// code-fixable blocker AND the session went badly, queue a mem_code_proposals
	// candidate so the nightly self-improve loop actually REPAIRS it — instead of
	// the lesson dead-ending as prose. This is the answer to "why isn't a fix
	// queued?": failure reflections now become work items. Same contract the
	// Voyager source-extractor + self-improve loop already consume. Deduped on
	// (source_session) so re-reflecting one session doesn't stack proposals.
	if res.Fix != nil && quality < 0.5 {
		fix := res.Fix
		title := strings.TrimSpace(fix.Title)
		change := strings.TrimSpace(fix.Change)
		// Deterministic floor behind the critic-prompt steer (Rule #1b): even if
		// the model emits a fix for a behavior code already enforces, a fix whose
		// target is a vague phrase ("delegate model routing", "the post-deploy
		// skill") rather than a concrete code location can't become a candidate.
		// This is what stops the false-alarm proposals the boss kept seeing.
		if title != "" && change != "" && looksLikeCodeTarget(fix.TargetHint) {
			risk := strings.ToLower(strings.TrimSpace(fix.Risk))
			switch risk {
			case "low", "medium", "high":
			default:
				risk = "medium"
			}
			ev, _ := json.Marshal(map[string]any{
				"origin":        "reflection_fix",
				"reflection_id": id,
				"critique":      truncateReflection(res.Critique, 500),
				"quality_score": quality,
			})
			if _, err := tx.Exec(ctx, `
				INSERT INTO mem_code_proposals
				  (target_path, title, rationale, proposed_change, evidence, risk_level, status, source_session)
				SELECT $1, $2, $3, $4, $5::jsonb, $6, 'candidate', NULLIF($7,'')::uuid
				WHERE NOT EXISTS (
				  SELECT 1 FROM mem_code_proposals
				   WHERE source_session = NULLIF($7,'')::uuid AND status IN ('candidate','approved')
				)
			`,
				strings.TrimSpace(fix.TargetHint),
				truncateReflection(title, 200),
				truncateReflection("Queued from a failed-session reflection: "+res.Critique, 1000),
				truncateReflection(change, 2000),
				string(ev),
				risk,
				sessionID,
			); err != nil {
				// Don't fail the reflection over a proposal insert — log + move on.
				fmt.Printf("[reflector] queue fix proposal: %v\n", err)
			}
		}
	}

	return id, tx.Commit(ctx)
}

// looksLikeCodeTarget reports whether a critic's target_hint points at a
// concrete code location (a path or symbol) rather than a vague behavioral
// phrase. The false-alarm code proposals the boss saw all had phrase targets
// ("delegate model routing", "post-deploy-verify skill") — the critic
// "noticing" a behavior Go already enforces deterministically. Requiring a real
// target is the deterministic floor: even if the model emits a fix anyway, a
// non-code target can't become a candidate.
func looksLikeCodeTarget(hint string) bool {
	h := strings.TrimSpace(hint)
	if h == "" {
		return false
	}
	// A concrete code target is a SINGLE token — a path ("core/internal/x.go"),
	// a filename ("openai_oauth.go"), or a symbol ("classifyOutcome"). A
	// multi-word phrase is a behavior description, not a code location, and is
	// exactly the false-alarm shape the boss kept seeing: every one of the four
	// he flagged had a phrase target like "bridge routing / bash_run failover"
	// or "delegate model routing / Codex auth compatibility" — note the "/" there
	// is "A / B", NOT a file path, so a bare Contains("/") check would wrongly
	// pass them. Requiring no whitespace catches them deterministically.
	if strings.ContainsAny(h, " \t\n") {
		return false
	}
	return true
}

// truncateReflection trims a string to n runes for the proposal columns.
func truncateReflection(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Reflections returns the most recent reflections, newest first. Useful for
// the Studio Memory tab + the curiosity scanner (low-quality reflections seed
// curiosity questions).
func (r *Reflector) Reflections(ctx context.Context, limit int) ([]Reflection, error) {
	if r == nil || r.pool == nil {
		return nil, nil
	}
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	rows, err := r.pool.Query(ctx, `
		SELECT id::text, COALESCE(session_id::text, ''), kind, critique,
		       COALESCE(lessons::text, '[]'), quality_score, importance, created_at
		  FROM mem_reflections
		 ORDER BY created_at DESC
		 LIMIT $1
	`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Reflection
	for rows.Next() {
		var rf Reflection
		var lessonsJSON string
		if err := rows.Scan(&rf.ID, &rf.SessionID, &rf.Kind, &rf.Critique,
			&lessonsJSON, &rf.QualityScore, &rf.Importance, &rf.CreatedAt); err != nil {
			return nil, err
		}
		rf.Lessons = normalizeLessonsJSON(lessonsJSON)
		out = append(out, rf)
	}
	return out, rows.Err()
}

// Reflection is the wire shape for stored reflections.
type Reflection struct {
	ID           string    `json:"id"`
	SessionID    string    `json:"session_id,omitempty"`
	Kind         string    `json:"kind"`
	Critique     string    `json:"critique"`
	Lessons      []Lesson  `json:"lessons"`
	QualityScore float64   `json:"quality_score"`
	Importance   int       `json:"importance"`
	CreatedAt    time.Time `json:"created_at"`
}

// importanceFromQuality inverts the quality score - a *low*-quality session
// is *more* important to remember because it's the one carrying lessons. A
// flawless session has nothing useful to learn from.
func importanceFromQuality(q float64) int {
	imp := 8 - int(q*5) // q=0 → 8 (very important); q=1 → 3 (mundane)
	if imp < 1 {
		imp = 1
	}
	if imp > 10 {
		imp = 10
	}
	return imp
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
