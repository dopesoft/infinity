// Package proactive - questions.go: the COMPOUND-MERGE substrate for
// curiosity questions and heartbeat findings.
//
// Why this exists: the dashboard piles up because every detector writes
// a fresh row whenever the wording shifts. "Tool fs_read failed 3
// times" / "...failed 6 times" / "...failed 9 times" all looked
// distinct to Postgres's unique-on-(question) index and produced three
// rows for the same underlying issue. UpsertQuestion / UpsertFinding
// fix that structurally: dedup is on source_tag (the stable per-topic
// key the detector already carries), and on conflict the helpers
// UPDATE in place - appending to evidence_log, bumping
// occurrences_count, refreshing the rationale to the latest sample,
// and escalating importance if the topic got hotter.
//
// All callers in healing.go and curiosity.go route through these
// helpers. There is no other supported way to insert a question or
// finding once migration 040 lands.

package proactive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// evidenceCap is the max number of evidence_log entries we retain per
// row. We keep the tail (most recent), so when the agent reads a
// long-running finding it sees the latest 20 occurrences with their
// timestamps - enough to spot patterns, not enough to blow the JSONB
// out to megabytes.
const evidenceCap = 20

// cooldownAfterDismiss is how long a source_tag stays silent after the
// boss dismisses a question/finding. The plan calls for 24h; centralized
// here so the dismiss endpoints + cooldown precheck agree.
const cooldownAfterDismiss = 24 * time.Hour

// QuestionDraft is the input to UpsertQuestion. Mirrors the columns of
// mem_curiosity_questions that a detector controls. evidence_log is
// constructed from `Sample` (one new entry per call).
type QuestionDraft struct {
	Question   string   // canonical phrasing; stays constant across merges
	Rationale  string   // refreshed to latest on each merge
	SourceKind string   // free-form taxonomy label, e.g. "repeated_tool_error"
	SourceTag  string   // STABLE dedup key, e.g. "repeated_tool_error:fs_read"
	SourceIDs  []string // observation UUIDs that fed this draft
	Importance int      // 0-10; max() wins on merge
	Sample     string   // short text snapshot of the triggering event
	// Expected captures the Pre prediction the healer makes when it
	// fires this question ("condition clears within 5 turns of boss
	// approving + agent acting"). Surface_tag-keyed; the LearningHub
	// records it into mem_predictions when non-empty.
	Expected string
}

// Hub is the optional learning hub. When non-nil, UpsertQuestion calls
// into it to: (a) check IsPatternSuppressed before inserting, (b) bind
// the new question to a mem_memories twin with provenance edges, and
// (c) record a Pre prediction if Expected was provided. Package-level
// variable so callers don't have to thread it through every emit site.
var Hub *LearningHub

// SetHub wires the package-level learning hub. Called from serve.go
// boot after memory.Store + ProceduralStore are constructed.
func SetHub(h *LearningHub) { Hub = h }

// FindingDraft is the heartbeat-finding equivalent.
type FindingDraft struct {
	Kind        string // "self_heal" | "curiosity" | "security" | ...
	Source      string // detector name
	SourceTag   string // STABLE dedup key
	Title       string // canonical phrasing
	Detail      string // refreshed to latest on each merge
	Importance  int    // 0-10
	Sample      string // triggering snapshot
	HeartbeatID string // FK to mem_heartbeats - set on INSERT, preserved on UPDATE
	PreApproved bool   // whether the action this finding suggests is pre-authorized
}

