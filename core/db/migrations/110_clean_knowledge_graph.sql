-- 110_clean_knowledge_graph.sql — clean the knowledge graph + entity store.
--
-- Final audit of the graph layer exposed two messes:
--   1. mem_graph_nodes (1,659): 557 ORPHANED nodes (their backing observations
--      were deleted in the reset, leaving dangling entities) + 230 'file' nodes
--      (transient file paths from coding sessions, never durable knowledge).
--   2. mem_entities (2,351): the world-model extractor mislabeled every
--      slash-separated fragment as a "project" — 1,966 of them: Gmail message ids
--      ("inbox/19e7fc..."), word pairs ("to/read", "memory/db"), dates ("5/29"),
--      file refs ("soul/soul.md"). Garbage, not projects.
--
-- Preserves the boss's real identity nodes (kai/khaya/boss/Infinity/Jarvis) and
-- every node still backed by observation evidence. Graph edges +
-- node_observations CASCADE on node delete. Idempotent.

BEGIN;

-- 1. Graph nodes: drop transient file nodes + orphans (evidence gone), but never
--    the boss's core identity entities.
DELETE FROM mem_graph_nodes n
 WHERE n.type = 'file'
    OR ( NOT EXISTS (SELECT 1 FROM mem_graph_node_observations o WHERE o.node_id = n.id)
         AND lower(n.name) NOT IN
             ('kai','boss','khaya','malabie industries','mr khaya','infinity','jarvis') );

-- 2. Entity store: nuke the slash-fragment / id / date garbage mislabeled as
--    projects. Real projects (no slash, real names like "Infinity") survive.
DELETE FROM mem_entities
 WHERE kind = 'project'
   AND ( name LIKE '%/%'
      OR name ~ '^[0-9]'
      OR name ILIKE 'inbox/%'
      OR length(trim(name)) < 3 );

COMMIT;
