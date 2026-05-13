-- 012_openai_oauth.sql — Persist OAuth credentials for ChatGPT-subscription
-- inference (the `openai_oauth` LLM provider).
--
-- Two tables:
--   • mem_oauth_sessions  — short-lived PKCE verifier + state pairs created
--     when the user clicks "Connect ChatGPT" in Studio. Looked up by `state`
--     when the user pastes back the callback code. TTL-pruned (~15min).
--   • mem_provider_tokens — long-lived access/refresh token pairs keyed by
--     (provider, account_id). Refreshed in-place by the background worker
--     before expiry. The latest refresh_token replaces the prior one on
--     rotation (OpenAI rotates on every refresh).
--
-- Both tables are intentionally minimal — we don't store id_token claims
-- beyond what's needed to address the account (email + sub). Anything richer
-- (display name, avatar, etc.) we fetch on demand from the API rather than
-- cache here, so a corrupted row never carries stale identity.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_oauth_sessions (
    state           TEXT PRIMARY KEY,
    provider        TEXT NOT NULL,
    code_verifier   TEXT NOT NULL,
    redirect_uri    TEXT NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    expires_at      TIMESTAMPTZ NOT NULL DEFAULT (now() + interval '15 minutes')
);

CREATE INDEX IF NOT EXISTS idx_mem_oauth_sessions_expires
    ON mem_oauth_sessions (expires_at);

CREATE TABLE IF NOT EXISTS mem_provider_tokens (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    provider        TEXT NOT NULL,
    account_id      TEXT NOT NULL DEFAULT '',
    account_email   TEXT NOT NULL DEFAULT '',
    access_token    TEXT NOT NULL,
    refresh_token   TEXT NOT NULL DEFAULT '',
    id_token        TEXT NOT NULL DEFAULT '',
    token_type      TEXT NOT NULL DEFAULT 'Bearer',
    scope           TEXT NOT NULL DEFAULT '',
    expires_at      TIMESTAMPTZ,
    last_refreshed  TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (provider, account_id)
);

CREATE INDEX IF NOT EXISTS idx_mem_provider_tokens_provider
    ON mem_provider_tokens (provider);

-- Touch updated_at on every UPDATE so the refresh worker's bookkeeping
-- shows up in Studio's "last refreshed" badge without a separate column.
CREATE OR REPLACE FUNCTION mem_provider_tokens_touch()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at := now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_mem_provider_tokens_touch ON mem_provider_tokens;
CREATE TRIGGER trg_mem_provider_tokens_touch
    BEFORE UPDATE ON mem_provider_tokens
    FOR EACH ROW EXECUTE FUNCTION mem_provider_tokens_touch();

COMMIT;
