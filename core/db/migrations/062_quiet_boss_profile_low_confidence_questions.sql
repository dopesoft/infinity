-- 062_quiet_boss_profile_low_confidence_questions.sql
--
-- Low-confidence curiosity is useful for ordinary semantic memory, but the
-- boss profile (`project='_self'`) is the always-on primer. Older binaries
-- turned those profile rows into junk questions like "Is this still true:
-- Name?". Soft-close the already-open rows; the code fix prevents new ones.

BEGIN;

UPDATE mem_curiosity_questions q
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       resolved_reason = 'auto_quiet_062',
       auto_dismissed_at = NOW(),
       cooldown_until = NOW() + INTERVAL '30 days'
 WHERE q.status = 'open'
   AND q.source_kind = 'low_confidence'
   AND (
       EXISTS (
           SELECT 1
             FROM mem_memories m
            WHERE m.project = '_self'
              AND m.id = ANY(q.source_ids)
       )
       OR q.question IN (
           'Is this still true: Name?',
           'Is this still true: Role?',
           'Is this still true: Active projects?',
           'Is this still true: Communication style?',
           'Is this still true: Working hours?',
           'Is this still true: Key relationships?',
           'Is this still true: Anything else?'
       )
   );

UPDATE mem_heartbeat_findings
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       auto_dismissed_at = NOW(),
       cooldown_until = NOW() + INTERVAL '30 days'
 WHERE status = 'open'
   AND kind = 'curiosity'
   AND source = 'low_confidence'
   AND title IN (
       'Is this still true: Name?',
       'Is this still true: Role?',
       'Is this still true: Active projects?',
       'Is this still true: Communication style?',
       'Is this still true: Working hours?',
       'Is this still true: Key relationships?',
       'Is this still true: Anything else?'
   );

COMMIT;