// UpsertQuestion writes (or merges into) a curiosity question keyed on
// source_tag. Returns the row id, whether this was a fresh INSERT
// (vs a merge into an existing draft), the new occurrences_count, and
// any error. A nil pool / empty source_tag falls through to the legacy
// "INSERT ... ON CONFLICT DO NOTHING" behavior on question text, so
// callers without a stable tag still don't generate dupes.
//
// Cooldown precheck: if an open or recently-dismissed row exists for
// the same source_tag with cooldown_until > NOW(), the upsert refuses
// to write (returns isNew=false, count=0). The dismiss endpoint sets
// cooldown_until on every row sharing the tag, so the boss telling
// Jarvis "stop bothering me about X" buys 24h of silence regardless of
// which detector tries to fire next.
func UpsertQuestion(
	ctx context.Context,
	pool *pgxpool.Pool,
	logger *slog.Logger,
	d QuestionDraft,
) (id string, isNew bool, count int, err error) {
	if pool == nil {
		return "", false, 0, nil
	}
	d.Question = strings.TrimSpace(d.Question)
	d.SourceTag = strings.TrimSpace(d.SourceTag)
	if d.Question == "" {
		return "", false, 0, nil
	}

	// Cooldown precheck - silence wins. A non-empty tag that's cooling
	// (open or recently dismissed) blocks the new write entirely.
	if d.SourceTag != "" && questionInCooldown(ctx, pool, d.SourceTag) {
		return "", false, 0, nil
	}

	// Procedural-memory suppression check (Opt 1): when the boss has
	// dismissed N+ questions in this pattern_key, the LearningHub has
	// already promoted a "don't emit X-class" procedural memory. The
	// hub keeps that signal warm; we just consult it here so the
	// detector loop doesn't have to learn the policy itself.
	if d.SourceTag != "" && Hub != nil && Hub.IsPatternSuppressed(ctx, d.SourceTag) {
		return "", false, 0, nil
	}

	evidenceEntry := buildEvidenceEntry(d.Sample, d.SourceKind)

	if d.SourceTag == "" {
		// No stable tag - fall through to the legacy (question)-text
		// dedup. Still writes evidence_log + occurrences_count on the
		// fresh row, so future merges via the hash fallback are
		// possible without backfill.
		return insertQuestionLegacy(ctx, pool, d, evidenceEntry)
	}

	// Try a merge into the open row first. Most production calls hit
	// this path because the detectors run on a heartbeat schedule.
	row := pool.QueryRow(ctx, `
		UPDATE mem_curiosity_questions
		   SET occurrences_count = occurrences_count + 1,
		       evidence_log      = (
		           SELECT COALESCE(jsonb_agg(e), '[]'::jsonb)
		             FROM (
		                 SELECT e FROM jsonb_array_elements(evidence_log || $2::jsonb) AS elem(e)
		                 ORDER BY (e->>'at')::timestamptz DESC NULLS LAST
		                 LIMIT $3
		             ) trimmed
		       ),
		       last_seen_at = NOW(),
		       rationale    = COALESCE(NULLIF($4, ''), rationale),
		       importance   = GREATEST(importance, $5)
		 WHERE source_tag = $1 AND status = 'open'
		 RETURNING id::text, occurrences_count
	`, d.SourceTag, evidenceEntry, evidenceCap, d.Rationale, d.Importance)
	if scanErr := row.Scan(&id, &count); scanErr == nil {
		return id, false, count, nil
	}

	// No open row for this tag - insert fresh. Race window is closed
	// by the partial unique index from migration 040; on conflict the
	// next iteration of the loop UPDATEs.
	row = pool.QueryRow(ctx, `
		INSERT INTO mem_curiosity_questions
		  (question, rationale, source_kind, source_ids, importance, status,
		   source_tag, occurrences_count, evidence_log, last_seen_at)
		VALUES ($1, $2, $3, $4::uuid[], $5, 'open', $6, 1, jsonb_build_array($7::jsonb), NOW())
		ON CONFLICT (source_tag) WHERE status='open' AND source_tag IS NOT NULL AND source_tag <> ''
		DO UPDATE SET
		  occurrences_count = mem_curiosity_questions.occurrences_count + 1,
		  evidence_log      = mem_curiosity_questions.evidence_log || EXCLUDED.evidence_log,
		  last_seen_at      = NOW(),
		  rationale         = COALESCE(NULLIF(EXCLUDED.rationale, ''), mem_curiosity_questions.rationale),
		  importance        = GREATEST(mem_curiosity_questions.importance, EXCLUDED.importance)
		RETURNING id::text, occurrences_count, (occurrences_count = 1)
	`, d.Question, d.Rationale, d.SourceKind, uuidArray(d.SourceIDs),
		d.Importance, d.SourceTag, evidenceEntry)
	if scanErr := row.Scan(&id, &count, &isNew); scanErr != nil {
		if logger != nil {
			logger.Warn("UpsertQuestion insert", "tag", d.SourceTag, "err", scanErr)
		}
		return "", false, 0, scanErr
	}

	// AGI-loop wiring (Opts 4 + 5): bind question -> mem_memories twin
	// with provenance edges to the source observations, and record a
	// Pre prediction so post-resolution surprise scoring fuels Voyager
	// curriculum. Both fire only on fresh rows so a merge into an
	// existing draft doesn't duplicate edges or predictions.
	if isNew && Hub != nil {
		_ = Hub.BindQuestionToMemory(ctx, id, d.Question, d.Rationale, d.SourceIDs)
		if d.Expected != "" {
			_ = Hub.RecordHealerPrediction(ctx, id, d.Expected)
		}
	}

	return id, isNew, count, nil
}

