-- 199_quotes.sql
--
-- The daily quote under the dashboard greeting.
--
-- TWO TABLES, NOT ONE COLUMN. "Which quote is today's" is a fact about a DAY,
-- not a mutable field on a quote. Splitting them is what buys the three
-- properties this feature actually needs:
--
--   idempotent   PRIMARY KEY (day) means a day resolves to the same quote
--                forever. The boss refreshing at 9am and again at 4pm reads
--                the same line; no clever hashing required and no drift when
--                the corpus grows.
--   concurrent   Two tabs (or two Railway replicas) hitting a fresh day
--                cannot produce two different quotes - one wins the insert,
--                the other blocks on its row lock and reads the winner.
--   cycling      "least recently shown, never-shown first" is a query over
--                history rather than a counter someone has to remember to
--                increment. Every quote is shown once before any repeats.
--
-- No cron writes here and no Go job. The assignment is made lazily by the
-- first /api/dashboard request of the boss's local day, and never again.
--
-- Deliberately NOT added to supabase_realtime: the publication is opt-in per
-- table, and a midnight insert into mem_quote_days would otherwise wake
-- useRealtime("*") on every open tab and trigger a dashboard refetch storm
-- for something purely cosmetic.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_quotes (
    id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- UNIQUE is load-bearing: it is what lets the seed migration re-run, and
    -- what stops the same line entering twice under two spellings of source.
    text        TEXT NOT NULL UNIQUE,
    author      TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '',
    active      BOOLEAN NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS mem_quote_days (
    -- The boss's LOCAL day (INFINITY_USER_TIMEZONE, default America/Chicago),
    -- not UTC. A quote that changes at 7pm because a server is in London is
    -- a bug, not a rounding difference.
    day         DATE PRIMARY KEY,
    quote_id    UUID NOT NULL REFERENCES mem_quotes(id) ON DELETE RESTRICT,
    assigned_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Drives the "max(day) per quote, NULLS FIRST" ordering that picks the next
-- one. Without it the pick is a seq scan over the whole history each morning.
CREATE INDEX IF NOT EXISTS idx_mem_quote_days_quote
    ON mem_quote_days (quote_id, day DESC);

COMMENT ON TABLE mem_quotes IS
    'Hand-curated, well-attested quotes shown one per day under the dashboard greeting. Content only: no code reads any individual row by id, so adding to the corpus is a seed migration and nothing else.';
COMMENT ON TABLE mem_quote_days IS
    'One row per local day pinning that day to a quote. PRIMARY KEY (day) is the idempotency AND concurrency guarantee.';

COMMIT;
