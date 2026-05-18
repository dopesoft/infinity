-- 052_quiet_notify_surprise_question.sql
--
-- The notify tool returning an unexpected payload is implementation noise, not
-- a boss-facing question. Cool existing rows so older binaries do not re-open
-- the same high-surprise prediction before the code suppressor deploys.

BEGIN;

UPDATE mem_curiosity_questions
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       resolved_reason = 'auto_quiet_052',
       auto_dismissed_at = NOW(),
       cooldown_until = NOW() + INTERVAL '24 hours'
 WHERE status = 'open'
   AND source_kind = 'high_surprise'
   AND question ILIKE 'Tool notify returned something unexpected%';

UPDATE mem_heartbeat_findings
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       auto_dismissed_at = NOW(),
       cooldown_until = NOW() + INTERVAL '24 hours'
 WHERE status = 'open'
   AND kind = 'curiosity'
   AND title ILIKE 'Tool notify returned something unexpected%';

COMMIT;
