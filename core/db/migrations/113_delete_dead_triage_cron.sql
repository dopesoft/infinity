-- 113_delete_dead_triage_cron.sql — remove the dead, hardwired triage cron.
--
-- The boss: "delete the dead skill based cron."
--
-- `triage-gmail-followups-central` is the OLD triage cron: disabled, NOT
-- skill-based (it inlined the whole recipe), and it hardcodes the now-dead
-- malabie account id `ca_C69i5-U21q6Z` — the exact "hardcoded to old ids after
-- revoke/reauth" footgun that silently dropped a mailbox. It is superseded by
-- the enabled, skill-based `inbox-triage` cron (job_kind isolated_agent_turn,
-- first action skills_invoke({name:"inbox-triage"})), which discovers mailboxes
-- live via connector_accounts_list and covers the reconnected malabie account.
--
-- Guarded DELETE: matches the dead row by id AND name AND enabled=false, so it
-- can NEVER touch the live `inbox-triage` cron (enabled=true) even if ids drift.
-- Idempotent — re-running is a no-op once the row is gone.

BEGIN;

DELETE FROM mem_crons
 WHERE id = 'b811f004-3650-48c8-a3f1-d69e8033d9a6'
   AND name = 'triage-gmail-followups-central'
   AND enabled = false;

COMMIT;
