-- 053_reflection_chains_referenced.sql - track when a chain was last folded
-- into the agent's per-turn context.
--
-- Without this the ReflectionChainsProvider would surface the same top-
-- confidence lesson on every turn forever. The provider stamps
-- last_referenced_at when it emits a chain so it can round-robin among the
-- top-confidence rows: each turn picks the most-confident chain whose
-- last_referenced_at is oldest, then bumps the stamp.

BEGIN;

ALTER TABLE mem_reflection_chains
    ADD COLUMN IF NOT EXISTS last_referenced_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_mem_reflection_chains_active
    ON mem_reflection_chains (confidence DESC, last_referenced_at NULLS FIRST);

COMMIT;
