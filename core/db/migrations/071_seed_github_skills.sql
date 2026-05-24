-- 071_seed_github_skills.sql
--
-- Adopt Hermes's GitHub bundled skills as Infinity recipes over OUR substrate:
--   • composio__GITHUB_* verbs (dormant; load via tool_search → load_tools) for
--     the REST surface (PRs, issues, reviews, repo admin).
--   • the `github` MCP (api.githubcopilot.com/mcp) for read/search.
--   • git itself via the bridge (git_status/diff/stage/commit/push, bash_run) on
--     the Mac OR the cloud workspace — see `cloud-workspace`.
-- We never hardcode a possibly-wrong Composio verb slug: the recipe tells the
-- agent to SEARCH the catalog for the right verb, matching how Composio's
-- dynamic toolkit catalog actually works.
--
-- Rule #1: cognition in SKILL.md, not Go. Idempotent on all three tables.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────
-- github-pr-workflow
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'github-pr-workflow',
  'Run the full GitHub pull-request lifecycle: branch off main, commit focused changes, push, open a PR with a clear body, watch CI, and merge when green. Uses git via the bridge + composio__GITHUB_* for the PR API. Use when shipping a code change as a PR.',
  'medium',
  '["composio:github","workspace:*"]'::jsonb,
  '["open a pr","make a pull request","ship this as a pr","create a branch and pr","raise a pr","pr for this change","merge the pr"]'::jsonb,
  '[{"name":"repo","type":"string","doc":"owner/name"},{"name":"change","type":"string","doc":"what to implement/commit"},{"name":"base","type":"string","doc":"base branch; default main","required":false}]'::jsonb,
  '[{"name":"pr_url","type":"string"},{"name":"status","type":"string"}]'::jsonb,
  0.85, 'active', 'hermes_imported', 72,
  'The boss ships via PRs; this is the core code-delivery loop end to end.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'github-pr-workflow', 'v1.0-5-24-2026',
  $skill$---
name: github-pr-workflow
version: "v1.0-5-24-2026"
description: Full PR lifecycle — branch, commit, push, open PR, watch CI, merge — via git on the bridge + composio__GITHUB_* for the API.
trigger_phrases:
  - open a pr
  - make a pull request
  - ship this as a pr
  - raise a pr
inputs:
  - name: repo
    type: string
  - name: change
    type: string
  - name: base
    type: string
    required: false
outputs:
  - name: pr_url
    type: string
  - name: status
    type: string
risk_level: medium
network_egress: composio:github
confidence: 0.85
---

# GitHub PR workflow

Git operations run through the bridge (`git_status`, `git_diff`, `git_stage`,
`git_commit`, `git_push`, `bash_run`) on the Mac or the cloud workspace. The PR/CI
API surface comes from Composio's GitHub toolkit — load the verbs first:

1. `tool_search("github pull request create")` then `load_tools([...])` to pull the
   `composio__GITHUB_*` verbs you need (create PR, list checks, merge). Don't guess
   slug names — search the live catalog. (The `github` MCP also serves read/search.)
2. **Branch.** Off the base (default `main`): `git checkout -b <type>/<short-desc>`.
   Never commit straight to main.
