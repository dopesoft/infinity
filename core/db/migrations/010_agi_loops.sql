-- 010_agi_loops.sql — Close the AGI loops Infinity advertised but didn't have.
--
-- Adds substrate for:
--   • Reflections (metacognition) — agent critiques its own sessions.
--   • Predictions (predict-then-act) — pre-tool expectation paired with reality.
--   • A-MEM associative links — relation_type='associative' on mem_relations.
--   • GEPA Pareto frontier — multiple proposals per optimization run, ranked.
--   • Procedural memory tier — index supports always-injected top-K.
--   • Curiosity questions — agent-generated probes for knowledge gaps.

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. Reflections (Park-style + MAR critic persona). One row per critique.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_reflections (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id     UUID REFERENCES mem_sessions(id) ON DELETE SET NULL,
    kind           TEXT NOT NULL DEFAULT 'session_critique',
        -- session_critique | turn_critique | error_postmortem | self_consistency
    critique       TEXT NOT NULL,
    lessons        JSONB NOT NULL DEFAULT '[]'::jsonb,
        -- [{ "text": "...", "confidence": 0.0..1.0 }, ...]
    quality_score  DOUBLE PRECISION NOT NULL DEFAULT 0.5,
    importance     INT NOT NULL DEFAULT 5,
    embedding      vector(384),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_reflections_session
    ON mem_reflections(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reflections_created
    ON mem_reflections(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_reflections_quality
    ON mem_reflections(quality_score);

-- ---------------------------------------------------------------------------
-- 2. Predictions (JEPA-posture predict-then-act). PreToolUse writes the
--    `expected` row; PostToolUse fills `actual` + `surprise_score`. High
--    surprise becomes a Voyager curriculum signal.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_predictions (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id      UUID,
    tool_call_id    TEXT NOT NULL,
    tool_name       TEXT NOT NULL,
    tool_input      JSONB NOT NULL DEFAULT '{}'::jsonb,
    expected        TEXT NOT NULL,
    actual          TEXT,
    matched         BOOLEAN,
    surprise_score  DOUBLE PRECISION,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at     TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_predictions_call
    ON mem_predictions(tool_call_id);
CREATE INDEX IF NOT EXISTS idx_predictions_session
    ON mem_predictions(session_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_predictions_unresolved
    ON mem_predictions(created_at DESC)
    WHERE resolved_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_predictions_surprise
    ON mem_predictions(surprise_score DESC NULLS LAST);

-- ---------------------------------------------------------------------------
-- 3. A-MEM associative links. The mem_relations table is already typed (TEXT);
--    we just need an index that supports lookup by type so the agent can pull
--    every 'associative' edge for a memory in one query.
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_mem_relations_type
    ON mem_relations(relation_type);

-- ---------------------------------------------------------------------------
-- 4. GEPA Pareto frontier columns on mem_skill_proposals. A single optimize
--    call now writes multiple candidate rows sharing a frontier_run_id; the
--    runner samples from the frontier instead of picking a champion.
-- ---------------------------------------------------------------------------
ALTER TABLE mem_skill_proposals
    ADD COLUMN IF NOT EXISTS frontier_run_id UUID,
    ADD COLUMN IF NOT EXISTS score DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS pareto_rank INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS gepa_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;

CREATE INDEX IF NOT EXISTS idx_skill_proposals_frontier
    ON mem_skill_proposals(frontier_run_id, pareto_rank);
CREATE INDEX IF NOT EXISTS idx_skill_proposals_parent_status
    ON mem_skill_proposals(parent_skill, status);

-- ---------------------------------------------------------------------------
-- 5. Procedural memory tier — supports the always-injected top-K used by
--    skill-aware system-prompt construction. Promoted skills materialise as
--    procedural memories so the agent loop can pull them via the same memory
--    machinery (RRF / search / forget) as any other knowledge.
-- ---------------------------------------------------------------------------
CREATE INDEX IF NOT EXISTS idx_memories_procedural_active
    ON mem_memories(tier, status, strength DESC)
    WHERE tier = 'procedural' AND status = 'active';

-- ---------------------------------------------------------------------------
-- 6. Curiosity questions — gap-driven questions surfaced by the heartbeat.
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_curiosity_questions (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    question     TEXT NOT NULL,
    rationale    TEXT NOT NULL DEFAULT '',
    source_kind  TEXT NOT NULL DEFAULT 'gap',
        -- gap | contradiction | low_confidence | uncovered_mention | high_surprise
    source_ids   UUID[] NOT NULL DEFAULT ARRAY[]::UUID[],
    importance   INT NOT NULL DEFAULT 5,
    status       TEXT NOT NULL DEFAULT 'open',
        -- open | asked | answered | dismissed
    answer       TEXT,
    asked_at     TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at  TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_curiosity_open
    ON mem_curiosity_questions(status, importance DESC, created_at DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_curiosity_open_question
    ON mem_curiosity_questions(question)
    WHERE status = 'open';

COMMIT;
