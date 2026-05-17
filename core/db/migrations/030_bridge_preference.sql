-- 030_bridge_preference.sql
--
-- Per-session bridge routing preference. Jarvis can run filesystem / bash
-- / git operations against either:
--
--   - the Mac bridge   (claude_code__* MCP via Cloudflare tunnel; Max-billed
--                       Claude Code is the editor sub-agent on the Mac).
--   - the cloud bridge (docker/workspace service on Railway; Jarvis is the
--                       only brain, ChatGPT subscription via openai_oauth
--                       covers all cognition, no metered API spend).
--
-- The two bridges are kept in sync via GitHub. Edit on one → push → pull
-- on the other when the boss switches devices.
--
-- bridge_preference values:
--   'auto'  - prefer Mac when its /health is up; fall back to cloud.
--   'mac'   - Mac only; surface an error if it's offline (no silent fall).
--   'cloud' - Cloud only; pin the session to the Railway workspace.

ALTER TABLE mem_sessions
    ADD COLUMN IF NOT EXISTS bridge_preference TEXT
        NOT NULL DEFAULT 'auto'
        CHECK (bridge_preference IN ('auto', 'mac', 'cloud'));

COMMENT ON COLUMN mem_sessions.bridge_preference IS
    'Which bridge this session prefers for fs/bash/git ops: auto | mac | cloud.';
