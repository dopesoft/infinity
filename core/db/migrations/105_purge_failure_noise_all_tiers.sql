-- 105_purge_failure_noise_all_tiers.sql — catch what 100-104 missed.
--
-- My earlier purges scoped to tier='episodic' and a narrow set of phrasings. The
-- boss's spot-check found the rest: ~90 WORKING-tier and 1 PROCEDURAL-tier
-- failure memories (procedural is injected into the system prompt EVERY turn, so
-- a "cron is failing" procedural memory was actively biasing the agent), plus
-- episodic ones worded differently ("is failing", "launch failed", "404 error",
-- "JSON array length check", "session failures", "question:...fix it?").
--
-- This sweeps ALL tiers using failure-EVENT patterns (specific incidents, never
-- legit concepts like "failover" or "fail-fast"). FK refs CASCADE/SET NULL.
-- Idempotent.

BEGIN;

DELETE FROM mem_memories
 WHERE ( title ILIKE '%is failing%'
      OR title ILIKE '%are failing%'
      OR title ILIKE '%keeps failing%'
      OR title ILIKE '%failing cron%'
      OR title ILIKE '%cron%fail%'
      OR title ILIKE '%launch failed%'
      OR title ILIKE '%failed on mac%'
      OR title ILIKE '%404 error%'
      OR title ILIKE '%cognition%fail%'
      OR title ILIKE '%session failures%'
      OR title ILIKE '%multiple session failures%'
      OR title ILIKE '%tool failure%'
      OR title ILIKE '%tool failures%'
      OR title ILIKE '%internal tool%fail%'
      OR title ILIKE '%blocked inbox triage%'
      OR title ILIKE '%blocked inbox%'
      OR title ILIKE '%two internal tool%'
      OR title ILIKE '%harden%cron%'
      OR title ILIKE '%harden failing%'
      OR title ILIKE '%json array length%'
      OR title ILIKE '%git command failed%'
      OR title ILIKE '%oauth%rate limit%'
      OR title ILIKE '%loop detect%'
      OR title ILIKE '%tool blocked%'
      OR title ILIKE '%compact context%'
      OR title ILIKE '%triage blocked%'
      OR title ILIKE '%payload too large%'
      OR title ILIKE '%413%'
      OR title LIKE 'question:%fix it%'
      OR title LIKE 'question:%failing%'
      OR title LIKE 'question:%keeps failing%' );

COMMIT;
