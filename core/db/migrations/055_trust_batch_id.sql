-- 055_trust_batch_id.sql - group Trust contracts that came from the same
-- batch (inbox triage drafts, calendar prep replies, etc.) so the Studio UI
-- can render an "Approve all N" button instead of forcing per-row taps.
--
-- The TrustQueuePanel groups by batch_id; rows with batch_id IS NULL keep
-- the existing single-item flow. Migration 056 seeds the inbox-triage skill
-- whose action_spec writes a fresh UUID into batch_id for every draft it
-- queues.

BEGIN;

ALTER TABLE mem_trust_contracts
    ADD COLUMN IF NOT EXISTS batch_id UUID;

CREATE INDEX IF NOT EXISTS idx_mem_trust_contracts_batch
    ON mem_trust_contracts (batch_id, status)
    WHERE batch_id IS NOT NULL;

COMMIT;
