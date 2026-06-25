-- 159_extension_auth_session.sql
--
-- Scope the Canvas "Sign in to <tool>" card (CanvasAuthCard) to the ONE
-- conversation that started the sign-in.
--
-- The bug this fixes: mem_extensions rows are global (no session binding), so a
-- single extension stuck at status='pending_auth' made the auth card hijack the
-- Preview pane of EVERY session — including a brand-new conversation about
-- something unrelated. higgsfield (which can't complete its browser-redirect
-- sign-in headlessly) sat pending forever, so the card kept seizing whatever
-- conversation the boss happened to open and spun "Preparing your sign-in
-- link…". The earlier "fix" only made the card dismissable; it never isolated
-- it to the originating session.
--
-- The fix records WHICH session initiated the sign-in. The frontend then only
-- renders the card in that session. A pending row with no originating session
-- (boot-time MCP parks, migration-forced rows like the old higgsfield one) no
-- longer takes over any Preview pane — it surfaces in Settings → Extensions
-- instead. Nullable, no backfill: existing pending rows have no originator, so
-- they correctly stop hijacking conversations.

ALTER TABLE mem_extensions
  ADD COLUMN IF NOT EXISTS auth_session_id TEXT;

COMMENT ON COLUMN mem_extensions.auth_session_id IS
  'Session id that initiated this extension''s pending sign-in. The Canvas auth card renders ONLY in this session; null/empty means no originating session, so the card never hijacks a conversation (surfaces in Settings instead). Cleared when the extension goes active.';
