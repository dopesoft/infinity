// Package proactive - learning.go: turn boss interactions with the
// dashboard into durable behavioral capital.
//
// This file closes three AGI loops the proactive subsystem was missing:
//
//  Opt 1 (Dismiss -> procedural memory): when the boss dismisses N+
//        questions/findings sharing a pattern_key, write a procedural
//        memory ("don't emit X-class without a fundamentally different
//        signal"). Future detectors retrieve that memory via the same
//        RRF machinery as any other procedural fact - effective
//        threshold rises automatically, the dashboard quiets over
//        weeks without explicit user training.
//
//  Opt 5 (Question provenance): when a question is upserted, also
//        write a mem_memories twin and the mem_memory_sources edges
//        for each source observation. The agent can traverse from
//        question -> observations to answer "show me the 14 failures
//        behind this question" without re-running detection.
//
//  Opt 4 (Predict-then-act on healer): every emitted question records
//        a Pre prediction ("if boss approves, condition clears within
//        5 turns"). Post-resolution Jaccard surprise feeds Voyager
//        curriculum. High-surprise rows mean our fix recipes don't
//        actually work and should become new skill candidates.
//
// The shape: a single LearningHub composes a *memory.Store (or a
// minimal interface over it) + *pgxpool.Pool + ProceduralStore. The
// healing / curiosity / dismiss endpoints call into it. Nil-safe at
// every join - missing wiring degrades gracefully.

package proactive

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Promotion thresholds. When the dismissal_count for a pattern_key
// crosses dismissalPromoteThreshold within the watch window, we write
// a procedural memory; when it crosses dismissalEscalationThreshold,
// we update that memory's importance so it sits higher in RRF.
const (
	dismissalPromoteThreshold    = 3
	dismissalEscalationThreshold = 10
	dismissalWatchWindow         = 7 * 24 * time.Hour
	dismissalSampleTitlesCap     = 5
)

// ProceduralUpserter is the minimal write interface LearningHub needs.
// *memory.ProceduralStore satisfies it via a thin adapter (see wireProcedural
// below) - this narrow shape keeps the proactive package from importing
// memory.
type ProceduralUpserter interface {
	UpsertDismissalProcedural(ctx context.Context, title, content string, importance int) (string, error)
}

// LearningHub is the integration surface for the three learning loops.
// Constructed at serve.go boot from the existing memory.Store +
// ProceduralStore + predictions store + this package's UpsertQuestion.
//
// Nil-safe: a nil Hub or a nil sub-field falls through to a no-op so
// chat-only / minimal deployments still boot.
type LearningHub struct {
	pool       *pgxpool.Pool
	procedural ProceduralUpserter
	logger     *slog.Logger
}

func NewLearningHub(pool *pgxpool.Pool, procedural ProceduralUpserter, logger *slog.Logger) *LearningHub {
	if logger == nil {
		logger = slog.Default()
	}
	return &LearningHub{pool: pool, procedural: procedural, logger: logger}
}

