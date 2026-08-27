-- 192_pursuit_experience.sql
--
-- Adds a reusable "experience" contract on top of the existing pursuits
-- surface, and the first non-ordinary experience: Psycho-Cybernetics.
--
-- Rule #1a compliance. Ordinary pursuits keep working exactly as before.
-- Every existing row is left on experience = 'ordinary' and the dashboard
-- keeps rendering them the same way. A pursuit only becomes a bespoke
-- coached programme when the caller flips its experience to a non-default
-- value (currently: 'psycho_cybernetics'). Adding a future experience is a
-- new value on this column plus a new coach adapter, not a fork of the
-- pursuit substrate.
--
-- The Psycho-Cybernetics experience adds:
--   mem_pursuit_pc_state       one row per pursuit, holds the identity,
--                              objective, limiting pattern, cycle number,
--                              current day, missed-days counter, timezone
--                              and last morning/midday/evening timestamps.
--   mem_pursuit_pc_sessions    one row per coaching session (onboarding,
--                              morning, midday, evening, recovery, review,
--                              adjustment) with the freeform answers on it.
--   mem_pursuit_pc_proofs      the daily deliberate proof action: what the
--                              boss will do to test the identity today, and
--                              whether it was taken.
--   mem_pursuit_pc_evidence    daytime captures of evidence for the new
--                              identity or resistance against it.
--   mem_pursuit_pc_memories    the "victory memory" bank the coach can pull
--                              back at rehearsal time.
--   mem_pursuit_pc_patterns    limiting patterns, operating identities, and
--                              corrections logged over time.
--   mem_pursuit_pc_reviews     the deliberate end-of-cycle review that
--                              carries wins, misses, adjustments, and the
--                              next cycle's identity/objective/pattern.
--
-- Framing: Maltz explicitly presents this method as reflective self-
-- experimentation ("try it on yourself for 21 days"), not therapy for
-- clinical anxiety or trauma. The coach layer keeps that frame, and no
-- backend field records anything clinical. Everything a caller stores here
-- is the boss's own writing about his own performance experiment.

BEGIN;

-- ── Reusable experience type on mem_pursuits ───────────────────────────────
-- Two nullable-safe columns. Existing rows land on 'ordinary' and an empty
-- config so the current PursuitsCard + pursuit_create tool see no change.

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = 'public'
           AND table_name = 'mem_pursuits'
           AND column_name = 'experience'
    ) THEN
        ALTER TABLE mem_pursuits
            ADD COLUMN experience TEXT NOT NULL DEFAULT 'ordinary';
    END IF;
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns
         WHERE table_schema = 'public'
           AND table_name = 'mem_pursuits'
           AND column_name = 'config'
    ) THEN
        ALTER TABLE mem_pursuits
            ADD COLUMN config JSONB NOT NULL DEFAULT '{}'::jsonb;
    END IF;
END $$;

CREATE INDEX IF NOT EXISTS idx_mem_pursuits_experience
    ON mem_pursuits(experience);

DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_mem_pursuits_experience') THEN
        ALTER TABLE mem_pursuits ADD CONSTRAINT chk_mem_pursuits_experience
            CHECK (experience IN ('ordinary', 'psycho_cybernetics'));
    END IF;
END $$;

