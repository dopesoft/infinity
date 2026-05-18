-- 057_training_examples_embedding.sql - embeddings on Gym lessons.
--
-- The reflex layer (plasticity.Provider) currently ranks training examples
-- by lexical token overlap with the user's query. That misses every case
-- where the lesson talks about "email triage" and the query says "inbox" -
-- semantically identical, lexically zero match. We had pgvector everywhere
-- else, just not here.
--
-- This migration adds an `embedding vector(384)` column + HNSW index so
-- the provider can do cosine retrieval over lessons the same way memory
-- search retrieves observations. Extraction populates the column going
-- forward; older rows have NULL and degrade gracefully back to the
-- lexical path.

BEGIN;

ALTER TABLE mem_training_examples
    ADD COLUMN IF NOT EXISTS embedding vector(384);

-- HNSW cosine index matches the rest of the codebase's vector indexes
-- (mem_memories, mem_observations, mem_reflections). Partial - skip rows
-- without an embedding so the index stays tight while backfill catches up.
CREATE INDEX IF NOT EXISTS idx_mem_training_examples_embedding
    ON mem_training_examples
       USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64)
    WHERE embedding IS NOT NULL;

COMMIT;
