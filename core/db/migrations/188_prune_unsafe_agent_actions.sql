-- 188_prune_unsafe_agent_actions.sql
--
-- Removes three action-vocabulary rows the agent registered for itself in
-- mem_action_schemas that should never have been executable. mem_act is the
-- only reader of this table, so removing a row removes an action verb and
-- nothing else. No user data is touched.
--
--   mem_surface_items / stash_draft_text  (set_null on `body`)
--     Registered as "temporarily store the drafted reply text in the item
--     body". Its actual operation blanks that column. One call away from
--     erasing the captured email body off a surfaced follow-up. Go now rejects
--     set_null on any non-marker column (core/internal/tools/mem_substrate.go,
--     guardSetNull), so this row is already inert; it is deleted so nobody has
--     to rediscover why it is there and inert.
--
--   mem_followups / noop                  (set_status status='open')
--     Description, verbatim: "placeholder". A no-op verb whose only effect is
--     to make a failed action look like a successful one.
--
--   mem_memories / drafted                (set_status status='drafted')
--     Described as "mark trust queue rows as drafted" but registered against
--     mem_memories. Wrong table; zero rows ever carried the status.
--
-- Undo, should any of these turn out to be wanted:
--   INSERT INTO mem_action_schemas (table_name,action_name,op,column_name,value,description,source)
--   VALUES ('mem_surface_items','stash_draft_text','set_null','body',NULL,'…','agent'),
--          ('mem_followups','noop','set_status','status','open','placeholder','agent'),
--          ('mem_memories','drafted','set_status','status','drafted','Mark trust queue rows as drafted','agent');

BEGIN;

DELETE FROM mem_action_schemas
 WHERE (table_name, action_name) IN (
   ('mem_surface_items', 'stash_draft_text'),
   ('mem_followups',     'noop'),
   ('mem_memories',      'drafted')
 );

COMMIT;
