-- 102_purge_failure_noise_full_history.sql — finish the clean slate.
--
-- Migrations 100/101 were date-bounded to the recent mess. But the survey showed
-- the SAME failure-noise patterns accumulated all the way back to 2026-05-16:
-- loop-gate "tool blocked: loop detected" memories, recurring "Gmail triage
-- blocked by auth/connector" notices, cron-failure and 413/payload memories, and
-- their knowledge-graph error nodes. This is exactly the garbage the boss wants
-- gone so it stops poisoning retrieval/graph/gym going forward.
--
-- Preserved (NOT noise): genuine action/decision records ("Pushed healing.go...",
-- "Approved finding..."), and legit skill/tool/person/project graph entities
-- (e.g. the compact_context TOOL node) — we only remove the error/failure ones.
--
-- Idempotent.

BEGIN;

-- 1. Episodic failure-noise memories, full history. Exclude action/decision
--    records so we keep real history, not the recurring infra-block chatter.
DELETE FROM mem_memories
 WHERE tier = 'episodic'
   AND title NOT ILIKE '%pushed%'
   AND title NOT ILIKE '%approved finding%'
   AND (
        title ILIKE '%compact context%'
     OR title ILIKE '%loop detect%'
     OR title ILIKE '%blocked: loop%'
     OR title ILIKE '%blocked due to loop%'
     OR title ILIKE '%blocked by loop%'
     OR title ILIKE '%tool blocked%'
     OR title ILIKE '%triage blocked%'
     OR title ILIKE '%blocked on primary gmail%'
     OR title ILIKE '%blocked by composio%'
     OR title ILIKE '%blocked by connector%'
     OR title ILIKE '%blocked by%auth%'
     OR title ILIKE '%blocked by expired%'
     OR title ILIKE '%payload too large%'
     OR title ILIKE '%413%'
     OR title ILIKE '%cron failure%'
     OR title ILIKE '%cron%failing%'
     OR title ILIKE '%cron job%failed%'
   );

-- 2. Knowledge-graph FAILURE nodes (edges + node_observations CASCADE). Only
--    error-type and failure-named concepts; skill/tool/person/project survive.
DELETE FROM mem_graph_nodes
 WHERE type IN ('error', 'concept')
   AND (
        name ILIKE '%loop detect%'
     OR name ILIKE '%loop_detect%'
     OR name ILIKE '%rapid_loop%'
     OR name ILIKE '%413%'
     OR name ILIKE '%payload too large%'
     OR name ILIKE '%payloadtoolarge%'
     OR name ILIKE '%triage block%'
     OR name ILIKE '%triage%blocked%'
     OR name ILIKE '%unknown skill%'
     OR name ILIKE '%auth failure%'
     OR name ILIKE '%authentication failure%'
     OR name ILIKE '%core_restart%'
     OR name ILIKE '%incomplete gmail%'
     OR name ILIKE '%incomplete_verification%'
     OR (type = 'concept' AND name ILIKE '%compact_context%')
   );

-- 3. Failure-derived lessons, full history (legit older triage lessons stay).
DELETE FROM mem_lessons
 WHERE ( lesson_text ILIKE '%paginat%'
      OR lesson_text ILIKE '%413%'
      OR lesson_text ILIKE '%payload too large%'
      OR lesson_text ILIKE '%compact%context%'
      OR lesson_text ILIKE '%loop%detect%'
      OR (lesson_text ILIKE '%triage%' AND lesson_text ILIKE '%fail%')
      OR (lesson_text ILIKE '%triage%' AND lesson_text ILIKE '%blocker%')
      OR (lesson_text ILIKE '%validate%' AND lesson_text ILIKE '%auth%' AND lesson_text ILIKE '%triage%') );

-- 4. Gym training examples mined from failure post-mortems, full history.
DELETE FROM mem_training_examples
 WHERE ( input_text  ILIKE '%413%'              OR output_text ILIKE '%413%'
      OR input_text  ILIKE '%payload too large%' OR output_text ILIKE '%payload too large%'
      OR input_text  ILIKE '%compact%context%'  OR output_text ILIKE '%compact%context%'
      OR input_text  ILIKE '%loop detect%'      OR output_text ILIKE '%loop detect%'
      OR input_text  ILIKE '%triage block%'     OR output_text ILIKE '%triage block%'
      OR input_text  ILIKE '%unknown skill%'    OR output_text ILIKE '%unknown skill%' );

COMMIT;
