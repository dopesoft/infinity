-- 151_mem_turns_cache_tokens.sql - capture the prompt-caching breakdown per turn.
--
-- With prompt caching wired (stable-first prompt + cache_control breakpoints),
-- a turn's prompt splits into uncached input + cache reads (0.1x cost) + cache
-- writes (1.25x). mem_turns.input_tokens already records the FULL prompt size
-- (uncached + cache reads/writes) so the /logs token figure and the context
-- meter stay accurate. These two columns add the breakdown so the trace UI can
-- show the caching EFFECT - how much of each turn was served from cache - for
-- every model (0 on models/turns with no cache hit).
--
-- Additive, NOT NULL DEFAULT 0 so existing rows backfill cleanly. No data is
-- moved or dropped.
BEGIN;

ALTER TABLE mem_turns
    ADD COLUMN IF NOT EXISTS cache_read_tokens  INT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_write_tokens INT NOT NULL DEFAULT 0;

COMMIT;
