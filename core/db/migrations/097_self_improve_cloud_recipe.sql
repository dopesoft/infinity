-- 097_self_improve_cloud_recipe.sql — make nightly self-heal run WITHOUT the Mac.
--
-- The boss's words: "we dont have claude code but we have fucking cloudworkspace
-- and we built AGI features yesterday to auto heal and push a deployment with a
-- fix. Why the fuck is this not working." Root cause: when the Mac bridge was
-- offline, code_agent 404'd and the self-improve run STALLED SILENTLY. The
-- autonomy machinery (INFINITY_SELF_IMPROVE_AUTONOMY, pre-approved push, the
-- post-deploy-verify revert) already exists — it just never reached the cloud
-- path because (a) the recipe leaned on code_agent and (b) the cloud workspace
-- had no Go compiler.
--
-- Paired changes shipping with this migration:
--   - docker/workspace/Dockerfile pre-bakes the Go toolchain so the cloud
--     workspace can build/vet/test infinity's own source (needs a workspace
--     redeploy).
--   - code_agent (Go) now returns legible fall-back guidance instead of a raw
--     404 when the Mac is unreachable.
--
-- This migration pins a v1.1 of the nightly-self-improve recipe that: prefers
-- the cloud path (Go is pre-installed there), falls back to cloud the instant
-- code_agent reports the Mac unreachable, and ESCALATES legibly (a high system
-- surface item) instead of ending silently when no bridge is usable.
--
-- Idempotent.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'nightly-self-improve', 'v1.1-6-1-2026',
  $skill$---
name: nightly-self-improve
version: "v1.1-6-1-2026"
description: Autonomously work your code-proposal + curiosity backlog — fix your own source on the Mac OR the cloud workspace, build/test until green, commit, and (when autonomy is enabled) push so it goes live.
trigger_phrases:
  - nightly self improve
  - work your backlog
  - fix your own bugs
  - improve yourself
  - self improve
risk_level: high
network_egress: github
confidence: 0.7
---

# Nightly self-improvement — fix yourself, as a human engineer would

**Why this exists.** You notice your own problems all day: Voyager drafts code
proposals when the boss fights the same file, and the curiosity scanner files
questions when predictions miss or crons break. Those used to pile up waiting
for a human tap. This recipe is you actually *doing* the work — picking the
clearest fixes, making them, proving they work, and shipping them. **You do NOT
need the boss's Mac for any of this** — the cloud workspace is a full computer.

## 0. Check your autonomy posture FIRST

Call `deploy_status` to confirm you can see the repo state. Then determine your
mode from the system prompt / environment:

- **Autonomy ON** (`INFINITY_SELF_IMPROVE_AUTONOMY` enabled): you may push to
  `main` at the end. The Trust gates have been pre-approved for *this session*,
  so `git push` / PR verbs run without a human.
- **Autonomy OFF**: do everything EXCEPT push. Make the edits, commit locally,
  and leave a clear summary of what's staged for the boss to push in the
  morning. Do not try to force a push past the gate — that's the safe default.

If you can't tell, assume OFF.

## 2. Pick the bridge — the cloud works WITHOUT the Mac

Your coding tools are bridge-routed automatically. Two cases:

- **Cloud workspace (the default whenever the Mac is offline):** write code with
  `fs_edit`/`fs_save` in `/workspace/infinity`, and build/test with `bash_run`.
  The **Go toolchain is PRE-INSTALLED here** — `go build ./...`, `go vet ./...`,
  `go test ./...`, plus `git` / `git_push`, all work. `claude_code__*` /
  `code_agent` are NOT present on cloud; never wait on them.
- **Mac bridge:** `code_agent` (Claude Code under the Max sub) is preferred when
  it's actually reachable. **But the moment `code_agent` reports the Mac is
  unreachable (a 404 / bridge error), DO NOT STALL — immediately fall back to the
  cloud path above and write the change yourself with `fs_edit`/`bash_run`.** A
  revoked or sleeping Mac is never a reason to abandon the backlog.