-- ── mem_pursuit_pc_state ───────────────────────────────────────────────────
-- One row per Psycho-Cybernetics pursuit. Insert on onboarding, update as
-- the cycle advances. The Coach reads this on every phase entry to decide
-- what prompt to show next; the Cockpit renders straight from it.
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_state (
    pursuit_id             UUID PRIMARY KEY REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    -- The current cycle. Starts at 1 on onboarding and increments each
    -- time a review closes the previous cycle. Never resets.
    cycle_number           INT  NOT NULL DEFAULT 1,

    -- Total cycle length. Fixed at 21 for the first cut per Maltz's
    -- author framing (see chapter 1). Stored on the row so a future
    -- experience variant can shorten it without a schema change.
    cycle_length_days      INT  NOT NULL DEFAULT 21,

    -- 1 through cycle_length_days. Derived from cycle_started_at + the
    -- pursuit's timezone (day boundary is midnight local), but stored so
    -- the cockpit and coach do not have to recompute per read.
    current_day            INT  NOT NULL DEFAULT 1,

    -- Timestamp the current cycle started (in UTC). Advances when a
    -- review closes the cycle and a new one begins.
    cycle_started_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),

    -- Sliding count of days that ended with no morning/midday/evening
    -- session recorded. Recovery decrements + emits a soft prompt; never
    -- shame-driven.
    missed_days_count      INT  NOT NULL DEFAULT 0,

    -- Current framing the coach reflects back. Author-framed as "the
    -- self-image / operating identity the boss is experimenting with."
    current_identity       TEXT NOT NULL DEFAULT '',
    current_objective      TEXT NOT NULL DEFAULT '',
    current_limiting_pattern TEXT NOT NULL DEFAULT '',

    -- Optional three-part pressure test taken at onboarding. Reflected back
    -- during morning rehearsal so the boss sees where the identity might
    -- crack under pressure and can rehearse the corrective response.
    pressure_test          JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- IANA timezone (America/Chicago default per boss timezone memory).
    -- Determines when "today" flips over for the daily phase slots.
    timezone               TEXT NOT NULL DEFAULT 'America/Chicago',

    -- Last observed timestamps for each daily phase. Powers "morning done"
    -- badges on the cockpit and skip logic in the coach.
    last_morning_at        TIMESTAMPTZ,
    last_midday_at         TIMESTAMPTZ,
    last_evening_at        TIMESTAMPTZ,

    created_at             TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at             TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_state_current_day
    ON mem_pursuit_pc_state(current_day);

