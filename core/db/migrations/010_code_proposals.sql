-- 010_code_proposals.sql - Voyager source-extraction substrate.
--
-- Skill proposals (mem_skill_proposals) crystallize *behavior* into SKILL.md.
-- Code proposals crystallize *struggle* into a suggested source change. When
-- the boss spends a session fighting the same file (multiple edits, failing
-- bash invocations between them, error ratios high), Voyager's source
-- extractor drafts a "here's what I think you should refactor" proposal
-- via Haiku and lands a row here. The boss reviews in Studio and decides
-- whether the agent should attempt the change (which then routes through
-- ClaudeCodeGate for the actual edit) or reject it.
--
-- Nothing is auto-applied. This is autonomous *noticing*, not autonomous
-- writing. The Trust queue still gates every claude_code__edit/write/bash
-- that would land the change on disk.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_code_proposals (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- The file the proposal targets. Paths are repo-relative
    -- (e.g. "core/internal/memory/search.go"). NULL when the proposal
    -- is multi-file or whole-package; rare but allowed.
    target_path    TEXT,

    -- Short headline ("Extract RRF scoring into its own function").
    title          TEXT NOT NULL,

    -- One-paragraph rationale: what symptom Voyager observed and why
    -- the suggested change should resolve it.
    rationale      TEXT NOT NULL DEFAULT '',

    -- Free-form summary of the change. Could be prose, a pseudo-diff,
    -- or a bullet list of steps. We do NOT store an applied diff -
    -- the agent generates the real edit at promote-time so the diff
    -- always reflects current HEAD.
    proposed_change TEXT NOT NULL DEFAULT '',

    -- What Voyager actually saw in the session: edit count, failure
    -- ratio, distinct bash commands run, etc. JSON for forward-compat.
    evidence       JSONB NOT NULL DEFAULT '{}'::jsonb,

    risk_level     TEXT NOT NULL DEFAULT 'medium',  -- low|medium|high|critical
    status         TEXT NOT NULL DEFAULT 'candidate', -- candidate|approved|rejected|applied
    source_session UUID,                            -- mem_sessions.id that triggered it

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    decided_at     TIMESTAMPTZ,
    decision_note  TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_mem_code_proposals_status_created
    ON mem_code_proposals(status, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_mem_code_proposals_target_path
    ON mem_code_proposals(target_path) WHERE target_path IS NOT NULL;

-- Realtime: same pattern as 008_realtime.sql. Studio subscribes to
-- mem_code_proposals so the /code-proposals page lights up the moment
-- Voyager drops a fresh candidate.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) AND NOT EXISTS (
        SELECT 1 FROM pg_publication_tables
        WHERE pubname = 'supabase_realtime'
          AND schemaname = 'public'
          AND tablename = 'mem_code_proposals'
    ) THEN
        ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_code_proposals;
    END IF;
END $$;

COMMIT;
