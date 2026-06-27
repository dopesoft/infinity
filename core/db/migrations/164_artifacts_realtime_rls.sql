-- 164_artifacts_realtime_rls.sql
--
-- Make the Media gallery actually live. mem_artifacts is in the
-- supabase_realtime publication (136) with REPLICA IDENTITY FULL, and Studio
-- subscribes to it (useSessionArtifacts), BUT it has RLS enabled with ZERO
-- policies — so the authenticated realtime socket is denied every row and
-- Supabase silently drops all its change events. New documents only appeared
-- after a manual refresh (which fetches through Core, bypassing RLS).
--
-- Root cause: 136 added mem_artifacts to the publication AFTER 083 granted the
-- authenticated read policies, so 083's loop never covered it. Fix: grant the
-- same read policy mem_runs (and every other realtime table) already has —
-- scoped to mem_artifacts only. Single-user-safe: read-only, authenticated-only
-- (== the boss); Core owns writes via pgx and is unaffected by RLS. Idempotent.

ALTER TABLE public.mem_artifacts ENABLE ROW LEVEL SECURITY; -- no-op if already on

DROP POLICY IF EXISTS mem_artifacts_realtime_authenticated_read ON public.mem_artifacts;

CREATE POLICY mem_artifacts_realtime_authenticated_read
  ON public.mem_artifacts
  FOR SELECT TO authenticated
  USING (true);
