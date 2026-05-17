-- 023_seed_self_improve_skill.sql - seed the self-improve-from-finding skill.
--
-- This is a default skill Infinity ships with - the closed-loop recipe the
-- agent follows when the boss approves a heartbeat finding (diagnose → fix →
-- verify → confirm). It is NOT a project scaffold, so it does not ride the
-- embed → MaterializeScaffoldSkills → disk path the scaffold-* skills use.
--
-- Instead it's seeded straight into the durable store (mem_skills +
-- mem_skill_versions + mem_skill_active) - the same tables skill_create and
-- Voyager write to. On boot, MaterializeActiveSkills derives it to the
-- on-disk skills root and Registry.Reload loads it, so it's live immediately
-- and survives Railway's ephemeral filesystem. Editing it later is a normal
-- runtime path (skill_create with a bumped version, Voyager evolution, or the
-- Skills tab) - no core rebuild required.
--
-- ON CONFLICT DO NOTHING throughout: idempotent, and it never clobbers a
-- version the agent or Voyager has since evolved.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'self-improve-from-finding',
  'When the boss approves a heartbeat finding, diagnose what went wrong and apply a durable fix to how you operate - rework a skill, write a procedural memory rule, resolve a contradiction - then confirm exactly what changed.',
  'low',
  '[]'::jsonb,
  '["self improve from finding","apply a durable fix","fix this finding","apply the fix and confirm","fix yourself so it doesn''t recur","approved go ahead and fix this","rework the skill that caused this"]'::jsonb,
  '[{"name":"finding","type":"string","required":false,"doc":"the approved finding being acted on - usually already in this conversation as the heartbeat card or the seeded \"From dashboard\" context block, so you rarely pass it explicitly"}]'::jsonb,
  '[{"name":"change_summary","type":"string","doc":"what changed, the artifact it lives in (skill name + version, or memory id), and how it was verified"}]'::jsonb,
  0.9,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'self-improve-from-finding',
  '1.0.0',
  $skill$---
name: self-improve-from-finding
version: "1.0.0"
description: When the boss approves a heartbeat finding, diagnose what went wrong and apply a durable fix to how you operate - rework a skill, write a procedural memory rule, resolve a contradiction - then confirm exactly what changed.
trigger_phrases:
  - self improve from finding
  - apply a durable fix
  - fix this finding
  - apply the fix and confirm
  - fix yourself so it doesn't recur
  - approved go ahead and fix this
  - rework the skill that caused this
inputs:
  - name: finding
    type: string
    required: false
    doc: the approved finding being acted on - usually already in this conversation as the heartbeat card or the seeded "From dashboard" context block, so you rarely pass it explicitly
outputs:
  - name: change_summary
    type: string
    doc: what changed, the artifact it lives in (skill name + version, or memory id), and how it was verified
risk_level: low
network_egress: none
confidence: 0.9
---

# Self-improve from an approved finding

The boss approved a finding your heartbeat raised - a prediction that missed, two
memories that disagree, a tool that behaved unexpectedly. Your job now is to turn
that signal into a **durable change to how you operate**, then report back. This
is the closed loop: notice → approve → fix → confirm.

The finding is already in this conversation - the heartbeat card, the seeded
"From dashboard" context block, or the message just above. Read it first.

## 1. Diagnose - what actually went wrong

Don't skip this. Pull the evidence before you touch anything.

- **Prediction miss** ("tool X returned something unexpected"): compare what you
  expected against what actually came back. The mismatch is the lesson - usually
  a wrong assumption about a tool's response shape, an API's behaviour, or a
  parameter.
- **Contradiction** (two memories disagree): `memory_recall` both, work out which
  one is current and correct, and which should be superseded.
- **Uncovered mention** (the boss referenced something repeatedly, nothing
  captured): the fix is almost always a memory, not a skill.
- Ground yourself with `memory_search` / `memory_recall`. Use `skills_list` /
  `skills_discover` to find the skill involved, if any, and read its body.

State the root cause in one sentence before moving on. If you can't, keep
digging - a fix without a diagnosis is a guess.

## 2. Choose the smallest durable fix

Pick the **lightest** change that actually prevents recurrence. In rough order of
preference:

- **Rework an existing skill** - if a skill drove the bad behaviour, fix its
  body. Re-author it with `skill_create` using the **same `name` and a bumped
  `version`** so the corrected recipe replaces the old one. This is the best
  outcome: the judgment lives in the skill, visible and improvable.
- **Write a procedural memory rule** - if there's no skill but there is a durable
  "always do X / never assume Y" lesson, `memory_write` it with
  `tier: "procedural"`. Procedural memories are injected into your system prompt
  on future turns, so the lesson applies everywhere.
- **Resolve a contradiction** - supersede the wrong memory so the graph stops
  serving a stale fact.
- **Create a new skill** - only when the finding exposes a whole missing
  capability, not just a tweak.

Do **not** make a sweeping change, rewrite things that already worked, or "fix"
something you haven't diagnosed. Minimal and targeted.

## 3. Apply it

Use your own tools - `skill_create`, `memory_write`, `memory_recall`,
`memory_search`. You already have everything you need; you don't need the boss to
run anything. If the only safe fix genuinely requires a code change to the
Infinity repo itself, that's out of scope here - say so and propose it as the
smallest next step rather than half-applying something.

## 4. Verify

A fix you can't check is a claim, not a fix. Re-ground against the original
evidence:

- Reworked a skill → re-read the new body. Does it now handle the case that
  surprised you?
- Wrote a memory rule → `memory_recall` it and confirm it says what you intended.
- Resolved a contradiction → confirm the stale memory is superseded and the
  correct one stands.

## 5. Confirm to the boss

Reply in this conversation. Be specific and honest:

- **What you changed** - name the artifact: the skill name + new version, or the
  memory id.
- **Why** - the one-sentence root cause from step 1.
- **How you verified it.**
- If you could only partially fix it, say exactly what's left and the smallest
  next step.

## Hard rules

- Never claim a fix you didn't actually apply. If a tool call failed, report the
  failure - don't paper over it.
- Always cite the artifact. "I reworked the `composio-search` skill to v1.1.0" -
  not "I improved how I handle that."
- One finding, one focused fix. Don't bundle unrelated changes.
- If you genuinely can't fix it safely, that is a valid outcome - diagnose it,
  explain what's blocking you, and propose the next step. A clear "here's what's
  in the way" beats a vague "done."
$skill$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('self-improve-from-finding', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
