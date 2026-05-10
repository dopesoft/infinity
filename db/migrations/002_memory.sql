-- 002_memory.sql — Phase 3 memory schema.
-- Ports rohitg00/agentmemory's TypeScript schema (Apache-2.0) to Postgres+pgvector.
-- Embedding dimension: 384 (Xenova all-MiniLM-L6-v2).

-- ---------------------------------------------------------------------------
-- Sessions
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_sessions (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project     TEXT,
    started_at  TIMESTAMPTZ DEFAULT NOW(),
    ended_at    TIMESTAMPTZ,
    metadata    JSONB DEFAULT '{}'::jsonb
);

CREATE INDEX IF NOT EXISTS mem_sessions_project_idx ON mem_sessions(project);
CREATE INDEX IF NOT EXISTS mem_sessions_started_idx ON mem_sessions(started_at DESC);

-- ---------------------------------------------------------------------------
-- Observations: raw captures from hooks
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_observations (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    session_id  UUID REFERENCES mem_sessions(id) ON DELETE CASCADE,
    hook_name   TEXT NOT NULL,
    payload     JSONB DEFAULT '{}'::jsonb,
    raw_text    TEXT,
    embedding   vector(384),
    fts_doc     tsvector,
    importance  INT DEFAULT 5,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mem_observations_session_idx ON mem_observations(session_id);
CREATE INDEX IF NOT EXISTS mem_observations_hook_idx ON mem_observations(hook_name);
CREATE INDEX IF NOT EXISTS mem_observations_created_idx ON mem_observations(created_at DESC);
CREATE INDEX IF NOT EXISTS mem_observations_fts_idx ON mem_observations USING gin (fts_doc);
-- HNSW index. Drop ef_construction to 64 if 16M+ rows ever materialize.
CREATE INDEX IF NOT EXISTS mem_observations_embedding_idx
    ON mem_observations USING hnsw (embedding vector_cosine_ops);

-- ---------------------------------------------------------------------------
-- Summaries: end-of-session digests
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_summaries (
    session_id    UUID PRIMARY KEY REFERENCES mem_sessions(id) ON DELETE CASCADE,
    summary_text  TEXT,
    key_decisions JSONB DEFAULT '[]'::jsonb,
    embedding     vector(384),
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Memories: long-term, versioned, with relationships
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_memories (
    id              UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    title           TEXT,
    content         TEXT,
    tier            TEXT NOT NULL,           -- working, episodic, semantic, procedural
    version         INT DEFAULT 1,
    superseded_by   UUID REFERENCES mem_memories(id) ON DELETE SET NULL,
    status          TEXT DEFAULT 'active',   -- active, superseded, archived
    strength        REAL DEFAULT 1.0,
    importance      INT DEFAULT 5,
    embedding       vector(384),
    fts_doc         tsvector,
    forget_after    TIMESTAMPTZ,
    project         TEXT,
    created_at      TIMESTAMPTZ DEFAULT NOW(),
    updated_at      TIMESTAMPTZ DEFAULT NOW(),
    last_accessed_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mem_memories_tier_status_idx ON mem_memories(tier, status);
CREATE INDEX IF NOT EXISTS mem_memories_project_idx ON mem_memories(project);
CREATE INDEX IF NOT EXISTS mem_memories_fts_idx ON mem_memories USING gin (fts_doc);
CREATE INDEX IF NOT EXISTS mem_memories_embedding_idx
    ON mem_memories USING hnsw (embedding vector_cosine_ops);
CREATE INDEX IF NOT EXISTS mem_memories_strength_idx ON mem_memories(strength DESC);
CREATE INDEX IF NOT EXISTS mem_memories_last_accessed_idx ON mem_memories(last_accessed_at DESC);

-- ---------------------------------------------------------------------------
-- Memory sources (provenance: memory ← which observations)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_memory_sources (
    memory_id      UUID REFERENCES mem_memories(id) ON DELETE CASCADE,
    observation_id UUID REFERENCES mem_observations(id) ON DELETE CASCADE,
    confidence     REAL DEFAULT 1.0,
    PRIMARY KEY (memory_id, observation_id)
);

CREATE INDEX IF NOT EXISTS mem_memory_sources_obs_idx ON mem_memory_sources(observation_id);

-- ---------------------------------------------------------------------------
-- Relations (memory-to-memory typed graph)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_relations (
    id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source_id     UUID REFERENCES mem_memories(id) ON DELETE CASCADE,
    target_id     UUID REFERENCES mem_memories(id) ON DELETE CASCADE,
    relation_type TEXT NOT NULL,    -- supersedes, extends, derives, contradicts, related
    confidence    REAL DEFAULT 1.0,
    created_at    TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mem_relations_source_idx ON mem_relations(source_id, relation_type);
CREATE INDEX IF NOT EXISTS mem_relations_target_idx ON mem_relations(target_id, relation_type);

-- ---------------------------------------------------------------------------
-- Profiles (per-project rolled-up state)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_profiles (
    project        TEXT PRIMARY KEY,
    top_concepts   JSONB DEFAULT '[]'::jsonb,
    top_files      JSONB DEFAULT '[]'::jsonb,
    conventions    JSONB DEFAULT '[]'::jsonb,
    common_errors  JSONB DEFAULT '[]'::jsonb,
    session_count  INT DEFAULT 0,
    updated_at     TIMESTAMPTZ DEFAULT NOW()
);

-- ---------------------------------------------------------------------------
-- Graph nodes (entities)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_graph_nodes (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    type        TEXT NOT NULL,      -- person, project, file, concept, decision, error, skill
    name        TEXT NOT NULL,
    metadata    JSONB DEFAULT '{}'::jsonb,
    stale_flag  BOOLEAN DEFAULT FALSE,
    created_at  TIMESTAMPTZ DEFAULT NOW(),
    UNIQUE(type, name)
);

CREATE INDEX IF NOT EXISTS mem_graph_nodes_type_idx ON mem_graph_nodes(type);

-- ---------------------------------------------------------------------------
-- Graph edges (typed relationships)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_graph_edges (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    source_id   UUID REFERENCES mem_graph_nodes(id) ON DELETE CASCADE,
    target_id   UUID REFERENCES mem_graph_nodes(id) ON DELETE CASCADE,
    edge_type   TEXT NOT NULL,
    confidence  REAL DEFAULT 1.0,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mem_graph_edges_source_idx ON mem_graph_edges(source_id, edge_type);
CREATE INDEX IF NOT EXISTS mem_graph_edges_target_idx ON mem_graph_edges(target_id, edge_type);

-- Bridge: which observations mention which graph nodes (for graph stream)
CREATE TABLE IF NOT EXISTS mem_graph_node_observations (
    node_id        UUID REFERENCES mem_graph_nodes(id) ON DELETE CASCADE,
    observation_id UUID REFERENCES mem_observations(id) ON DELETE CASCADE,
    PRIMARY KEY (node_id, observation_id)
);

CREATE INDEX IF NOT EXISTS mem_graph_node_obs_obs_idx ON mem_graph_node_observations(observation_id);

-- ---------------------------------------------------------------------------
-- Audit log (every mutation)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_audit (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    operation    TEXT,    -- create, update, delete, supersede
    target_table TEXT,
    target_id    UUID,
    actor        TEXT,    -- user, agent, skill, auto-evolution
    diff         JSONB,
    created_at   TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mem_audit_target_idx ON mem_audit(target_id);
CREATE INDEX IF NOT EXISTS mem_audit_created_idx ON mem_audit(created_at DESC);

-- ---------------------------------------------------------------------------
-- Lessons (confidence-scored, decay)
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS mem_lessons (
    id                  UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    project             TEXT,
    lesson_text         TEXT,
    confidence          REAL,
    times_reinforced    INT DEFAULT 1,
    last_reinforced_at  TIMESTAMPTZ DEFAULT NOW(),
    created_at          TIMESTAMPTZ DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS mem_lessons_project_idx ON mem_lessons(project);
CREATE INDEX IF NOT EXISTS mem_lessons_confidence_idx ON mem_lessons(confidence DESC);
