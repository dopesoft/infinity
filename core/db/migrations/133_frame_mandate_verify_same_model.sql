-- 133_frame_mandate_verify_same_model.sql — correct the frame-the-mandate skill
-- text. Migration 131 seeded prose saying verification uses "a different AI
-- model". It does NOT: verification runs on the boss's OWN selected brain (his
-- ChatGPT subscription), with a fresh adversarial persona for independence — no
-- other vendor, no separately-billed API. 131 is already applied and
-- ON CONFLICT DO NOTHING won't re-seed, so update the live row here.

BEGIN;

UPDATE mem_skill_versions
   SET skill_md = replace(
         skill_md,
         'A high-stakes mandate will
not let you close it until you run `mandate_verify`, which has a *different* AI
model audit your result against the criteria. That''s a feature: on the work
that matters, you don''t get to grade your own homework.',
         'A high-stakes mandate will
not let you close it until you run `mandate_verify`, which has a fresh,
independent, skeptical pass — on the boss''s own model — re-check your result
against the criteria. That''s a feature: on the work that matters, you don''t get
to wave your own work through.'
       )
 WHERE skill_name = 'frame-the-mandate' AND version = '1.0.0';

COMMIT;
