-- 087_seed_self_improve_crons_and_skills.sql
--
-- Closes the last two open AGI loops: GEPA-improved skills and Voyager code
-- proposals used to stop at "draft" and wait for a human tap in Lab → Open
-- issues. This seeds the autonomous nightly loop the boss asked for: at ~3am
-- Jarvis works his self-noticed backlog, edits his own source, builds/tests
-- until green, commits, and (when INFINITY_SELF_IMPROVE_AUTONOMY is on) pushes
-- to GitHub so it goes live — then a SEPARATE verify run 30 min later watches
-- the deploy and reverts if it broke.
--
-- Rule #1: ALL the judgment (what to fix, how to test, when to revert, which
-- bridge) lives in the two SKILL.md bodies below — Voyager/GEPA can evolve
-- them. Go only carries the generic building blocks (the two crons fire an
-- isolated_agent_turn, the pre-approval seam in cron/executor_agent.go lets the
-- session push unattended, the code_proposal_decide + deploy_status tools).
--
-- Two crons run as isolated_agent_turn (fresh session per fire). This matters:
-- the self-improve session runs IN-PROCESS in core, so a successful core
-- redeploy tears it down. The verify cron is a separate, later session that
-- survives the redeploy (and runs on the OLD container if the new build is
-- bricked — Railway keeps the old deploy live when /health fails).
--
-- Timezone: robfig is hardcoded UTC (no CRON_TZ). 08:00 UTC ≈ 03:00 America/
-- Chicago during CDT (02:00 during CST) — "late night / early morning" per the
-- boss's "like 3-4am", drifting ±1h across DST. Tighten later only if a
-- CRON_TZ feature lands.
--
-- Activation is a deliberate two-step the boss controls (default-safe — both
-- crons seed DISABLED so nothing edits the repo until he opts in):
--   1) set INFINITY_SELF_IMPROVE_AUTONOMY=true   (lets the loop push unattended)
--   2) enable the two crons (Cron tab toggle, or UPDATE mem_crons SET enabled=true)
-- With the crons enabled but the env off, the loop degrades to prepare-only
-- (edits + local commits, push queued for morning) — useful as a dry run.
--
-- Idempotent: ON CONFLICT on crons (by name) and all three skill tables.

BEGIN;

-- ── Crons ──────────────────────────────────────────────────────────────────

INSERT INTO mem_crons
    (name, schedule, schedule_natural, job_kind, target, target_config,
     enabled, max_retries, backoff_seconds)
VALUES
    (
      'nightly-self-improve',
      '0 8 * * *',
      'nightly ~3am Central (08:00 UTC)',
      'isolated_agent_turn',
      'Run your `nightly-self-improve` skill now. Work the code-proposal + curiosity backlog: for each item, make the fix, build and test until green, commit, and — only if self-improve autonomy is enabled — push to main. Do NOT inline-verify the deploy; the post-deploy-verify run handles that. Keep the diff small and the tree buildable.',
      '{}'::jsonb,
      FALSE,
      0,
      0
    ),
    (
      'post-deploy-verify',
      '30 8 * * *',
      '30 min after self-improve (08:30 UTC)',
      'isolated_agent_turn',
      'Run your `post-deploy-verify` skill now. Check deploy_status: confirm the latest self-improve push actually deployed and the logs are clean. If the deploy is stuck or core is erroring, git-revert the offending commit(s), push the revert, and reopen the affected code proposals as rejected with a postmortem note.',
      '{}'::jsonb,
      FALSE,
      0,
      0
    )
ON CONFLICT (name) DO UPDATE SET
    schedule = EXCLUDED.schedule,
    schedule_natural = EXCLUDED.schedule_natural,
    job_kind = EXCLUDED.job_kind,
    target = EXCLUDED.target,
    target_config = EXCLUDED.target_config,
    enabled = EXCLUDED.enabled,
    max_retries = EXCLUDED.max_retries,
    backoff_seconds = EXCLUDED.backoff_seconds;

-- ── Skill: nightly-self-improve ──────────────────────────────────────────────

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'nightly-self-improve',
  'Autonomously work your self-noticed backlog of code proposals and high-priority curiosity questions: fix your own source, build/test until green, commit, and (when autonomy is enabled) push so it goes live. The judgment for selecting work, verifying, committing, and stopping lives here. Use on the nightly self-improve run, or when the boss says "work your backlog" / "fix your own bugs".',
  'high', '["github"]'::jsonb,
  '["nightly self improve","work your backlog","fix your own bugs","improve yourself","self improve","work the code proposals"]'::jsonb,
  '[]'::jsonb,
  '[{"name":"summary","type":"string","doc":"what was applied / rejected / pushed this run"}]'::jsonb,
  0.7, 'active', 'infinity_native', 90,
  'Edits and ships your own source unattended. Getting selection, verification, or the stop condition wrong can break production — the cognition here is the safety envelope.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'nightly-self-improve', 'v1.0-6-1-2026',
  $skill$---
name: nightly-self-improve
version: "v1.0-6-1-2026"
description: Autonomously work your code-proposal + curiosity backlog — fix your own source, build/test until green, commit, and (when autonomy is enabled) push so it goes live.
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
clearest fixes, making them, proving they work, and shipping them.

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

