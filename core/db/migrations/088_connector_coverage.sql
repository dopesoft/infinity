-- 088_connector_coverage.sql — per-mailbox triage coverage tracking.
--
-- Why this exists (the 2026-05-28 incident): the boss's lawyer emailed his
-- malabieindustries mailbox and it was never surfaced to Follow-ups. Root
-- cause: that mailbox was reconnected (Composio minted a NEW account id) but
-- the only cron covering it had the OLD id hardcoded and was disabled — so the
-- inbox had ZERO triage coverage for ~9 days. The cron that DID keep running
-- reported "ok" the whole time because it was scanning a DIFFERENT mailbox.
--
-- mem_runs (035) tracks a run at the CRON level — one row per fire — so it
-- cannot see "account 3 of 4 was never actually scanned." This table tracks
-- coverage at the MAILBOX level: the triage recipe calls connector_coverage_mark
-- once per account at the end of its pass (even when the inbox was quiet), and
-- proactive.ConnectorCoverageChecklist raises a finding when any active mailbox
-- goes stale. Per-account coverage is what makes a silently-dropped mailbox
-- visible the same day instead of nine days later.
--
-- Generic: one row per (toolkit, account_id). Adding a new toolkit needs no
-- schema change — the recipe just passes a different toolkit slug.
--
-- Idempotent: CREATE IF NOT EXISTS + guarded index.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_connector_coverage (
    toolkit         TEXT NOT NULL,                     -- e.g. 'gmail'
    account_id      TEXT NOT NULL,                     -- Composio connected_account id (ca_xxx)
    identity        TEXT,                              -- best-effort upstream identity (email/handle) for readable alarms
    last_triaged_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),-- last successful (or attempted) coverage pass
    last_status     TEXT NOT NULL DEFAULT 'ok'         -- 'ok' | 'error'
                    CHECK (last_status IN ('ok','error')),
    last_error      TEXT,                              -- trimmed error when last_status='error'
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (toolkit, account_id)
);

-- Hot query: the coverage checklist pulls every gmail row each heartbeat tick.
CREATE INDEX IF NOT EXISTS idx_mem_connector_coverage_toolkit
    ON mem_connector_coverage(toolkit);

INSERT INTO infinity_meta (key, value)
VALUES ('mem_connector_coverage_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
