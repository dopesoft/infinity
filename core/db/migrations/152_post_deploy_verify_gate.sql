-- 152_post_deploy_verify_gate.sql — make the deploy-verify loop match the new
-- code_proposal_decide gate (added in the same PR).
--
-- The gate in tools/selfimprove_tools.go now BLOCKS code_proposal_decide with
-- decision="applied" during autonomous turns unless running_sha == latest_sha.
-- "applied" is the "it shipped and works" stamp; "approved" is the "I committed
-- intent" stamp. The skills were using them interchangeably, which the gate
-- now rejects.
--
-- Two recipe updates:
--
-- 1. nightly-self-improve (v1.2-6-22-2026): after a successful build + commit,
--    mark the proposal "approved" (committed, not yet deployed) instead of
--    "applied". The nightly run does NOT know if the deploy landed — the 30-min
--    post-deploy-verify run does.
--
-- 2. post-deploy-verify (v1.1-6-22-2026): when the healthy-deploy path fires
--    (running_sha == latest_sha, logs clean), call code_proposal_decide with
--    "applied" for each tonight's proposal that is still "approved". This is
--    the ONLY path that may stamp "applied" — because it runs AFTER the deploy
--    and can confirm it. When reverting (bricked path) the tool still stamps
--    "rejected" as before; "rejected" is never blocked by the deploy gate.
--
-- Both changes are recipe-only (SKILL.md prose). They are the judgment layer
-- (which decision to record at which stage) that sits on top of the mechanical
-- gate (the tool refusing "applied" when the deploy hasn't landed).
--
-- Idempotent: ON CONFLICT (skill_name, version) DO NOTHING.

BEGIN;

-- ── 1. nightly-self-improve v1.2 — mark "approved" after commit, not "applied"

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'nightly-self-improve', 'v1.2-6-22-2026',
  $skill$---
name: nightly-self-improve
version: "v1.2-6-22-2026"
description: Autonomously work your code-proposal + curiosity backlog — fix your own source on the Mac OR the cloud workspace, build/test until green, commit, and (when autonomy is enabled) push so it goes live.
trigger_phrases:
  - nightly self improve
  - work your backlog
  - fix your own bugs
  - improve yourself
  - self improve
risk_level: high
network_egress: github
confidence: 0.75
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
5. **Record commit intent** — NOT "applied" yet (the deploy hasn't happened):
   `code_proposal_decide(id, "approved", "[auto] committed: build + tests green, awaiting deploy")`.
   Use "approved" here. "applied" is stamped by the post-deploy-verify run AFTER
   it confirms the deploy is live and healthy — the tool will block "applied" if
   the SHA hasn't caught up.

## 4. Push — then STOP

After all items are committed:

- **Autonomy OFF:** stop here. Write a memory/summary observation: "N changes
  committed locally, awaiting your push approval," listing the commit SHAs.
- **Autonomy ON:** `git push` to `main`. That's it — **your job ends at the
  push.** Pushing redeploys core, which will kill this very session; that's
  expected and fine, because the work is already committed and pushed. A
  separate `post-deploy-verify` run (30 min later, fresh session) watches the
  deploy and stamps "applied" once confirmed healthy. Do NOT sit in a loop
  polling the deploy here — you'll be torn down mid-poll.

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
  '', 0.75, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint nightly-self-improve active version to v1.2.
INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('nightly-self-improve', 'v1.2-6-22-2026')
ON CONFLICT (skill_name) DO UPDATE SET
  active_version = EXCLUDED.active_version,
  updated_at     = NOW();

UPDATE mem_skills
   SET last_evolved = NOW()
 WHERE name = 'nightly-self-improve';

-- ── 2. post-deploy-verify v1.1 — stamp "applied" on the healthy-deploy path

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'post-deploy-verify', 'v1.1-6-22-2026',
  $skill$---
name: post-deploy-verify
version: "v1.1-6-22-2026"
description: Confirm the latest self-improve deploy landed and is healthy; revert + push if it broke.
trigger_phrases:
  - verify the deploy
  - post deploy check
  - did the deploy break
  - check the deploy
  - roll back the deploy
risk_level: high
network_egress: github
confidence: 0.75
---

# Post-deploy verify — the human who watches the deploy and rolls back

**Why this exists.** The `nightly-self-improve` run pushes and then dies (its
own redeploy tears it down). Someone has to watch what happened. That's you, in
a fresh session 30 min later. If the new build crashed on boot, Railway kept the
OLD core alive on a failed `/health` — which means you're running on the
old, healthy binary and CAN fix it. If the build is fine, you confirm it and
stamp the proposals "applied" so the boss sees a closed loop.

## 1. Read the state — deploy_status_refresh FIRST

Call `deploy_status_refresh` (not the cached `deploy_status`) to force a fresh
GitHub check. This is the authoritative read for this session:

- `running_sha` — the commit the live binary was built from.
- `latest_sha` — HEAD of `main`.
- `behind` / `commits_behind` — is the running binary behind main?

Do not proceed past this step without the result in hand.

## 2. Decide: healthy, bricked, or no-op

- **No self-improve push happened** (running_sha == latest_sha and the git log
  shows no `[self-improve]` commit from tonight): nothing to verify. Write a
  brief "nothing to verify" observation and stop.
- **Healthy deploy** (`running_sha == latest_sha`, i.e. the new build is live):
  scan recent logs / your own error observations for the post-deploy window. If
  clean → go to **Step 3 (Confirm)**. If error rate clearly spiked vs. before
  the deploy, treat it as a breakage → go to **Step 4 (Revert)**.
- **Bricked / stuck** (`latest_sha` was pushed tonight but `running_sha` is
  still the old commit well past the deploy window, OR core is visibly
  erroring): the new build failed to come up → go to **Step 4 (Revert)**.

## 3. Confirm healthy deploy — stamp "applied"

When `running_sha == latest_sha` and logs are clean:

1. Look up the `mem_code_proposals` rows that the nightly run marked "approved"
   tonight (status='approved', note starts with '[auto] committed:').
2. For each one call:
   `code_proposal_decide(id, "applied", "[auto] applied: deploy healthy running_sha=<sha>")`.
   The tool gate enforces that running_sha == latest_sha before it allows this
   stamp — if it refuses, DO NOT retry; surface a system item explaining why.
3. Write a success observation (`verdict: healthy`) and stop.

## 4. Revert (only when genuinely broken)

Before reverting, confirm both conditions are true:
- The log check from Step 2 shows real errors (not just startup noise) OR the
  deploy is visibly stuck (running_sha != latest_sha after the full settle window).
- The commit(s) to revert are tagged `[self-improve]` and from tonight.

Then:
1. `git revert --no-edit <sha>` (or the range). Never force-push.
2. `git push` the revert — this restores the known-good build.
3. For each affected proposal call:
   `code_proposal_decide(id, "rejected", "[auto] reverted: <what broke>")`.
4. Write a postmortem observation: what deployed, how it failed, what reverted.
   `verdict: reverted`.

## 5. Escalate if the revert doesn't help

Verify ONCE after the revert. If still broken, do NOT keep thrashing — stop,
write a loud observation, and surface it to the boss for hands-on help.
`verdict: escalated`.

## Hard rules

- Never force-push. Never revert a healthy deploy.
- Call `deploy_status_refresh` before any revert decision — never revert on a
  cached or stale snapshot.
- Touch only the self-improve commit(s) from tonight.
- One revert attempt, then escalate. No infinite rollback loops.
- If the tool gate blocks "applied" (running_sha != latest_sha), believe it and
  stop — do not try to work around it. Surface a system item explaining the
  discrepancy.
$skill$,
  '', 0.75, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint post-deploy-verify active version to v1.1.
INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('post-deploy-verify', 'v1.1-6-22-2026')
ON CONFLICT (skill_name) DO UPDATE SET
  active_version = EXCLUDED.active_version,
  updated_at     = NOW();

UPDATE mem_skills
   SET last_evolved = NOW()
 WHERE name = 'post-deploy-verify';

COMMIT;
