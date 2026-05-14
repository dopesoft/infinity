-- 018_extensions.sql — runtime self-extension.
--
-- Phase 3 of the assembly substrate. The agent extends its OWN toolset at
-- runtime: it wires a new MCP server or registers a REST API as a tool,
-- and that capability is live this session AND durable across restarts —
-- no rebuild of the embedded mcp.yaml, no redeploy.
--
-- mem_extensions stores runtime-registered capability providers. Two
-- kinds today:
--   mcp        — a remote MCP server (url + transport + auth-by-env-var)
--   http_tool  — a single REST endpoint exposed as a named native tool
--
-- Secrets never land here. MCP auth references env var NAMES, never
-- values — the same rule the embedded mcp.yaml follows. The agent
-- registers through the extension_* tools, never raw SQL.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_extensions (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name         TEXT NOT NULL UNIQUE,           -- kebab-case identifier
    kind         TEXT NOT NULL,                  -- mcp | http_tool
    description  TEXT NOT NULL DEFAULT '',
    -- Kind-specific config.
    --   mcp:       { url, transport, auth, auth_token_env, auth_header_name }
    --   http_tool: { method, url, headers, body_template, params }
    -- String values in an http_tool url/headers/body may contain {{param}}
    -- placeholders, filled from the generated tool's call args.
    config       JSONB NOT NULL DEFAULT '{}'::jsonb,
    enabled      BOOLEAN NOT NULL DEFAULT TRUE,
    source       TEXT NOT NULL DEFAULT 'agent',  -- agent | manual
    -- active   → loaded into the live tool registry
    -- error    → activation failed; last_error has why
    -- disabled → kept for history, not loaded
    status       TEXT NOT NULL DEFAULT 'active',
    last_error   TEXT NOT NULL DEFAULT '',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_extensions_enabled
    ON mem_extensions (kind)
    WHERE enabled = TRUE;

-- Realtime: Studio subscribes so the extensions list live-updates.
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime') THEN
        IF NOT EXISTS (
            SELECT 1 FROM pg_publication_tables
             WHERE pubname = 'supabase_realtime'
               AND schemaname = 'public'
               AND tablename = 'mem_extensions'
        ) THEN
            EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE public.mem_extensions';
        END IF;
    END IF;
END $$;

COMMIT;
