-- 068_seed_coordinate_agent_team_skill.sql
--
-- Default recipe for deciding when and how to use the generic agent-team
-- substrate. Go owns only the durable team/tool machinery; this SKILL.md owns
-- the judgment.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'coordinate-agent-team',
  'Create and supervise a temporary team of specialist agents for complex, wide, artifact-heavy work such as multimedia story packages, competing debugging hypotheses, cross-layer implementation, research synthesis, or critique/revision loops.',
  'medium',
  '[]'::jsonb,
  '["use an agent team","spin up a team","parallel agents","swarm this","full tilt","make a multimedia package","story package with images","coordinate specialists"]'::jsonb,
  '[{"name":"goal","type":"string","doc":"the overall goal to decompose"},{"name":"mode","type":"string","doc":"optional: multimedia, research, code, connector, critic, general"}]'::jsonb,
  '[{"name":"team_id","type":"string"},{"name":"final_synthesis","type":"string"},{"name":"artifacts","type":"array"}]'::jsonb,
  0.9,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'coordinate-agent-team',
  '1.0.0',
  $skill$---
name: coordinate-agent-team
version: "1.0.0"
description: Create and supervise a temporary team of specialist agents for complex, wide, artifact-heavy work such as multimedia story packages, competing debugging hypotheses, cross-layer implementation, research synthesis, or critique/revision loops.
trigger_phrases:
  - use an agent team
  - spin up a team
  - parallel agents
  - swarm this
  - full tilt
  - make a multimedia package
  - story package with images
  - coordinate specialists
inputs:
  - name: goal
    type: string
  - name: mode
    type: string
    required: false
outputs:
  - name: team_id
    type: string
  - name: final_synthesis
    type: string
  - name: artifacts
    type: array
risk_level: medium
network_egress: none
confidence: 0.9
---

# Coordinate an agent team

Use `agent_team_start` when the boss asks for work that is wide, ambiguous,
artifact-heavy, or benefits from independent specialist perspectives. This is
not coding-specific. It is the general Infinity organization primitive.

Good uses:

- Multimedia packages: story, narration script, image direction, image prompts,
  generated image assets, QA/continuity review.
- Competing hypotheses: each worker verifies a different cause with tools, then
  you synthesize the evidence.
- Cross-domain workflows: research + writing + code + review + final packaging.
- Critique loops: one or more workers produce, another attacks gaps, the lead
  revises the final answer.

Bad uses:

- Simple factual answers.
- One short edit.
- Sequential tasks where worker B cannot start until worker A finishes.
- Cases where the missing piece is clearly a tool/API, not more cognition.

## Team design

Create concrete, non-overlapping roles. Prefer specialists over generic
"helper" agents.

For a multimedia story package, use roles like:

- Story Architect: plot, structure, continuity rules.
- Script Writer: final prose and narration script.
- Image Director: visual bible, shot list, character consistency.
- Prompt Engineer: production-ready image prompts.
- Image Generator: call image/artifact tools if available.
- Critic/QA: age fit, continuity, missing scenes, repetition, safety.

For debugging or research, use roles like:

- Hypothesis A Verifier
- Hypothesis B Verifier
- Logs/Deploy Inspector
- Schema/Migration Inspector
- Critic/Synthesizer

## Execution

1. Decide whether a team is justified. If Settings requires confirmation,
   ask the boss first.
2. Call `agent_team_start` with:
   - `goal`: the full self-contained brief.
   - `reason`: why parallel agents help here.
   - `members`: role/task/capability objects.
3. Read the returned member summaries.
4. Synthesize. Do not paste every worker transcript. Resolve contradictions,
   name the useful artifacts, and clearly say what is final vs draft.

## Hard rules

- Do not spawn a team just to look impressive. If one agent can do it cleanly,
  do it directly.
- Keep roles specific. "Researcher 1/2/3" is weak unless each has a separate
  hypothesis or source lane.
- Treat worker output as evidence, not truth. The lead is responsible for the
  final judgment.
- For external sends, destructive actions, or irreversible operations, Trust
  approval still applies.
$skill$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('coordinate-agent-team', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
