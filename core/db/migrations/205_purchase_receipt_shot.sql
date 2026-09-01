-- 205: keep the picture of the confirmation page.
--
-- WHY THIS WAS A BUG WORTH A MIGRATION
--
-- BrowserExecutor already screenshots the merchant's confirmation page, with
-- the payment fields masked, immediately after the order lands. It set the
-- bytes on the Receipt struct... and the only caller then wrote order_id, url,
-- total and currency into `confirmation` and dropped the image on the floor.
-- The proof of what was bought was being captured and thrown away every time.
--
-- That is the built-but-not-wired failure in CLAUDE.md Rule #1c: the expensive
-- half shipped, the one line that keeps it did not, and nothing looked broken.
--
-- BYTEA, NOT INSIDE THE JSONB
--
-- A PNG is 50-500KB. Inside `confirmation` it would be base64 in a jsonb column
-- that every status read selects, so every list query would drag the image
-- along. A separate BYTEA column is not selected unless something asks for it,
-- which is the same reason mem_surface_items keeps cached bodies out of its
-- list queries.

BEGIN;

ALTER TABLE mem_purchase_obligations
    ADD COLUMN IF NOT EXISTS confirmation_shot BYTEA;

COMMENT ON COLUMN mem_purchase_obligations.confirmation_shot IS
    'PNG of the merchant confirmation page, payment fields masked before capture. Write-mostly: never selected by the status/list queries, only by the endpoint that shows one receipt.';

COMMIT;
