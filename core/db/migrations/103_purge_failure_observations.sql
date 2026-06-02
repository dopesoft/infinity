-- 103_purge_failure_observations.sql — remove the regeneration seed.
--
-- Migrations 100-102 deleted the failure MEMORIES, graph nodes, lessons, and gym
-- examples. But the raw mem_observations they were distilled from survived, and
-- those are the SEED: the nightly reflection/gym passes re-mine recent sessions,
-- and reflection only skips a session that STILL has a reflection (we deleted
-- those). Left alone, tonight's cognition could regenerate the exact noise we
-- just purged — a clean slate that refills itself isn't clean.
--
-- This deletes the raw failure-noise observations (loop-gate blocks, 413/payload
-- errors, "unknown skill" misfires, auth-block triage chatter). FK refs
-- (mem_memory_sources, mem_graph_node_observations) CASCADE, so no orphans.
-- Operational tool-block logs, not durable knowledge.
--
-- Idempotent.

BEGIN;

DELETE FROM mem_observations
 WHERE raw_text ILIKE '%loop detected%'
    OR raw_text ILIKE '%blocked: loop%'
    OR raw_text ILIKE '%payloadtoolarge%'
    OR raw_text ILIKE '%payload too large%'
    OR raw_text ILIKE '%413%'
    OR raw_text ILIKE '%unknown skill: inbox-triage%'
    OR raw_text ILIKE '%compact_context%loop%'
    OR (raw_text ILIKE '%triage%' AND raw_text ILIKE '%blocked%' AND raw_text ILIKE '%auth%');

COMMIT;