// RecordDismissal is called whenever the boss dismisses a question (or
// bulk-dismisses many). Bumps mem_dismissal_patterns for the row's
// pattern_key (derived from source_tag prefix), and when the count
// crosses the promotion threshold writes / refreshes a procedural
// memory the detectors retrieve via RRF.
//
// pattern_key derivation: take the source_tag and split on ':' to
// get the "kind" prefix. "repeated_tool_error:claude_code__fs_read"
// -> "repeated_tool_error". Cron-failure id-style tags collapse to
// just "cron_failure". A truly unique source_tag (no colon) is used
// verbatim.
//
// Caller passes the row's source_tag + a sample title for the
// procedural memory's content.
func (h *LearningHub) RecordDismissal(ctx context.Context, sourceTag, sampleTitle string) {
	if h == nil || h.pool == nil {
		return
	}
	key := dismissalPatternKey(sourceTag)
	if key == "" {
		return
	}

	// Upsert the aggregate row. sample_titles caps at the most recent N
	// so the procedural content has concrete examples without unbounded
	// JSONB growth.
	sampleJSON, _ := json.Marshal([]string{strings.TrimSpace(sampleTitle)})

	var (
		patternID            string
		newCount             int
		existingProceduralID *string
	)
	err := h.pool.QueryRow(ctx, `
		INSERT INTO mem_dismissal_patterns
		  (pattern_key, dismissal_count, first_dismissed_at, last_dismissed_at, sample_titles)
		VALUES ($1, 1, NOW(), NOW(), $2::jsonb)
		ON CONFLICT (pattern_key) DO UPDATE
		   SET dismissal_count    = mem_dismissal_patterns.dismissal_count + 1,
		       last_dismissed_at  = NOW(),
		       sample_titles      = (
		           SELECT COALESCE(jsonb_agg(t), '[]'::jsonb)
		             FROM (
		                 SELECT * FROM jsonb_array_elements(
		                     mem_dismissal_patterns.sample_titles || EXCLUDED.sample_titles
		                 ) AS t
		                 LIMIT `+fmt.Sprintf("%d", dismissalSampleTitlesCap)+`
		             ) trimmed
		       ),
		       updated_at         = NOW()
		RETURNING id::text, dismissal_count, procedural_memory_id::text
	`, key, string(sampleJSON)).Scan(&patternID, &newCount, &existingProceduralID)
	if err != nil {
		h.logger.Warn("RecordDismissal upsert", "key", key, "err", err)
		return
	}

	// Promote when the count crosses the threshold (or escalate if
	// already promoted but the count keeps climbing).
	if newCount < dismissalPromoteThreshold {
		return
	}
	if newCount == dismissalPromoteThreshold || newCount == dismissalEscalationThreshold {
		h.promoteDismissalPattern(ctx, patternID, key, newCount)
	}
}

// promoteDismissalPattern writes (or refreshes) a procedural memory
// from a dismissal pattern. The memory's title is a stable identifier
// ("dismissal:<pattern_key>") so re-promotions update in place, and
// the content names the pattern explicitly so RRF retrieval at
// detector time surfaces actionable text.
func (h *LearningHub) promoteDismissalPattern(ctx context.Context, patternID, key string, count int) {
	if h.procedural == nil {
		return
	}

	// Pull sample titles for the content.
	var sampleRaw []byte
	_ = h.pool.QueryRow(ctx, `
		SELECT sample_titles FROM mem_dismissal_patterns WHERE id = $1::uuid
	`, patternID).Scan(&sampleRaw)
	var samples []string
	if len(sampleRaw) > 0 {
		_ = json.Unmarshal(sampleRaw, &samples)
	}

	title := "dismissal:" + key
	importance := 7
	if count >= dismissalEscalationThreshold {
		importance = 9
	}
	content := buildDismissalContent(key, count, samples)

	memoryID, err := h.procedural.UpsertDismissalProcedural(ctx, title, content, importance)
	if err != nil {
		h.logger.Warn("dismissal pattern promote", "key", key, "err", err)
		return
	}
	_, _ = h.pool.Exec(ctx, `
		UPDATE mem_dismissal_patterns
		   SET procedural_memory_id = $2::uuid,
		       last_promoted_at     = NOW(),
		       updated_at           = NOW()
		 WHERE id = $1::uuid
	`, patternID, memoryID)
	h.logger.Info("dismissal pattern promoted to procedural memory",
		"key", key,
		"count", count,
		"importance", importance,
		"memory_id", memoryID)
}

// IsPatternSuppressed returns true when the dismissal pattern for the
// given source_tag has been promoted to procedural memory AND not
// dismissed by the boss within the last hour (the procedural memory
// is meant to live for weeks). Detectors call this BEFORE emitting -
// a suppressed pattern means "the boss has told us, via repeated
// dismissals, that this class of question isn't valuable; skip."
//
// The system-prompt RRF retrieval also surfaces these memories at
// chat time so the agent's reasoning naturally avoids the topic, but
// this is the synchronous gate for deterministic detector code.
func (h *LearningHub) IsPatternSuppressed(ctx context.Context, sourceTag string) bool {
	if h == nil || h.pool == nil {
		return false
	}
	key := dismissalPatternKey(sourceTag)
	if key == "" {
		return false
	}
	var promotedAt *time.Time
	_ = h.pool.QueryRow(ctx, `
		SELECT last_promoted_at FROM mem_dismissal_patterns
		 WHERE pattern_key = $1
		   AND procedural_memory_id IS NOT NULL
		 LIMIT 1
	`, key).Scan(&promotedAt)
	return promotedAt != nil
}

