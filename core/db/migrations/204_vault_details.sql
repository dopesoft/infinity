-- 204: mem_vault_details — everything Jarvis knows about the boss personally,
-- with a per-item switch saying whether he may hand it over.
--
-- WHY THIS IS ONE GENERIC TABLE AND NOT A COLUMN PER FIELD
--
-- The settings screen needed a first name, a last name, an email, a shipping
-- address and a billing address, and every one of them needed its own "he may
-- read this out" switch. Done as columns that is eleven new columns, eleven
-- booleans beside them, and a twelfth conversation the next time a checkout
-- asks for something we did not think of. Done as rows it is a catalog entry in
-- Go (vault/details.go) and nothing else: the schema never moves again, the
-- screen renders whatever the catalog lists, and the checkout filler matches on
-- the hints the catalog carries. Rule #1 — the contract is generic, the
-- knowledge is data.
--
-- WHAT `releasable` ACTUALLY DOES, AND WHY IT IS HERE RATHER THAN IN A PROMPT
--
-- It is enforced at the single read path (Details.Release) that both the phone
-- brief and the checkout filler go through. A detail with releasable=false is
-- not redacted downstream and is not something the model is asked nicely not to
-- mention: it is never loaded, so there is nothing to leak. That is Rule #1b —
-- the mechanic lives in code where an LLM having an off day cannot drop it.
--
-- SEALED OR CLEAR, PER ROW
--
--   clear   what you would write on an envelope. Your name, your address, your
--           email. It has to be readable so the settings screen can show it
--           back to you and let you correct a typo, and hiding an address you
--           are trying to edit would be theatre rather than security.
--   sealed  what a bank asks to prove you are you. Date of birth, the last four
--           of a social, an account number, the spoken password. AES-256-GCM
--           under INFINITY_VAULT_KEY, never returned by any route, opened only
--           inside the boundary that uses it.
--
-- Exactly one of (value, sealed) is populated per row. Which one is decided by
-- the catalog, not by the caller, so a detail cannot be stored the wrong way by
-- an endpoint that forgot.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_vault_details (
    key         TEXT PRIMARY KEY,

    -- The clear half, for details that are meant to be readable.
    value       TEXT,

    -- The sealed half, for details that are not. nonce is per-row and never
    -- reused; the key lives in the environment, never here.
    sealed      BYTEA,
    nonce       BYTEA,
    key_version INTEGER NOT NULL DEFAULT 1,

    -- Whether Jarvis may hand this over: read out on a call, typed into a
    -- checkout. Enforced in Go at the one read path, not by asking the model.
    releasable  BOOLEAN NOT NULL DEFAULT TRUE,

    updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id     UUID,

    -- A row is one or the other, never both and never neither. This is the
    -- guarantee that a sealed detail cannot quietly acquire a plaintext copy.
    CONSTRAINT vault_detail_one_form CHECK (
        (value IS NOT NULL AND sealed IS NULL)
     OR (sealed IS NOT NULL AND nonce IS NOT NULL AND value IS NULL)
    )
);

COMMENT ON COLUMN mem_vault_details.releasable IS
    'Whether Jarvis may hand this detail over. Enforced at Details.Release in Go: a false row is never loaded, so there is nothing downstream to leak.';

COMMENT ON COLUMN mem_vault_details.sealed IS
    'AES-256-GCM ciphertext. Never selected by any HTTP route; opened only inside the boundary that uses the value.';

COMMIT;
