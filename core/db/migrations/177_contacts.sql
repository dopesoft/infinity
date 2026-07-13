-- 177_contacts.sql — give the phone book a spine.
--
-- The boss: "once i tell him ariana and her number, he should store that in my
-- phone book so next time i just say call ariana and it works... and if i say
-- call goodfellas pizza, hopefully he websearches and finds it, maybe by
-- confirming its location ('the one on preston road?') and then its saved to the
-- address book so next time i can mention it by name only."
--
-- The contact book he sees (PhoneCard -> ContactBookModal, GET /api/phone/contacts)
-- already exists, but it is a READ-ONLY PROJECTION of call history. Its own code
-- says so: "Read straight off the phone:history:* keyed-state cells - no separate
-- contacts table." That shape cannot do the job:
--
--   * a contact only exists AFTER a call, so telling Jarvis a number saves nothing
--   * it is keyed by NUMBER, and the name is scraped out of a history sentence
--   * so nothing can resolve "Ariana" -> +19293100906, and "call Ariana" is a guess
--
-- This is the spine under the SAME surface: one writable, name-indexed contact
-- record. The dashboard book keeps its component and its endpoint and simply
-- starts reading something real. phone:history:* stays exactly as it is: it is the
-- rolling narrative of what was said, which is a different thing from who someone
-- is, and the call agent still reads it for recall.

CREATE TABLE IF NOT EXISTS mem_contacts (
    id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    -- What the boss calls them: "Ariana", "Goodfellas Pizza".
    name           TEXT NOT NULL,
    -- name, normalized for lookup: lowercase, letters and digits only.
    name_norm      TEXT NOT NULL,
    -- Every other way he might say it: "my wife", "Ari", "the pizza place".
    aliases        TEXT[] NOT NULL DEFAULT '{}',
    number         TEXT NOT NULL,  -- E.164
    kind           TEXT NOT NULL DEFAULT 'person',  -- person | org
    -- What tells two Goodfellas apart: "the one on Preston Road".
    location       TEXT NOT NULL DEFAULT '',
    -- How to treat them, and anything worth knowing next time.
    note           TEXT NOT NULL DEFAULT '',
    source         TEXT NOT NULL DEFAULT 'agent',  -- boss | call | web | agent
    times_called   INTEGER NOT NULL DEFAULT 0,
    last_called_at TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- One record per number: calling the same line again enriches the contact that
-- already exists instead of forking a duplicate.
CREATE UNIQUE INDEX IF NOT EXISTS mem_contacts_number_key ON mem_contacts (number);
CREATE INDEX IF NOT EXISTS mem_contacts_name_norm_idx ON mem_contacts (name_norm);
CREATE INDEX IF NOT EXISTS mem_contacts_aliases_idx ON mem_contacts USING GIN (aliases);

-- Seed the book from every number Jarvis already has history with, so it is not
-- born empty and the dashboard shows the same people it showed yesterday. The
-- name is parsed exactly the way the endpoint parses it today: a named call reads
-- "Goodfellas Pizza: Outbound call Jul 10 2026...", while an unnamed one starts
-- with "Inbound"/"Outbound" and has no name to take.
--
-- History keys hold the last 10 digits, so a seeded US number is +1 plus those.
-- Live calls upsert the true E.164 from the dial itself, which agrees with this.
INSERT INTO mem_contacts (name, name_norm, number, kind, note, source, last_called_at)
SELECT
    COALESCE(h.parsed_name, 'Unknown'),
    lower(regexp_replace(COALESCE(h.parsed_name, ''), '[^a-zA-Z0-9]', '', 'g')),
    '+1' || h.digits,
    COALESCE(k.value #>> '{}', 'person'),
    left(h.hist, 1000),
    'call',
    h.updated_at
FROM (
    SELECT
        replace(s.key, 'phone:history:', '')          AS digits,
        s.value #>> '{}'                              AS hist,
        s.updated_at,
        CASE
            WHEN split_part(s.value #>> '{}', ' | ', 1) LIKE 'Inbound%'  THEN NULL
            WHEN split_part(s.value #>> '{}', ' | ', 1) LIKE 'Outbound%' THEN NULL
            WHEN position(': ' IN split_part(s.value #>> '{}', ' | ', 1)) > 0
                THEN left(split_part(s.value #>> '{}', ' | ', 1),
                          position(': ' IN split_part(s.value #>> '{}', ' | ', 1)) - 1)
            ELSE NULL
        END                                           AS parsed_name
    FROM mem_agent_state s
    WHERE s.key LIKE 'phone:history:%'
      AND length(replace(s.key, 'phone:history:', '')) = 10
) h
LEFT JOIN mem_agent_state k ON k.key = 'phone:kind:' || h.digits
ON CONFLICT (number) DO NOTHING;
