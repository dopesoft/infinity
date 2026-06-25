-- 158_verify_revert_deterministic_only.sql — make the post-deploy-verify loop
-- revert ONLY on a deterministic build/boot failure, never on a "red run".
--
-- THE BUG THIS FIXES (2026-06-25). The post-deploy-verify recipe (v1.1, migration
-- 152) told the runtime brain: "If error rate clearly spiked vs. before the
-- deploy, treat it as a breakage -> Revert." But the whole point of the
-- error-visibility / honesty machinery (httpx instrumented transport,
-- mem_http_failures, the cron.classifyOutcome hard-failure veto, the triage
-- truthfulness gate) is to MAKE runs go red and log errors on real EXTERNAL
-- failures (401s, 5xx). To the verify brain a post-deploy rise in error
-- observations reads as "my new build broke things" — when in fact the new build
-- is correctly SURFACING pre-existing external failures. That false positive is
-- what got the honesty machinery auto-reverted (twice). Per CLAUDE.md: "a run
-- going RED because it correctly surfaced a real external failure is that guard
-- WORKING — never a regression to revert."
--
-- THE FIX (v1.2): the ONLY revert trigger is deterministic — running_sha never
-- advanced to latest_sha within the deploy window (the new build failed to boot,
-- so Railway kept the old binary alive on a failed /health). The "scan logs for
-- an error spike" trigger is DELETED. A booted-but-semantically-wrong regression
-- is covered by the external sentry outage path + the boss's review surface, NOT
-- by asking a gpt-5.x brain to eyeball logs it can't reliably query. This is the
-- Rule #1b move: the load-bearing rollback MECHANIC stops depending on a fragile
-- log-judgment in droppable prose. The deterministic code-level guards (the
-- sentry tag-scope + protected-path veto, the revert-veto chokepoint) are the
-- real enforcement; this recipe change removes the cognitive footgun.
--
-- Recipe-only change (SKILL.md prose). Idempotent: ON CONFLICT DO NOTHING +
-- active-version upsert.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'post-deploy-verify', 'v1.2-6-25-2026',
  $skill$---
name: post-deploy-verify
version: "v1.2-6-25-2026"
description: Confirm the latest self-improve deploy booted and is live; revert + push ONLY if the new build failed to come up. Never revert on a "red run".
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
OLD core alive on a failed `/health` — which means you're running on the old,
healthy binary and CAN fix it. If the build came up, you confirm it and stamp
the proposals "applied" so the boss sees a closed loop.

## 1. Read the state — deploy_status_refresh FIRST

Call `deploy_status_refresh` (not the cached `deploy_status`) to force a fresh
GitHub check. This is the authoritative read for this session:

- `running_sha` — the commit the live binary was built from.
- `latest_sha` — HEAD of `main`.
- `behind` / `commits_behind` — is the running binary behind main?

Do not proceed past this step without the result in hand.

## 2. Decide: healthy, bricked, or no-op — on the SHA, not on logs

There is exactly ONE deterministic signal and you use only it. Railway advances
`running_sha` to `latest_sha` ONLY when the new build booted and passed its
`/health` check. So:

- **No self-improve push happened** (`running_sha == latest_sha` AND the git log
  shows no `[self-improve]` commit from tonight): nothing to verify. Write a
  brief "nothing to verify" observation and stop.
- **Healthy deploy** (`running_sha == latest_sha`, and there IS a `[self-improve]`
  commit tonight): the new build booted and is serving — that is success. Go to
  **Step 3 (Confirm)**. **Do NOT scan logs to decide whether to revert.** A rise
  in error observations / `mem_http_failures` / red runs AFTER the deploy is the
  error-visibility machinery WORKING (surfacing real external 401s/5xx) — it is
  NOT evidence your build broke, and it is NEVER a revert trigger.
- **Bricked / stuck** (a `[self-improve]` commit was pushed tonight but
  `running_sha` is STILL the old commit well past the full deploy/settle window):
  the new build failed to come up → go to **Step 4 (Revert)**. This is the only
  thing that warrants a revert.

## 3. Confirm healthy deploy — stamp "applied"

When `running_sha == latest_sha`:

1. Look up the `mem_code_proposals` rows the nightly run marked "approved"
   tonight (status='approved', note starts with '[auto] committed:').
2. For each one call:
   `code_proposal_decide(id, "applied", "[auto] applied: deploy booted, running_sha=<sha>")`.
   The tool gate enforces that running_sha == latest_sha before it allows this
   stamp — if it refuses, DO NOT retry; surface a system item explaining why.
3. Write a success observation (`verdict: healthy`) and stop.

## 4. Revert — ONLY when the new build failed to boot

The single precondition: `running_sha != latest_sha` after the full settle
window — i.e. the deploy is genuinely stuck on the old binary because the new
one didn't come up. If `running_sha == latest_sha`, the build booted; you do NOT
revert, no matter what the logs look like.

Then:
1. `git revert --no-edit <sha>` for tonight's `[self-improve]` commit(s) only.
   Never force-push. The push path re-syncs (fetch+rebase) and a code-level veto
   will refuse any revert touching the honesty machinery — if your revert is
   refused, believe it and surface a system item; do not work around it.
2. `git push` the revert — this restores the known-good build.
3. For each affected proposal call:
   `code_proposal_decide(id, "rejected", "[auto] reverted: new build failed to boot")`.
4. Write a postmortem observation: what deployed, that it never came up, what
   reverted. `verdict: reverted`.

## 5. Escalate if the revert doesn't help

Verify ONCE after the revert. If still stuck, do NOT keep thrashing — stop,
write a loud observation, and surface it to the boss for hands-on help.
`verdict: escalated`.

## Hard rules

- The ONLY revert trigger is `running_sha != latest_sha` past the settle window
  (the build didn't boot). NEVER revert because errors/red runs went up — that's
  the honesty machinery working, and reverting it is the exact bug this recipe
  was rewritten to kill.
- Never force-push. Never revert a deploy that booted (running_sha == latest_sha).
- Never revert the error-visibility / self-healing machinery (httpx,
  mem_http_failures, the cron failure veto, the triage truthfulness gate). A
  spike in surfaced errors is success, not a regression.
- Call `deploy_status_refresh` before any revert decision — never on a stale snapshot.
- Touch only tonight's `[self-improve]` commit(s). One revert attempt, then escalate.
- If the tool gate or the revert-veto blocks you, believe it and stop. Surface a
  system item explaining the discrepancy.
$skill$,
  '', 0.75, 'infinity_native'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint post-deploy-verify active version to v1.2.
INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('post-deploy-verify', 'v1.2-6-25-2026')
ON CONFLICT (skill_name) DO UPDATE SET
  active_version = EXCLUDED.active_version,
  updated_at     = NOW();

UPDATE mem_skills
   SET last_evolved = NOW()
 WHERE name = 'post-deploy-verify';

COMMIT;
