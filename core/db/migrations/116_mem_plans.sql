-- 116_mem_plans.sql - the durable PLAN substrate ("the Cortex").
--
-- Rule #1 building block. The agent already has an in-context todolist
-- (todo_tools.go -> mem_runs.meta.todos) but it is per-background-run and
-- ephemeral: it does not survive compaction, restart, or session boundaries,
-- has no verification, no checkpoints, and the boss cannot steer it. This is
-- the generic, durable contract behind plan -> execute -> verify -> replan:
--
--   * a plan is an INSTANCE (not a reusable template like mem_workflows), so
--     it is lean - one plan + its ordered steps.
--   * the AGENT executes steps in its own loop; this is durable working
--     memory, NOT the autonomous workflow.Engine. The substrate just records
--     what the model is doing and how each step went.
--   * PlanProvider re-injects the active plan every turn, so a long task
--     resumes from saved step state after compaction / restart / a fresh
--     session - the agent reads exactly where it left off.
--
-- The agent NEVER writes these tables with raw SQL. The plan_create /
-- plan_update / plan_verify native tools ARE the contract - the boundary the
-- LLM assembles against. Studio renders mem_plans generically (dashboard
-- PlanCard + /plans page + detail timeline).

BEGIN;

CREATE TABLE IF NOT EXISTS mem_plans (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- The session that owns this plan. One active plan per session
    -- (plan_create supersedes the prior). SET NULL on session delete so a
    -- finished plan survives for history.
    session_id    UUID REFERENCES mem_sessions(id) ON DELETE SET NULL,

    -- Optional link to a durable agent goal (mem_agent_goals). When set, a
    -- multi-day plan survives session boundaries: the GoalsProvider keeps the
    -- objective alive across sessions and the plan hangs off it.
    goal_id       UUID,

    -- Display + reasoning payload.
    title         TEXT NOT NULL,
    goal          TEXT NOT NULL DEFAULT '',

    -- Lifecycle. active -> the agent is working it. paused -> waiting on a
    -- checkpoint / the boss. completed/failed/cancelled -> terminal, kept for
    -- history + provenance.
    status        TEXT NOT NULL DEFAULT 'active',  -- active|paused|completed|failed|cancelled

    -- The index of the step currently in flight (0-based), maintained by
    -- plan_update as steps advance. Lets the provider point at "next pending".
    current_step  INT NOT NULL DEFAULT 0,

    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mem_plan_steps (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    plan_id         UUID NOT NULL REFERENCES mem_plans(id) ON DELETE CASCADE,

    -- Position in the ordered checklist (0-based).
    idx             INT NOT NULL,

    title           TEXT NOT NULL,
    detail          TEXT NOT NULL DEFAULT '',

    -- Step lifecycle. blocked = verification failed / awaiting replan.
    status          TEXT NOT NULL DEFAULT 'pending',  -- pending|in_progress|blocked|done|failed|skipped

    -- A checkpoint step pauses for the boss: when the plan reaches it the
    -- agent surfaces it to the dashboard (surface='plan_checkpoints') for an
    -- explicit Approve / Skip before continuing.
    is_checkpoint   BOOLEAN NOT NULL DEFAULT FALSE,

    -- Self-verification. When verify_required, the step is only 'done' once
    -- plan_verify records a passing verdict; a failing verdict flips it to
    -- 'blocked' and triggers a replan. verify_result holds
    -- {verdict, evidence, method, at}.
    verify_required BOOLEAN NOT NULL DEFAULT FALSE,
    verify_result   JSONB,

    -- Short human-readable narrative of how the step went (mirrors the
    -- "runs carry a narrative" rule). Shown in the step timeline.
    result_summary  TEXT NOT NULL DEFAULT '',

    -- Links to the mem_runs row booked when this step executes, so the UI
    -- shows a navigation-proof live spinner per in-flight step.
    run_id          UUID,

    started_at      TIMESTAMPTZ,
    ended_at        TIMESTAMPTZ,

    UNIQUE (plan_id, idx)
);

-- The dashboard / provider read path: "the active plan for this session."
CREATE INDEX IF NOT EXISTS idx_mem_plans_session_status
    ON mem_plans (session_id, status, updated_at DESC);

-- "Everything active" - the /plans page + nav badge aggregate read.
CREATE INDEX IF NOT EXISTS idx_mem_plans_active
    ON mem_plans (updated_at DESC)
    WHERE status IN ('active', 'paused');

-- Steps for a plan, in order.
CREATE INDEX IF NOT EXISTS idx_mem_plan_steps_plan_idx
    ON mem_plan_steps (plan_id, idx);

-- Realtime: Studio subscribes so plans + steps live-update without poll.
-- Matches 016_surface_items.sql.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_plans'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_plans';
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_plan_steps'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_plan_steps';
        END IF;
    END IF;
END $$;

-- RLS read policies for the authenticated role. 083 looped over the
-- publication at the time it ran, so tables added afterward (these two) need
-- their own policy or realtime silently drops every change event before it
-- reaches the browser. Single-user safe: authenticated == boss; Core (owner)
-- bypasses RLS for its own reads/writes via pgx. Idempotent.
DO $$
DECLARE
    t TEXT;
    pol_name TEXT;
BEGIN
    FOREACH t IN ARRAY ARRAY['mem_plans', 'mem_plan_steps'] LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY', t);
        pol_name := t || '_realtime_authenticated_read';
        EXECUTE format('DROP POLICY IF EXISTS %I ON public.%I', pol_name, t);
        EXECUTE format(
            'CREATE POLICY %I ON public.%I FOR SELECT TO authenticated USING (true)',
            pol_name, t
        );
    END LOOP;
END $$;

COMMIT;
