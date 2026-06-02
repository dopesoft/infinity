-- 104_purge_residue.sql — the last of the cleanup the boss approved.
--
-- After 100-103 the brain is clean. This clears the harmless-but-cluttering
-- residue he okayed: rejected skill-proposal dead weight, the failed-run rows
-- from the mess (observability clutter, not knowledge), and the final two
-- triage-related "real history" memories he asked to wipe for literally zero
-- residue. The compress.go operational-noise filter (shipping same deploy)
-- stops new versions of this from ever being created.
--
-- Idempotent.

BEGIN;

-- 1. Rejected skill proposals: decided, never retrieved, pure dead weight.
DELETE FROM mem_skill_proposals WHERE status = 'rejected';

-- 2. Failed run rows from the triage/cron/self-improve mess (the runs UI
--    history). Successful + non-mess runs stay.
DELETE FROM mem_runs
 WHERE status = 'error'
   AND ( label ILIKE '%triage%'
      OR label ILIKE '%cognition%'
      OR label ILIKE '%inbox%'
      OR label ILIKE '%post-deploy%'
      OR label ILIKE '%self-improve%'
      OR label ILIKE '%bulk skill%'
      OR kind = 'code_agent' );

-- 3. The final two triage-residue memories (kept earlier as "real history",
--    now wiped per the boss for zero residue).
DELETE FROM mem_memories
 WHERE tier = 'episodic'
   AND ( title ILIKE '%pushed healing.go%'
      OR (title ILIKE '%approved finding%' AND title ILIKE '%root-cause%') );

COMMIT;
