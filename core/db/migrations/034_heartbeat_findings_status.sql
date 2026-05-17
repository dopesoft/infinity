-- 034_heartbeat_findings_status.sql
--
-- mem_heartbeat_findings was an append-only log: every heartbeat tick
-- wrote a new row, nothing ever marked a row as resolved or dismissed,
-- and the activity feed on the dashboard kept surfacing stale entries
-- forever. The classic symptom was the connector-identity finding
-- showing "2 connected account(s) need identity resolution" alongside
-- an older "4 connected account(s) need identity resolution" - the
-- title varied with the count so dedup by (kind, title) couldn't
-- collapse them, and there was no way for the dashboard to know either
-- row was already stale.
--
-- This migration adds three columns so findings have a proper
-- lifecycle:
--
--   status       open | resolved | dismissed
--                'open' is the default. 'resolved' is set by the
--                heartbeat itself when a new finding with the same
--                source_tag lands (it supersedes the older ones), or
--                when a checklist explicitly clears its condition
--                (e.g. ConnectorIdentityChecklist sees missing=0).
--                'dismissed' is set by the boss via the dismiss
--                endpoint.
--
--   resolved_at  TIMESTAMPTZ stamped when status flips off 'open'.
--
--   source_tag   stable identifier for "what condition this finding
--                is about", e.g. 'connector_identity_resolution'. New
--                findings with the same source_tag supersede the
--                previous ones, which means a stream of count-varying
--                titles never piles up.
--
-- The activity feed in dashboard.api.loadActivity filters to
-- status='open' so resolved/dismissed rows fall off without losing
-- their history (great for /heartbeat archaeology).

BEGIN;

ALTER TABLE mem_heartbeat_findings
    ADD COLUMN IF NOT EXISTS status      TEXT NOT NULL DEFAULT 'open',
    ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS source_tag  TEXT NOT NULL DEFAULT '';

-- Backfill: every finding written before this migration is now
-- considered resolved. They've been showing for hours/days already
-- and the boss has either acted on them or doesn't care anymore. A
-- fresh tick will re-emit the actually-current ones with status=open.
UPDATE mem_heartbeat_findings
   SET status = 'resolved',
       resolved_at = COALESCE(resolved_at, NOW())
 WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_hb_findings_open_recent
    ON mem_heartbeat_findings (created_at DESC)
    WHERE status = 'open';

CREATE INDEX IF NOT EXISTS idx_hb_findings_source_tag
    ON mem_heartbeat_findings (source_tag, status)
    WHERE source_tag <> '';

COMMIT;