// UpsertFinding is the heartbeat-finding equivalent. Same compound-merge
// shape as UpsertQuestion: dedup on source_tag, merge on conflict.
func UpsertFinding(
	ctx context.Context,
	pool *pgxpool.Pool,
	logger *slog.Logger,
	d FindingDraft,
) (id string, isNew bool, count int, err error) {
	if pool == nil {
		return "", false, 0, nil
	}
	d.Title = strings.TrimSpace(d.Title)
	d.SourceTag = strings.TrimSpace(d.SourceTag)
	if d.Title == "" {
		return "", false, 0, nil
	}
	if d.SourceTag != "" && findingInCooldown(ctx, pool, d.SourceTag) {
		return "", false, 0, nil
	}

	evidenceEntry := buildEvidenceEntry(d.Sample, d.Source)

	if d.SourceTag == "" {
		// Fallback dedup key: hash of (kind, normalized title).
		d.SourceTag = hashedFallbackTag(d.Kind, d.Title)
	}

	var heartbeatID any
	if d.HeartbeatID != "" {
		heartbeatID = d.HeartbeatID
	} else {
		heartbeatID = nil
	}
	row := pool.QueryRow(ctx, `
		INSERT INTO mem_heartbeat_findings
		  (heartbeat_id, kind, source, source_tag, title, detail, importance, status,
		   occurrences_count, evidence_log, last_seen_at, pre_approved)
		VALUES ($1::uuid, $2, $3, $4, $5, $6, $7, 'open', 1, jsonb_build_array($8::jsonb), NOW(), $9)
		ON CONFLICT (source_tag) WHERE status='open' AND source_tag IS NOT NULL AND source_tag <> ''
		DO UPDATE SET
		  occurrences_count = mem_heartbeat_findings.occurrences_count + 1,
		  evidence_log      = (
		      SELECT COALESCE(jsonb_agg(e), '[]'::jsonb)
		        FROM (
		            SELECT e FROM jsonb_array_elements(
		                mem_heartbeat_findings.evidence_log || EXCLUDED.evidence_log
		            ) AS elem(e)
		            ORDER BY (e->>'at')::timestamptz DESC NULLS LAST
		            LIMIT `+fmt.Sprintf("%d", evidenceCap)+`
		        ) trimmed
		  ),
		  heartbeat_id      = COALESCE(mem_heartbeat_findings.heartbeat_id, EXCLUDED.heartbeat_id),
		  last_seen_at      = NOW(),
		  detail            = COALESCE(NULLIF(EXCLUDED.detail, ''), mem_heartbeat_findings.detail),
		  importance        = GREATEST(mem_heartbeat_findings.importance, EXCLUDED.importance),
		  pre_approved      = mem_heartbeat_findings.pre_approved OR EXCLUDED.pre_approved
		RETURNING id::text, occurrences_count, (occurrences_count = 1)
	`, heartbeatID, d.Kind, d.Source, d.SourceTag, d.Title, d.Detail, d.Importance, evidenceEntry, d.PreApproved)
	if scanErr := row.Scan(&id, &count, &isNew); scanErr != nil {
		if logger != nil {
			logger.Warn("UpsertFinding insert", "tag", d.SourceTag, "err", scanErr)
		}
		return "", false, 0, scanErr
	}
	return id, isNew, count, nil
}

