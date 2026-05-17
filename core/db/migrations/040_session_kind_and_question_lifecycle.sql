-- 040_session_kind_and_question_lifecycle.sql - the bulk of the
-- "dashboard pile-up" + "background-session clutter" fix.
--
-- Three independent things in one migration because they're all called
-- out by the same plan and ship together:
--
-- 1. mem_sessions gains `kind` so cron / heartbeat / voyager / sentinel
--    sessions can be hidden from the default sessions list. Also
--    origin_ref JSONB so the Namer can synthesize names like
--    "Cron: gmail-triage" without an LLM call.
--
-- 2. mem_curiosity_questions and mem_heartbeat_findings gain the
--    compound-merge substrate (occurrences_count, evidence_log,
--    last_seen_at) + cooldown_until + auto_dismissed_at. Plus a
--    stricter partial unique index on source_tag so detectors UPSERT
--    into the open row instead of inserting duplicates. This is the
--    biggest single win in the plan: 14 "fs_read failed N times" rows
--    become one row with x14 in occurrences_count.
--
-- 3. The existing question text-uniqueness index is supplemented (not
--    replaced) by the new source_tag uniqueness, so callers can dedupe
--    on either key.
--
-- Idempotent.

BEGIN;

-- ── 1. mem_sessions kind / origin_ref ────────────────────────────────────────

ALTER TABLE mem_sessions
    ADD COLUMN IF NOT EXISTS kind        TEXT         NOT NULL DEFAULT 'user',
    ADD COLUMN IF NOT EXISTS origin_ref  JSONB        NOT NULL DEFAULT '{}'::jsonb,
    ADD COLUMN IF NOT EXISTS auto_named  BOOLEAN      NOT NULL DEFAULT FALSE;

-- Constrain kind to the known set. Adding via NOT VALID first then
-- VALIDATE so the migration doesn't lock the table for the full scan
-- on a hot prod DB. The default 'user' makes every existing row valid.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
         WHERE conname = 'mem_sessions_kind_check'
    ) THEN
        ALTER TABLE mem_sessions
            ADD CONSTRAINT mem_sessions_kind_check
            CHECK (kind IN ('user', 'cron', 'heartbeat', 'voyager', 'sentinel', 'workflow'))
            NOT VALID;
        ALTER TABLE mem_sessions VALIDATE CONSTRAINT mem_sessions_kind_check;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_mem_sessions_kind_started
    ON mem_sessions (kind, started_at DESC)
    WHERE deleted_at IS NULL;

-- ── 2. mem_curiosity_questions: compound merge + cooldown ────────────────────

ALTER TABLE mem_curiosity_questions
    ADD COLUMN IF NOT EXISTS occurrences_count INT          NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS evidence_log      JSONB        NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_seen_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cooldown_until    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS auto_dismissed_at TIMESTAMPTZ;

-- Stricter partial unique index on source_tag so detectors hitting the
-- same topic UPDATE the open row instead of inserting a duplicate. The
-- prior unique-on-(question) index (from migration 011) stays as a
-- defensive secondary - if a detector has no tag and the question text
-- still happens to collide, ON CONFLICT DO NOTHING fires through that.
CREATE UNIQUE INDEX IF NOT EXISTS uq_curiosity_open_by_tag
    ON mem_curiosity_questions (source_tag)
    WHERE status = 'open' AND source_tag IS NOT NULL AND source_tag <> '';

CREATE INDEX IF NOT EXISTS idx_curiosity_cooldown
    ON mem_curiosity_questions (source_tag, cooldown_until)
    WHERE cooldown_until IS NOT NULL;

-- ── 3. mem_heartbeat_findings: same shape ────────────────────────────────────

ALTER TABLE mem_heartbeat_findings
    ADD COLUMN IF NOT EXISTS occurrences_count INT          NOT NULL DEFAULT 1,
    ADD COLUMN IF NOT EXISTS evidence_log      JSONB        NOT NULL DEFAULT '[]'::jsonb,
    ADD COLUMN IF NOT EXISTS last_seen_at      TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS cooldown_until    TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS auto_dismissed_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS source            TEXT         NOT NULL DEFAULT 'heartbeat',
    ADD COLUMN IF NOT EXISTS importance        INT          NOT NULL DEFAULT 5;

-- heartbeat_id was NOT NULL in migration 005. Findings can now be
-- created outside a heartbeat tick (e.g. a cron writes one directly),
-- so relax the constraint.
ALTER TABLE mem_heartbeat_findings ALTER COLUMN heartbeat_id DROP NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_findings_open_by_tag
    ON mem_heartbeat_findings (source_tag)
    WHERE status = 'open' AND source_tag IS NOT NULL AND source_tag <> '';

CREATE INDEX IF NOT EXISTS idx_findings_cooldown
    ON mem_heartbeat_findings (source_tag, cooldown_until)
    WHERE cooldown_until IS NOT NULL;

COMMIT;
