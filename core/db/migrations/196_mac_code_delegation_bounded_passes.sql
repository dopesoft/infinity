-- 196_mac_code_delegation_bounded_passes.sql
--
-- THE FIX for "everytime I try to build with him, he fails miserably and I
-- can't rely on him to build something and walk away."
--
-- The answer to "is he doing something in his coding that stops him being a
-- proper coding assistant?" turned out to be yes, and it was WRITTEN DOWN. The
-- v1.0 body of this skill said: "hand the WHOLE thing to `code_agent` with a
-- complete brief", and deferred every check to "after it returns". So on
-- 2026-08-29 he fired one ten-requirement brief into a black box, waited 47
-- minutes blind, and only then looked at anything. That is a dispatcher, not a
-- coding assistant. A real one reads, changes one thing, runs it, looks at the
-- result, and adapts — so a wrong turn at minute 3 surfaces at minute 3.
--
-- Worse, its recovery advice pointed at a wall: "follow-up work can resume with
-- `claude --continue` in that repo" is blocked outright by
-- core/internal/tools/claude_raw_guard.go, and has been since that guard
-- shipped. `code_agent`'s own `resume_session` is the sanctioned way in and the
-- skill never mentioned it.
--
-- What is NOT in the new body, on purpose (Rule #1b — mechanics live in code,
-- never in prose): noticing that a job finished, reading its transcript,
-- reaping a dead process, forwarding its steps into the chat, refusing a
-- duplicate launch, refusing a hand-rolled wait loop. Every one of those is now
-- guaranteed by Go and cannot be dropped by a model that skims. What is left
-- here is judgment: how big a pass should be, what to verify, when to stop.
--
-- Idempotent: a new version row plus a pointer move, so re-running is a no-op.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'mac-code-delegation', 'v2.0-8-29-2026',
  $skill$---
name: mac-code-delegation
version: "v2.0-8-29-2026"
description: On the Mac bridge, coding runs on Claude Code under the boss's Max subscription — in BOUNDED passes you verify one at a time, never one blind monolith.
trigger_phrases:
  - write code
  - build a feature
  - implement
  - refactor
  - fix the bug
  - work on the repo
inputs:
  - name: task
    type: string
    doc: the coding work to do
outputs:
  - name: result
    type: string
risk_level: low
network_egress: none
confidence: 0.9
---

# Coding on the Mac — work like a coding assistant, not a dispatcher

On the Mac bridge, `code_agent` runs the real Claude Code agent under the boss's
**Max subscription**. You orchestrate; it implements. Never author real code
yourself with `claude_code__Edit` / `fs_edit` here — those are dumb file-writers,
so every byte would be billed to his ChatGPT plan instead.

That much was always right. What was wrong was the SHAPE of the delegation.

## One plan step, one pass, verified before the next

A 47-minute run with ten requirements in one brief is a black box: if it takes a
wrong turn at minute three, nobody finds out until minute forty-seven, and the
boss watches a spinner the whole time with no idea whether to trust it.

So: **each `code_agent` call is ONE bounded piece of work you can check when it
comes back.** Not "build the coach redesign" — "wire the data layer for the
coach redesign; `go build ./...` clean". Then look, then the next one.

- Scope a pass to something a good engineer would finish in one sitting and
  could show you: a layer, a surface, a bug, a migration.
- Give the pass its own acceptance in the brief — the command that proves it.
- **Verify before you start the next pass.** Run the build/test yourself with
  `bash_run` and look at the diff. Do not take "done" on faith; a pass that
  reports success and does not compile is the failure mode this exists for.
- If a plan is open, one pass maps to one step: verify it, `plan_update` it,
  move on. That is also what keeps his board honest.
- Between passes, say in ONE line what landed: "data layer's in, builds clean —
  starting the UI." He should never have to ask what is happening.

If a brief is growing past a handful of distinct requirements, that is the
signal to split it — not to write a longer brief.

## Picking work back up

Every run records Claude's own session id. To continue one, call `code_agent`
with **`resume_session`** and give `task` only what is LEFT — it reloads
everything that session already read, wrote and tried, so it does not redo work
already on disk.

`claude --continue` and raw `claude` in a shell are blocked and always will be:
they run outside the subscription proof and the delete gate. `resume_session` is
the way in.

## Writing the brief

`code_agent` has NO chat history — it sees your `task` string and the repo,
nothing else. Every brief carries:

1. **Goal** — this pass only, in a sentence or two.
2. **Where** — the repo (pass `repo`), and the files or area if you know them.
3. **Constraints** — patterns to follow, what not to touch.
4. **Acceptance** — the exact command that proves this pass worked.

Give it the brief you would want if someone handed you the task cold.

## When something is blocked

`code_agent` runs freely: it edits, builds, tests, installs, commits. The one
thing it cannot do unattended is **delete**. When a delete is blocked, tell the
boss plainly what it wanted gone and why, and on his go-ahead run that ONE
command through the normal approval. Never try to route around the gate.

## Where each kind of work goes

- **Mac + real coding →** `code_agent`, in bounded passes as above.
- **A one-line, deterministic edit →** `fs_edit` / `claude_code__Edit` inline. A
  whole Claude Code run for a version bump is waste.
- **Cloud bridge →** there is no Claude Code there; write it yourself with
  `fs_edit` / `fs_save` / `bash_run` in `/workspace`. Expected and fine.
- **Genuinely walk-away work →** `background_build`. Not the default: it is for
  work the boss has explicitly stepped away from, and it costs you the
  pass-by-pass checking above.
$skill$,
  '', 0.9, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Point the skill at the new body. UPDATE, not ON CONFLICT DO NOTHING: the row
-- already exists from 085, so an insert-only statement would leave every
-- install still running the v1.0 text this migration exists to replace.
UPDATE mem_skill_active
   SET active_version = 'v2.0-8-29-2026'
 WHERE skill_name = 'mac-code-delegation';

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('mac-code-delegation', 'v2.0-8-29-2026')
ON CONFLICT (skill_name) DO NOTHING;

UPDATE mem_skills
   SET description = 'On the Mac bridge, coding runs on `code_agent` (Claude Code under the boss''s Max subscription) in BOUNDED passes — one verifiable piece of work per call, checked before the next one starts — rather than one blind monolithic brief. Covers how big a pass should be, how to verify it, how to resume one with resume_session, and how to handle a blocked delete. Use whenever you are about to write or change code and the Mac bridge is active.'
 WHERE name = 'mac-code-delegation';

COMMIT;
