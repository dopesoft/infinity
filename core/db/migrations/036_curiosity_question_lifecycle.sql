-- 036_curiosity_question_lifecycle.sql
--
-- mem_curiosity_questions accumulates forever because nothing
-- auto-resolves a question whose underlying condition has cleared. The
-- healing checklist re-runs every tick and the unique index on
-- (question) WHERE status='open' dedupes identical text, but a cron job
-- that started passing again doesn't trigger anything to flip its old
-- "Cron job X is failing" question to resolved. The boss sees these in
-- the dashboard activity feed and Jarvis correctly diagnoses them as
-- "old" - the system just never had the lifecycle to close them.
--
-- Same fix shape as migration 034 did for mem_heartbeat_findings. Add
-- source_tag + resolved_reason, then the healing scanners can call a
-- ResolveQuestionsBySourceTag helper at the end of each scan when the
-- condition is no longer present.
--
-- The unique index uq_curiosity_open_question (on question WHERE
-- status='open') stays. New columns are additive.

BEGIN;

ALTER TABLE mem_curiosity_questions
    ADD COLUMN IF NOT EXISTS source_tag      TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS resolved_reason TEXT NOT NULL DEFAULT '';

-- Backfill: every open question written before this migration is
-- considered stale. The scanners will re-emit anything genuinely current
-- on the next tick, with proper source_tags so future runs supersede
-- cleanly. The 'auto_backfill' reason lets us tell apart "boss
-- dismissed" from "system swept" in audit later.
UPDATE mem_curiosity_questions
   SET status          = 'dismissed',
       resolved_at     = COALESCE(resolved_at, NOW()),
       resolved_reason = 'auto_backfill_036'
 WHERE status = 'open'
   AND created_at < NOW() - INTERVAL '24 hours';

CREATE INDEX IF NOT EXISTS idx_curiosity_source_tag
    ON mem_curiosity_questions (source_tag, status)
    WHERE source_tag <> '';

COMMIT;
