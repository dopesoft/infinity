-- 083_realtime_rls_read_policies.sql
--
-- Make the app actually realtime. Every mem_* brain table is in the
-- supabase_realtime publication and the Studio frontend subscribes to them
-- (lib/realtime/provider.tsx), BUT the tables had RLS enabled with ZERO
-- policies. Supabase realtime enforces RLS, so with no SELECT policy for the
-- authenticated role it silently dropped every change event before it reached
-- the browser. Result: nothing updated without a manual refresh (chat session
-- naming, runs, surfaces, dashboard counts, ...). The app only looked alive on
-- reload because reloads re-fetch through Core, which connects as the table
-- owner and bypasses RLS entirely.
--
-- Fix: for every table in the supabase_realtime publication, ensure RLS is on
-- and grant a SELECT policy to the `authenticated` role. This is single-user
-- safe: only a logged-in Supabase session (the boss) can read; the public anon
-- key alone still cannot. It exposes nothing Core doesn't already serve the
-- boss, and it does NOT open writes (Core owns those via pgx and is unaffected
-- by RLS). This is configuring RLS, not bypassing it.
--
-- Idempotent: re-running drops and recreates each read policy, and ALTER TABLE
-- ... ENABLE ROW LEVEL SECURITY is a no-op when already enabled.

DO $$
DECLARE
  r RECORD;
  pol_name TEXT;
BEGIN
  FOR r IN
    SELECT schemaname, tablename
    FROM pg_publication_tables
    WHERE pubname = 'supabase_realtime'
      AND schemaname = 'public'
  LOOP
    -- RLS must be on for the policy to be the gate (and per the project's
    -- "RLS on every table" rule). No-op if already enabled. Core (owner)
    -- bypasses RLS, so this never breaks server-side reads/writes.
    EXECUTE format(
      'ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY',
      r.schemaname, r.tablename
    );

    pol_name := r.tablename || '_realtime_authenticated_read';

    EXECUTE format(
      'DROP POLICY IF EXISTS %I ON %I.%I',
      pol_name, r.schemaname, r.tablename
    );

    -- Read-only, authenticated-only. Single-user app: authenticated == boss.
    EXECUTE format(
      'CREATE POLICY %I ON %I.%I FOR SELECT TO authenticated USING (true)',
      pol_name, r.schemaname, r.tablename
    );
  END LOOP;
END $$;
