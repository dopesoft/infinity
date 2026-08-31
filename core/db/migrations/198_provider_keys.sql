-- 198: mem_provider_keys - API keys the boss pastes into Studio.
--
-- WHY
--
-- Every LLM vendor credential was read from the process environment at boot
-- (ANTHROPIC_API_KEY / OPENAI_API_KEY / GOOGLE_API_KEY). That made adding a
-- vendor a deploy: set a Railway variable, wait for a restart, and only then
-- does the Settings vendor picker stop saying "not configured". A brain the
-- boss cannot switch to without a redeploy is a brain he does not have when
-- the one he is on runs out of usage - which is the exact moment he needs it.
--
-- This table is the runtime half of that credential lookup: one row per
-- provider id, written by PUT /api/settings/provider-keys, read by
-- llm.BuildRegistry so a pasted key registers the provider in the live
-- registry on save - no restart, no env var. Generic by construction: the
-- provider id is the key, so a new vendor needs zero schema work.
--
-- Precedence is DB over env. A key typed in the UI is the most recent
-- explicit instruction the boss gave; an env var is the deploy-time default.
-- The API never returns api_key - only a masked hint (last 4) so the UI can
-- show WHICH key is stored without ever shipping the secret to a browser.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_provider_keys (
    provider    TEXT PRIMARY KEY,
    api_key     TEXT NOT NULL,
    label       TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE mem_provider_keys IS
    'LLM vendor API keys pasted into Studio Settings, keyed by provider id (anthropic/openai/google/deepseek/...). Read by llm.BuildRegistry; takes precedence over the matching env var. api_key is never returned over the wire - the API surfaces a masked last-4 hint only.';

COMMENT ON COLUMN mem_provider_keys.label IS
    'Optional human note for which account/plan this key belongs to. Safe to surface in the UI.';

COMMIT;
