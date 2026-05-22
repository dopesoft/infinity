-- 067_agent_teams.sql
--
-- Generic, model-agnostic agent-team substrate. This is intentionally not
-- Claude-specific and not coding-specific: the agent can assemble temporary
-- organizations for research, multimedia workflows, code, connector work, or
-- any other wide task. Cognition lives in the coordinate-agent-team skill;
-- these tables are just durable coordination/artifact state.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_agent_teams (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  parent_session_id TEXT,
  goal TEXT NOT NULL,
  reason TEXT,
  status TEXT NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'ok', 'error', 'cancelled')),
  settings JSONB NOT NULL DEFAULT '{}'::jsonb,
  token_budget INTEGER NOT NULL DEFAULT 0,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at TIMESTAMPTZ,
  error TEXT
);

CREATE TABLE IF NOT EXISTS mem_agent_members (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id UUID NOT NULL REFERENCES mem_agent_teams(id) ON DELETE CASCADE,
  role TEXT NOT NULL,
  task TEXT NOT NULL,
  capability TEXT NOT NULL DEFAULT 'general',
  session_id TEXT NOT NULL,
  model TEXT,
  status TEXT NOT NULL DEFAULT 'running'
    CHECK (status IN ('running', 'ok', 'error', 'cancelled')),
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  started_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  ended_at TIMESTAMPTZ,
  error TEXT
);

CREATE TABLE IF NOT EXISTS mem_agent_team_artifacts (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id UUID NOT NULL REFERENCES mem_agent_teams(id) ON DELETE CASCADE,
  member_id UUID REFERENCES mem_agent_members(id) ON DELETE SET NULL,
  kind TEXT NOT NULL DEFAULT 'summary',
  title TEXT NOT NULL,
  body TEXT NOT NULL DEFAULT '',
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS mem_agent_team_events (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  team_id UUID NOT NULL REFERENCES mem_agent_teams(id) ON DELETE CASCADE,
  member_id UUID REFERENCES mem_agent_members(id) ON DELETE SET NULL,
  event_type TEXT NOT NULL,
  message TEXT NOT NULL DEFAULT '',
  data JSONB NOT NULL DEFAULT '{}'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_agent_teams_parent_started
  ON mem_agent_teams(parent_session_id, started_at DESC);

CREATE INDEX IF NOT EXISTS idx_mem_agent_members_team
  ON mem_agent_members(team_id, started_at);

CREATE INDEX IF NOT EXISTS idx_mem_agent_team_artifacts_team
  ON mem_agent_team_artifacts(team_id, created_at);

CREATE INDEX IF NOT EXISTS idx_mem_agent_team_events_team
  ON mem_agent_team_events(team_id, created_at);

COMMIT;
