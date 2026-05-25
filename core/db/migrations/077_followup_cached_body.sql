-- 077_followup_cached_body.sql - durable email body so opening a follow-up
-- never depends on a live connector.
--
-- Why this exists: the full HTML/plain-text email body was fetched LAZILY at
-- open time from the connector (Gmail via Composio). That made the viewer
-- hostage to the connector's liveness - the moment an account went REVOKED /
-- EXPIRED (admin revoke, token expiry, OAuth scope change), opening an old
-- follow-up showed nothing (or, worse, fell back to the list-chip subtext)
-- even though the email had been readable seconds earlier. Bad experience:
-- "show me the email" must keep working regardless of connector state.
--
-- The fix is capture-once-then-serve: the FIRST successful fetch persists the
-- rendered body here, and every subsequent open serves it straight from the
-- database with no live call. Email bodies are immutable, so a cache is
-- always correct, and it makes the read instant AND revocation-proof.
--
-- Dedicated columns (not metadata jsonb) on purpose: the dashboard list query
-- pulls metadata wholesale, and an HTML email is 50-200KB - storing it in
-- metadata would bloat every list load. These columns are SELECTed only by the
-- single-message endpoint, never the list, so the list stays lean.
--
-- Nullable, no default, no backfill: zero data loss, and existing rows simply
-- cache on their next successful open.

ALTER TABLE mem_surface_items
  ADD COLUMN IF NOT EXISTS cached_html text,
  ADD COLUMN IF NOT EXISTS cached_text text;

ALTER TABLE mem_followups
  ADD COLUMN IF NOT EXISTS cached_html text,
  ADD COLUMN IF NOT EXISTS cached_text text;
