-- 037_seed_skill_self_authoring.sql - seed the two skills the agent uses
-- to author and evolve its own skill library.
--
-- The deterministic SkillAuthoringChecklist emits two kinds of heartbeat
-- finding:
--
--   skill_opportunity   the agent has performed the same 3-tool recipe 3+
--                       times in 7d and no active skill maps to it.
--   skill_drift         the agent ran an installed skill, the boss steered
--                       a divergent (successful) path, and the same
--                       divergence has recurred 2+ times.
--
-- When the boss taps "Approve & fix" on either finding in /lab, the
-- self-improve-from-finding skill (seed migration 023) routes the work
-- to one of the two skills below, depending on the finding kind. Each
-- skill teaches the agent the specific recipe to draft a clean SKILL.md
-- and call the matching tool (skill_propose for new, skill_optimize for
-- evolved).
--
-- Cognition lives in the skill body. Go is pure substrate.

BEGIN;

-- ---------------------------------------------------------------------------
-- 1. propose-skill-from-pattern
-- ---------------------------------------------------------------------------

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'propose-skill-from-pattern',
  'When a skill_opportunity finding fires, read the evidence observations, draft a clean SKILL.md, and call skill_propose so the boss can install the new skill inline in chat.',
  'low',
  '[]'::jsonb,
  '["propose skill from pattern","crystallize this pattern into a skill","make a skill for what i keep doing","author a skill from this finding","skill opportunity approved"]'::jsonb,
  '[{"name":"finding","type":"string","required":false,"doc":"the skill_opportunity finding being acted on - usually already in this conversation via the seeded From dashboard block, so you rarely pass it explicitly"}]'::jsonb,
  '[{"name":"proposal_id","type":"string","doc":"the mem_skill_proposals row id returned by skill_propose"}]'::jsonb,
  0.9,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'propose-skill-from-pattern',
  '1.0.0',
  $skill$---
name: propose-skill-from-pattern
version: "1.0.0"
description: When a skill_opportunity finding fires, read the evidence observations, draft a clean SKILL.md, and call skill_propose so the boss can install the new skill inline in chat.
trigger_phrases:
  - propose skill from pattern
  - crystallize this pattern into a skill
  - make a skill for what i keep doing
  - author a skill from this finding
  - skill opportunity approved
inputs:
  - name: finding
    type: string
    required: false
    doc: the skill_opportunity finding being acted on - usually already in this conversation via the seeded From dashboard block, so you rarely pass it explicitly
outputs:
  - name: proposal_id
    type: string
    doc: the mem_skill_proposals row id returned by skill_propose
risk_level: low
network_egress: none
confidence: 0.9
---

# Propose a new skill from a repeated pattern

The boss approved a `skill_opportunity` finding. The heartbeat noticed you've run
the same multi-step recipe at least three times in the last week, and there's no
active skill that captures it. Your job is to turn that pattern into a real skill
the boss can install inline.

## 1. Read the evidence

The finding's `Detail` carries the tool sequence signature and the source
observation ids. Pull them up with `memory_recall` for each id, or `mem_list` on
`mem_observations` filtering by the ids. Look at:

- What tools fire, in what order, with what arguments.
- The surrounding turns (`mem_observations` PreToolUse / PostToolUse). What was
  the boss trying to accomplish each time?
- Any variation across the repetitions. The skill should generalize, not
  hardcode one run's specifics.

## 2. Draft a clean SKILL.md

Write a body that captures:

- **name** - short, kebab-case, descriptive. Not "do-the-thing-fast", say what it
  accomplishes ("triage-inbox", "summarize-and-save").
- **description** - one sentence the agent uses to match natural-language asks.
- **trigger_phrases** - 3-7 short ways the boss might ask for this.
- **inputs / outputs** - real arguments, with docs. Default optional when the
  agent can infer from context.
- **risk_level** - low unless the recipe writes to external systems.
- **body** - the actual steps. Use the same numbered-section structure as
  existing default skills. Be specific about which tools to call in which order,
  with what parameters. The agent reading this later needs no further guesswork.

## 3. Propose it

Call `skill_propose` with the full SKILL.md body. The tool inserts a row into
`mem_skill_proposals` (status='candidate'), and the SkillProposalCard component
in /live renders the proposal inline with Approve & install / Edit body /
Dismiss actions. The boss reviews it without leaving chat.

## 4. Confirm

Reply with one short sentence naming the proposed skill and the proposal id.
Don't restate the body - the card shows it. Don't apologize for not auto-
installing - that's the design.

## Hard rules

