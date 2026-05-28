-- 082_extensions_cli.sql - CLI tools as a third extension kind, with a
-- human-in-the-loop auth state.
--
-- mem_extensions already carries kind=mcp (a remote MCP server) and
-- kind=http_tool (a REST endpoint as a named tool). This adds kind=cli:
-- a binary Jarvis installs into the persistent cloud workspace and runs
-- via bash_run. Unlike mcp/http_tool, a CLI often needs an interactive
-- auth step the agent can't complete alone (device-code OAuth, a browser
-- login). That's the human-in-the-loop moment:
--
--   1. Jarvis installs the CLI, runs its check command.
--   2. The check says "not authenticated" → Jarvis runs the auth command,
--      captures the device-login URL, and parks the extension in
--      status='pending_auth' with auth_url + auth_instructions.
--   3. Jarvis hands the boss the URL and ends the turn.
--   4. The ExtensionAuthChecklist heartbeat hook re-runs the check command
--      on each tick; when it finally passes, the extension flips to
--      'active' and a Finding fires that resumes whatever Jarvis was doing
--      (resume_intent) - the loop closes without the boss prompting again.
--
-- Columns are additive + defaulted so existing mcp/http_tool rows are
-- untouched. status stays free-text (no CHECK constraint) so 'pending_auth'
-- needs no enum migration.

BEGIN;

ALTER TABLE mem_extensions
    ADD COLUMN IF NOT EXISTS auth_url          TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS auth_instructions TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS resume_intent     TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS last_checked_at   TIMESTAMPTZ;

-- The heartbeat re-probes pending_auth rows every tick; index the hot path.
CREATE INDEX IF NOT EXISTS idx_mem_extensions_pending_auth
    ON mem_extensions (status)
    WHERE status = 'pending_auth';

COMMIT;