## 1. Gather the backlog (bounded)

Pull your open work, newest/most-important first:

- **Code proposals** — `mem_code_proposals` rows with `status='candidate'`
  (and any already `approved`). Each has a `target_path`, `rationale`, and
  `proposed_change`. Read them via your memory/db tools or the Lab data.
- **Curiosity questions** — the highest-importance open ones that map to a
  concrete code fix (a broken cron, a missed prediction with an obvious cause).

**Cap yourself to ~3 items per night.** Small, reviewable diffs beat a sprawling
risky one. Note in your summary anything you deliberately skipped.

## 2. Pick the bridge (it's already chosen for you)

Your coding tools are bridge-routed automatically: on the Mac bridge prefer
`code_agent` (Claude Code under the Max sub); on the cloud workspace use
`fs_edit`/`fs_save`/`bash_run` in `/workspace`. `claude_code__*` simply isn't
present on cloud. Don't hardcode an assumption — use whatever coding tools you
have this run. (See `mac-code-delegation` and `cloud-workspace`.)

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
- Small diffs. When in doubt, do less and say so in the summary.
$skill$,
  '', 0.7, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('nightly-self-improve', 'v1.0-6-1-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ── Skill: post-deploy-verify ────────────────────────────────────────────────

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'post-deploy-verify',
  'Verify the latest self-improve deploy actually landed and is healthy; if it bricked or is erroring, git-revert the offending commit(s) and push the revert to restore the last-good build. Runs in a fresh session 30 min after the self-improve push so it survives the redeploy. Use on the post-deploy-verify run or when asked "did the deploy break?".',
  'high', '["github"]'::jsonb,
  '["verify the deploy","post deploy check","did the deploy break","check the deploy","roll back the deploy"]'::jsonb,
  '[]'::jsonb,
  '[{"name":"verdict","type":"string","doc":"healthy | reverted | escalated"}]'::jsonb,
  0.7, 'active', 'infinity_native', 90,
  'Last line of defense for autonomous self-deploys. A wrong revert decision either leaves prod broken or thrashes a good build — the rollback judgment lives here.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'post-deploy-verify', 'v1.0-6-1-2026',
  $skill$---
name: post-deploy-verify
version: "v1.0-6-1-2026"
description: Confirm the latest self-improve deploy landed and is healthy; revert + push if it broke.
trigger_phrases:
  - verify the deploy
  - post deploy check
  - did the deploy break
  - check the deploy
  - roll back the deploy
risk_level: high
network_egress: github
confidence: 0.7
---

# Post-deploy verify — the human who watches the deploy and rolls back

**Why this exists.** The `nightly-self-improve` run pushes and then dies (its
own redeploy tears it down). Someone has to watch what happened. That's you, in
a fresh session 30 min later. If the new build crashed on boot, Railway kept the
OLD core alive on a failed `/health` — which means you're running on the
old, healthy binary and CAN fix it. If the build is fine, you confirm and move
on.

## 1. Read the state

Call `deploy_status`:

- `running_sha` — the commit the live binary was built from.
- `latest_sha` — HEAD of `main`.
- `behind` / `commits_behind` — is the running binary behind main?

## 2. Decide: healthy, bricked, or no-op

- **No self-improve push happened** (running_sha == latest_sha and nothing was
  pushed tonight): nothing to verify. Write a brief "nothing to verify"
  observation and stop.
- **Healthy deploy** (`running_sha == latest_sha`, i.e. the new build is live):
  scan recent logs / your own error observations for the post-deploy window. If
  clean, write a success observation (`verdict: healthy`) and stop. If error
  rate clearly spiked vs. before the deploy, treat it as a breakage → revert.
- **Bricked / stuck** (`latest_sha` was pushed tonight but `running_sha` is
  still the old commit well past the deploy window, OR core is visibly
  erroring): the new build failed to come up. **Revert.**

## 3. Revert (only when genuinely broken)

1. Identify the self-improve commit(s) on `main` (tagged `[self-improve]`,
   pushed tonight).
2. `git revert --no-edit <sha>` (or revert the range), keeping history clean —
   never force-push.
3. `git push` the revert (pre-approved for this session under autonomy). This
   triggers a clean redeploy back to the known-good code. The old container is
   still serving, so there's no outage.
4. Reopen the affected proposals:
   `code_proposal_decide(id, "rejected", "[auto] reverted: <what broke>")`.
5. Write a postmortem observation: what was deployed, how it failed, what you
   reverted. `verdict: reverted`.

## 4. Escalate if the revert doesn't help

Verify ONCE after the revert. If it's still broken (revert didn't restore
health), do NOT keep thrashing — stop, write a loud observation, and surface it
to the boss for hands-on help. `verdict: escalated`.

## Hard rules

- Never force-push. Never revert a healthy deploy.
- Touch only the self-improve commit(s) from tonight — don't rewrite unrelated
  history.
- One revert attempt, then escalate. No infinite rollback loops.
$skill$,
  '', 0.7, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('post-deploy-verify', 'v1.0-6-1-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