// CoolSourceTag marks every row sharing the given source_tag (on the
// given table) with cooldown_until = NOW() + 24h. Called by the
// dismiss endpoints when the boss explicitly drops a question/finding
// - every detector that tries to re-emit the same topic within 24h is
// blocked by the cooldown precheck in UpsertQuestion / UpsertFinding.
//
// Empty tag is a no-op (we have nothing to group by). Single-row
// dismiss is the caller's responsibility; this helper only extends the
// silence across the tag's siblings.
func CoolSourceTag(ctx context.Context, pool *pgxpool.Pool, table, tag string) {
	if pool == nil || tag == "" {
		return
	}
	switch table {
	case "mem_curiosity_questions", "mem_heartbeat_findings":
		// allowed
	default:
		return
	}
	until := time.Now().Add(cooldownAfterDismiss)
	_, _ = pool.Exec(ctx, fmt.Sprintf(`
		UPDATE %s
		   SET cooldown_until = $2
		 WHERE source_tag = $1
		   AND (cooldown_until IS NULL OR cooldown_until < $2)
	`, table), tag, until)
}

// questionInCooldown returns true when any row for this source_tag has
// cooldown_until > NOW(). The detector should refuse to emit while
// this is true.
func questionInCooldown(ctx context.Context, pool *pgxpool.Pool, tag string) bool {
	var exists bool
	_ = pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM mem_curiosity_questions
		   WHERE source_tag = $1
		     AND cooldown_until IS NOT NULL
		     AND cooldown_until > NOW()
		  LIMIT 1
		)
	`, tag).Scan(&exists)
	return exists
}

func findingInCooldown(ctx context.Context, pool *pgxpool.Pool, tag string) bool {
	var exists bool
	_ = pool.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1 FROM mem_heartbeat_findings
		   WHERE source_tag = $1
		     AND cooldown_until IS NOT NULL
		     AND cooldown_until > NOW()
		  LIMIT 1
		)
	`, tag).Scan(&exists)
	return exists
}

// buildEvidenceEntry produces the JSONB payload appended to evidence_log
// on every merge. Schema: {at, sample, source}.
func buildEvidenceEntry(sample, source string) string {
	entry := map[string]any{
		"at":     time.Now().UTC().Format(time.RFC3339),
		"sample": truncate(sample, 600),
		"source": source,
	}
	b, _ := json.Marshal(entry)
	return string(b)
}

// hashedFallbackTag synthesizes a deterministic tag for callers that
// don't carry a stable source_tag. Hash of (kind, normalized title)
// keeps the dedup key short and stable across runs without bleeding
// into the human-readable space the regular tags occupy.
func hashedFallbackTag(kind, title string) string {
	h := sha256.New()
	h.Write([]byte(strings.ToLower(strings.TrimSpace(kind))))
	h.Write([]byte("|"))
	h.Write([]byte(strings.ToLower(strings.TrimSpace(title))))
	return "hash:" + hex.EncodeToString(h.Sum(nil)[:8])
}

// insertQuestionLegacy is the no-tag fallback. Mirrors the old
// insertHealingQuestion shape - ON CONFLICT DO NOTHING on the
// question-text unique index from migration 011 - but adds the
// evidence_log seeding so a later upsert with a tag can merge.
func insertQuestionLegacy(
	ctx context.Context,
	pool *pgxpool.Pool,
	d QuestionDraft,
	evidence string,
) (string, bool, int, error) {
	var id string
	row := pool.QueryRow(ctx, `
		INSERT INTO mem_curiosity_questions
		  (question, rationale, source_kind, source_ids, importance, status,
		   source_tag, occurrences_count, evidence_log, last_seen_at)
		VALUES ($1, $2, $3, $4::uuid[], $5, 'open', '', 1, jsonb_build_array($6::jsonb), NOW())
		ON CONFLICT DO NOTHING
		RETURNING id::text
	`, d.Question, d.Rationale, d.SourceKind, uuidArray(d.SourceIDs),
		d.Importance, evidence)
	if scanErr := row.Scan(&id); scanErr != nil {
		// "no rows" means a duplicate (question)-text row already exists.
		// Not an error from the caller's perspective.
		return "", false, 0, nil
	}
	return id, true, 1, nil
}