-- ── mem_pursuit_pc_sessions ────────────────────────────────────────────────
-- One row per coaching session. Answers land as JSONB so the coach can
-- evolve its questions without a schema change.
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_sessions (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id     UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    -- onboarding | morning | midday | evening | recovery | review | adjustment
    kind           TEXT NOT NULL,

    -- The cycle and day-in-cycle this session belongs to. Denormalised so
    -- reviews at the end of a cycle can lift the exact set of daily
    -- sessions without re-deriving from timestamps.
    cycle_number   INT  NOT NULL DEFAULT 1,
    day_in_cycle   INT  NOT NULL DEFAULT 1,

    -- Freeform answers. Shape depends on kind: morning stores
    -- {rehearsal, proof_pledge}, evening stores {fact, interpretation,
    -- lesson, correction}, review stores {wins, misses}, etc.
    answers        JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- The coach's own note about what it observed and adapted for the
    -- next session. Never a moral judgement, always a next-step nudge.
    coach_note     TEXT NOT NULL DEFAULT '',

    occurred_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_sessions_pursuit_time
    ON mem_pursuit_pc_sessions(pursuit_id, occurred_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_sessions_kind
    ON mem_pursuit_pc_sessions(pursuit_id, kind, occurred_at DESC);

-- ── mem_pursuit_pc_proofs ──────────────────────────────────────────────────
-- The deliberate proof action pledged in the morning session (what the boss
-- will do today to prove the new identity) and reconciled in the evening.
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_proofs (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id     UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,
    session_id     UUID REFERENCES mem_pursuit_pc_sessions(id) ON DELETE SET NULL,

    label          TEXT NOT NULL,

    -- Both cycle metrics for review roll-up. Not FK-derived because a
    -- proof can outlive its originating session (cycle advances).
    cycle_number   INT  NOT NULL DEFAULT 1,
    day_in_cycle   INT  NOT NULL DEFAULT 1,

    planned_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    taken          BOOLEAN NOT NULL DEFAULT FALSE,
    taken_at       TIMESTAMPTZ,
    note           TEXT NOT NULL DEFAULT '',

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_proofs_pursuit_planned
    ON mem_pursuit_pc_proofs(pursuit_id, planned_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_proofs_taken
    ON mem_pursuit_pc_proofs(pursuit_id, taken, planned_at DESC);

-- ── mem_pursuit_pc_evidence ────────────────────────────────────────────────
-- Daytime captures. kind='evidence' is "the identity worked here"; kind=
-- 'resistance' is "the old pattern showed up here." Both feed the evening
-- one-question flow and the next-morning rehearsal.
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_evidence (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id     UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,
    session_id     UUID REFERENCES mem_pursuit_pc_sessions(id) ON DELETE SET NULL,

    kind           TEXT NOT NULL DEFAULT 'evidence',
    body           TEXT NOT NULL,
    tags           JSONB NOT NULL DEFAULT '[]'::jsonb,

    cycle_number   INT  NOT NULL DEFAULT 1,
    day_in_cycle   INT  NOT NULL DEFAULT 1,

    captured_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_evidence_pursuit_captured
    ON mem_pursuit_pc_evidence(pursuit_id, captured_at DESC);
CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_evidence_kind
    ON mem_pursuit_pc_evidence(pursuit_id, kind, captured_at DESC);

-- ── mem_pursuit_pc_memories ────────────────────────────────────────────────
-- The "victory memory" bank. Mined during onboarding and topped up as new
-- wins land. The coach pulls a memory to prime morning rehearsal
-- (Maltz's "winning feeling", author framing).
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_memories (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id     UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    title          TEXT NOT NULL,
    body           TEXT NOT NULL DEFAULT '',
    tags           JSONB NOT NULL DEFAULT '[]'::jsonb,

    -- Ranking hint the coach uses when picking the memory to rehearse.
    -- 0 to 100, higher gets picked more often. Manual for now; the coach
    -- can tune it later based on how often a memory precedes a proof
    -- getting taken.
    weight         INT  NOT NULL DEFAULT 50,

    saved_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_memories_pursuit_saved
    ON mem_pursuit_pc_memories(pursuit_id, saved_at DESC);

-- ── mem_pursuit_pc_patterns ────────────────────────────────────────────────
-- Rolling record of the limiting patterns the boss has identified, the
-- operating identities he has adopted, and the deliberate corrections
-- logged when a resistance surfaced.
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_patterns (
    id             UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id     UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    -- limiting | operating | correction
    kind           TEXT NOT NULL DEFAULT 'limiting',
    body           TEXT NOT NULL,
    refs           JSONB NOT NULL DEFAULT '{}'::jsonb,

    cycle_number   INT  NOT NULL DEFAULT 1,
    day_in_cycle   INT  NOT NULL DEFAULT 1,

    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_patterns_pursuit_kind_created
    ON mem_pursuit_pc_patterns(pursuit_id, kind, created_at DESC);

-- ── mem_pursuit_pc_reviews ─────────────────────────────────────────────────
-- End-of-cycle deliberate identity review. Closes a cycle and opens the
-- next by supplying the next_identity / next_objective / next_pattern.
CREATE TABLE IF NOT EXISTS mem_pursuit_pc_reviews (
    id                UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    pursuit_id        UUID NOT NULL REFERENCES mem_pursuits(id) ON DELETE CASCADE,

    cycle_number      INT  NOT NULL,

    wins              TEXT NOT NULL DEFAULT '',
    misses            TEXT NOT NULL DEFAULT '',

    -- Framing for the next cycle. Any field can be blank to signal "keep
    -- the current value."
    next_identity     TEXT NOT NULL DEFAULT '',
    next_objective    TEXT NOT NULL DEFAULT '',
    next_pattern      TEXT NOT NULL DEFAULT '',

    -- Freeform adjustments applied for the next cycle (adaptive coaching
    -- decisions the boss chose to keep). One JSONB blob so the coach can
    -- add fields without another migration.
    adjustments       JSONB NOT NULL DEFAULT '{}'::jsonb,

    completed_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_mem_pursuit_pc_reviews_pursuit_cycle
    ON mem_pursuit_pc_reviews(pursuit_id, cycle_number);

-- Database guards keep malformed API or tool writes from corrupting the
-- programme state. Each named constraint is added only once so migrations
-- remain safe across partially-upgraded installations.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_state_cycle_number') THEN
        ALTER TABLE mem_pursuit_pc_state ADD CONSTRAINT chk_pc_state_cycle_number CHECK (cycle_number >= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_state_cycle_length') THEN
        ALTER TABLE mem_pursuit_pc_state ADD CONSTRAINT chk_pc_state_cycle_length CHECK (cycle_length_days >= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_state_current_day') THEN
        ALTER TABLE mem_pursuit_pc_state ADD CONSTRAINT chk_pc_state_current_day CHECK (current_day BETWEEN 1 AND cycle_length_days);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_state_missed_days') THEN
        ALTER TABLE mem_pursuit_pc_state ADD CONSTRAINT chk_pc_state_missed_days CHECK (missed_days_count >= 0);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_session_kind') THEN
        ALTER TABLE mem_pursuit_pc_sessions ADD CONSTRAINT chk_pc_session_kind
            CHECK (kind IN ('onboarding', 'morning', 'midday', 'evening', 'recovery', 'review', 'adjustment'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_session_cycle_day') THEN
        ALTER TABLE mem_pursuit_pc_sessions ADD CONSTRAINT chk_pc_session_cycle_day
            CHECK (cycle_number >= 1 AND day_in_cycle >= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_proof_cycle_day') THEN
        ALTER TABLE mem_pursuit_pc_proofs ADD CONSTRAINT chk_pc_proof_cycle_day
            CHECK (cycle_number >= 1 AND day_in_cycle >= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_proof_taken_at') THEN
        ALTER TABLE mem_pursuit_pc_proofs ADD CONSTRAINT chk_pc_proof_taken_at
            CHECK ((taken AND taken_at IS NOT NULL) OR (NOT taken));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_evidence_kind') THEN
        ALTER TABLE mem_pursuit_pc_evidence ADD CONSTRAINT chk_pc_evidence_kind
            CHECK (kind IN ('evidence', 'resistance'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_evidence_cycle_day') THEN
        ALTER TABLE mem_pursuit_pc_evidence ADD CONSTRAINT chk_pc_evidence_cycle_day
            CHECK (cycle_number >= 1 AND day_in_cycle >= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_memory_weight') THEN
        ALTER TABLE mem_pursuit_pc_memories ADD CONSTRAINT chk_pc_memory_weight CHECK (weight BETWEEN 0 AND 100);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_pattern_kind') THEN
        ALTER TABLE mem_pursuit_pc_patterns ADD CONSTRAINT chk_pc_pattern_kind
            CHECK (kind IN ('limiting', 'operating', 'correction'));
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_pattern_cycle_day') THEN
        ALTER TABLE mem_pursuit_pc_patterns ADD CONSTRAINT chk_pc_pattern_cycle_day
            CHECK (cycle_number >= 1 AND day_in_cycle >= 1);
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'chk_pc_review_cycle') THEN
        ALTER TABLE mem_pursuit_pc_reviews ADD CONSTRAINT chk_pc_review_cycle CHECK (cycle_number >= 1);
    END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_pursuit_pc_proofs_one_daily_label
    ON mem_pursuit_pc_proofs(pursuit_id, cycle_number, day_in_cycle, label);
CREATE UNIQUE INDEX IF NOT EXISTS idx_mem_pursuit_pc_reviews_one_cycle
    ON mem_pursuit_pc_reviews(pursuit_id, cycle_number);

-- ── Realtime publication (Supabase) ────────────────────────────────────────
DO $$
DECLARE
    tname TEXT;
BEGIN
    IF EXISTS (
        SELECT 1 FROM pg_publication WHERE pubname = 'supabase_realtime'
    ) THEN
        FOREACH tname IN ARRAY ARRAY[
            'mem_pursuit_pc_state',
            'mem_pursuit_pc_sessions',
            'mem_pursuit_pc_proofs',
            'mem_pursuit_pc_evidence',
            'mem_pursuit_pc_memories',
            'mem_pursuit_pc_patterns',
            'mem_pursuit_pc_reviews'
        ]
        LOOP
            IF NOT EXISTS (
                SELECT 1 FROM pg_publication_tables
                 WHERE pubname = 'supabase_realtime'
                   AND schemaname = 'public'
                   AND tablename = tname
            ) THEN
                EXECUTE format('ALTER PUBLICATION supabase_realtime ADD TABLE public.%I', tname);
            END IF;
        END LOOP;
    END IF;
END $$;

COMMIT;
