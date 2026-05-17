-- 001_init.sql - base extensions and connection sanity for Infinity Phase 0.
-- Memory schema (mem_*) lands in 002_memory.sql.

CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Sanity ping - every Infinity build records its first migration here.
CREATE TABLE IF NOT EXISTS infinity_meta (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);

INSERT INTO infinity_meta (key, value)
VALUES ('schema_initialized_at', NOW()::TEXT)
ON CONFLICT (key) DO NOTHING;
