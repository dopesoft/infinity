-- 041_agi_loop_wiring.sql - close the AGI loop on the dashboard.
--
-- Six small additions that let the proactive subsystem stop being
-- write-only telemetry and start FEEDING the memory graph:
--
-- 1. mem_curiosity_questions.memory_id  - link the question to its twin
--    mem_memories row so the agent can RRF-retrieve "we have an open
--    question about X" while reasoning, and so provenance edges
--    (mem_memory_sources) tie back to the underlying observations the
--    question was distilled from. Same idea for findings.
--
-- 2. *.prediction_id  - when the healer emits "Tool X keeps failing,
--    fix?", we record a Pre prediction ("if boss approves and agent
--    acts, condition clears within N turns"). Post-resolution Jaccard
--    surprise fuels Voyager curriculum: the fixes that DIDN'T work
--    become new skill candidates.
--
-- 3. mem_dismissal_patterns aggregation view (materialized into a
--    proper table for fast lookups). When the boss bulk-dismisses a
--    class of questions, the count crosses a threshold and we write
--    a procedural memory ("don't emit X-class without a fundamentally
--    different signal"). The detectors retrieve it via the same RRF
--    machinery as any other procedural fact.
--
-- Idempotent.

BEGIN;

-- ── 1 + 2. Question / finding -> memory_id + prediction_id ──────────────────

ALTER TABLE mem_curiosity_questions
    ADD COLUMN IF NOT EXISTS memory_id     UUID REFERENCES mem_memories(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS prediction_id UUID;

CREATE INDEX IF NOT EXISTS idx_curiosity_memory_id
    ON mem_curiosity_questions(memory_id) WHERE memory_id IS NOT NULL;

ALTER TABLE mem_heartbeat_findings
    ADD COLUMN IF NOT EXISTS memory_id     UUID REFERENCES mem_memories(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS prediction_id UUID;

CREATE INDEX IF NOT EXISTS idx_findings_memory_id
    ON mem_heartbeat_findings(memory_id) WHERE memory_id IS NOT NULL;

-- Healer-emission predictions share mem_predictions with tool-call
-- predictions. Existing tool-call predictions keep kind='tool_prediction';
-- healer rows set tool_name='healer_emission' and kind='healer_emission'.
ALTER TABLE mem_predictions
    ADD COLUMN IF NOT EXISTS kind      TEXT        NOT NULL DEFAULT 'tool_prediction',
    ADD COLUMN IF NOT EXISTS scored_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_predictions_kind_created
    ON mem_predictions(kind, created_at DESC);

-- ── 3. mem_dismissal_patterns - lightweight aggregation for the
-- "the boss keeps dismissing this CLASS" learning signal ─────────────────────
--
-- We could compute this on demand from mem_curiosity_questions, but the
-- detectors check it on every heartbeat tick so a precomputed row keyed
-- on source_tag_prefix is cheap and avoids a scan-each-time penalty.
CREATE TABLE IF NOT EXISTS mem_dismissal_patterns (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- Stable grouping key. Most source_tags are "<kind>:<id>" - the
    -- "kind" alone is the right grouping for "boss keeps dismissing
    -- repeated_tool_error" but the specific id is too narrow. We
    -- accept either: a prefix is the source_kind ("repeated_tool_error"),
    -- a full source_tag ("repeated_tool_error:fs_read"), or any custom
    -- semantic key the caller chooses.
    pattern_key         TEXT NOT NULL UNIQUE,

    -- Rolling count of dismissals over the watch window (7 days by
    -- default; bumped per dismissal, decayed on a schedule).
    dismissal_count     INT NOT NULL DEFAULT 0,

    -- The procedural-memory row this pattern produced (NULL until the
    -- count crosses the promotion threshold and we write to
    -- mem_memories tier='procedural').
    procedural_memory_id UUID REFERENCES mem_memories(id) ON DELETE SET NULL,

    first_dismissed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_dismissed_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_promoted_at    TIMESTAMPTZ,

    -- A capped list of recent dismissed question titles so the
    -- procedural memory's content captures concrete examples ("you
    -- keep emitting things like 'X failed N times' - stop").
    sample_titles       JSONB NOT NULL DEFAULT '[]'::jsonb,

    created_at          TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at          TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_dismissal_patterns_count
    ON mem_dismissal_patterns(dismissal_count DESC, last_dismissed_at DESC);

COMMIT;
