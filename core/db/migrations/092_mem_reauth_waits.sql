-- 092_mem_reauth_waits.sql — park-and-resume substrate for model re-auth.
--
-- Why (2026-06-01): the boss's active brain (Studio Settings → openai_oauth /
-- gpt-5.4) hit a 401 token_revoked and the turn just hard-errored with a raw
-- 401 dumped into chat. His ask: "we should be notified it needs a reauth or
-- refresh, in the chat convo, so I can fix it quickly, and then when it comes
-- back online it sends the turn." Provider-agnostic — same for Anthropic key,
-- ChatGPT OAuth, Google, whatever the active model is.
--
-- The shape mirrors mem_watches (086): one generic durable contract, polled by
-- a Go ticker (deterministic code, not LLM cognition). When the agent loop hits
-- an auth failure it PARKS the turn here and surfaces a reconnect message into
-- the chat; the reauth poller probes the active brain's health and, the moment
-- it works again (boss re-authed OR switched to a healthy model), REPLAYS the
-- turn and delivers the answer into the same session. Durable across restarts.
--
-- One active wait per session (a session has at most one in-flight turn).
-- Idempotent: CREATE IF NOT EXISTS + guarded indexes.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_reauth_waits (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id    UUID NOT NULL,                    -- where the reconnect message + replayed answer land
    provider      TEXT NOT NULL,                    -- the brain that failed (openai_oauth | anthropic | google | …)
    model         TEXT NOT NULL DEFAULT '',         -- active model id at park time (diagnostic)
    user_text     TEXT NOT NULL DEFAULT '',         -- the turn to replay once the credential is healthy
    reason        TEXT NOT NULL DEFAULT '',         -- trimmed provider error that triggered the park
    status        TEXT NOT NULL DEFAULT 'waiting'   -- 'waiting' | 'resumed' | 'expired' | 'cancelled'
                  CHECK (status IN ('waiting','resumed','expired','cancelled')),
    created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    next_probe_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resumed_at    TIMESTAMPTZ,
    expires_at    TIMESTAMPTZ NOT NULL DEFAULT NOW() + INTERVAL '24 hours'
);

-- At most one waiting turn per session — a fresh failure refreshes it.
CREATE UNIQUE INDEX IF NOT EXISTS uq_mem_reauth_waits_session
    ON mem_reauth_waits(session_id) WHERE status = 'waiting';

-- Hot query: the poller pulls due, still-waiting rows each tick.
CREATE INDEX IF NOT EXISTS idx_mem_reauth_waits_due
    ON mem_reauth_waits(next_probe_at) WHERE status = 'waiting';

INSERT INTO infinity_meta (key, value)
VALUES ('mem_reauth_waits_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
