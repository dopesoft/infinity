-- 140_workflow_inputs.sql - declared input schema on a workflow definition.
--
-- A workflow run is parameterized by `input` (mem_workflow_runs.input, templated
-- into steps via {{input.KEY}}). Until now nothing declared WHICH inputs a
-- definition needs - so the Workflows tab couldn't render a "fill these in"
-- form, and a chat trigger couldn't know what to ask for. This adds a declared
-- schema to the definition:
--
--   inputs: [ { "key":"youtube_url", "label":"YouTube URL", "type":"text",
--              "required":true },
--             { "key":"aspect", "type":"enum", "options":["9:16","1:1","16:9"],
--              "default":"9:16" } ]
--
-- type is text | enum | number. The Workflows-tab Run form builds itself from
-- this; the agent reads it to ask for missing inputs in chat before
-- workflow_run. Additive + defaulted, so every existing definition is untouched
-- (empty schema = "no inputs needed, run directly").

BEGIN;

ALTER TABLE mem_workflows
    ADD COLUMN IF NOT EXISTS inputs JSONB NOT NULL DEFAULT '[]'::jsonb;

COMMIT;