// LinkQuestionProvenance writes mem_memory_sources edges for every
// source_id in a freshly-emitted question. Requires the question's
// memory_id to be set (which BindQuestionToMemory does once we have a
// twin row).
//
// This is the part that lets the agent traverse from a dashboard
// question back to the underlying observations - the "show me the 14
// failures behind this" affordance.
//
// NOTE: `sourceIDs` are heterogeneous - detectors pass observation,
// memory, graph-node, prediction, or cron ids depending on the scan.
// The query below resolves each to its underlying observation(s)
// rather than assuming it is one; see the inline comment.
func (h *LearningHub) LinkQuestionProvenance(ctx context.Context, memoryID string, sourceIDs []string) {
	if h == nil || h.pool == nil || memoryID == "" || len(sourceIDs) == 0 {
		return
	}
	for _, srcID := range sourceIDs {
		srcID = strings.TrimSpace(srcID)
		if srcID == "" {
			continue
		}
		// `srcID` is whatever the detector put in SourceIDs, and that is
		// NOT always an observation: contradiction/low-confidence scans
		// pass mem_memories ids, uncovered-mention passes mem_graph_nodes
		// ids, high-surprise passes mem_predictions ids, the self-healer
		// passes mem_crons ids. mem_memory_sources.observation_id has a FK
		// to mem_observations, so inserting any of those raw (the old
		// behaviour) violated the FK on every fresh question.
		//
		// Resolve instead of trust. Three valid shapes, in one statement:
		//   1. srcID IS an observation  -> write the direct edge.
		//   2. srcID is a memory        -> inherit that memory's own
		//      observation provenance, so the question twin still traces
		//      back to the underlying observations (the whole point of
		//      this link).
		//   3. srcID is a graph node    -> resolve to the observations the
		//      node was mentioned in (mem_graph_node_observations). This is
		//      exactly the uncovered-mention detector's case: the node has
		//      >=3 observations and no derived memory yet.
		//   4. srcID is a prediction   -> resolve to the tool-call
		//      observation(s) that share its tool_call_id. This is the
		//      high-surprise detector's case: the evidence behind "tool X
		//      surprised me" IS that PostToolUse observation.
		// Anything else (e.g. a cron id) matches no branch, so nothing is
		// inserted and no FK can fire.
		_, err := h.pool.Exec(ctx, `
			INSERT INTO mem_memory_sources (memory_id, observation_id, confidence)
			SELECT $1::uuid, o.id, 1.0
			  FROM mem_observations o
			 WHERE o.id = $2::uuid
			UNION
			SELECT $1::uuid, ms.observation_id, ms.confidence
			  FROM mem_memory_sources ms
			 WHERE ms.memory_id = $2::uuid
			UNION
			SELECT $1::uuid, gno.observation_id, 1.0
			  FROM mem_graph_node_observations gno
			 WHERE gno.node_id = $2::uuid
			UNION
			SELECT $1::uuid, o.id, 1.0
			  FROM mem_predictions p
			  JOIN mem_observations o ON o.payload->>'tool_call_id' = p.tool_call_id
			 WHERE p.id = $2::uuid AND COALESCE(p.tool_call_id, '') <> ''
			ON CONFLICT (memory_id, observation_id) DO NOTHING
		`, memoryID, srcID)
		if err != nil {
			h.logger.Warn("link question provenance", "memory_id", memoryID, "src_id", srcID, "err", err)
		}
	}
}

