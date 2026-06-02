-- 096_mem_deployments.sql — deploy-awareness so the boss isn't alarmed by
-- failure memories from BEFORE he fixed the thing.
--
-- The complaint: "I fix something in code then get weird memories/surfaces that
-- fire without knowing about my updates." Memories had no notion of WHICH code
-- version observed them, so a failure logged at 2pm looked identical to one
-- logged after the 6pm deploy that fixed it.
--
-- This adds:
--   1. mem_deployments — a timeline of running commits. deploy_marker.go inserts
--      a row at boot whenever RAILWAY_GIT_COMMIT_SHA changes.
--   2. current_deploy_sha() — reads the latest deployment.
--   3. A commit_sha column on mem_memories + mem_observations that DEFAULTS to
--      current_deploy_sha(), so every new row is auto-stamped with the code
--      version that produced it — ZERO changes to the ~8 Go insert sites.
--
-- The Studio Memory tab compares a row's commit_sha to the current deploy and
-- shows a subtle "from before your latest deploy" badge. Honest history: nothing
-- is deleted or relabeled "resolved" (mem_memories has no error TYPE to key on —
-- it's tier-based — so we stamp provenance, not a fake resolution flag).
--
-- Idempotent.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_deployments (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  commit_sha  TEXT NOT NULL,
  deployed_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  note        TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_mem_deployments_deployed_at
  ON mem_deployments (deployed_at DESC);

-- STABLE read of the latest running commit. Explicit search_path per the
-- security rules; INVOKER is correct here because Core connects as the DB owner
-- via pgx (no PostgREST/RLS path), and it only reads a non-sensitive timeline.
CREATE OR REPLACE FUNCTION current_deploy_sha()
RETURNS TEXT
LANGUAGE sql
STABLE
SET search_path = public
AS $$
  SELECT commit_sha FROM mem_deployments ORDER BY deployed_at DESC LIMIT 1
$$;

-- Auto-stamp every new memory/observation with the code version that produced
-- it. Existing rows stay NULL (pre-feature) and the UI treats NULL as "old".
ALTER TABLE mem_memories
  ADD COLUMN IF NOT EXISTS commit_sha TEXT DEFAULT current_deploy_sha();

ALTER TABLE mem_observations
  ADD COLUMN IF NOT EXISTS commit_sha TEXT DEFAULT current_deploy_sha();

COMMIT;
