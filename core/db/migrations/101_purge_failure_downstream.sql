-- 101_purge_failure_downstream.sql — purge everything the triage mess leaked into.
--
-- Migration 100 deleted the episodic failure memories + stale surfaces. But the
-- failure sessions ALSO leaked downstream, and the boss is right that this
-- "messes us up going forward":
--   - mem_reflections: robotic post-mortems of the failed sessions (the ones he
--     pasted, "Session was a partial failure...").
--   - mem_lessons: failure-derived guidance ("validate auth before triage",
--     pagination notes) that's now obsolete (the recipe handles it).
--   - mem_training_examples (GYM): the failed sessions were mined as
--     error_postmortem / session_critique training data — i.e. the system was
--     literally learning to expect triage failure.
--   - mem_graph_nodes (KNOWLEDGE GRAPH): failure entities like "413
--     Upstream_PayloadTooLarge", "unknown skill inbox-triage", "Loop detected in
--     compact_context" got linked into the graph, so they'd be retrieved as
--     "facts" about how the system behaves.
--
-- All scoped to the 2026-06-01+ mess window + specific failure patterns, so
-- older legit reflections/lessons/training/graph knowledge is untouched. Graph
-- edges + node_observations CASCADE on node delete. One-shot cleanup, NOT a new
-- deletion policy. Idempotent.

BEGIN;

-- 1. Reflections: the failed-session post-mortems from the mess window.
DELETE FROM mem_reflections
 WHERE created_at >= '2026-06-01'
   AND ( quality_score < 0.6
      OR critique ILIKE '%413%'
      OR critique ILIKE '%payload%'
      OR critique ILIKE '%pagination%'
      OR critique ILIKE '%compact%context%'
      OR critique ILIKE '%triage%' );

-- 2. Lessons: failure-derived triage guidance from the mess window (older
--    legit triage lessons stay).
DELETE FROM mem_lessons
 WHERE created_at >= '2026-06-01'
   AND ( lesson_text ILIKE '%paginat%'
      OR lesson_text ILIKE '%413%'
      OR lesson_text ILIKE '%payload%'
      OR lesson_text ILIKE '%compact%context%'
      OR lesson_text ILIKE '%loop%detect%'
      OR lesson_text ILIKE '%validate%auth%'
      OR (lesson_text ILIKE '%triage%' AND lesson_text ILIKE '%fail%')
      OR (lesson_text ILIKE '%triage%' AND lesson_text ILIKE '%blocker%') );

-- 3. Gym training examples mined from the failed sessions (don't train on the
--    failure noise).
DELETE FROM mem_training_examples
 WHERE created_at >= '2026-06-01'
   AND ( input_text  ILIKE '%413%'              OR output_text ILIKE '%413%'
      OR input_text  ILIKE '%payload%'          OR output_text ILIKE '%payload%'
      OR input_text  ILIKE '%compact%context%'  OR output_text ILIKE '%compact%context%'
      OR input_text  ILIKE '%pagination%'       OR output_text ILIKE '%pagination%'
      OR input_text  ILIKE '%unknown skill%'    OR output_text ILIKE '%unknown skill%'
      OR input_text  ILIKE '%triage%block%'     OR output_text ILIKE '%triage%block%'
      OR input_text  ILIKE '%nightly cognition%fail%'
      OR input_text  ILIKE '%cron%inbox-triage%fail%' );

-- 4. Knowledge-graph nodes for the failure entities/concepts. Edges +
--    node_observations CASCADE. Date-bounded so older legit nodes with similar
--    names survive.
DELETE FROM mem_graph_nodes
 WHERE created_at >= '2026-06-01'
   AND ( name ILIKE '%413%'
      OR name ILIKE '%payload too large%'
      OR name ILIKE '%payloadtoolarge%'
      OR name ILIKE '%triage block%'
      OR name ILIKE '%triage%blocked%'
      OR name ILIKE '%compact%context%'
      OR name ILIKE '%loop detect%'
      OR name ILIKE '%loop_detect%'
      OR name ILIKE '%rapid_loop%'
      OR name ILIKE '%harden-inbox%'
      OR name ILIKE '%oauth connectivity%'
      OR name ILIKE '%incomplete gmail%'
      OR name ILIKE '%unknown skill%'
      OR name ILIKE '%core_restart%' );

COMMIT;
