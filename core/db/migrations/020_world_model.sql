-- 020_world_model.sql — the world model + agent-owned goals.
--
-- Phase 5 of the assembly substrate. Honcho models the BOSS; mem_memories
-- is episodic recall. Neither is a structured model of the boss's WORLD —
-- the people, projects, accounts, and threads the agent acts on, and their
-- current state. And mem_pursuits is the boss's dashboard goals; the agent
-- had no durable goals of its OWN.
--
-- Three tables:
--   mem_entities      — the nodes of the world model (person/project/…)
--   mem_entity_links  — typed edges between entities
--   mem_agent_goals   — the agent's own objectives, each with a living plan
--
-- The agent reads/writes via the entity_* and goal_* tools — never raw SQL.

BEGIN;

-- ── mem_entities — the nodes of the world model ───────────────────────────
CREATE TABLE IF NOT EXISTS mem_entities (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    -- person | project | account | org | thread | commitment | place | … —
    -- free-form so the agent can model a new kind of thing without a
    -- migration.
    kind         TEXT NOT NULL,
    name         TEXT NOT NULL,                       -- canonical name
    aliases      JSONB NOT NULL DEFAULT '[]'::jsonb,  -- for resolution
    -- Arbitrary structured facts: {role, email, status, repo, …}.
    attributes   JSONB NOT NULL DEFAULT '{}'::jsonb,
    summary      TEXT NOT NULL DEFAULT '',            -- one-paragraph current state
    status       TEXT NOT NULL DEFAULT 'active',      -- active | dormant | archived
    -- 0-100: how central this entity is to the boss's world right now.
    salience     SMALLINT NOT NULL DEFAULT 50,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (kind, name)
);

CREATE INDEX IF NOT EXISTS idx_mem_entities_kind_salience
    ON mem_entities (kind, salience DESC)
    WHERE status = 'active';
CREATE INDEX IF NOT EXISTS idx_mem_entities_name
    ON mem_entities (lower(name));

-- ── mem_entity_links — typed edges between entities ───────────────────────
CREATE TABLE IF NOT EXISTS mem_entity_links (
    id         UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    from_id    UUID NOT NULL REFERENCES mem_entities(id) ON DELETE CASCADE,
    to_id      UUID NOT NULL REFERENCES mem_entities(id) ON DELETE CASCADE,
    -- works_on | reports_to | belongs_to | blocked_by | collaborates_with | …
    relation   TEXT NOT NULL,
    note       TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (from_id, to_id, relation)
);

CREATE INDEX IF NOT EXISTS idx_mem_entity_links_from ON mem_entity_links (from_id);
CREATE INDEX IF NOT EXISTS idx_mem_entity_links_to   ON mem_entity_links (to_id);

-- ── mem_agent_goals — the agent's own objectives ──────────────────────────
CREATE TABLE IF NOT EXISTS mem_agent_goals (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title        TEXT NOT NULL,
    description  TEXT NOT NULL DEFAULT '',
    status       TEXT NOT NULL DEFAULT 'active',   -- active | blocked | done | abandoned
    priority     TEXT NOT NULL DEFAULT 'med',      -- low | med | high
    -- The agent's CURRENT plan — a JSONB array of { step, done }. Rewritten
    -- as the agent re-plans; it is a living document, not an audit log.
    plan         JSONB NOT NULL DEFAULT '[]'::jsonb,
    -- A running narrative of progress — appended to as work happens.
    progress     TEXT NOT NULL DEFAULT '',
    -- What's blocking it, when status = 'blocked'.
    blocker      TEXT NOT NULL DEFAULT '',
    -- Optional link to a world-model entity this goal is about.
    entity_id    UUID REFERENCES mem_entities(id) ON DELETE SET NULL,
    due_at       TIMESTAMPTZ,
    -- The autonomous-pursuit loop reads this: a goal not touched in a while
    -- gets resurfaced by the heartbeat so the agent revisits it.
    last_progress_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_agent_goals_active
    ON mem_agent_goals (priority, due_at NULLS LAST)
    WHERE status IN ('active', 'blocked');

-- Realtime: Studio subscribes so the world model + goals live-update.
DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        FOREACH tname IN ARRAY ARRAY[
            'mem_entities', 'mem_entity_links', 'mem_agent_goals'
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
