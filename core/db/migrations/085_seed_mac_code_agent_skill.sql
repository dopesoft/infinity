-- 085_seed_mac_code_agent_skill.sql
--
-- THE FIX for "I was on the Mac bridge but it was still burning my ChatGPT
-- plan." Before this, Mac-bridge coding used claude_code__Edit/Write as dumb
-- file-writers while the CHAT model (ChatGPT OAuth) authored every byte — so
-- the boss's Max subscription paid only for the tunnel and his ChatGPT quota
-- paid for all the thinking. The new `code_agent` tool (core/internal/tools/
-- code_agent.go) delegates the actual coding to `claude -p` on the Mac, which
-- runs under the Max subscription. This skill carries the cognition for HOW to
-- use it well: when to delegate vs inline, how to write a self-contained brief,
-- and how to handle a blocked delete.
--
-- Rule #1: the judgment lives in SKILL.md (Voyager/GEPA can evolve it), not Go.
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'mac-code-delegation',
  'On the Mac bridge, delegate real coding to `code_agent` (it runs Claude Code / `claude -p` under the boss''s Anthropic Max subscription) instead of authoring code yourself with claude_code__Edit/fs_edit — which spends your own chat-model quota. Covers when to delegate vs inline, how to write a self-contained brief, and how to handle a blocked delete. Use whenever you''re about to write or change code and the Mac bridge is active.',
  'low', '[]'::jsonb,
  '["write code","build a feature","implement","refactor","fix the bug","add a function","edit the file","change the code","code this","work on the repo"]'::jsonb,
  '[{"name":"task","type":"string","doc":"the coding work to do"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"summary of what Claude Code changed"}]'::jsonb,
  0.9, 'active', 'infinity_native', 88,
  'Directly controls who pays for coding cognition (Max sub vs the boss''s ChatGPT plan). Getting this wrong silently burns his subscription — the exact bug this skill fixes.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'mac-code-delegation', 'v1.0-5-31-2026',
  $skill$---
name: mac-code-delegation
version: "v1.0-5-31-2026"
description: On the Mac bridge, delegate real coding to `code_agent` (Claude Code under the boss's Max subscription) instead of authoring it yourself against your own quota.
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

# Coding on the Mac bridge — delegate, don't author

**Why this exists.** On the Mac bridge, `claude_code__Edit` / `claude_code__Write` /
`fs_edit` are *dumb file-writers* — they don't think. If YOU (the chat model) generate
the code and call them to write it, every byte is authored against *your* quota (the
boss's ChatGPT OAuth plan), and his Anthropic **Max** subscription pays only for the
tunnel. That's backwards, and it's what was burning his ChatGPT plan during long Mac
coding sessions.

`code_agent` fixes it: it runs `claude -p` — the *real* Claude Code agent — on the Mac
under the **Max subscription**. Claude does the coding cognition; you just orchestrate.

## The rule

- **Mac bridge + any non-trivial coding → `code_agent`.** Building a feature, a
  refactor, a multi-file change, "fix this bug", "add X" — hand the whole thing to
  `code_agent` with a complete brief. Don't pre-write the code yourself.
- **Tiny / deterministic single-shot edits → inline is fine.** Bumping a version
  string, flipping one boolean, a one-line config change — just use `fs_edit` /
  `claude_code__Edit`. Spinning up a full Claude Code run for a one-liner is wasteful.
- **Cloud bridge → there is no Claude Code; YOU write the code** directly with
  `fs_edit`/`fs_save`/`bash_run` in `/workspace`. That's expected (see `cloud-workspace`).
- **Heavy / very long jobs → `background_build`** so the boss can walk away; it can
  itself lean on `code_agent` for the coding steps.

## Writing the brief (this is the skill)

`code_agent` has NO chat history — it sees only your `task` string plus the repo. A weak
brief gets weak code. Include, every time:

1. **Goal** — what to build/change, in one or two sentences.
2. **Where** — the repo path (pass `repo`), and the files/area to touch if you know them.
3. **Constraints** — conventions to follow, patterns to reuse, what NOT to touch.
4. **Acceptance** — how it should verify itself: the build/test command to run and what
   "done" looks like ("`go build ./...` passes", "the new endpoint returns 200").

Give it the same standard of brief you'd want if someone handed YOU the task cold.

## Autonomy & the delete gate

`code_agent` runs **freely** — it edits, builds, runs tests, installs deps, commits —
without stopping you. The ONE thing it can't do unattended is **delete** (rm -rf, shred,
truncate, git reset --hard, find -delete, …). A PreToolUse hook blocks those and Claude
reports back what it wanted to delete. When you see a delete was blocked:

1. Surface it to the boss plainly: what Claude wanted to delete and why.
2. On his go-ahead, run that ONE command yourself via `bash_run` / `claude_code__Bash` —
   it routes through the normal Trust approval (a card he taps). Don't try to disable the
   gate.

## After it returns

Read `code_agent`'s summary, confirm the build/tests passed (re-run with `bash_run` if
unsure — don't take "done" on faith), then report the result to the boss. If it hit the
inline time limit and is still running, tell him; follow-up work on the same task can
resume with `claude --continue` in that repo.
$skill$,
  '', 0.9, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('mac-code-delegation', 'v1.0-5-31-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
