-- 028_domain_hints.sql - persistent table↔tool-prefix mappings used by
-- system_map. Storing these in the DB (instead of a Go slice) lets the
-- agent extend its own topology via the domain_hint_add tool: the next
-- system_map() call reflects the new hint without a deploy.
--
-- This is the AGI close-the-loop move for runtime introspection:
-- system_map already auto-discovers tables + tools. Irregular pairings
-- (mem_curiosity_questions → question_*, etc.) used to require a Go
-- edit. With this table, the agent learns the mapping once and persists
-- it.
--
-- Idempotent.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_domain_hints (
    -- The mem_* table this hint covers. PK so re-asserting the same
    -- hint just updates the prefix/display fields.
    table_name   TEXT PRIMARY KEY,

    -- Tool-name prefix this domain owns. Tools matching `<prefix>_*`
    -- or named exactly `<prefix>` belong to this surface.
    tool_prefix  TEXT NOT NULL,

    -- Human-readable name for the dashboard / system_map output.
    display_as   TEXT NOT NULL DEFAULT '',

    -- Free-form note the agent can attach (rationale for the mapping,
    -- discovered context, etc.). Surfaced in system_map for debugging.
    notes        TEXT NOT NULL DEFAULT '',

    -- Who/what wrote this hint. 'seed' for migration-seeded rows,
    -- 'agent' for runtime-added, plus any explicit source string.
    source       TEXT NOT NULL DEFAULT 'agent',

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Seed the irregular cases known at migration time. Each row uses ON
-- CONFLICT DO NOTHING so re-running the migration (or an agent that's
-- already registered a hint) doesn't clobber learned state.
INSERT INTO mem_domain_hints (table_name, tool_prefix, display_as, source) VALUES
    ('mem_curiosity_questions', 'question',       'Questions',                  'seed'),
    ('mem_surface_items',       'surface',        'Surface items (generic)',    'seed'),
    ('mem_pursuit_checkins',    'pursuit',        'Pursuit check-ins',          'seed'),
    ('mem_code_proposals',      'code_proposal',  'Code proposals',             'seed'),
    ('mem_trust_contracts',     'trust',          'Trust approvals',            'seed'),
    ('mem_skill_proposals',     'skill_propose',  'Skill proposals',            'seed'),
    ('mem_skill_runs',          'skills_history', 'Skill runs (history)',       'seed'),
    ('mem_heartbeat_findings',  'heartbeat',      'Heartbeat findings',         'seed'),
    ('mem_agent_goals',         'goal',           'Agent goals',                'seed')
ON CONFLICT (table_name) DO NOTHING;

INSERT INTO infinity_meta (key, value)
VALUES ('domain_hints_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = NOW();

COMMIT;
