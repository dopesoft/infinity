-- 090_normalize_gmail_surface_ids.sql — migrate existing Follow-ups rows to the
-- reconnect-proof external_id format.
--
-- Pairs with the store-level normalization in surface/store.go (every NEW
-- write now canonicalizes gmail:<account>:<msgid> → gmail:<msgid>). This
-- back-fills the rows written before that change so a re-surface after deploy
-- updates the existing row instead of creating a second one under the new key.
--
-- NON-DESTRUCTIVE BY DESIGN: this only UPDATEs external_id. It never deletes a
-- row. To stay collision-safe it rewrites at most one row per (source, msgid)
-- — the most-recently-updated — and skips any rewrite that would clash with an
-- already-canonical row. The rare leftover legacy sibling is harmless and ages
-- out as the boss resolves it. The original Composio account id is preserved in
-- metadata.account for provenance.
--
-- Idempotent: re-running finds no 3-part gmail ids left to rewrite.

BEGIN;

WITH ranked AS (
  SELECT id, source,
         'gmail:' || split_part(external_id, ':', 3) AS new_ext,
         split_part(external_id, ':', 2)             AS acct,
         row_number() OVER (
           PARTITION BY source, split_part(external_id, ':', 3)
           ORDER BY updated_at DESC
         ) AS rn
  FROM mem_surface_items
  WHERE external_id LIKE 'gmail:%'
    AND array_length(string_to_array(external_id, ':'), 1) = 3
    AND split_part(external_id, ':', 3) <> ''
)
UPDATE mem_surface_items t
SET external_id = r.new_ext,
    metadata    = CASE WHEN t.metadata ? 'account' THEN t.metadata
                       ELSE t.metadata || jsonb_build_object('account', r.acct) END,
    updated_at  = NOW()
FROM ranked r
WHERE t.id = r.id
  AND r.rn = 1
  AND NOT EXISTS (
    SELECT 1 FROM mem_surface_items x
    WHERE x.source = r.source AND x.external_id = r.new_ext AND x.id <> t.id
  );

COMMIT;
