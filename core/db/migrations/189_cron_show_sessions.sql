-- 189_cron_show_sessions.sql - which scheduled runs are worth a row in the
-- boss's Sessions list.
--
-- The Automated tab shows a session per agent-driven run. His rule for what
-- belongs there, verbatim: "stuff like getting me an ai report or things that
-- make sense to talk with the agent about i'm cool with.. but especially inbox
-- triage is kinda useless to see sessions about."
--
-- That distinction is a PROPERTY OF THE JOB, not something to infer per run and
-- not something to hardcode in Go against a cron's name. So the job declares it:
-- one boolean column, defaulting to visible, and two registered mem_act verbs so
-- the boss (or Jarvis, on his say-so) can flip any cron either way with no code
-- change and no deploy.
--
-- Idempotent.

BEGIN;

ALTER TABLE mem_crons
    ADD COLUMN IF NOT EXISTS show_sessions BOOLEAN NOT NULL DEFAULT TRUE;

COMMENT ON COLUMN mem_crons.show_sessions IS
    'When false, this job''s sessions are hidden from the Sessions list. For '
    'routine chores whose result already lives on another surface (triage lands '
    'in Follow-ups; a deploy check lands in the run log), so the list stays '
    'conversations worth having. Runs, plans, and history are unaffected.';

-- ── The two chores, by name ─────────────────────────────────────────────────
-- inbox-triage: named explicitly by the boss. Its output IS the Follow-ups
--   card; the session behind it is bookkeeping.
-- post-deploy-verify: the same class of thing, and the larger of the two (33
--   sessions vs 8). A health check that passes is not a conversation.
--
-- Everything else stays visible: the weekly AI brief, the content autopilot,
-- and nightly self-improve all produce something there is a reason to open.
UPDATE mem_crons
   SET show_sessions = FALSE
 WHERE name IN ('inbox-triage', 'post-deploy-verify');

-- ── Attribute the anonymous cron containers ─────────────────────────────────
-- The scheduler's ensureSession booked a session row per run as an FK parent
-- for the run's plan, stamped kind='cron' but nothing else. 488 rows carry no
-- cron_id, so they can't be labelled with their job's name and the switch above
-- can't find them. Recover the link from the run ledger, which does record both.
-- (Go now stamps it at creation; this is for the rows already written.)
UPDATE mem_sessions s
   SET origin_ref = jsonb_build_object('cron_id', r.target_id, 'cron_name', c.name)
  FROM mem_runs r
  JOIN mem_crons c ON c.id::text = r.target_id
 WHERE r.kind = 'cron'
   AND r.meta->>'session_id' = s.id::text
   AND COALESCE(s.kind,'') = 'cron'
   AND COALESCE(s.origin_ref, '{}'::jsonb) = '{}'::jsonb;

-- ── Let the agent flip it without a deploy ──────────────────────────────────
-- Registering the verbs here means "stop showing me sessions for X" is a single
-- mem_act call against the generic action substrate, not a code change. Same
-- bounded op vocabulary as every other registered action (set_bool).
INSERT INTO mem_action_schemas (table_name, action_name, op, column_name, value, description, source)
VALUES
  ('mem_crons', 'hide_sessions', 'set_bool', 'show_sessions', 'false',
   'Stop listing this job''s sessions in the boss''s Sessions list. For routine chores whose result already shows up elsewhere.', 'seed'),
  ('mem_crons', 'show_sessions', 'set_bool', 'show_sessions', 'true',
   'List this job''s sessions again in the boss''s Sessions list.', 'seed')
ON CONFLICT (table_name, action_name) DO UPDATE
   SET op          = EXCLUDED.op,
       column_name = EXCLUDED.column_name,
       value       = EXCLUDED.value,
       description = EXCLUDED.description;

COMMIT;
