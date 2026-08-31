-- 201: mem_purchase_obligations — the binding record behind every purchase.
--
-- WHY
--
-- Before this, the entire defence against Jarvis spending money wrongly was
-- BrowserGate.isTransactionalAct: a lowercase substring match against the
-- `label` the model volunteered on a browser_act. A button labelled "Continue"
-- went through unattended, and an approval granted for a $40 cart still fired
-- if the cart had silently become $400, because the approval was attached to a
-- CLICK rather than to a PURCHASE. Nothing checked afterwards whether the
-- order actually happened.
--
-- An obligation is the missing noun. It binds every term of the transaction —
-- who, which merchant, which cart, which currency, what exact total, which
-- origins may receive the card, which browser session, which card, and by
-- when — and the fill boundary re-checks all of it twice: once before it
-- claims, and again against the live page immediately before it submits. The
-- boss approves THE OBLIGATION, not a click, so a cart that changes during the
-- approval wait invalidates the thing he agreed to instead of quietly riding
-- on it.
--
-- THE STATE MACHINE, and why the order matters
--
--   draft -> pending_approval -> approved -> claimed -> submitted -> confirmed
--                                                                \-> uncertain
--                             \-> cancelled | expired | failed
--                                            \-> awaiting_3ds -> submitted
--
-- Two transitions carry the whole no-double-charge guarantee:
--
--   approved -> claimed    a single atomic UPDATE ... WHERE status='approved'.
--                          Exactly one caller can win. Everyone else gets zero
--                          rows and must NOT retry.
--
--   claimed -> submitted   written BEFORE the charge-bearing click, never
--                          after. If the process dies mid-click the row reads
--                          'submitted' with no confirmation, which resolves to
--                          'uncertain' and is never retried automatically.
--                          Writing it after the click would leave a crash
--                          looking identical to "never happened", and the
--                          repair for that mistake is a second charge.
--
-- Nothing here is in the realtime publication: an obligation reaches Studio
-- through the Trust contract that gates it, not by streaming itself.

BEGIN;

CREATE TABLE IF NOT EXISTS mem_purchase_obligations (
    id                 UUID PRIMARY KEY DEFAULT uuid_generate_v4(),

    -- Provenance: which conversation asked for this, and which run carried it.
    session_id         TEXT NOT NULL DEFAULT '',
    trust_contract_id  UUID,

    -- WHAT is being bought, and from whom.
    merchant_host      TEXT NOT NULL,
    merchant_name      TEXT NOT NULL DEFAULT '',
    cart               JSONB NOT NULL DEFAULT '[]'::jsonb,
    currency           TEXT NOT NULL,
    total_cents        BIGINT NOT NULL CHECK (total_cents > 0),
    recipient          JSONB NOT NULL DEFAULT '{}'::jsonb,

    -- WHERE the card may be typed. The fill boundary refuses any frame whose
    -- origin is not in this list, which is what stops a card being entered on
    -- a page that merely looks like the checkout.
    payment_origins    TEXT[] NOT NULL DEFAULT ARRAY[]::TEXT[],

    -- WHICH browser and WHICH card. Both are opaque references: the planning
    -- model never receives either the session's contents or the card's digits.
    browser_session_id TEXT NOT NULL DEFAULT '',
    card_id            UUID,

    status             TEXT NOT NULL DEFAULT 'draft',

    -- A DETERMINISTIC fingerprint of (merchant, currency, total, cart), not a
    -- random nonce. A random one would let a second propose for the same cart
    -- create a second approvable obligation, and the boss would approve what
    -- looks like the same purchase twice. Deriving it from the contents means
    -- the same cart is always the same obligation.
    idempotency_key    TEXT NOT NULL,

    expires_at         TIMESTAMPTZ NOT NULL,
    claimed_at         TIMESTAMPTZ,
    submitted_at       TIMESTAMPTZ,
    confirmed_at       TIMESTAMPTZ,

    -- The receipt. An order id is the only thing that makes an 'uncertain'
    -- resolvable later (against the merchant's own history or the confirmation
    -- email) without risking a second charge, so success is defined as having
    -- one rather than as having seen a page that looked right.
    confirmation       JSONB NOT NULL DEFAULT '{}'::jsonb,
    failure            JSONB NOT NULL DEFAULT '{}'::jsonb,

    created_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at         TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    user_id            UUID,

    CONSTRAINT mem_purchase_obligations_status_check CHECK (status IN (
        'draft', 'pending_approval', 'approved', 'claimed', 'submitted',
        'awaiting_3ds', 'confirmed', 'uncertain', 'failed', 'cancelled', 'expired'
    ))
);

-- One LIVE obligation per distinct cart. Dead ones are excluded so the boss can
-- legitimately buy the same thing again after a cancellation or a failure.
CREATE UNIQUE INDEX IF NOT EXISTS uq_purchase_obligations_live_fingerprint
    ON mem_purchase_obligations(idempotency_key)
 WHERE status NOT IN ('cancelled', 'expired', 'failed');

-- Hot query: the claim, which reads by id and status.
CREATE INDEX IF NOT EXISTS idx_purchase_obligations_status
    ON mem_purchase_obligations(status, created_at DESC);

-- The boot sweep: submitted rows with no confirmation are the ones that need
-- resolving to 'uncertain' after a crash.
CREATE INDEX IF NOT EXISTS idx_purchase_obligations_in_flight
    ON mem_purchase_obligations(submitted_at)
 WHERE status = 'submitted';

CREATE INDEX IF NOT EXISTS idx_purchase_obligations_session
    ON mem_purchase_obligations(session_id, created_at DESC);

COMMENT ON COLUMN mem_purchase_obligations.idempotency_key IS
    'sha256 over merchant_host, currency, total_cents and the canonical cart. Deterministic on purpose: proposing the same cart twice must return the SAME obligation, never a second approvable one.';

COMMENT ON COLUMN mem_purchase_obligations.submitted_at IS
    'Stamped BEFORE the charge-bearing click, never after. A crash between click and confirmation must read as uncertain, not as never-happened, because the repair for never-happened is a second charge.';

COMMENT ON COLUMN mem_purchase_obligations.payment_origins IS
    'Origins allowed to receive card data. The fill boundary refuses any frame outside this list, which is what stops a card being typed into a page that merely resembles the checkout.';

COMMIT;
