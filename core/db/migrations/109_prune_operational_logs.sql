-- 109_prune_operational_logs.sql — the big one I missed: operational log bloat.
--
-- A full schema audit (every mem_ table, not just the "brain") exposed where the
-- weight actually is, and that none of it had a reaper:
--   mem_heartbeats        14,310  ticker "ran" logs (~680/day)
--   mem_heartbeat_findings 6,921  of which 6,906 are resolved/dismissed (15 open)
--   mem_predictions        2,225  every one already resolved
--   mem_cost_events        5,243  summing to $0.00 (subscription, not API billing)
--   mem_audit              5,446  legit audit trail — KEPT
--
-- These aren't retrieved into the agent's reasoning (they're logs/ledgers/
-- dashboard findings), but they grow without bound. This prunes the dead history
-- now; the forget loop (this same deploy) keeps them pruned nightly. Open
-- findings + recent rows are preserved. Idempotent.

BEGIN;

-- Heartbeat ticker logs: keep 2 days for debugging, drop the rest.
DELETE FROM mem_heartbeats WHERE started_at < NOW() - INTERVAL '2 days';

-- Heartbeat findings: keep all OPEN ones + anything resolved/dismissed in the
-- last 7 days; drop older settled findings.
DELETE FROM mem_heartbeat_findings
 WHERE status IN ('resolved','dismissed','auto_dismissed')
   AND COALESCE(resolved_at, auto_dismissed_at, last_seen_at, created_at) < NOW() - INTERVAL '7 days';

-- Predictions: keep 14 days (for surprise/gym mining), drop older resolved ones.
DELETE FROM mem_predictions
 WHERE resolved_at IS NOT NULL AND resolved_at < NOW() - INTERVAL '14 days';

-- Cost events ($0 under subscription): keep 14 days.
DELETE FROM mem_cost_events WHERE created_at < NOW() - INTERVAL '14 days';

-- mem_audit deliberately NOT pruned here (it's the audit trail); the forget loop
-- caps it at 90 days going forward.

COMMIT;
