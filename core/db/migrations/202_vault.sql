-- 202: mem_vault_cards + mem_vault_enrollments — secrets encrypted at rest.
--
-- WHY
--
-- Infinity had no encryption-at-rest helper of any kind. Every crypto import in
-- the tree was hashing or webhook-signature verification. Meanwhile the phone
-- vault kept the boss's real card number, expiry, CVC, date of birth and
-- account number as PLAINTEXT rows in infinity_meta, and GET /api/meta?key=...
-- served any of them back to any authenticated caller with no key filtering.
-- The one saving grace was the phone tool's shape, which is worth keeping and
-- generalising: the card is spliced into the call brief server-side and the
-- planning model only ever passes a boolean.
--
-- This table keeps that shape and fixes the storage. The secret half is sealed
-- with AES-256-GCM under a key that lives in the environment, never in the
-- database, so a dump of this table is not a dump of the card. The clear half
-- is only what a human needs to recognise which card they are looking at:
-- label, brand, last four, expiry month and year. That is deliberately the
-- same set a payment vault would hand back, so replacing this with a PCI
-- vendor later is a change of implementation behind CardVault, not a change of
-- schema.
--
-- WHAT MUST NEVER HAPPEN HERE
--
--   - No route returns `sealed`. Not filtered, not redacted: never selected.
--     Decryption happens inside the fill boundary in Go and nowhere else.
--   - Not in the realtime publication. A card must not stream to a client.
--   - key_version exists so the key can be rotated without a migration: new
--     rows seal under the new version, old rows stay readable until re-sealed.
--
-- If INFINITY_VAULT_KEY is lost, these rows are unrecoverable by design and the
-- card has to be entered again. That is the correct trade against the
-- alternative, which is what we had: plaintext readable over HTTP.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_vault_cards (
    id           UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- The clear half: enough to tell two cards apart, useless to a thief.
    label        TEXT NOT NULL DEFAULT '',
    brand        TEXT NOT NULL DEFAULT '',
    last4        TEXT NOT NULL DEFAULT '',
    exp_month    SMALLINT,
    exp_year     SMALLINT,
    billing_complete BOOLEAN NOT NULL DEFAULT FALSE,

    -- The sealed half: AES-256-GCM over the JSON {pan, cvc, name, billing}.
    -- nonce is per-row and never reused. The key is in the environment.
    sealed       BYTEA NOT NULL,
    nonce        BYTEA NOT NULL,
    key_version  INTEGER NOT NULL DEFAULT 1,

    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_used_at TIMESTAMPTZ,
    revoked_at   TIMESTAMPTZ,
    user_id      UUID
);

CREATE INDEX IF NOT EXISTS idx_vault_cards_live
    ON mem_vault_cards(created_at DESC) WHERE revoked_at IS NULL;

COMMENT ON COLUMN mem_vault_cards.sealed IS
    'AES-256-GCM ciphertext of the card secrets. Never selected by any HTTP route; decrypted only inside the fill boundary. A dump of this table is not a dump of the card.';

-- Enrollment links: how a card gets in without passing through chat.
--
-- The token is stored HASHED, so the row cannot be replayed even by someone
-- holding the database. Single use and short lived, because the whole point is
-- that the window in which a link is worth anything is a few minutes wide.
CREATE TABLE IF NOT EXISTS mem_vault_enrollments (
    id          UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    token_hash  TEXT NOT NULL UNIQUE,
    purpose     TEXT NOT NULL DEFAULT 'card',
    created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    expires_at  TIMESTAMPTZ NOT NULL,
    used_at     TIMESTAMPTZ,
    card_id     UUID REFERENCES mem_vault_cards(id) ON DELETE SET NULL,
    user_id     UUID
);

CREATE INDEX IF NOT EXISTS idx_vault_enrollments_open
    ON mem_vault_enrollments(expires_at) WHERE used_at IS NULL;

COMMENT ON COLUMN mem_vault_enrollments.token_hash IS
    'sha256 of the enrollment token. The token itself is shown once, to the boss, and never stored, so a leaked database cannot mint a working link.';

COMMIT;
