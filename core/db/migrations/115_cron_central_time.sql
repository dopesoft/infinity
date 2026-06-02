-- 115_cron_central_time.sql
--
-- ONE timezone convention for crons: the boss's local frame (America/Chicago),
-- never UTC. The companion Go change (core/internal/cron/scheduler.go) switches
-- robfig from a hardcoded time.UTC location to INFINITY_USER_TIMEZONE
-- (default America/Chicago) — the SAME env the LocalTimeProvider already uses.
--
-- Because the engine now interprets every cron expression on the boss's wall
-- clock, the four seeded rows below — which were authored as UTC expressions —
-- would otherwise shift 5-6 hours earlier. This migration rewrites each one to
-- the Central equivalent that PRESERVES its documented intent, and strips the
-- mixed "UTC"/"Central" wording out of schedule_natural so the surface speaks
-- one convention. Result is also DST-stable: "3am" stays 3am year-round because
-- the location, not a fixed offset, drives the schedule.
--
--   nightly-self-improve : 0 8  UTC ("~3am Central")          → 0 3   Central (3am)
--   post-deploy-verify   : 30 8 UTC ("30 min after, 08:30")   → 30 3  Central (3:30am)
--   nightly-cognition    : 0 4  UTC ("4am UTC" ≈ 11pm Central)→ 0 23  Central (11pm)
--   inbox-triage         : 7 */6 ("every 6 hours")            → unchanged expr,
--                          natural text already zone-neutral; left as-is.
--
-- Idempotent: pure UPDATEs keyed by name. Re-running is a no-op.

BEGIN;

UPDATE mem_crons
   SET schedule = '0 3 * * *',
       schedule_natural = 'every night at 3am'
 WHERE name = 'nightly-self-improve';

UPDATE mem_crons
   SET schedule = '30 3 * * *',
       schedule_natural = 'every night at 3:30am, 30 min after self-improve'
 WHERE name = 'post-deploy-verify';

UPDATE mem_crons
   SET schedule = '0 23 * * *',
       schedule_natural = 'every night at 11pm'
 WHERE name = 'nightly-cognition';

-- inbox-triage keeps "7 */6 * * *" — "every 6 hours" is zone-neutral and stays
-- true under the Central engine; only normalise the label wording.
UPDATE mem_crons
   SET schedule_natural = 'every 6 hours, across all connected Gmail inboxes'
 WHERE name = 'inbox-triage';

COMMIT;
