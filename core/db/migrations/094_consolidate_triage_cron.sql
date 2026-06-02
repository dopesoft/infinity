-- 094_consolidate_triage_cron.sql — collapse the forked triage cron back to
-- one canonical name.
--
-- Why: a self-improve session "hardened" the inbox-triage cron by calling
-- cron_create_agent(name="inbox-triage-hardened", ...) instead of updating the
-- existing "inbox-triage" cron in place. Result: two conceptual triage crons,
-- and the boss saw "inbox-triage" AND "inbox-triage-hardened" in DONE TODAY /
-- run history right after being told skills were consolidated. The skills
-- table was already clean (one canonical inbox-triage skill); the fragmentation
-- had simply moved to the CRON layer, which had no dedup guard. The Go fix
-- (cron_tools.go normalizeCronName) stops new -hardened/-update twins from being
-- created; this migration heals the live row now.
--
-- The dead, already-disabled legacy cron (triage-gmail-followups-central) is
-- left in place (disabled) — we don't delete cron history without the boss.
--
-- Idempotent: the rename only fires when the hardened row exists AND no
-- canonical row already holds the name, so re-running is a no-op.

BEGIN;

UPDATE mem_crons
   SET name = 'inbox-triage'
 WHERE name = 'inbox-triage-hardened'
   AND NOT EXISTS (SELECT 1 FROM mem_crons WHERE name = 'inbox-triage');

COMMIT;
