-- 185_calls_never_expire.sql - give the boss his call log back, permanently.
--
-- surface='calls' is the CALL LOG behind PhoneCard. It was never added to
-- surface.bossOwnedSurfaces, so applyDefaultTTL treated each call as a
-- disposable informational card and stamped it with the default 72h TTL. Every
-- call therefore deleted itself from his dashboard three days to the minute
-- after it rang, and SweepExpired flipped it to 'dismissed' that same night.
--
-- On 2026-07-16 at 17:07-17:29 CST the last seven calls aged out together (they
-- had all rung in one burst on 07-13), and the Phone card went from a full log
-- to empty in front of him. The card literally promises "Jarvis answers his line
-- and logs every call here."
--
-- A call is a durable record of a thing that happened. It does not go stale, so
-- nothing automated should retire it. The permanent fix is the one-line carve-out
-- in surface/store.go (bossOwnedSurfaces), which stops NEW calls being stamped.
-- This migration repairs the calls already stamped.
--
-- Idempotent.

BEGIN;

-- ── 1. Un-dismiss the calls the TTL sweep took ───────────────────────────────
-- ONLY the sweep's own work. SweepExpired runs `SET status='dismissed',
-- decided_at=NOW()` strictly on rows where expires_at < NOW(), so its
-- fingerprint is decided_at >= expires_at. A card the boss (or the agent) cleared
-- deliberately was decided BEFORE its expiry and is left exactly as he left it:
-- undo what the machine did, keep what he did.
--
-- Must run BEFORE step 2 - clearing expires_at first would erase the very
-- evidence this WHERE clause matches on.
UPDATE mem_surface_items
   SET status     = 'open',
       decided_at = NULL,
       updated_at = NOW()
 WHERE surface     = 'calls'
   AND status      = 'dismissed'
   AND expires_at IS NOT NULL
   AND decided_at IS NOT NULL
   AND decided_at >= expires_at;

-- ── 2. Strip the TTL off every call, past and present ────────────────────────
-- The seven that expired at 17:07-17:29 today are still status='open' (the sweep
-- is nightly and hasn't run yet); they're invisible purely because the dashboard
-- filters `expires_at > NOW()`. Nulling expires_at restores them and makes every
-- existing call permanent, matching what store.go now does for new ones.
UPDATE mem_surface_items
   SET expires_at = NULL,
       updated_at = NOW()
 WHERE surface = 'calls'
   AND expires_at IS NOT NULL;

COMMIT;
