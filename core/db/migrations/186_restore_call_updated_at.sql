-- 186_restore_call_updated_at.sql - undo damage migration 185 did to the call log.
--
-- MY BUG, and it is worth naming precisely so nobody repeats it. 185 restored the
-- boss's calls by clearing the bogus TTL, and carried `updated_at = NOW()` along
-- for the ride out of pure reflex. Clearing an expiry column is not an update to
-- the CALL: nothing the boss can see about it changed. But the dashboard renders
--
--     relTime(item.updatedAt ?? item.createdAt)      -- SurfaceCard.tsx
--
-- so every call in his log started reporting the age of MY MIGRATION instead of
-- the age of his call. All 12 rows carried the identical stamp
-- 2026-07-16 19:08:42.091703-05, which read as "29m ago" on the card while the
-- transcript inside said 3 days ago. He caught the contradiction immediately.
--
-- The lesson: `updated_at` means "the content the boss reads changed", NOT "a row
-- was written". Housekeeping that touches a column he cannot see must leave it
-- alone. A migration that repairs data should be invisible in the UI, and this
-- one screamed.
--
-- A call is an immutable record of a thing that happened at a moment. Nothing
-- legitimately updates it after it is logged, so updated_at = created_at is the
-- truth for every row here, with one exception restored explicitly below.
--
-- Idempotent.

BEGIN;

-- ── 1. The call log tells the truth about when each call rang ────────────────
UPDATE mem_surface_items
   SET updated_at = created_at
 WHERE surface = 'calls'
   AND updated_at <> created_at;

-- ── 2. The one row where updated_at was NOT the call time ────────────────────
-- The 2026-07-10 12:31 "Inbound call" was cleared deliberately (dismissed with no
-- decided_at, i.e. not the TTL sweep's doing - see 185), and its real updated_at
-- was 2026-07-10 14:25:29.33396 CST, which 185 overwrote. Restoring the recorded
-- value rather than flattening it to created_at: it is his action, and step 1
-- above would erase the record of when he took it. Postgres does the timezone
-- math so the intent stays readable.
UPDATE mem_surface_items
   SET updated_at = TIMESTAMP '2026-07-10 14:25:29.33396' AT TIME ZONE 'America/Chicago'
 WHERE surface     = 'calls'
   AND status      = 'dismissed'
   AND decided_at IS NULL
   AND created_at  = TIMESTAMP '2026-07-10 12:31:45.767045' AT TIME ZONE 'America/Chicago';

COMMIT;
