-- 014_dashboard.sql — Dashboard substrate + PWA push subscriptions.
--
-- Lands the storage for the new root surface (Dashboard) and the iOS/macOS
-- notification pipeline that surfaces actionable items on the boss's phone
-- and dock.
--
-- Surfaces introduced:
--   mem_tasks              — todos (manual / agent-created / email-derived /
--                            cron-output)
--   mem_pursuits           — habits, weekly cadences, and long-term goals
--                            merged into one entity with a cadence tag
--   mem_pursuit_checkins   — habit-history rows; one per check-in event
--   mem_followups          — connector-surfaced messages awaiting reply
--                            (Gmail, Slack, iMessage, Linear, …)
--   mem_saved              — articles, links, notes, quotes the boss stashed
--   mem_calendar_events    — events ingested from a calendar connector with
--                            agent-classified type + agent-flagged prep
--   mem_push_subscriptions — Web Push endpoints per installed device
--
-- And the seeded-session column that lets a dashboard tap open `/live` with
-- the source artifact pre-hydrated into the system prompt:
--   ALTER TABLE mem_sessions ADD COLUMN seeded_from JSONB
--
-- Nothing here is auto-mutated by Core yet. The Studio Dashboard reads
-- mock fixtures until the agent's native tools (task_*, pursuit_*,
-- followup_*) land. This migration unblocks that wiring.

BEGIN;

