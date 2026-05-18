-- 050_quiet_repeated_dashboard_questions.sql
--
-- Clean up the noisy rows produced before the heartbeat/question dedupe fix:
-- repeated single-tool "crystallize this into a skill?" prompts and untagged
-- curiosity heartbeat wrappers. This is a soft close only; history remains.

BEGIN;

UPDATE mem_curiosity_questions
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       resolved_reason = 'auto_quiet_050',
       auto_dismissed_at = NOW()
 WHERE status = 'open'
   AND source_kind = 'skill_opportunity'
   AND (
       question ILIKE '%composio__GMAIL_FETCH_EMAILS%'
       OR question ILIKE '%surface_item -> surface_item%'
       OR question ILIKE '%system_map -> load_tools%'
       OR question ILIKE '%surface_item -> notify%'
   );

UPDATE mem_heartbeat_findings
   SET status = 'dismissed',
       resolved_at = COALESCE(resolved_at, NOW()),
       auto_dismissed_at = NOW()
 WHERE status = 'open'
   AND (
       (kind = 'curiosity' AND COALESCE(source_tag, '') = '')
       OR (
           kind = 'skill_opportunity'
           AND (
               title ILIKE '%composio__GMAIL_FETCH_EMAILS%'
               OR title ILIKE '%surface_item -> surface_item%'
               OR title ILIKE '%system_map -> load_tools%'
               OR title ILIKE '%surface_item -> notify%'
           )
       )
   );

COMMIT;
