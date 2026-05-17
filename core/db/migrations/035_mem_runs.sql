-- 035_mem_runs.sql - the generic "long server action in flight" substrate.
--
-- Why this exists: every long-running action in the system (cron run, skill
-- invoke, heartbeat scan, voyager optimize, gym extract, sentinel dispatch,
-- agent turn fired from Studio) was tracking its "is this in flight?" state
-- in component-local React useState. When the user navigated away or
-- backgrounded the tab, the React component unmounted and the spinner
-- vanished - even though the action was still running server-side. When
-- the user came back, the only signal was last_run_at AFTER it finished,
-- never the "still running" intermediate state.
--
-- The fix is to track in-flight runs in the database with realtime push,
-- so any device watching any tab sees the same truth: started, running,
-- finished/errored. The Go side wraps every long action in
-- core/internal/runs.Track(); the Studio side reads via useRuns() +
-- <RunIndicator>. Spinner survives navigation, refresh, focus loss,
-- second-device viewing. This is now a hard rule in CLAUDE.md
-- ("Server-tracked progress").
--
-- One generic shape - not per-kind tables. The `kind` column is a free
-- string the producer picks (eg. 'cron', 'skill', 'heartbeat', 'voyager.optimize',
-- 'gym.extract'). target_id is the row id of the thing being acted on
-- (the cron's id, the skill's name, etc) so consumers can filter to "is
-- THIS row running?".
--
-- Idempotent: every block guards against re-running.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_runs (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    kind            TEXT NOT NULL,                    -- 'cron' | 'skill' | 'heartbeat' | 'voyager.optimize' | ...
    target_id       TEXT NOT NULL DEFAULT '',         -- the row being acted on (cron id, skill name, ...)
    label           TEXT NOT NULL DEFAULT '',         -- human-readable label for the run
    source          TEXT NOT NULL DEFAULT 'manual',   -- 'manual' | 'scheduled' | 'agent' | 'heartbeat' | 'sentinel'
    status          TEXT NOT NULL DEFAULT 'running'   -- 'running' | 'ok' | 'error'
                    CHECK (status IN ('running','ok','error')),
    progress        REAL,                             -- optional 0..1 progress, NULL when indeterminate
    progress_label  TEXT NOT NULL DEFAULT '',         -- optional "step 2/5" style label
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at        TIMESTAMPTZ,
    duration_ms     INTEGER,
    error           TEXT NOT NULL DEFAULT '',
    result_summary  TEXT NOT NULL DEFAULT '',         -- short stringified result for surfaces
    meta            JSONB NOT NULL DEFAULT '{}'::jsonb,
    user_id         UUID                              -- optional, future per-user filtering
);

-- Hot queries:
--   1. "is THIS target currently running?" → (kind, target_id) where status='running'
--   2. "what's running globally right now?" → status='running' order by started_at desc
--   3. "recent runs for this target" → (kind, target_id) order by started_at desc
CREATE INDEX IF NOT EXISTS idx_mem_runs_target_running
    ON mem_runs(kind, target_id) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS idx_mem_runs_running
    ON mem_runs(started_at DESC) WHERE status = 'running';
CREATE INDEX IF NOT EXISTS idx_mem_runs_recent
    ON mem_runs(kind, target_id, started_at DESC);

-- ── realtime publication ──────────────────────────────────────────────────
-- mem_runs is the entire point of this substrate - it MUST replicate so
-- the studio spinner pushes live across devices and persists across focus
-- changes. Same idempotent pattern as 026/027.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) THEN
        CREATE PUBLICATION supabase_realtime;
    END IF;

    IF NOT EXISTS (
        SELECT 1
          FROM pg_publication_tables
         WHERE pubname = 'supabase_realtime'
           AND schemaname = 'public'
           AND tablename = 'mem_runs'
    ) THEN
        ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_runs;
    END IF;

    -- REPLICA IDENTITY FULL so UPDATE payloads carry every column (the
    -- studio reads status / progress / error from the UPDATE event, not
    -- a separate fetch). No-op when already set.
    ALTER TABLE public.mem_runs REPLICA IDENTITY FULL;
END $$;

INSERT INTO infinity_meta (key, value)
VALUES ('mem_runs_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
