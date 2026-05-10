-- 005_proactive.sql — Phase 5 (Proactive engine).
--
-- Adds tables for:
--   • mem_session_state    — durable WAL output (SESSION-STATE.md replacement)
--   • mem_working_buffer   — danger-zone exchange log (60%-of-context capture)
--   • mem_intent_decisions — IntentFlow control-token history per turn
--   • mem_heartbeats       — record of every heartbeat run + findings count
--   • mem_heartbeat_findings — individual findings produced by a heartbeat
--   • mem_outcomes         — non-trivial decisions awaiting follow-up
--   • mem_trust_contracts  — Trust-Contract approval queue
--   • mem_patterns         — detected repeated request patterns (skill candidates)

BEGIN;

CREATE TABLE IF NOT EXISTS mem_session_state (
    session_id  UUID PRIMARY KEY,
    body        TEXT NOT NULL DEFAULT '',
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mem_working_buffer (
    session_id  UUID PRIMARY KEY,
    body        TEXT NOT NULL DEFAULT '',
    threshold   DOUBLE PRECISION NOT NULL DEFAULT 0.6,
    active      BOOLEAN NOT NULL DEFAULT FALSE,
    cleared_at  TIMESTAMPTZ,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mem_intent_decisions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id  UUID,
    user_msg    TEXT NOT NULL,
    token       TEXT NOT NULL CHECK (token IN ('silent','fast_intervention','full_assistance')),
    confidence  DOUBLE PRECISION NOT NULL DEFAULT 0,
    reason      TEXT NOT NULL DEFAULT '',
    suggested   TEXT NOT NULL DEFAULT '',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_intent_session_created
    ON mem_intent_decisions(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS mem_heartbeats (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    started_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    findings    INT NOT NULL DEFAULT 0,
    summary     TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'ok'  -- ok | error
);

CREATE INDEX IF NOT EXISTS idx_heartbeats_started ON mem_heartbeats(started_at DESC);

CREATE TABLE IF NOT EXISTS mem_heartbeat_findings (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    heartbeat_id UUID NOT NULL REFERENCES mem_heartbeats(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,         -- pattern | outcome | curiosity | surprise | security | self_heal
    title        TEXT NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',
    pre_approved BOOLEAN NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_heartbeat_findings_hb
    ON mem_heartbeat_findings(heartbeat_id);

CREATE TABLE IF NOT EXISTS mem_outcomes (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    decision_text   TEXT NOT NULL,
    decided_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    follow_up_at    TIMESTAMPTZ,
    status          TEXT NOT NULL DEFAULT 'pending', -- pending | followed_up | resolved
    source_session  UUID,
    resolution_text TEXT
);

CREATE INDEX IF NOT EXISTS idx_outcomes_followup ON mem_outcomes(follow_up_at, status);

CREATE TABLE IF NOT EXISTS mem_trust_contracts (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title          TEXT NOT NULL,
    risk_level     TEXT NOT NULL DEFAULT 'medium',
    source         TEXT NOT NULL DEFAULT 'heartbeat', -- heartbeat | live | cron | sentinel
    action_spec    JSONB NOT NULL DEFAULT '{}'::jsonb,
    reasoning      TEXT NOT NULL DEFAULT '',
    cited_memory_ids UUID[] NOT NULL DEFAULT ARRAY[]::UUID[],
    risk_assessment JSONB NOT NULL DEFAULT '{}'::jsonb,
    preview        TEXT NOT NULL DEFAULT '',
    status         TEXT NOT NULL DEFAULT 'pending',   -- pending | approved | denied | snoozed
    decided_at     TIMESTAMPTZ,
    decision_note  TEXT,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_trust_status_created
    ON mem_trust_contracts(status, created_at DESC);

CREATE TABLE IF NOT EXISTS mem_patterns (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    description     TEXT NOT NULL,
    occurrences     INT NOT NULL DEFAULT 1,
    suggested_skill TEXT,
    last_seen_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    status          TEXT NOT NULL DEFAULT 'open' -- open | accepted | dismissed
);

COMMIT;
