-- 131_seed_frame_mandate_skill.sql — seed the JUDGMENT-only "frame-the-mandate"
-- skill (mirrors 023_seed_self_improve_skill.sql).
--
-- Rule #1b split: the MECHANICS of a Mandate live in Go and cannot be dropped —
-- mandate_open/mandate_check/mandate_close, the "can't close until every
-- criterion passes" gate, and the "high_stakes needs a passing Crosscheck"
-- gate are all enforced in mandate.Store. This skill carries only the JUDGMENT
-- the model is actually for: deciding WHEN a task deserves a Mandate, HOW to
-- decompose it into binary criteria, and WHETHER it's high-stakes. Delete any
-- line of this skill and no feature breaks — that's the test it passes.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'frame-the-mandate',
  'For non-trivial or "make sure it''s right" work, turn the ask into a Mandate: a short definition of done with binary, testable acceptance criteria, then work until every criterion passes. Decide when the task is high-stakes (needs a second model to verify before done).',
  'low',
  '[]'::jsonb,
  '["frame the mandate","define done","what does done look like","make sure this is done right","set acceptance criteria","open a mandate","make sure it''s actually finished","do this properly and verify"]'::jsonb,
  '[{"name":"task","type":"string","required":false,"doc":"the work to frame — usually already the boss''s message in this conversation, so you rarely pass it explicitly"}]'::jsonb,
  '[{"name":"mandate_id","type":"string","doc":"the opened mandate''s id"},{"name":"criteria","type":"array","doc":"the binary acceptance criteria you committed to"}]'::jsonb,
  0.9,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'frame-the-mandate',
  '1.0.0',
  $skill$---
name: frame-the-mandate
description: Turn a non-trivial task into a verifiable definition of done.
---

# Frame the Mandate

When the boss asks for something that is more than a one-liner — anything
multi-step, anything where "did it actually work?" matters, anything phrased
like "make sure", "properly", "for real this time" — don't just start doing it.
First decide what *done* means, out loud, as a contract you can be held to.

This skill is pure judgment. The machinery (opening the mandate, checking
criteria, the rule that you cannot mark it done until every criterion passes,
the rule that a high-stakes mandate needs a second model to verify) is handled
for you by the `mandate_open`, `mandate_check`, and `mandate_close` tools. Your
job is the thinking.

## When to open a Mandate

Open one when the task has a real definition of done that could plausibly be
gotten *wrong*: a deliverable, a fix that has to actually fix it, a multi-step
job, anything the boss would be annoyed to discover was only half-done. Skip it
for trivial chat, a single fact, a quick edit — a Mandate there is overhead.

If a Gauge read for this turn came back `deep`, that's your tell: open one.

## How to write the criteria (this is the whole skill)

Each criterion must be **binary** — true or false, no "mostly". A criterion you
can't check off with evidence is a wish, not a criterion. Rewrite it until you
could prove it.

- Bad: "the deck looks good." → Good: "all 12 slides export to PDF with no
  overflow", "the revenue figures match the model to the dollar", "it's shared
  with kai@dopesoft.io with comment access".
- Bad: "tests pass." → Good: "`go build ./...` exits 0", "the new endpoint
  returns 200 for a valid request and 401 without a token".

Cover the things that actually fail: the output exists, it's correct, it's in
the right place, it's been verified — not just "I did the steps".

## When to flag high_stakes

Set `high_stakes: true` when being wrong is expensive or hard to undo — money
moves, something is sent or published, data is deleted, the boss is going to
act on the result without re-checking it himself. A high-stakes mandate will
not let you close it until you run `mandate_verify`, which has a *different* AI
model audit your result against the criteria. That's a feature: on the work
that matters, you don't get to grade your own homework.

## The loop

1. `mandate_open` with the title, a one-line summary, the binary criteria, and
   high_stakes.
2. Do the work. As each criterion becomes true, `mandate_check` it with the
   concrete evidence (the command output, the URL, the figure).
3. For high-stakes work, `mandate_verify` before closing.
4. `mandate_close`. If it refuses, a criterion isn't actually satisfied — go
   finish it, don't argue with the gate.

Then tell the boss it's done in plain English, and say what *done* meant.
$skill$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('frame-the-mandate', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
