-- 106_purge_all_error_noise.sql — the definitive failure-noise sweep.
--
-- The boss spot-checked and found dozens MORE operational-error memories my
-- narrow patterns missed: build failures, git failures, 404/400/500s, bash exit
-- codes, "file does not exist", merge conflicts, "X is failing. Fix it?"
-- questions — across episodic, working, AND procedural tiers. The agent had been
-- recording every transient hiccup as a durable importance-7 memory, then
-- graphing and training on them.
--
-- This is the comprehensive sweep across memories (all tiers), the knowledge
-- graph (all error nodes), the seed observations, lessons, and Gym examples.
-- It PROTECTS real work/decision records by excluding titles that start with an
-- action/knowledge verb (Fixed, Added, Pushed, Built, Decided, etc.) — those are
-- history worth keeping, not failure chatter. The compress.go operational-noise
-- filter (shipping this deploy) stops new ones from ever being recorded.
--
-- Idempotent.

BEGIN;

-- 1. Error-EVENT memories, every tier. The leading-verb exclusion keeps genuine
--    work/decision/knowledge records ("Fixed X", "Added Y", "Decided Z").
DELETE FROM mem_memories
 WHERE status = 'active'
   AND title !~* '^(fix|fixed|fixing|add|added|adding|implement|implemented|create|created|update|updated|build|built|ship|shipped|wrote|write|refactor|refactored|design|designed|push|pushed|decide|decided|approve|approved|plan|planned|configure|configured|enable|enabled|set up|prefer|prefers|boss |kai )'
   AND (
        title ILIKE '%failed%'      OR title ILIKE '%failure%'  OR title ILIKE '%failing%'
     OR title ILIKE '%fails %'      OR title ILIKE '%fails:%'   OR title ILIKE '%fails.%'
     OR title ILIKE '%blocked%'     OR title ILIKE '%broke%'    OR title ILIKE '%broken%'
     OR title ILIKE '%stuck%'       OR title ILIKE '%stalled%'  OR title ILIKE '%orphan%'
     OR title ILIKE '%404%'         OR title ILIKE '%400 %'     OR title ILIKE '%http 500%'
     OR title ILIKE '%500 error%'   OR title ILIKE '%413%'      OR title ILIKE '%bad request%'
     OR title ILIKE '%server error%' OR title ILIKE '%stage error%' OR title ILIKE '%2 error%'
     OR title ILIKE '%invalid id%'  OR title ILIKE '%invalid argument%' OR title ILIKE '%invalid option%'
     OR title ILIKE '%invalid ordering%' OR title ILIKE '%constraint violation%' OR title ILIKE '%merge conflict%'
     OR title ILIKE '%not found%'   OR title ILIKE '%does not exist%' OR title ILIKE '%not a git%'
     OR title ILIKE '%no upstream%' OR title ILIKE '%exit code 1%'
     OR title ILIKE '%cannot%'      OR title ILIKE '%couldn%'   OR title ILIKE '%unable%'
     OR title ILIKE '%not work%'    OR title ILIKE '%timeout%'  OR title ILIKE '%timed out%'
     OR title ILIKE '%loop detect%' OR title ILIKE '%tool blocked%' OR title ILIKE '%compact context%'
     OR title ILIKE '%triage blocked%' OR title ILIKE '%payload too large%'
     OR title ILIKE '%errors%'      OR title ILIKE '%error occurred%' OR title ILIKE '%with error%'
     OR title ILIKE '%returned%error%' OR title ILIKE '%error:%' OR title ILIKE '%returns 4%' OR title ILIKE '%returns 5%'
     OR title ILIKE '%revoked%'     OR title ILIKE '%expired%'
     OR title LIKE  'question:%'
   );

-- 2. Knowledge graph: every error-type node is a failure entity, not knowledge
--    about the boss or his work. Edges + node_observations CASCADE.
DELETE FROM mem_graph_nodes
 WHERE type = 'error'
    OR (type = 'concept' AND (
          name ILIKE '%loop%detect%' OR name ILIKE '%413%' OR name ILIKE '%payload%'
       OR name ILIKE '%compact%context%' OR name ILIKE '%failure%' OR name ILIKE '%blocked%'
       OR name ILIKE '%timeout%' OR name ILIKE '%404%' ));

-- 3. Seed observations: tool failures + error text, so nothing regenerates.
DELETE FROM mem_observations
 WHERE hook_name = 'PostToolUseFailure'
    OR raw_text ILIKE '%loop detected%'
    OR raw_text ILIKE '%payloadtoolarge%'
    OR raw_text ILIKE '%payload too large%'
    OR raw_text ILIKE '%413%'
    OR raw_text ILIKE '%unknown skill%'
    OR raw_text ILIKE '%404 not found%'
    OR raw_text ILIKE '%exit code 1%'
    OR raw_text ILIKE '%BLOCKED: tool%';

-- 4. Lessons + Gym examples derived from failure noise.
DELETE FROM mem_lessons
 WHERE lesson_text ILIKE '%paginat%' OR lesson_text ILIKE '%413%' OR lesson_text ILIKE '%payload%'
    OR lesson_text ILIKE '%compact%context%' OR lesson_text ILIKE '%loop%detect%'
    OR lesson_text ILIKE '%404%' OR lesson_text ILIKE '%failing cron%'
    OR (lesson_text ILIKE '%triage%' AND lesson_text ILIKE '%fail%');

DELETE FROM mem_training_examples
 WHERE input_text ILIKE '%413%' OR output_text ILIKE '%413%'
    OR input_text ILIKE '%payload too large%' OR output_text ILIKE '%payload too large%'
    OR input_text ILIKE '%compact%context%' OR output_text ILIKE '%compact%context%'
    OR input_text ILIKE '%loop detect%' OR output_text ILIKE '%loop detect%'
    OR input_text ILIKE '%unknown skill%' OR output_text ILIKE '%unknown skill%'
    OR input_text ILIKE '%error_postmortem%' OR input_text ILIKE '%404%';

COMMIT;
