-- 197: last_moved_at on mem_runs — evidence that a run is actually working.
--
-- WHY
--
-- A run's liveness was inferred entirely from its status column: status =
-- 'running' meant the board drew a pulsing dot and the word "running". But a
-- status column is a claim, set once at the start, and nothing guarantees
-- anything ever sets it back. So a process that died left a row that went on
-- animating "I am working on it" for as long as the row survived. An animated
-- assertion is the strongest one an interface can make, and it was the one
-- with the least evidence behind it.
--
-- started_at could not help: it only says when the claim was made. This column
-- is the missing half — the last moment we had PROOF the run did something.
-- Every progress beat and every tool call stamps it, so "is it moving" becomes
-- a question about evidence instead of a question about a flag.
--
-- NULL is meaningful and is not backfilled to NOW(): it means "this run has
-- never reported movement", which is exactly true of every row written before
-- this migration, and is different from "it moved a long time ago". Surfaces
-- fall back to started_at when it is NULL rather than inventing a beat.

BEGIN;

ALTER TABLE mem_runs
    ADD COLUMN IF NOT EXISTS last_moved_at TIMESTAMPTZ;

-- Hot query: "which running rows have moved recently?" — the board asks this
-- for every in-flight item on every refresh.
CREATE INDEX IF NOT EXISTS idx_mem_runs_last_moved
    ON mem_runs(last_moved_at DESC) WHERE status = 'running';

COMMENT ON COLUMN mem_runs.last_moved_at IS
    'Last moment there was evidence this run did something (a progress beat or a tool call). NULL means it has never reported movement. Drives whether a surface may animate it as live; never inferred from status alone.';

COMMIT;