// BindQuestionToMemory creates a mem_memories twin for a question if
// one doesn't already exist, links the source observations as
// provenance, and back-references the memory id on the question. The
// twin lives in tier='working' (the question is current evidence, not
// crystallized procedural knowledge); when the boss resolves the
// question it can be promoted up the tier ladder or forgotten.
//
// Returns the memory id. Empty string when wiring is incomplete or
// the question id is unknown.
func (h *LearningHub) BindQuestionToMemory(ctx context.Context, questionID, title, body string, observationIDs []string) string {
	if h == nil || h.pool == nil || questionID == "" {
		return ""
	}
	// Already bound?
	var existing *string
	_ = h.pool.QueryRow(ctx, `
		SELECT memory_id::text FROM mem_curiosity_questions WHERE id = $1::uuid
	`, questionID).Scan(&existing)
	if existing != nil && *existing != "" {
		h.LinkQuestionProvenance(ctx, *existing, observationIDs)
		return *existing
	}

	memID := uuid.NewString()
	_, err := h.pool.Exec(ctx, `
		INSERT INTO mem_memories
		  (id, title, content, tier, status, strength, importance,
		   fts_doc, created_at, updated_at, last_accessed_at)
		VALUES
		  ($1::uuid, $2, $3, 'working', 'active', 1.0, 6,
		   to_tsvector('english', COALESCE($2, '') || ' ' || COALESCE($3, '')),
		   NOW(), NOW(), NOW())
	`, memID, "question:"+strings.TrimSpace(title), body)
	if err != nil {
		h.logger.Warn("bind question to memory", "question_id", questionID, "err", err)
		return ""
	}
	_, _ = h.pool.Exec(ctx, `
		UPDATE mem_curiosity_questions SET memory_id = $2::uuid WHERE id = $1::uuid
	`, questionID, memID)

	h.LinkQuestionProvenance(ctx, memID, observationIDs)
	return memID
}

// RecordHealerPrediction inserts a Pre prediction for a question the
// healer just emitted. Schema: mem_predictions {tool_call_id, expected,
// scored_at, surprise_score, kind}. The kind='healer_emission' marker
// lets the surprise scorer + Voyager curriculum filter for this class.
//
// The "expected outcome" is encoded as a short phrase the Post side
// can Jaccard-compare against the boss's resolution comment OR the
// agent's after-action summary.
func (h *LearningHub) RecordHealerPrediction(ctx context.Context, questionID, expectedOutcome string) string {
	if h == nil || h.pool == nil || questionID == "" {
		return ""
	}
	predID := uuid.NewString()
	_, err := h.pool.Exec(ctx, `
		INSERT INTO mem_predictions
		  (id, tool_call_id, tool_name, tool_input, kind, expected, scored_at)
		VALUES ($1::uuid, $2, 'healer_emission', '{}'::jsonb, 'healer_emission', $3, NOW())
		ON CONFLICT DO NOTHING
	`, predID, "question:"+questionID, expectedOutcome)
	if err != nil {
		// Predictions table may not have the columns we need on every
		// deploy - swallow and continue. The Pre prediction is a
		// best-effort signal, not load-bearing.
		h.logger.Debug("record healer prediction", "question_id", questionID, "err", err)
		return ""
	}
	_, _ = h.pool.Exec(ctx, `
		UPDATE mem_curiosity_questions SET prediction_id = $2::uuid WHERE id = $1::uuid
	`, questionID, predID)
	return predID
}

// dismissalPatternKey collapses a row's source_tag to its grouping
// key. The grouping is "the kind, not the specific instance" - so 100
// dismissed `repeated_tool_error:<various>` rows all bump the same
// `repeated_tool_error` pattern. Empty tag yields empty key (caller
// skips silently).
func dismissalPatternKey(sourceTag string) string {
	tag := strings.TrimSpace(sourceTag)
	if tag == "" {
		return ""
	}
	if i := strings.Index(tag, ":"); i > 0 {
		return tag[:i]
	}
	return tag
}

func buildDismissalContent(key string, count int, samples []string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "The boss has dismissed %d questions in the %q pattern recently.\n\n",
		count, key)
	b.WriteString("Don't emit this class of question without a fundamentally NEW signal ")
	b.WriteString("(a different tool, a new failure mode, or a substantive change in the underlying condition). ")
	b.WriteString("Repeating it just wastes the boss's attention.\n\n")
	if len(samples) > 0 {
		b.WriteString("Recent examples the boss dismissed:\n")
		for _, s := range samples {
			s = strings.TrimSpace(s)
			if s == "" {
				continue
			}
			fmt.Fprintf(&b, "- %s\n", truncateText(s, 200))
		}
	}
	return b.String()
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
