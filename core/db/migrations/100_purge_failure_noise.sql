-- 100_purge_failure_noise.sql — clean slate after the triage/self-heal mess.
--
-- The boss, explicitly: "delete those fucking memories so we can start from a
-- more peaceful state, or this can mess us up going forward." He's right — these
-- episodic FAILURE memories get RRF-retrieved into the system prompt every turn
-- and bias the agent toward "triage is broken / 413 happens / cron fails", long
-- after the underlying issues were fixed (pagination v1.1, cron reauth park,
-- nightly-cognition SQL, cron consolidation). They are stale noise, not durable
-- knowledge, so they're deleted (he approved the delete; I listed the exact set
-- first). FK refs to mem_memories are CASCADE (sources, relations) or SET NULL
-- (curiosity/heartbeat/dismissal), so this leaves no orphans.
--
-- This is a one-shot cleanup of a specific, dated, pattern-matched set — NOT a
-- new policy of deleting memories. Going forward, stale failure signals
-- de-rank via the deploy-provenance badge (migration 096), and failures read
-- human (errs.Humanize + soul voice) instead of piling up as robotic noise.
--
-- Idempotent (re-running matches nothing once purged).

BEGIN;

-- 1. Episodic failure / loop-spam noise from the 2026-06-01/02 sessions.
DELETE FROM mem_memories
 WHERE tier = 'episodic'
   AND created_at >= '2026-06-01'
   AND (
        title ILIKE '%compact context%'                 -- 7+ dup loop-block memories
     OR title ILIKE '%inbox triage blocked%'            -- old session, pre-fix
     OR title ILIKE '%backfill payload too large%'      -- fixed by pagination v1.1
     OR title ILIKE '%cron job inbox-triage failing%'   -- fixed by cron reauth park
     OR title ILIKE '%nightly cognition cron job failed%' -- fixed (reflection_chains)
     OR title ILIKE '%harden-inbox-triage-cron%'        -- the fragmentation we removed
   );

-- 2. Stale system surface notices cluttering the dashboard: "blocked" states
--    that are resolved, and "hardened" notices referencing the cron name we
--    collapsed. Benign informational notices (no-draft-worthy / skipped) are
--    left alone.
DELETE FROM mem_surface_items
 WHERE surface = 'system'
   AND status IN ('open', 'snoozed')
   AND (
        title ILIKE '%triage hardened%'
     OR title ILIKE '%triage cron hardened%'
     OR title ILIKE '%triage blocked%'
     OR title ILIKE '%gmail triage blocked%'
   );

COMMIT;
