-- 044_seed_autoskill_repair.sql
--
-- Seed the default AutoSkill repair recipe. The deterministic detectors
-- notice failures and drift; this SKILL.md carries the cognition for
-- deciding whether the right response is skill_optimize, skill_propose,
-- procedural memory, or an infra/code proposal.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'autoskill-repair',
  'Turn a failing skill, repeated tool error, skill drift, or approved fix-this finding into an evidence-backed skill update, new skill proposal, procedural memory, or infra proposal.',
  'low',
  '[]'::jsonb,
  '["autoskill repair","repair this skill","fix this finding","approve and fix","self improve from failure","skill keeps failing","repair failing workflow"]'::jsonb,
  '[{"name":"finding","type":"string","required":false,"doc":"curiosity question, heartbeat finding, failed skill name, or repeated tool error to repair"}]'::jsonb,
  '[{"name":"proposal_id","type":"string","doc":"skill proposal id when a skill proposal or optimization is created"},{"name":"action","type":"string","doc":"skill_optimize, skill_propose, procedural_memory, code_proposal, surface_item, or no_action"}]'::jsonb,
  0.92,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'autoskill-repair',
  '1.0.0',
  $skill$---
name: autoskill-repair
version: "1.0.0"
description: Turn a failing skill, repeated tool error, skill drift, or approved fix-this finding into an evidence-backed skill update, new skill proposal, procedural memory, or infra proposal.
trigger_phrases:
  - autoskill repair
  - repair this skill
  - fix this finding
  - approve and fix
  - self improve from failure
  - skill keeps failing
  - repair failing workflow
inputs:
  - name: finding
    type: string
    required: false
    doc: curiosity question, heartbeat finding, failed skill name, or repeated tool error to repair
outputs:
  - name: proposal_id
    type: string
    doc: skill proposal id when a skill proposal or optimization is created
  - name: action
    type: string
    doc: skill_optimize, skill_propose, procedural_memory, code_proposal, surface_item, or no_action
risk_level: low
network_egress: none
confidence: 0.92
---

# AutoSkill repair

The boss approved a fix-this item, a skill is repeatedly failing, or the
heartbeat found skill drift. Your job is to turn real evidence into the right
durable improvement. Do not just describe what should happen. End by calling
the tool that creates the reviewable artifact.

## 1. Gather evidence first

Use the finding text plus memory and trace tools to collect:

- Related `mem_skill_runs` for the named skill or workflow.
- Related `mem_predictions`, especially high-surprise or `healer_emission`
  rows.
- Relevant `mem_observations` around failures, user corrections, and tool
  errors.
- Related reflections and lessons from memory search.
- The current open candidate with `skill_proposal_get` when an existing skill
  is involved.

If a source id is available, inspect that exact row. If only text is available,
use `memory_recall`, `mem_list`, `trace_recent`, or `system_map` to find the
closest evidence. Never repair from vibes alone.

## 2. Choose the correct durable form

Pick exactly one:

- Existing skill is wrong or incomplete -> call `skill_optimize`.
- Repeated successful recipe has no active skill -> call `skill_propose`.
- Lesson is one sentence of behavior -> write procedural memory with the memory
  tools.
- Broken infra, schema, deploy, or code path -> create/surface a code or infra
  proposal. Do not hide infra work inside a SKILL.md.
- Evidence is too thin -> no proposal. Explain what evidence is missing.

## 3. Existing skill repair path

When repairing an existing skill:

1. Identify the parent skill name.
2. Call `skill_proposal_get({parent_skill})`.
3. Draft a full replacement `SKILL.md` that preserves the current skill,
   includes any pending candidate edits, and adds the new repair.
4. Keep the same skill `name`.
5. Bump the version appropriately.
6. Call `skill_optimize({parent_skill, new_skill_md, reasoning})`.

The tool merges into the one active draft for that parent skill. Running this
recipe twice should increment `revision` and append `changes_log`, not create a
duplicate candidate.

## 4. New skill path

When the evidence shows a repeatable recipe with no active skill:

1. Check `skills_list` first to avoid duplicates.
2. Draft one focused `SKILL.md`.
3. Call `skill_propose`.

One recipe per proposal. Do not bundle unrelated repairs.

## 5. Procedural-memory path

When the lesson is a durable one-line rule, use memory tools to write it as a
procedural memory. Examples:

- "When a dashboard finding has no source ids, inspect source_tag before
  proposing a repair."
- "Do not re-emit repeated_tool_error findings after the boss dismissed that
  pattern unless the tool or failure mode changed."

Do not create a full skill for a one-sentence habit.

## 6. Infra path

When the evidence points to missing substrate, schema, deploy state, or broken
code, create a reviewable code/infra proposal or surface item. A SKILL.md cannot
fix missing Go infrastructure.

## 7. Report

Reply with:

- action taken
- proposal id or memory id when one was created
- one sentence of evidence

Do not paste the full SKILL.md body into chat. The proposal card shows it.

## Hard rules

- Evidence first, artifact second.
- Never auto-install proposals.
- Never create duplicate open drafts for an existing parent skill.
- Never put credentials, connected account ids, or run-specific data into a
  reusable skill body.
- Never solve infrastructure failures by pretending they are skill prompt
  failures.
$skill$,
  '',
  0.92,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('autoskill-repair', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
