-- 117_seed_planning_skill.sql - seed the plan-and-verify default skill.
--
-- Rule #1: the durable plan substrate (migration 116 + plan_tools.go) is the
-- generic CONTRACT; the COGNITION - how to decompose a task, when a step needs
-- verification, what counts as proof, how to replan - lives here, in a skill
-- body the agent reads and the Skills tab shows, NOT in a Go const. Voyager/GEPA
-- can evolve it over time, so the agent's planning gets better on its own.
--
-- Seeded straight into the durable store (mem_skills + mem_skill_versions +
-- mem_skill_active), mirroring 023_seed_self_improve_skill.sql. On boot,
-- MaterializeActiveSkills derives it to disk and Registry.Reload loads it, so
-- it's live immediately and survives Railway's ephemeral filesystem.
--
-- ON CONFLICT DO NOTHING throughout: idempotent, never clobbers an evolved
-- version.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'plan-and-verify',
  'Turn a multi-step task into a durable, verifiable plan: decompose it with plan_create, work it step by step with plan_update, and prove each step with plan_verify before calling it done. Replan on failure. This is how you stay honest and resumable on anything non-trivial.',
  'low',
  '[]'::jsonb,
  '["plan this out","make a plan","lay out the steps","break this down","plan and verify","work through this step by step","create a plan for","let''s plan this"]'::jsonb,
  '[{"name":"task","type":"string","required":false,"doc":"the multi-step task to plan - usually already described in this conversation, so you rarely pass it explicitly"}]'::jsonb,
  '[{"name":"outcome","type":"string","doc":"the finished result plus, for each step, the evidence it actually worked"}]'::jsonb,
  0.9,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'plan-and-verify',
  '1.0.0',
  $skill$---
name: plan-and-verify
version: "1.0.0"
description: Turn a multi-step task into a durable, verifiable plan - decompose it with plan_create, work it step by step with plan_update, prove each step with plan_verify before calling it done, and replan on failure.
trigger_phrases:
  - plan this out
  - make a plan
  - lay out the steps
  - break this down
  - plan and verify
  - work through this step by step
  - create a plan for
  - let's plan this
inputs:
  - name: task
    type: string
    required: false
    doc: the multi-step task to plan - usually already described in this conversation, so you rarely pass it explicitly
outputs:
  - name: outcome
    type: string
    doc: the finished result plus, for each step, the evidence it actually worked
risk_level: low
network_egress: none
confidence: 0.9
---

# Plan, then verify every step

This is the recipe for anything non-trivial: a task with three or more steps, one
that spans several tool calls, or one that could outlast this turn. It is how you
beat every reactive agent - you do not just react step to step, you hold a durable
plan, prove each step, and resume exactly where you left off after any
interruption. The plan lives in the substrate (plan_create / plan_update /
plan_verify), survives compaction and restarts, and the boss watches it live.

## 1. Decompose - lay out the plan first

Before you touch a tool, call `plan_create` with an ordered list of **concrete,
verifiable** steps. A good step names a specific action with an observable result
("Pull the last 7 days of email", "Draft the digest as a document", "Save it to
the dashboard"), not a vague phase ("research", "do the thing").

For each step decide two flags:

- **verify_required: true** when the step has a checkable result - a file should
  exist, a build should pass, an API should return 200, a row should be written,
  the output should match what was asked. Most real work steps qualify. If you
  cannot say how you would prove a step worked, the step is too vague - split it.
- **is_checkpoint: true** when you should stop for the boss before continuing -
  anything irreversible, costly, or a fork where you genuinely need their call
  (sending something external, spending money, deleting, a "which direction"
  decision). When the plan reaches a checkpoint it pauses and surfaces to the
  dashboard for the boss to approve or skip. Do not checkpoint routine steps - that
  just nags.

Keep it tight. Three to eight steps is usual. A twenty-step plan is a sign you are
listing keystrokes, not steps.

## 2. Work the plan - one step at a time

- `plan_update` the step you are starting to **in_progress** (exactly one in
  flight at a time). That books a live spinner the boss sees.
- Do the actual work with your real tools.
- When the step is genuinely finished, `plan_update` it to **done** with a one-line
  `result_summary` of what happened. For a `verify_required` step you must verify
  first (next section) - the substrate will refuse `done` without a passing
  verdict, so do not fight it, just verify.
- Use **skipped** for a step that turned out unnecessary, **failed** for one that
  cannot be completed (say why in result_summary).

## 3. Verify - prove it, do not assume it

For every `verify_required` step, before marking it done call `plan_verify` with:

- **verdict: pass** and the concrete **evidence** you checked - the literal proof,
  not a feeling. "go build ./... exited 0". "GET /health returned 200". "file
  dist/app.js exists, 4.2kb". "the document has all 7 sections". Cheap to check,
  worth everything: this is the line between a trustworthy agent and a confidently
  wrong one.
- **verdict: fail** with what you found when it did not work. The step flips to
  **blocked** and you replan it (next section).

A fix you cannot point to proof for is a claim, not a fix. Never report a step or
the whole task done on a hunch.

## 3a. Checkpoints - hand the decision to the boss

When the plan reaches an `is_checkpoint` step, do NOT decide unilaterally. Put it
in front of the boss and stop, using the same dashboard return-path everything
else uses - `surface_item`:

    surface_item(
      surface='plan_checkpoints', kind='checkpoint',
      title='<the step title>', subtitle='<plan title>',
      metadata={ plan_id: '<id>', step_id: '<id>' },
      actions=[
        { id:'approve', label:'Approve', style:'primary',
          intent:'The boss approved checkpoint step <step_id> of plan <plan_id>. Mark it done with plan_update and continue the plan from the next step.' },
        { id:'skip', label:'Skip', style:'secondary',
          intent:'The boss chose to skip checkpoint step <step_id> of plan <plan_id>. Mark it skipped with plan_update and continue the plan.' }
      ])

Then end your turn. When the boss taps Approve or Skip on the dashboard, that
seeds a fresh turn carrying the action's intent - you then `plan_update` the step
and carry on. This is the steerable part of the plan: the boss can watch it pause,
and one tap resumes it.

## 4. Replan on failure

When a step blocks (verification failed, or the world changed), do not push past
it and do not silently abandon the plan:

- Diagnose why in one sentence.
- Either redo the step correctly and re-verify, or revise the plan - the right
  shape may have changed. Insert, reorder, or drop steps with a fresh `plan_create`
  if the remaining work is genuinely different now (creating a new plan supersedes
  the old one for this session, so carry forward what is still true).

## 5. Resume cleanly after any interruption

Your active plan is injected into your prompt every turn, including after a
compaction or a restart. If you come back mid-task, read the plan, find the next
pending step, and continue from there - never restart work that is already marked
done. If you are unsure of the current state, `plan_get` re-reads it.

## 6. Close out

When the last step is done (and verified), the plan completes on its own. Report to
the boss: the result, and - briefly - the evidence behind it. If you could only
finish part of it, say exactly which steps remain and why.

## Hard rules

- Never mark a `verify_required` step done without a real passing `plan_verify`.
  Proof, not optimism.
- Never claim the task is done while any step is still pending, in_progress, or
  blocked. Honesty over a tidy-looking finish.
- One plan per task. Keep it current as you work - a stale plan the boss is
  watching is worse than no plan.
- Do not over-plan trivial one-step asks. This recipe is for real multi-step work,
  not for "what's the weather".
$skill$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('plan-and-verify', '1.0.0')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
