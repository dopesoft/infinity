-- 029_action_substrate.sql — the bounded-action vocabulary that lets the
-- agent mutate any mem_* table without a bespoke Go tool per domain.
--
-- Why this exists: even with system_map auto-discovering tables and
-- domain_hint_add letting the agent pair them with tool prefixes,
-- ACTING on a new table still required shipping a new Go mutate tool.
-- This migration ends that by providing two pieces of substrate:
--
--   1. mem_action_schemas — a bounded vocabulary of (table, action) →
--      (op, column, value) tuples. The agent (or a seed row) declares
--      "for mem_followups, action 'dismiss' means set status='done'".
--      A new generic tool, mem_act, looks up the row and executes a
--      parameterised UPDATE — NO raw SQL from the agent, NO arbitrary
--      column writes, just the four bounded ops below.
--
--   2. count_filter on mem_domain_hints — replaces the hardcoded
--      heuristic ladder in system_map. Each table can say "open rows
--      are status='open'" and system_map uses that for live counts.
--
-- Bounded ops (enforced in Go, see mem_act tool):
--   set_status     → UPDATE table SET <column> = <value> WHERE id ∈ ids
--   set_timestamp  → UPDATE table SET <column> = NOW()   WHERE id ∈ ids
--   set_null       → UPDATE table SET <column> = NULL     WHERE id ∈ ids
--   set_bool       → UPDATE table SET <column> = <bool>   WHERE id ∈ ids
--
-- That vocabulary covers ~every dismissal/snooze/complete pattern in
-- mem_* without giving the agent the ability to flip arbitrary columns
-- or run arbitrary SQL. Adding a new op variant in the future is a
-- one-line Go change with explicit safety review.

BEGIN;

-- ── mem_action_schemas ─────────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS mem_action_schemas (
    table_name   TEXT NOT NULL,
    action_name  TEXT NOT NULL,
    op           TEXT NOT NULL CHECK (op IN ('set_status','set_timestamp','set_null','set_bool')),
    column_name  TEXT NOT NULL,
    value        TEXT,           -- literal for set_status; bool for set_bool; NULL for the others
    description  TEXT NOT NULL DEFAULT '',
    source       TEXT NOT NULL DEFAULT 'agent',
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (table_name, action_name)
);

CREATE INDEX IF NOT EXISTS idx_mem_action_schemas_table
    ON mem_action_schemas(table_name);

-- ── seed the known actions ────────────────────────────────────────────────
-- Each row mirrors what the existing bespoke Go tools (followup_dismiss,
-- task_done, question_decide, surface_update, etc.) do under the hood, so
-- the agent can use mem_act as a substitute the moment this migration lands.
-- ON CONFLICT DO NOTHING preserves any agent-registered overrides.

INSERT INTO mem_action_schemas (table_name, action_name, op, column_name, value, description, source) VALUES
    -- Curiosity questions: dismiss/answer/approve
    ('mem_curiosity_questions', 'dismissed', 'set_status', 'status', 'dismissed', 'Boss declined to act on the question.', 'seed'),
    ('mem_curiosity_questions', 'answered',  'set_status', 'status', 'answered',  'Question resolved with an answer.',       'seed'),
    ('mem_curiosity_questions', 'approved',  'set_status', 'status', 'approved',  'Boss told the agent to act on it.',       'seed'),

    -- Generic surface items: dismiss/done
    ('mem_surface_items', 'dismissed', 'set_status', 'status', 'dismissed', 'Item dismissed from the dashboard.', 'seed'),
    ('mem_surface_items', 'done',      'set_status', 'status', 'done',      'Item handled.',                     'seed'),

    -- Tasks: done/drop
    ('mem_tasks', 'done',    'set_status', 'status', 'done',    'Todo completed.', 'seed'),
    ('mem_tasks', 'dropped', 'set_status', 'status', 'dropped', 'Todo abandoned.', 'seed'),
    ('mem_tasks', 'reopen',  'set_status', 'status', 'open',    'Todo reopened.',  'seed'),

    -- Follow-ups: dismiss
    ('mem_followups', 'dismissed', 'set_status', 'status', 'done', 'Follow-up handled or no longer relevant.', 'seed'),
    ('mem_followups', 'read',      'set_bool',   'unread', 'false', 'Mark as read.',                          'seed'),

    -- Saved: mark read
    ('mem_saved', 'read', 'set_timestamp', 'read_at', NULL, 'Mark saved item as read.', 'seed'),

    -- Trust contracts: approve/deny (the boss-decide endpoint already exists,
    -- but exposing it as an action lets the agent act on its own queue when
    -- the boss has pre-authorised the contract via Studio.)
    ('mem_trust_contracts', 'approved', 'set_status', 'status', 'approved', 'Contract approved.', 'seed'),
    ('mem_trust_contracts', 'denied',   'set_status', 'status', 'denied',   'Contract denied.',   'seed'),

    -- Code proposals: approve/reject/applied
    ('mem_code_proposals', 'approved', 'set_status', 'status', 'approved', 'Code proposal approved.', 'seed'),
    ('mem_code_proposals', 'rejected', 'set_status', 'status', 'rejected', 'Code proposal rejected.', 'seed'),
    ('mem_code_proposals', 'applied',  'set_status', 'status', 'applied',  'Code proposal applied.',  'seed'),

    -- Cron: enable/disable (without deleting)
    ('mem_crons', 'pause',  'set_bool', 'enabled', 'false', 'Pause a cron.',  'seed'),
    ('mem_crons', 'resume', 'set_bool', 'enabled', 'true',  'Resume a cron.', 'seed'),

    -- Sessions: soft-delete
    ('mem_sessions', 'soft_delete', 'set_timestamp', 'deleted_at', NULL, 'Soft-delete the session.', 'seed')
ON CONFLICT (table_name, action_name) DO NOTHING;

-- ── count_filter on mem_domain_hints ──────────────────────────────────────
-- Replaces the heuristic ladder in system_map with explicit per-table
-- strategies. Bounded: the value is a status literal OR one of a fixed
-- set of recognised symbolic filters interpreted by Go.

ALTER TABLE mem_domain_hints
    ADD COLUMN IF NOT EXISTS count_filter TEXT NOT NULL DEFAULT '';

-- Seed the count filters for the irregular tables seeded in migration 028.
-- Convention: the value is interpreted as a status literal unless it's one
-- of the symbolic forms: 'enabled', 'unread', 'pending', 'proposed', 'open',
-- 'active', 'total'.
UPDATE mem_domain_hints SET count_filter = 'open'     WHERE table_name = 'mem_curiosity_questions' AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'open'     WHERE table_name = 'mem_surface_items'       AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'total'    WHERE table_name = 'mem_pursuit_checkins'    AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'proposed' WHERE table_name = 'mem_code_proposals'      AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'pending'  WHERE table_name = 'mem_trust_contracts'     AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'proposed' WHERE table_name = 'mem_skill_proposals'     AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'total'    WHERE table_name = 'mem_skill_runs'          AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'total'    WHERE table_name = 'mem_heartbeat_findings'  AND count_filter = '';
UPDATE mem_domain_hints SET count_filter = 'active'   WHERE table_name = 'mem_agent_goals'         AND count_filter = '';

INSERT INTO infinity_meta (key, value)
VALUES ('action_substrate_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
