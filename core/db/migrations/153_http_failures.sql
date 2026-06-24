-- 153_http_failures.sql — universal outbound-HTTP failure ledger.
--
-- Why this exists (the 2026-06-24 incident): a Composio v3 auth change made
-- EVERY outbound call 401, which silently zeroed inbox triage — the run reported
-- a green "no new mail" for days because the failing call's error was swallowed
-- by the caller. The boss's law: "Jarvis MUST ALWAYS see errors he gets — no
-- false greens that were really 401s, 404s, etc."
--
-- This table is the capture half of that guarantee. An instrumented
-- http.RoundTripper (core/internal/httpx) records EVERY response with status
-- >= 400 and every transport error here, for ANY outbound client that uses the
-- default transport — no per-vendor wiring. The surfacing half lives in the cron
-- outcome classifier: a run whose session logged a hard error (401/403/407/429/
-- 5xx) can no longer be classified "green", so it flows into the existing
-- cron_failure → code-proposal / self-improve backlog like any other failure.
--
-- Generic + bounded: one row per failed request, pruned by nightly cognition.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_http_failures (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    occurred_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    session_id  TEXT,                       -- run/session this call belonged to (best-effort, via request ctx)
    subsystem   TEXT,                       -- instrumented client name (e.g. 'default')
    method      TEXT NOT NULL DEFAULT '',
    host        TEXT NOT NULL DEFAULT '',   -- e.g. backend.composio.dev
    path        TEXT NOT NULL DEFAULT '',
    status      INT  NOT NULL DEFAULT 0,    -- HTTP status; 0 = transport error (no response)
    error       TEXT                        -- transport error text when status = 0
);

-- Hot query: the outcome classifier pulls failures for one session at run end.
CREATE INDEX IF NOT EXISTS idx_mem_http_failures_session
    ON mem_http_failures(session_id);
-- Pruning / time-window scans.
CREATE INDEX IF NOT EXISTS idx_mem_http_failures_occurred
    ON mem_http_failures(occurred_at);

COMMIT;
