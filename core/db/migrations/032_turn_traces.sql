-- 032_turn_traces.sql - LangSmith-style turn-by-turn traces.
--
-- One row per agent turn lands in mem_turns. Every per-event row
-- (mem_observations, mem_skill_runs, mem_intent_decisions, mem_predictions,
-- mem_trust_contracts) gains a nullable turn_id so the /logs UI + the
-- trace_* agent tools can reconstruct a turn as a single timeline.
--
-- The turn_id column is nullable on purpose: rows that predate this
-- migration stay queryable. New turns are stamped going forward; the
-- trace API falls back to a (session_id, created_at) grouping for older
-- rows so historical sessions still render in the list view.
--
-- REPLICA IDENTITY FULL on mem_turns + the per-event tables is required
-- so UPDATE events deliver the full row under Realtime RLS (per the
-- migration 025 fix). The publication ADD TABLE is wrapped in a DO block
-- because pg won't let you add a table that's already published.

BEGIN;

-- ----- mem_turns -------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_turns (
    id              UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id      UUID NOT NULL REFERENCES mem_sessions(id) ON DELETE CASCADE,
    user_id         UUID,
    started_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at        TIMESTAMPTZ,
    user_text       TEXT NOT NULL DEFAULT '',
    assistant_text  TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    stop_reason     TEXT NOT NULL DEFAULT '',
    input_tokens    INT NOT NULL DEFAULT 0,
    output_tokens   INT NOT NULL DEFAULT 0,
    tool_call_count INT NOT NULL DEFAULT 0,
    -- status discriminates the UI pip: in_flight | ok | empty | errored | interrupted
    status          TEXT NOT NULL DEFAULT 'in_flight',
    error           TEXT,
    -- summary is the short one-liner the /logs list view renders. Computed
    -- at close time (first ~140 chars of assistant_text or a synthetic
    -- "(no reply - N tool calls)" when empty).
    summary         TEXT NOT NULL DEFAULT ''
);

CREATE INDEX IF NOT EXISTS mem_turns_session_started_idx
    ON mem_turns(session_id, started_at DESC);
CREATE INDEX IF NOT EXISTS mem_turns_started_idx
    ON mem_turns(started_at DESC) WHERE ended_at IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_turns_active_idx
    ON mem_turns(started_at DESC) WHERE ended_at IS NULL;
CREATE INDEX IF NOT EXISTS mem_turns_status_idx
    ON mem_turns(status, started_at DESC);

ALTER TABLE mem_turns REPLICA IDENTITY FULL;

-- ----- turn_id on per-event tables ------------------------------------------
ALTER TABLE mem_observations    ADD COLUMN IF NOT EXISTS turn_id UUID;
ALTER TABLE mem_skill_runs      ADD COLUMN IF NOT EXISTS turn_id UUID;
ALTER TABLE mem_intent_decisions ADD COLUMN IF NOT EXISTS turn_id UUID;
ALTER TABLE mem_predictions     ADD COLUMN IF NOT EXISTS turn_id UUID;
ALTER TABLE mem_trust_contracts ADD COLUMN IF NOT EXISTS turn_id UUID;

CREATE INDEX IF NOT EXISTS mem_observations_turn_idx     ON mem_observations(turn_id)     WHERE turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_skill_runs_turn_idx       ON mem_skill_runs(turn_id)       WHERE turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_intent_decisions_turn_idx ON mem_intent_decisions(turn_id) WHERE turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_predictions_turn_idx      ON mem_predictions(turn_id)      WHERE turn_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS mem_trust_contracts_turn_idx  ON mem_trust_contracts(turn_id)  WHERE turn_id IS NOT NULL;

-- mem_predictions / mem_skill_runs aren't already FULL per migration 025.
-- Make them FULL so detail-page UPDATE events deliver the full row under RLS.
ALTER TABLE mem_predictions REPLICA IDENTITY FULL;
ALTER TABLE mem_skill_runs  REPLICA IDENTITY FULL;

-- ----- realtime publication -------------------------------------------------
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_publication_tables
        WHERE pubname = 'supabase_realtime' AND tablename = 'mem_turns'
    ) THEN
        EXECUTE 'ALTER PUBLICATION supabase_realtime ADD TABLE mem_turns';
    END IF;
END $$;

COMMIT;
