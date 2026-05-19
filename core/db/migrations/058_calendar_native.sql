-- 058_calendar_native.sql - native Google Calendar sync substrate.
--
-- Rule #1a building block. The existing Composio connector_poller can
-- write into mem_calendar_events via GOOGLECALENDAR_EVENTS_LIST, but
-- that path costs an LLM-routed action per tick, has no incremental
-- sync, and can't surface invite-response state. This migration adds:
--
--   1. Extra display + action columns on mem_calendar_events
--      (description, html_link, organizer, response_status,
--       hangout_link, conference, status, all_day flags). Additive,
--      backwards compatible with anything already polling into it.
--
--   2. mem_calendar_sync_state: per-account cursor for Google's
--      events.list?syncToken pattern. One row per (account_id,
--      calendar_id). The native ticker reads sync_token, calls
--      events.list, upserts events keyed by (source='google_calendar',
--      source_ref->>'remote_id'), and stores the nextSyncToken back.
--      On a 410 GONE we wipe the token and do a full re-sync.
--
-- The agent NEVER writes mem_calendar_sync_state directly. The
-- core/internal/calendar package owns it; agent tools call into that
-- package which calls Google. One shape for everything, per Rule #1.

BEGIN;

-- ── mem_calendar_events column extensions ────────────────────────────────

-- Composio connected_account id ("ca_xxx") that owns this event.
-- Lets one boss have multiple Google Calendar accounts (work/personal)
-- and keep their events deduped per-account, not merged into one bag.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS account_id TEXT;

-- Google's event status: 'confirmed' (normal), 'tentative' (organizer
-- marked it tentative), 'cancelled' (delete fanned out via sync token).
-- The sync loop hard-deletes 'cancelled' rows it owns; storing the field
-- explicitly lets us also surface "the organizer cancelled" as a state
-- without immediate deletion if we ever want a "recently cancelled" view.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'confirmed';

-- Full text body from the calendar event. Rendered in the modal under
-- a "Show all" expander matching the attached design.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

-- Direct deep-link to the event in Google Calendar UI ("Open in Google").
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS html_link TEXT;

-- Google Meet / Hangout video link (also surfaced in conference). Kept
-- as its own column because it's the most-clicked field on a meeting.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS hangout_link TEXT;

-- Generic conference data: { entry_points: [{type, uri, label}],
-- solution: { name, icon_uri } }. Holds Zoom / Teams / generic
-- video-conference info parsed from Google's conferenceData field.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS conference JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Organizer details: { email, display_name, self }.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS organizer JSONB NOT NULL DEFAULT '{}'::jsonb;

-- Boss's own response status on this event. One of:
--   'accepted' | 'declined' | 'tentative' | 'needsAction'
-- NULL when the boss is the organizer (no self-response).
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS response_status TEXT;

-- Recurrence summary. Either the original RRULE strings ["RRULE:FREQ=..."]
-- or a human-readable summary the sync layer derived ("Monthly on the
-- first Thursday"). Stored as JSONB to keep both shapes.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS recurrence JSONB NOT NULL DEFAULT '[]'::jsonb;

-- Etag from Google. Used for optimistic concurrency on PATCH calls so
-- we don't clobber a concurrent edit from another client.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS remote_etag TEXT;

-- The Google calendarId this event belongs to. Defaults to 'primary'.
-- Stored so we can scope a patch/respond call back to the right cal.
ALTER TABLE mem_calendar_events
    ADD COLUMN IF NOT EXISTS calendar_id TEXT NOT NULL DEFAULT 'primary';

-- Existing attendees column is JSONB (list of email strings). The native
-- sync needs richer per-attendee shape: { email, display_name, response,
-- self, organizer, optional }. We migrate IN PLACE - existing string
-- arrays remain readable; the new sync writer always emits objects. The
-- TS reader handles both shapes. No data loss.

-- ── per-account sync cursor ──────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS mem_calendar_sync_state (
    account_id       TEXT NOT NULL,
    calendar_id      TEXT NOT NULL DEFAULT 'primary',

    -- Google's nextSyncToken from the last successful events.list call.
    -- Pass it back on the next tick to receive only deltas. NULL means
    -- "do a full sync on the next tick" (fresh account or 410 recovery).
    sync_token       TEXT,

    -- When the last full (non-incremental) sync completed. Used for
    -- "your calendar last synced at X" UI and to force a periodic
    -- full re-sync (every 30d) to prune anything we missed.
    last_full_sync   TIMESTAMPTZ,

    -- Bookkeeping for the Studio "last sync" indicator.
    last_tick_at     TIMESTAMPTZ,
    last_error       TEXT NOT NULL DEFAULT '',
    events_seen      INT  NOT NULL DEFAULT 0,

    created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    PRIMARY KEY (account_id, calendar_id)
);

-- Read pattern: "give me every account that's due for a sync" -> ticker
-- orders by last_tick_at ASC NULLS FIRST and picks the staleest.
CREATE INDEX IF NOT EXISTS idx_mem_calendar_sync_state_due
    ON mem_calendar_sync_state (last_tick_at NULLS FIRST);

-- ── realtime publication ─────────────────────────────────────────────────
-- Studio subscribes to mem_calendar_events so an inbound sync tick
-- (new event from an inviter, attendee responded, organizer cancelled)
-- live-updates the Upcoming card and the open event modal without
-- waiting for the next dashboard refetch.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_calendar_events'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_calendar_events';
        END IF;
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_calendar_sync_state'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_calendar_sync_state';
        END IF;
    END IF;
END $$;

COMMIT;
