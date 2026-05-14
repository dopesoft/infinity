-- 016_surface_items.sql — the generic dashboard SURFACE CONTRACT.
--
-- Rule #1 substrate. Instead of a bespoke table + bespoke Go scorer +
-- bespoke widget per source (the followup_scoring.go anti-pattern this
-- migration replaces), ANY producer — a skill recipe, a connector poll,
-- a cron, the agent mid-conversation — writes ranked, structured items
-- through ONE contract. Studio renders them generically by `surface`
-- and `kind`; a new capability lands on the dashboard with zero new
-- table, zero new loader, zero new widget.
--
-- The agent NEVER writes this table with raw SQL. The `surface_item`
-- native tool IS the contract — that is the boundary the LLM assembles
-- against.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_surface_items (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- WHICH region of the dashboard this belongs to. Free-form so the
    -- agent can invent new surfaces ('followups', 'alerts', 'digest',
    -- 'insights', 'briefing', …) without a migration. Studio groups by
    -- this and renders a generic section for any surface it has no
    -- bespoke header for.
    surface           TEXT NOT NULL,

    -- SEMANTIC type — drives the icon + render hint in the generic card
    -- ('email', 'message', 'alert', 'article', 'metric', 'event',
    -- 'task', 'finding', …). Also free-form.
    kind              TEXT NOT NULL DEFAULT 'item',

    -- WHO produced it: a skill name, a connector slug, a cron name, or
    -- 'agent' for mid-conversation surfacing. Provenance + half of the
    -- dedup key.
    source            TEXT NOT NULL DEFAULT 'agent',

    -- Stable id from the producing system (gmail message id, slack ts,
    -- linear identifier, …). Lets a re-run upsert instead of duplicate.
    external_id       TEXT,

    -- Display payload. title is required; the rest are optional.
    title             TEXT NOT NULL,
    subtitle          TEXT NOT NULL DEFAULT '',
    body              TEXT NOT NULL DEFAULT '',
    url               TEXT,

    -- Ranking. NULL = unranked (a producer dropped it without judgment).
    -- 0-100 once a ranking pass has scored it. Studio sorts importance
    -- DESC NULLS LAST so ranked-important floats to the top.
    importance        SMALLINT,
    importance_reason TEXT NOT NULL DEFAULT '',

    -- The actual data contract: arbitrary structured payload the producer
    -- fills and the ObjectViewer / downstream skills read. e.g.
    -- {"from":"…","thread_url":"…","draft":"…","attachments":[…]}
    metadata          JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Lifecycle. open → visible. snoozed → hidden until snoozed_until.
    -- done/dismissed → handled, kept for history + provenance.
    status            TEXT NOT NULL DEFAULT 'open',  -- open|snoozed|done|dismissed
    snoozed_until     TIMESTAMPTZ,

    -- Optional TTL for ephemera (a 'digest' item stale by tomorrow). The
    -- nightly consolidate sweep purges expired open rows.
    expires_at        TIMESTAMPTZ,

    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    scored_at         TIMESTAMPTZ,
    decided_at        TIMESTAMPTZ
);

-- Dedup: a producer re-running its recipe upserts by (source, external_id)
-- instead of piling up duplicates. Partial — only constrains rows that
-- actually carry an external_id, so agent-surfaced one-offs (no external
-- id) are never blocked.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mem_surface_items_source_external
    ON mem_surface_items (source, external_id)
    WHERE external_id IS NOT NULL;

-- The dashboard read path: "open items for surface X, ranked."
CREATE INDEX IF NOT EXISTS idx_mem_surface_items_surface_status_rank
    ON mem_surface_items (surface, status, importance DESC NULLS LAST, created_at DESC);

-- "Everything open across all surfaces" — the dashboard aggregate read.
CREATE INDEX IF NOT EXISTS idx_mem_surface_items_open
    ON mem_surface_items (created_at DESC)
    WHERE status = 'open';

-- The expiry sweep run by the nightly consolidate job.
CREATE INDEX IF NOT EXISTS idx_mem_surface_items_expires
    ON mem_surface_items (expires_at)
    WHERE expires_at IS NOT NULL AND status = 'open';

-- Realtime: Studio subscribes so surfaced items live-update without poll.
-- Matches the pattern in 008_realtime.sql / 010_code_proposals.sql / 014.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_surface_items'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_surface_items';
        END IF;
    END IF;
END $$;

COMMIT;