-- ── mem_tasks ──────────────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS mem_tasks (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- Free-text description ("Call insurance about claim").
    title        TEXT NOT NULL,

    -- Optional longer body / notes. Used when the boss expands the
    -- ObjectViewer for the task — most rows leave it empty.
    body         TEXT NOT NULL DEFAULT '',

    -- Where this task came from. agent → Jarvis decided to file it;
    -- manual → boss typed it; email/slack/imessage/etc → derived from
    -- a connector message; cron → produced as the output of a scheduled
    -- run.
    source       TEXT NOT NULL DEFAULT 'manual',  -- manual|agent|email|slack|imessage|cron

    -- Optional pointer to the source artifact (a mem_followups row,
    -- a cron run id, etc). Free-form so future sources don't need
    -- another column.
    source_ref   JSONB NOT NULL DEFAULT '{}'::jsonb,

    priority     TEXT NOT NULL DEFAULT 'med',     -- low|med|high
    status       TEXT NOT NULL DEFAULT 'open',    -- open|done|dropped

    due_at       TIMESTAMPTZ,
    done_at      TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_tasks_status_due
    ON mem_tasks(status, due_at NULLS LAST);
CREATE INDEX IF NOT EXISTS idx_mem_tasks_source
    ON mem_tasks(source);

-- ── mem_pursuits ───────────────────────────────────────────────────────────
-- Pursuits unify habits, weekly cadences, and long-term goals. A cadence
-- tag distinguishes them. Habits use `streak_days` + checkin rows; goals
-- use `target_value` / `current_value` for progress; quarterly objectives
-- use `target_value` with a `due_at` deadline.
CREATE TABLE IF NOT EXISTS mem_pursuits (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           TEXT NOT NULL,

    cadence         TEXT NOT NULL DEFAULT 'daily', -- daily|weekly|goal|quarterly

    -- Goals: progress numerics. Habits leave these null and use the
    -- checkin table + streak_days instead.
    current_value   NUMERIC,
    target_value    NUMERIC,
    unit            TEXT,                          -- "books", "%", "lbs", "sessions"

    -- Pre-computed by a trigger or nightly job from mem_pursuit_checkins.
    -- Stored denormalized so /api/dashboard returns in one query.
    streak_days     INT NOT NULL DEFAULT 0,
    done_today      BOOLEAN NOT NULL DEFAULT FALSE,
    done_at         TIMESTAMPTZ,

    -- Goals only: when the target should be hit.
    due_at          TIMESTAMPTZ,

    -- Agent-classified progress vibe. NULL for habits.
    status          TEXT,                          -- on_track|slow|at_risk|ahead

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuits_cadence
    ON mem_pursuits(cadence);

-- ── mem_pursuit_checkins ───────────────────────────────────────────────────
-- One row per "today I did this." Daily habit toggles, weekly cadence
-- ticks, goal progress increments — all land here. Sorted by checked_at
-- to compute streaks deterministically. Idempotent on (pursuit_id, day)
-- so re-toggles don't duplicate.
CREATE TABLE IF NOT EXISTS mem_pursuit_checkins (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id   UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    -- The intentional day this check-in counts for (00:00 local).
    -- Lets us upsert by (pursuit_id, day) without duplicates if the
    -- boss double-taps the checkbox.
    day          DATE NOT NULL,
    checked_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Optional delta for progress-style pursuits. NULL for habits.
    delta        NUMERIC,
    note         TEXT NOT NULL DEFAULT '',

    UNIQUE (pursuit_id, day)
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_checkins_pursuit_checked
    ON mem_pursuit_checkins(pursuit_id, checked_at DESC);

-- ── mem_followups ──────────────────────────────────────────────────────────
-- Connector-surfaced items where a human (or Linear/etc) is waiting on
-- the boss. The agent decides which inbound messages get promoted into
-- this table — it's the actionable subset, not a mirror of every email.
CREATE TABLE IF NOT EXISTS mem_followups (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- gmail|slack|imessage|linear|other — drives the icon + display
    -- conventions in Studio.
    source       TEXT NOT NULL DEFAULT 'gmail',

    -- For multi-account setups: which mailbox / workspace surfaced
    -- this. Free-text so future connectors can plug in.
    account      TEXT NOT NULL DEFAULT '',

    -- Display fields surfaced in the FollowUps card preview row.
    from_name    TEXT NOT NULL DEFAULT '',
    subject      TEXT NOT NULL DEFAULT '',
    preview      TEXT NOT NULL DEFAULT '',

    -- Full content surfaced in the ObjectViewer — the "preview before
    -- discuss" surface relies on this being the actual artifact, not
    -- a summary.
    body         TEXT NOT NULL DEFAULT '',
    thread_url   TEXT NOT NULL DEFAULT '',

    -- Stable pointer back to the source system (Gmail messageId, Slack
    -- ts, Linear identifier, etc). Lets the agent skip re-surfacing
    -- on re-poll.
    source_ref   JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- Optional draft Jarvis has already started.
    draft        TEXT NOT NULL DEFAULT '',

    -- open → on the dashboard. snoozed → hidden until snoozed_until.
    -- done → user replied / dismissed.
    status        TEXT NOT NULL DEFAULT 'open',
    unread        BOOLEAN NOT NULL DEFAULT TRUE,

    received_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    snoozed_until TIMESTAMPTZ,
    decided_at    TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mem_followups_status_received
    ON mem_followups(status, received_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_followups_source
    ON mem_followups(source);

-- ── mem_saved ──────────────────────────────────────────────────────────────
-- Articles to read, links to revisit, quotes to remember, notes to keep
-- in working memory. Tile shelf on the Dashboard reads here.
CREATE TABLE IF NOT EXISTS mem_saved (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    kind         TEXT NOT NULL DEFAULT 'article', -- article|link|note|quote

    title        TEXT NOT NULL DEFAULT '',
    body         TEXT NOT NULL DEFAULT '',
    url          TEXT,
    source_label TEXT,                            -- "deepmind.google", "essay · gwern.net"

    -- Estimated reading time for articles. NULL for notes / quotes.
    reading_minutes INT,

    -- Tags, future-proofed as JSONB so the agent can carry classification.
    tags         JSONB NOT NULL DEFAULT '[]'::jsonb,

    saved_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    read_at      TIMESTAMPTZ
);

CREATE INDEX IF NOT EXISTS idx_mem_saved_saved_at
    ON mem_saved(saved_at DESC);

-- ── mem_calendar_events ────────────────────────────────────────────────────
-- Calendar items from a future connector (Google Cal / iCloud). The agent
-- classifies each event (meeting / concert / flight / dinner / ...) and
-- attaches a prep checklist that surfaces on the Dashboard Upcoming card.
CREATE TABLE IF NOT EXISTS mem_calendar_events (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    title           TEXT NOT NULL,
    location        TEXT NOT NULL DEFAULT '',
    attendees       JSONB NOT NULL DEFAULT '[]'::jsonb,

    starts_at       TIMESTAMPTZ NOT NULL,
    ends_at         TIMESTAMPTZ,
    all_day         BOOLEAN NOT NULL DEFAULT FALSE,

    -- Agent classification — drives the icon + the prep template choice.
    -- Free-text so the agent can invent new categories (per the boss's
    -- "schema should grow dynamically" direction in the design thread).
    classification  TEXT NOT NULL DEFAULT 'other',

    -- Prep items as a JSONB array of { id, label, done, rationale }.
    -- Stored on the event row (not a separate table) because prep is
    -- always read-with-event and never queried in isolation.
    prep            JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Source-of-truth pointer back to the calendar connector + remote ID.
    source          TEXT NOT NULL DEFAULT '',     -- "google_calendar", "icloud"
    source_ref      JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_calendar_events_starts_at
    ON mem_calendar_events(starts_at);
CREATE INDEX IF NOT EXISTS idx_mem_calendar_events_classification
    ON mem_calendar_events(classification);

-- ── mem_push_subscriptions ─────────────────────────────────────────────────
-- One row per installed PWA / browser that's opted into Web Push.
-- Endpoint is the unique identifier returned by PushManager.subscribe.
CREATE TABLE IF NOT EXISTS mem_push_subscriptions (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- The push endpoint URL (Apple / FCM). Unique per device.
    endpoint     TEXT NOT NULL UNIQUE,

    -- Crypto material the browser issued at subscribe time. Both fields
    -- are url-safe base64 strings. Required for webpush encryption.
    p256dh       TEXT NOT NULL,
    auth_key     TEXT NOT NULL,

    -- Human label for the Notifications settings screen. Defaults to
    -- "Safari on iOS" / "Chrome on macOS" derived from the user agent
    -- string; the boss can rename it.
    label        TEXT NOT NULL DEFAULT '',
    user_agent   TEXT NOT NULL DEFAULT '',

    -- Set true after the first 410 Gone from the push service so we
    -- stop trying — the boss can clean up via Settings.
    revoked      BOOLEAN NOT NULL DEFAULT FALSE,

    last_seen_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_push_subscriptions_active
    ON mem_push_subscriptions(revoked) WHERE NOT revoked;

-- ── mem_sessions.seeded_from ───────────────────────────────────────────────
-- Adds the discriminated-union pointer used by the seeded-session pattern:
-- tapping a dashboard item opens /live with this column set to
-- {"kind": "followup", "id": "..."} so the system prompt is built with
-- the source artifact hydrated as a Context Block.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = 'public'
           AND table_name = 'mem_sessions'
           AND column_name = 'seeded_from'
    ) THEN
        ALTER TABLE mem_sessions
            ADD COLUMN seeded_from JSONB;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_mem_sessions_seeded_from_kind
    ON mem_sessions((seeded_from->>'kind'))
    WHERE seeded_from IS NOT NULL;

-- ── Realtime publication (Supabase) ────────────────────────────────────────
-- Studio subscribes to each table so dashboard cards live-update without
-- polling. Matches the pattern in 008_realtime.sql and 010_code_proposals.sql.
DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) THEN
        FOREACH tname IN ARRAY ARRAY[
            'mem_tasks',
            'mem_pursuits',
            'mem_pursuit_checkins',
            'mem_followups',
            'mem_saved',
            'mem_calendar_events',
            'mem_push_subscriptions'
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