- Don't propose a skill that duplicates an existing active one. Check
  `skills_list` first. If you find a close match, use `evolve-skill-from-deviation`
  instead.
- Don't bundle two patterns into one skill. One recipe per proposal.
- Don't include credentials, account ids, or run-specific data in the body. The
  skill is a recipe, not a script.
- If the evidence is genuinely thin (only 3 weak runs, all very different), say
  so and don't propose. A noisy skill is worse than no skill.
$skill$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('propose-skill-from-pattern', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

-- ---------------------------------------------------------------------------
-- 2. evolve-skill-from-deviation
-- ---------------------------------------------------------------------------

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'evolve-skill-from-deviation',
  'When a skill_drift finding fires, compare the installed skill body to the divergent (successful) sequence, draft an updated SKILL.md, and call skill_optimize so the boss can review the diff inline in chat.',
  'low',
  '[]'::jsonb,
  '["evolve skill from deviation","update skill from drift","optimize skill from divergence","skill drift approved","improve the skill based on what i did"]'::jsonb,
  '[{"name":"finding","type":"string","required":false,"doc":"the skill_drift finding being acted on - usually already in this conversation via the seeded From dashboard block, so you rarely pass it explicitly"}]'::jsonb,
  '[{"name":"proposal_id","type":"string","doc":"the mem_skill_proposals row id returned by skill_optimize"}]'::jsonb,
  0.9,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'evolve-skill-from-deviation',
  '1.0.0',
  $skill$---
name: evolve-skill-from-deviation
version: "1.0.0"
description: When a skill_drift finding fires, compare the installed skill body to the divergent (successful) sequence, draft an updated SKILL.md, and call skill_optimize so the boss can review the diff inline in chat.
trigger_phrases:
  - evolve skill from deviation
  - update skill from drift
  - optimize skill from divergence
  - skill drift approved
  - improve the skill based on what i did
inputs:
  - name: finding
    type: string
    required: false
    doc: the skill_drift finding being acted on - usually already in this conversation via the seeded From dashboard block, so you rarely pass it explicitly
outputs:
  - name: proposal_id
    type: string
    doc: the mem_skill_proposals row id returned by skill_optimize
risk_level: low
network_egress: none
confidence: 0.9
---

# Evolve a skill based on a proven divergence

The boss approved a `skill_drift` finding. You ran an installed skill, the boss
steered a divergent path, and the divergence produced a better result at least
twice. Your job is to fold the proven improvement into the skill body so future
runs follow the better path automatically.

## 1. Read both sides

- Pull the installed skill body with `skills_list` (find the skill name in the
  finding's Detail), then read the full SKILL.md.
- Pull the divergent observations from `mem_observations` using the ids the
  finding carries. Note exactly what tools the boss called, in what order, with
  what arguments, and what the results looked like.
- Identify the SPECIFIC step in the existing body that diverged. The whole
  skill rarely needs a rewrite; usually one section is wrong or incomplete.

## 2. Draft the updated body

- Keep the same `name` so the optimization replaces the existing skill rather
  than forking it.
- Bump the `version` to the next patch (1.0.0 → 1.0.1) for small tweaks, minor
  (1.0.0 → 1.1.0) when adding new behavior, major (1.0.0 → 2.0.0) when changing
  inputs or outputs.
- Edit only the section that needs to change. Keep everything else intact. A
  surgical diff is easier for the boss to review and reason about.
- Add a one-line comment in the body (in prose, inside the relevant section)
  explaining what changed and why, drawing from the evidence. The skill is its
  own documentation.

## 3. Propose the optimization

Call `skill_optimize` with the updated full SKILL.md AND the parent skill name.
The tool inserts a row into `mem_skill_proposals` with `parent_skill` set, and
the SkillProposalCard in /live renders it with a "vs current" diff so the boss
can see exactly what's changing.

## 4. Confirm

Reply with one short sentence naming the parent skill, the new version, and the
proposal id. Don't restate the diff - the card shows it. Don't apologize for not
auto-applying - the boss reviews each evolution.

## Hard rules

- Never change `name`. If the divergence is a different recipe entirely, use
  `propose-skill-from-pattern` to create a new skill, don't shoehorn it into the
  existing one.
- Never propose an optimization that REMOVES a feature the existing skill
  provides unless the evidence specifically shows that feature breaking. Add and
  refine, don't subtract on a hunch.
- Don't bundle two unrelated improvements into one optimization. One drift, one
  focused diff.
- If the evidence is genuinely thin (only 2 short runs, mostly identical to the
  existing body), say so and don't propose. Drift detection is noisy by design.
$skill$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('evolve-skill-from-deviation', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
