-- 107_reset_to_useful_core.sql — reset the brain to just the useful core.
--
-- Audit verdict: of 984 active memories, 908 (92%) were episodic event-log noise
-- (routine "inbox fetched" logs, dup "approved finding <uuid>" records,
-- re-recorded table-gap counts) with no value to the boss. The genuine value is
-- the 7 SEMANTIC profile facts (Name, Role, Working hours, Comm style, Active
-- projects, Valentino's birthday) and the behavioral PROCEDURAL skills.
--
-- Per the boss's explicit "do those recommendations immediately":
--   1. Wipe the entire episodic tier (the junk drawer).
--   2. Keep all 7 semantic facts (his profile — untouched).
--   3. Prune rotten/stale procedural lessons + dedupe (one is now actively
--      WRONG: "Claude code only on Mac" contradicts the cloud self-heal we just
--      built, and it injects into the prompt every turn).
--   4. Clear old reflections + Gym training examples so they regenerate clean
--      from a calm slate.
--
-- mem_memory_sources / mem_relations CASCADE on memory delete. Idempotent.

BEGIN;

-- 1. Episodic event-log: the noise. Gone.
DELETE FROM mem_memories WHERE status = 'active' AND tier = 'episodic';

-- 2. (semantic untouched — that's the profile.)

-- 3a. Rotten / stale procedural lessons.
DELETE FROM mem_memories
 WHERE tier = 'procedural'
   AND ( title ILIKE '%claude code is only relevant on mac%'  -- now FALSE (cloud self-heal exists)
      OR title ILIKE '%harden inbox triage%'                   -- mess-derived
      OR title ILIKE '%coverage regression%'                   -- mess-derived
      OR title ILIKE '%cron hardening%'                        -- mess-derived
      OR title ILIKE '%investigate infinity voice vad%' );     -- stale task, not a lesson

-- 3b. Dedupe procedural by title, keeping the most recent.
DELETE FROM mem_memories a
 USING mem_memories b
 WHERE a.tier = 'procedural' AND b.tier = 'procedural'
   AND lower(a.title) = lower(b.title)
   AND (a.created_at < b.created_at OR (a.created_at = b.created_at AND a.id < b.id));

-- 4. Clear old reflections + Gym so they rebuild from clean data.
DELETE FROM mem_reflections;
DELETE FROM mem_training_examples;

COMMIT;