3. **Implement + commit.** Make the focused change (see `test-driven-development` /
   `pre-commit-review` first if it's real code). Stage and commit with a clear message
   describing *why*, not just what. Keep commits scoped.
4. **Push.** `git_push` the branch to origin.
5. **Open the PR** via the loaded Composio verb (or `gh`/MCP): a descriptive title and a
   body covering what changed, why, and how it was verified. Link any issue it closes.
6. **Watch CI.** Poll the checks. If red, read the failure, fix on the branch, push
   again — don't merge red.
7. **Merge when green** only if that's the boss's intent. Many flows want the boss to
   review/merge himself — if unsure, open the PR and hand back the URL rather than
   auto-merging.

## Rules

- One logical change per PR. If it sprawls, split it.
- The boss owns his Mac's git identity; a cloud-side push uses the workspace's
  configured GitHub token (see `cloud-workspace`).
- Don't force-push a shared branch. Don't merge over failing CI.
- Surface the PR URL and CI status clearly when done.
$skill$,
  '', 0.85, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('github-pr-workflow', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- github-code-review
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'github-code-review',
  'Review a GitHub pull request: pull the diff, assess correctness/security/convention-fit, and leave prioritized inline comments + a summary review via the GitHub API. Use when asked to review a PR by number/URL.',
  'medium',
  '["composio:github"]'::jsonb,
  '["review this pr","review pr #","look at this pull request","leave review comments","approve or request changes"]'::jsonb,
  '[{"name":"repo","type":"string","doc":"owner/name"},{"name":"pr","type":"string","doc":"PR number or URL"}]'::jsonb,
  '[{"name":"review","type":"string"},{"name":"verdict","type":"string"}]'::jsonb,
  0.83, 'active', 'hermes_imported', 66,
  'Lets Jarvis review PRs (incoming or his own) with real inline comments, not just chat.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'github-code-review', 'v1.0-5-24-2026',
  $skill$---
name: github-code-review
version: "v1.0-5-24-2026"
description: Review a GitHub PR — pull the diff, assess, leave prioritized inline comments + summary via the GitHub API.
trigger_phrases:
  - review this pr
  - review pr #
  - leave review comments
inputs:
  - name: repo
    type: string
  - name: pr
    type: string
outputs:
  - name: review
    type: string
  - name: verdict
    type: string
risk_level: medium
network_egress: composio:github
confidence: 0.83
---

# GitHub code review

1. **Load tools.** `tool_search("github pull request files diff review comment")` →
   `load_tools([...])` for the `composio__GITHUB_*` verbs (get PR, list files/diff,
   create review, create review comment). The `github` MCP also reads PRs/files.
2. **Read the whole diff** plus enough surrounding code for context. Don't review hunks
   in isolation.
3. **Apply the `pre-commit-review` rubric**: correctness, security (secrets, injection,
   authz, RLS for Supabase), error handling, convention fit, test coverage.
4. **Leave inline comments** on the specific lines via the review-comment verb — each a
   concrete issue + suggested fix, prioritized blocker → nit. Don't pad with noise.
5. **Submit a summary review** with a clear verdict: approve / request changes /
   comment. State the top blockers first.

Be specific and kind. Cite `path:line`. If you'd block, say exactly what must change.
$skill$,
  '', 0.83, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('github-code-review', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- github-issues
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'github-issues',
  'Create, triage, label, assign, and close GitHub issues via the GitHub API. Use to file a bug/feature, sweep and label a backlog, or turn a finding into a tracked issue.',
  'medium',
  '["composio:github"]'::jsonb,
  '["create an issue","file a github issue","triage issues","label the issues","assign this issue","open a ticket on github","close issue #"]'::jsonb,
  '[{"name":"repo","type":"string","doc":"owner/name"},{"name":"action","type":"string","doc":"create | triage | label | assign | close"}]'::jsonb,
  '[{"name":"issue_url","type":"string"},{"name":"summary","type":"string"}]'::jsonb,
  0.82, 'active', 'hermes_imported', 60,
  'Turns findings/requests into tracked work; keeps the boss''s backlog tidy.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'github-issues', 'v1.0-5-24-2026',
  $skill$---
name: github-issues
version: "v1.0-5-24-2026"
description: Create, triage, label, assign, and close GitHub issues via the GitHub API.
trigger_phrases:
  - create an issue
  - file a github issue
  - triage issues
  - label the issues
inputs:
  - name: repo
    type: string
  - name: action
    type: string
outputs:
  - name: issue_url
    type: string
  - name: summary
    type: string
risk_level: medium
network_egress: composio:github
confidence: 0.82
---

# GitHub issues

1. **Load tools.** `tool_search("github issue create list label assign")` →
   `load_tools([...])` for the `composio__GITHUB_*` issue verbs (or use the `github` MCP
   for listing/search).
2. **Create:** a clear title, a body with repro/expected/actual for bugs or
   problem/proposal for features, and labels. Search existing issues first to avoid
   duplicates — link the dupe instead of opening another.
3. **Triage a backlog:** list open issues, group by type/area, apply consistent labels,
   assign owners if the boss specified a mapping, flag stale ones. Report the resulting
   shape, don't silently mass-edit.
4. **Close** only with a reason (fixed by #PR / wontfix / duplicate-of) — never close
   silently.

Surface the issue URL(s) and a one-line summary of what changed.
$skill$,
  '', 0.82, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('github-issues', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- github-repo-management
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'github-repo-management',
  'Manage GitHub repositories: clone/create/fork, manage remotes and branches, cut releases/tags, edit repo settings. Uses composio__GITHUB_* + git via the bridge. Use for repo-level admin rather than per-PR work.',
  'medium',
  '["composio:github","workspace:*"]'::jsonb,
  '["create a repo","fork the repo","clone the repo","cut a release","tag a version","manage the remote","new github repository"]'::jsonb,
  '[{"name":"action","type":"string","doc":"clone | create | fork | release | remote"},{"name":"repo","type":"string","doc":"owner/name or new name"}]'::jsonb,
  '[{"name":"result","type":"string"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 58,
  'Repo-level admin so Jarvis can stand up / release projects, not just edit files.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'github-repo-management', 'v1.0-5-24-2026',
  $skill$---
name: github-repo-management
version: "v1.0-5-24-2026"
description: Repo-level GitHub admin — clone/create/fork, remotes/branches, releases/tags, settings — via composio__GITHUB_* + git on the bridge.
trigger_phrases:
  - create a repo
  - fork the repo
  - cut a release
  - tag a version
inputs:
  - name: action
    type: string
  - name: repo
    type: string
outputs:
  - name: result
    type: string
risk_level: medium
network_egress: composio:github
confidence: 0.8
---

# GitHub repo management

1. **Load tools** for the action: `tool_search("github repo create fork release tag")`
   → `load_tools([...])`. Clone/checkout themselves run via the bridge git verbs +
   `bash_run` (Mac or cloud workspace — see `cloud-workspace`).
2. **Create / fork:** use the Composio repo verb; set visibility, description, default
   branch. For a new project also scaffold the initial structure and an initial commit.
3. **Releases / tags:** tag from the right commit, write release notes summarizing the
   changes since the last tag, publish via the release verb.
4. **Remotes / branches:** add/rename remotes, set upstream, protect branches only when
   asked.

## Rules

- Creating/forking/releasing is consequential — confirm the target (owner, name,
  visibility) before acting; don't create public repos by default.
- Deleting a repo or branch is destructive — STOP and get explicit approval, never
  default to it.
- Report the resulting repo/release URL.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('github-repo-management', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
