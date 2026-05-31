-- 084_surface_item_actions.sql
--
-- Surface return-path: give every generic surface item an optional set of
-- boss-tappable ACTIONS. Tapping one on the dashboard fires
-- POST /api/surface/action, which seeds an autonomous agent turn prompted
-- with the action's intent + the item's context - closing the loop from
-- "agent surfaces an item" to "boss taps → agent acts on it".
--
-- Generic + schema-driven (Rule #1): any producer attaches actions, the
-- dashboard renders them, the endpoint runs them. No per-action column, no
-- per-action Go, no per-action widget. The shape of each action is:
--   { "id": "draft_reply", "label": "Draft reply",
--     "intent": "Draft a reply to this email and save it as a draft.",
--     "style": "primary" }
--
-- mem_surface_items is already in the supabase_realtime publication, so the
-- new column replicates with the row - no publication change needed.

ALTER TABLE mem_surface_items
  ADD COLUMN IF NOT EXISTS actions jsonb NOT NULL DEFAULT '[]'::jsonb;
