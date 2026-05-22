-- 063_mark_seeded_dashboard_discussions_handled.sql
--
-- Before the session seed endpoint advanced source lifecycles, clicking
-- "Discuss with Jarvis" created a chat but left the originating dashboard row
-- open. Backfill existing seeded discussions so already-handled Surfaced by
-- Jarvis cards disappear without requiring the boss to dismiss them manually.

BEGIN;

UPDATE mem_surface_items s
   SET status = 'done',
       decided_at = COALESCE(decided_at, NOW()),
       updated_at = NOW()
 WHERE s.status IN ('open', 'snoozed')
   AND EXISTS (
       SELECT 1
         FROM mem_sessions sess
        WHERE sess.seeded_from->>'kind' = 'surface'
          AND sess.seeded_from->>'id' = s.id::text
   );

UPDATE mem_curiosity_questions q
   SET status = 'asked',
       asked_at = COALESCE(asked_at, NOW())
 WHERE q.status = 'open'
   AND EXISTS (
       SELECT 1
         FROM mem_sessions sess
        WHERE sess.seeded_from->>'id' = q.id::text
          AND (
              sess.seeded_from->>'kind' = 'curiosity'
              OR (
                  sess.seeded_from->>'kind' = 'approval'
                  AND sess.seeded_from->'snapshot'->>'kind' = 'curiosity'
              )
          )
   );

COMMIT;
