-- 129_mandates.sql — the Mandate: a per-task DEFINITION OF DONE.
--
-- "Goal-driven execution, loop until verified" was an operating rule with no
-- structure behind it — the agent decided when it was "done" by vibes. A
-- Mandate makes "done" a contract: a title, a short summary, and a list of
-- BINARY, testable acceptance criteria. The agent opens one for non-trivial
-- work, checks each criterion off with evidence as it satisfies it, and CANNOT
-- close the mandate until every criterion passes — that gate is enforced in Go
-- (mandate.Store.Close), not in skill prose, so it can't be forgotten.
--
-- high_stakes mandates additionally require a passing Crosscheck (a second LLM
-- vendor auditing the result) before they can close. The Crosscheck verdict
-- lands on the criteria (status + evidence) and as a mem_runs row.
--
-- criteria JSONB shape mirrors mem_agent_goals.plan:
--   [{ "id": "c1", "text": "...", "status": "pending|pass|fail", "evidence": "..." }]

BEGIN;

CREATE TABLE IF NOT EXISTS mem_mandates (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- The session that opened it (session-scoped provider injects the open one).
    -- Nullable so a cron / heartbeat mandate without a chat session is valid.
    session_id  UUID,
    title       TEXT NOT NULL,
    summary     TEXT NOT NULL DEFAULT '',
    -- open → being worked; verifying → crosscheck in flight; done → all criteria
    -- pass (and crosscheck passed when high_stakes); abandoned → dropped.
    status      TEXT NOT NULL DEFAULT 'open'
                CHECK (status IN ('open','verifying','done','abandoned')),
    -- The binary acceptance criteria — the heart of the contract.
    criteria    JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- When true, Close additionally requires a passing crosscheck verdict.
    high_stakes BOOLEAN NOT NULL DEFAULT FALSE,
    importance  SMALLINT,
    source      TEXT NOT NULL DEFAULT 'agent',  -- agent | cron | heartbeat
    -- Set when the most recent crosscheck passed (clears done-gate for
    -- high_stakes). NULL until a passing audit lands.
    verified_at TIMESTAMPTZ,
    -- The auditing vendor + overall verdict of the last crosscheck, for the UI.
    crosscheck  JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_mandates_session_open
    ON mem_mandates (session_id, updated_at DESC)
    WHERE status IN ('open','verifying');
CREATE INDEX IF NOT EXISTS idx_mem_mandates_recent
    ON mem_mandates (updated_at DESC);

-- Realtime: Studio's dashboard Mandates card + Memory history live-update.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_mandates'
        ) THEN
            ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_mandates;
        END IF;
    END IF;
END $$;

COMMIT;
