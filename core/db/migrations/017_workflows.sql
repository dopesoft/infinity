-- 017_workflows.sql - durable workflows + the job queue.
--
-- Phase 2 of the assembly substrate. A workflow is a first-class, durable,
-- multi-step, RESUMABLE object. The agent assembles one from natural
-- language (a step list); the Go engine runs it as a state machine that
-- persists after every step, so a process restart resumes mid-workflow.
--
-- Skills are single recipes. Workflows chain them - plus tools, sub-agent
-- turns, and human checkpoints - into processes that run over hours or
-- days. This is the spine everything downstream hangs off.
--
-- Three tables:
--   mem_workflows       - the definition (a named, reusable step list)
--   mem_workflow_runs   - one execution instance, with durable state
--   mem_workflow_steps  - per-run step state (status, output, retries)
--
-- The agent NEVER writes these with raw SQL. The workflow_* native tools
-- ARE the contract.

BEGIN;

-- ── mem_workflows - the reusable definition ───────────────────────────────
CREATE TABLE IF NOT EXISTS mem_workflows (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name         TEXT NOT NULL UNIQUE,            -- kebab-case
    description  TEXT NOT NULL DEFAULT '',
    -- The ordered step list - the definition. JSONB array of:
    --   { "name": "…", "kind": "tool|skill|agent|checkpoint",
    --     "spec": { … }, "max_attempts": 3 }
    -- The judgment of WHAT the steps are is the agent's; the engine only
    -- runs the machine.
    steps        JSONB NOT NULL DEFAULT '[]'::jsonb,
    source       TEXT NOT NULL DEFAULT 'agent',   -- agent | manual | <skill name>
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- ── mem_workflow_runs - one execution instance ────────────────────────────
CREATE TABLE IF NOT EXISTS mem_workflow_runs (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- Nullable: ad-hoc runs (inline step list, no saved definition) carry
    -- no workflow_id. workflow_name is always set, for display.
    workflow_id    UUID REFERENCES mem_workflows(id) ON DELETE SET NULL,
    workflow_name  TEXT NOT NULL,
    -- pending  → claimed by the engine on the next tick
    -- running  → actively advancing, step by step
    -- paused   → blocked on a checkpoint awaiting boss approval
    -- done     → every step finished
    -- failed   → a step exhausted its retries
    -- cancelled→ stopped by the boss
    status         TEXT NOT NULL DEFAULT 'pending',
    trigger_source TEXT NOT NULL DEFAULT 'manual', -- manual|cron|agent|sentinel
    input          JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- Accumulated state: { "input": {…}, "steps": { "0": "<output>", … } }.
    -- The engine templates later steps from prior outputs stored here.
    context        JSONB NOT NULL DEFAULT '{}'::jsonb,
    current_step   INT NOT NULL DEFAULT 0,
    error          TEXT NOT NULL DEFAULT '',
    started_at     TIMESTAMPTZ,
    finished_at    TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- The engine's hot query: "give me the oldest runnable run."
CREATE INDEX IF NOT EXISTS idx_mem_workflow_runs_runnable
    ON mem_workflow_runs (updated_at)
    WHERE status IN ('pending', 'running');
CREATE INDEX IF NOT EXISTS idx_mem_workflow_runs_recent
    ON mem_workflow_runs (created_at DESC);

-- ── mem_workflow_steps - per-run step state ───────────────────────────────
CREATE TABLE IF NOT EXISTS mem_workflow_steps (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    run_id        UUID NOT NULL REFERENCES mem_workflow_runs(id) ON DELETE CASCADE,
    step_index    INT NOT NULL,
    name          TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL,                  -- tool|skill|agent|checkpoint
    spec          JSONB NOT NULL DEFAULT '{}'::jsonb,
    -- pending|running|done|failed|skipped|awaiting
    status        TEXT NOT NULL DEFAULT 'pending',
    attempt       INT NOT NULL DEFAULT 0,
    max_attempts  INT NOT NULL DEFAULT 3,
    output        TEXT NOT NULL DEFAULT '',
    error         TEXT NOT NULL DEFAULT '',
    started_at    TIMESTAMPTZ,
    finished_at   TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (run_id, step_index)
);

CREATE INDEX IF NOT EXISTS idx_mem_workflow_steps_run
    ON mem_workflow_steps (run_id, step_index);

-- Realtime: Studio subscribes so the workflow board live-updates.
DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        FOREACH tname IN ARRAY ARRAY[
            'mem_workflows', 'mem_workflow_runs', 'mem_workflow_steps'
        ]
        LOOP
            IF NOT EXISTS (
                SELECT 1 FROM pg_publication_tables
                 WHERE pubname = 'supabase_realtime'
                   AND schemaname = 'public'
                   AND tablename = tname
            ) THEN
                EXECUTE format('ALTER PUBLICATION supabase_realtime ADD TABLE public.%I', tname);
            END IF;
        END LOOP;
    END IF;
END $$;

COMMIT;