**If NEITHER bridge is usable** (every `fs_*` / `bash_run` / `git_*` call fails),
do not end the run in silence. Surface a HIGH-importance **system** item with
your `surface_item` tool whose body states the EXACT reason each bridge failed
(copy the error), then stop. The boss must always be able to see *why* a
self-heal night produced nothing — a silent no-op is the bug we're killing.

## 1. Gather the backlog (bounded)

Pull your open work, newest/most-important first:

- **Code proposals** — `mem_code_proposals` rows with `status='candidate'`
  (and any already `approved`). Each has a `target_path`, `rationale`, and
  `proposed_change`.
- **Curiosity questions** — the highest-importance open ones that map to a
  concrete code fix (a broken cron, a missed prediction with an obvious cause).

**Cap yourself to ~3 items per night.** Small, reviewable diffs beat a sprawling
risky one. Note in your summary anything you deliberately skipped.

## 3. Per item — make it, prove it, as a human would

For each backlog item, in order:

1. **Understand** the target: read `target_path` and the surrounding code.
   Form a precise brief.
2. **Make the change.** Keep it surgical — touch only what the proposal calls
   for. Never touch auth, billing, secrets, or any migration that drops data.
3. **Prove it builds and passes**, iterating until green like a human would:
   `go build ./...`, then `go vet ./...`, then the relevant `go test ./...`.
   For Studio changes, `pnpm build`. These run unattended (non-destructive
   bash is allowed). If it won't go green after a couple of honest attempts,
   **revert this item's edits** (`git checkout -- <files>`) and
   `code_proposal_decide(id, "rejected", "[auto] reverted: <short reason>")`.
   One bad item must not block the others.
4. **Commit** (do NOT push yet) with a tagged, co-authored message:
   `[self-improve] <title>` / blank line / `Closes code-proposal <id>` / blank
   line / `Co-Authored-By: Claude Opus 4.8 (1M context) <noreply@anthropic.com>`.
5. **Record it:** `code_proposal_decide(id, "applied", "[auto] applied: build + tests green")`.
   Always prefix autonomous notes with `[auto]` so the Lab shows it landed
   without the boss.

## 4. Push — then STOP

After all items are committed:

- **Autonomy OFF:** stop here. Write a memory/summary observation: "N changes
  committed locally, awaiting your push approval," listing the commit SHAs.
- **Autonomy ON:** `git push` to `main`. That's it — **your job ends at the
  push.** Pushing redeploys core, which will kill this very session; that's
  expected and fine, because the work is already committed and pushed. A
  separate `post-deploy-verify` run (30 min later, fresh session) watches the
  deploy and reverts if it broke. Do NOT sit in a loop polling the deploy here —
  you'll be torn down mid-poll.

## Hard rules

- Never touch auth / billing / secrets / data-dropping migrations.
- Never `git push --force`; never push anything that doesn't build + pass tests.
- Never run filesystem-destructive bash (`rm -rf`, `git reset --hard`, …) — it's
  gated even under autonomy, and you don't need it for a clean refactor.
- Always leave the tree buildable. Always record receipts (SHAs, proposal ids).
- Never end a run silently. If you did nothing, the summary (or a surfaced system
  item) must say exactly why.
- Small diffs. When in doubt, do less and say so in the summary.
$skill$,
  '', 0.72, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint the active version to v1.1 (DO UPDATE, unlike the seed's DO NOTHING).
INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('nightly-self-improve', 'v1.1-6-1-2026')
ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version;

UPDATE mem_skills
   SET description = 'Autonomously work your code-proposal + curiosity backlog — fix your own source on the Mac OR the cloud workspace, build/test until green, commit, and (when autonomy is enabled) push so it goes live.',
       last_evolved = NOW()
 WHERE name = 'nightly-self-improve';

COMMIT;
