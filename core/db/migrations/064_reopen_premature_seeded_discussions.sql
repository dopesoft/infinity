-- 064_reopen_premature_seeded_discussions.sql
--
-- Migration 063 treated "Discuss with Jarvis" as completion. That was too
-- aggressive: discussion starts the work, it does not prove the work is fixed.
-- Reopen rows hidden solely because a seeded discussion exists. Jarvis should
-- close them later with surface_update/question_decide after actual resolution.

BEGIN;

UPDATE mem_surface_items s
   SET status = 'open',
       decided_at = NULL,
       updated_at = NOW()
 WHERE s.status = 'done'
   AND EXISTS (
       SELECT 1
         FROM mem_sessions sess
        WHERE sess.seeded_from->>'kind' = 'surface'
          AND sess.seeded_from->>'id' = s.id::text
   );

UPDATE mem_curiosity_questions q
   SET status = 'open',
       asked_at = NULL
 WHERE q.status = 'asked'
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
