-- 069_seed_coding_discipline_skills.sql
--
-- Adopt the best of Hermes's software-development bundled skills as Infinity
-- recipes. These are pure-cognition disciplines (TDD, root-cause debugging,
-- spikes, plan-writing, pre-commit review, codebase inspection). The judgment
-- IS the product; the mechanics are re-pointed onto OUR substrate —
-- `claude_code__*` on the Mac bridge + `delegate` for sub-agents — never
-- Hermes's assumed local CLIs.
--
-- Self-provisioning (the boss's question, answered in the recipes): when a
-- recipe needs a CLI/library that isn't on the Mac yet, the agent installs it
-- itself via `claude_code__Bash` (brew/pip/npm). Per ClaudeCodeGate
-- (core/internal/proactive/gate.go) only filesystem-destructive commands
-- (rm/dd/shred/…) gate — package installs run UNATTENDED and persist on the
-- real Mac. So these skills bootstrap their own tooling the first time.
--
-- Rule #1: cognition lives in SKILL.md (Voyager/GEPA can evolve it), not Go.
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────
-- test-driven-development
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'test-driven-development',
  'Enforce the RED → GREEN → REFACTOR loop: write a failing test that pins the intended behavior, write the minimum code to pass it, then refactor with the test as a guardrail. Use when implementing a behavior change in code the boss cares about.',
  'low', '[]'::jsonb,
  '["tdd","test driven","write tests first","red green refactor","add tests for","test-first"]'::jsonb,
  '[{"name":"target","type":"string","doc":"the behavior/function to build under test"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"passing test + implementation summary"}]'::jsonb,
  0.85, 'active', 'hermes_imported', 70,
  'Core coding discipline; keeps the boss''s production CRM changes verified, not vibes.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'test-driven-development', 'v1.0-5-24-2026',
  $skill$---
name: test-driven-development
version: "v1.0-5-24-2026"
description: Enforce RED → GREEN → REFACTOR — failing test first, minimum code to pass, then refactor under the test's guardrail.
trigger_phrases:
  - tdd
  - write tests first
  - red green refactor
  - add tests for
inputs:
  - name: target
    type: string
    doc: the behavior or function to build under test
outputs:
  - name: result
    type: string
risk_level: low
network_egress: none
confidence: 0.85
---

# Test-driven development

Drive real code changes with your file + bash tools — `fs_read`/`fs_save`/`fs_edit` +
`bash_run` (or `claude_code__*` when the Mac is the active bridge). These route to the
Mac when it's online and to your always-on **cloud workspace** otherwise, so you can do
this WITH OR WITHOUT the Mac (see the `cloud-workspace` skill). The loop is
non-negotiable and runs in this order:

## RED — write the failing test first

1. Find the existing test file/convention (`claude_code__Grep` for the framework:
   `go test`, `pytest`, `vitest`, `jest`, …). Match the project's style.
2. Write ONE test that encodes *why* the behavior matters — assert the intended
   outcome, not the current code. A test that can't fail when the logic changes is wrong.
3. Run it. Confirm it FAILS, and fails for the right reason (not a typo/import error).
   If the test passes before you wrote the code, the test is wrong — fix it.

## GREEN — minimum code to pass

4. Write the least code that makes the test pass. No speculative abstractions,
   no extra features. Run the test. Confirm GREEN.
5. Run the *whole* suite, not just your test — you must not break a sibling.

## REFACTOR — clean up under the guardrail

6. With the test green, improve names/structure/duplication. Re-run after each
   change. The test is your safety net; if it goes red, you broke something — revert.

## Rules

- Match the codebase's framework and conventions; never introduce a new test runner.
- One behavior per test; descriptive names that state the expectation.
- If the suite is slow, run the focused test in the loop and the full suite before declaring done.
- "Tests pass" is a lie if any were skipped — report skips explicitly.
- Missing a test runner? Install it yourself with `bash_run` (`pip install pytest`,
  `npm i -D vitest`, …). Installs run unattended (only filesystem-destructive commands
  gate) and persist in the cloud workspace's `/workspace` volume. See `cloud-workspace`.
$skill$,
  '', 0.85, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('test-driven-development', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- systematic-debugging
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'systematic-debugging',
  'Find the root cause before changing anything: reproduce, isolate, form a hypothesis, instrument, confirm, then fix the cause (not the symptom) and verify. Use for any non-obvious bug, flaky test, or "it works on my machine" report.',
  'low', '[]'::jsonb,
  '["debug this","why is this broken","root cause","it''s failing","flaky test","track down the bug","not working","investigate the error"]'::jsonb,
  '[{"name":"symptom","type":"string","doc":"the observed failure"}]'::jsonb,
  '[{"name":"root_cause","type":"string"},{"name":"fix","type":"string"}]'::jsonb,
  0.85, 'active', 'hermes_imported', 72,
  'Core coding discipline; prevents symptom-patching that masks production bugs.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'systematic-debugging', 'v1.0-5-24-2026',
  $skill$---
name: systematic-debugging
version: "v1.0-5-24-2026"
description: Root-cause first — reproduce, isolate, hypothesize, instrument, confirm, fix the cause, verify. Never patch a symptom you don't understand.
trigger_phrases:
  - debug this
  - why is this broken
  - root cause
  - flaky test
  - track down the bug
inputs:
  - name: symptom
    type: string
outputs:
  - name: root_cause
    type: string
  - name: fix
    type: string
risk_level: low
network_egress: none
confidence: 0.85
---

# Systematic debugging

Do NOT start editing. Understand the bug first. Four phases, in order:

## 1. Reproduce

- Get a reliable repro. If you can't reproduce it, you can't fix it — say so and
  gather more signal (exact steps, inputs, environment, logs).
- For prod issues, pull the real evidence first: `railway logs --service <svc> --lines 200 -d`,
  the failing request, the stack trace. Per CLAUDE.md, check Railway directly — don't speculate.
- For a `relation does not exist` (42P01) error, FIRST run `infinity migrate` — it's usually a
  stranded migration, not a code bug.

## 2. Isolate

- Narrow to the smallest failing unit. Bisect: comment out / git-bisect / binary-search the
  inputs until the boundary between works and fails is one change.
- Read the code around the boundary with `claude_code__Read`/`Grep`. Understand WHY it's shaped
  that way before assuming it's wrong.

## 3. Hypothesize + instrument

- State a specific, falsifiable hypothesis ("the nil comes from X because Y").
- Add temporary logging/asserts to confirm or kill it. Re-run. Let evidence decide — don't
  guess-and-check edits.

## 4. Fix the cause + verify

- Fix the ROOT cause, not the symptom. If you're tempted to add a null-check, ask where the
  null came from.
- Add a regression test (see `test-driven-development`) so it can't come back.
- Remove the temporary instrumentation. Run the full suite. Confirm green.

## Rules

- One hypothesis at a time. If you're changing five things hoping one works, stop — you don't
  understand it yet.
- Surface what you found even if it's "I couldn't reproduce." Fail loud.
- Your file/bash tools run on the Mac OR the always-on cloud workspace (whichever is healthy),
  so you can debug without the Mac. Install any missing diagnostic tool with `bash_run`
  (unattended, persists in `/workspace`; see `cloud-workspace`).
$skill$,
  '', 0.85, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('systematic-debugging', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- spike
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'spike',
  'Run a throwaway experiment to answer one question before committing to a build: "is this API shaped how I think?", "will this approach even work?". Time-boxed, disposable, the goal is the answer not the code. Use before a risky or uncertain implementation.',
  'low', '[]'::jsonb,
  '["spike","quick experiment","prototype this","prove it works first","throwaway test","try it out before","validate the approach"]'::jsonb,
  '[{"name":"question","type":"string","doc":"the single uncertainty to resolve"}]'::jsonb,
  '[{"name":"finding","type":"string","doc":"the answer + recommendation"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 62,
  'De-risks builds; cheap experiment beats expensive wrong implementation.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'spike', 'v1.0-5-24-2026',
  $skill$---
name: spike
version: "v1.0-5-24-2026"
description: Time-boxed throwaway experiment to resolve ONE uncertainty before building. The goal is the answer, not the code.
trigger_phrases:
  - spike
  - quick experiment
  - prove it works first
  - validate the approach
inputs:
  - name: question
    type: string
outputs:
  - name: finding
    type: string
risk_level: low
network_egress: none
confidence: 0.8
---

# Spike (throwaway experiment)

Use when you're about to build something on an assumption you haven't verified —
an unfamiliar API's real response shape, whether an approach scales, if a library
does what its README claims. A 15-minute spike beats a half-day of wrong code.

1. **Write down the ONE question.** A spike answers a single uncertainty. If you have
   three questions, that's three spikes (or you're not spiking, you're building).
2. **Build the smallest thing that answers it.** Scratch dir (`/workspace/tmp/spike-*`
   via `bash_run` — works on the Mac or the cloud workspace), hardcode inputs, skip error
   handling, no tests. Prefer `http_fetch`/`code_exec` for a quick probe over a full
   project. Per the
   "read vendor docs first" rule, if it's a 3rd-party API, also confirm the real
   field/endpoint against the vendor docs — a failing probe isn't proof a capability is absent.
3. **Get the answer.** Print the actual response, the actual timing, the actual behavior.
4. **THROW THE CODE AWAY.** The deliverable is the finding + a recommendation, written
   into the real plan/memory — NOT the spike code. Delete the scratch dir.
5. **Report:** the question, what you observed, and what you now recommend building.

Rule: never let spike code become production code. It has no tests and no error handling
by design. Spikes inform the real build; they are not the real build.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('spike', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- writing-implementation-plans
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'writing-implementation-plans',
  'Turn a vague feature request into a concrete, bite-sized implementation plan: context/why, the files to touch, the build sequence, and how to verify — grounded in the actual codebase, reusing existing patterns. Use before any non-trivial multi-file change.',
  'low', '[]'::jsonb,
  '["write a plan","plan this out","implementation plan","how would you build","break this down","plan the work","spec this"]'::jsonb,
  '[{"name":"request","type":"string","doc":"the feature/change to plan"}]'::jsonb,
  '[{"name":"plan","type":"string","doc":"the written plan (markdown)"}]'::jsonb,
  0.82, 'active', 'hermes_imported', 66,
  'Plan-first discipline; the boss reviews intent before code, catching wrong directions early.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'writing-implementation-plans', 'v1.0-5-24-2026',
  $skill$---
name: writing-implementation-plans
version: "v1.0-5-24-2026"
description: Vague request → concrete, codebase-grounded plan — context, files to touch, build sequence, verification. Reuse existing patterns.
trigger_phrases:
  - write a plan
  - implementation plan
  - how would you build
  - break this down
inputs:
  - name: request
    type: string
outputs:
  - name: plan
    type: string
risk_level: low
network_egress: none
confidence: 0.82
---

# Writing implementation plans

Before writing a non-trivial change, write the plan. A good plan lets the boss
correct your direction before you spend the tokens.

1. **Understand first.** Read the relevant code with `claude_code__Grep/Read`. Find the
   existing functions, utilities, and patterns you can REUSE — never propose new code when
   a suitable implementation already exists. Trace the call path end to end.
2. **State the context.** Why is this change being made — the problem it solves and the
   intended outcome. One short paragraph.
3. **Name the files.** List the specific files to create/modify with one line each on what
   changes. For a pattern repeated across many files, describe it once + list a few
   representative paths — don't enumerate every line.
4. **Sequence the work** into bite-sized, independently-verifiable steps. Each step should
   be something you could check off and test before the next.
5. **Verification.** Describe how to prove it works end to end — the command to run, the
   tool to call, the test that must pass. Strong success criteria let you loop independently.
6. **Surface the trade-offs** only where the choice is genuinely ambiguous. When the right
   form is obvious, pick it and say why — don't hand the boss a menu.

Keep it scannable but executable. Match this codebase's conventions even where you'd
personally choose differently — conformance > taste inside the repo.
$skill$,
  '', 0.82, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('writing-implementation-plans', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- pre-commit-review
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'pre-commit-review',
  'Review a code change before it ships: a quality + security gate over the diff. Check correctness, secrets/injection, error handling, convention fit, and test coverage. Delegate a focused critic pass for larger diffs. Use after writing code and before the boss commits.',
  'low', '[]'::jsonb,
  '["review my code","review the diff","review before commit","code review","check this change","is this safe to ship","security review the diff"]'::jsonb,
  '[{"name":"scope","type":"string","doc":"path/repo/diff to review; default the working diff"}]'::jsonb,
  '[{"name":"findings","type":"array","doc":"prioritized issues"},{"name":"verdict","type":"string"}]'::jsonb,
  0.85, 'active', 'hermes_imported', 70,
  'Quality/security gate on the boss''s production code before it ships.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'pre-commit-review', 'v1.0-5-24-2026',
  $skill$---
name: pre-commit-review
version: "v1.0-5-24-2026"
description: Quality + security gate over a diff before it ships — correctness, secrets/injection, error handling, convention fit, test coverage.
trigger_phrases:
  - review my code
  - review the diff
  - code review
  - is this safe to ship
inputs:
  - name: scope
    type: string
    doc: path/repo/diff; default the working diff
outputs:
  - name: findings
    type: array
  - name: verdict
    type: string
risk_level: low
network_egress: none
confidence: 0.85
---

# Pre-commit review

Gate the change before it lands. The boss commits — you make sure it's worth committing.

1. **Get the diff.** Use `git_diff` (and `git_status`) — they route to the repo on the Mac
   or the cloud workspace. Read every hunk; read the surrounding code with `fs_read`
   (or `claude_code__Read` on the Mac) so you review in context, not in isolation.
2. **Run the checks that matter** for this scope:
   - **Correctness:** logic errors, off-by-one, nil/undefined, wrong branch, race conditions.
   - **Security:** hardcoded secrets/keys, SQL/command/prompt injection, missing authz, RLS
     gaps. For Supabase work confirm `SECURITY DEFINER` + `SET search_path` and RLS-on per
     this project's hard rules. Never let a service-role key into the diff.
   - **Error handling:** swallowed errors, unchecked returns, missing context on failures.
     Per the logging rule, successes → stdout, failures → stderr; flag `log.Printf` used for
     a success line.
   - **Convention fit:** matches existing style/naming? No inline CSS / `style={}` in Studio?
     Uses the right primitive (`ResponsiveModal`, `Section`, `useRuns`) instead of a fork?
   - **Tests:** does the behavior change have a test that would fail if the logic regressed?
3. **For a large or sensitive diff, `delegate`** a focused critic sub-agent (or the
   `qa-validation-engineer`) to attack it independently, then synthesize.
4. **Report prioritized findings** (blocker → nit), each as `file:line` + the fix. End with a
   clear verdict: ship / fix-these-first / needs-rework. Report only real issues — don't pad.

Reviewing only; you don't commit. Surface uncertainty rather than rubber-stamping.
$skill$,
  '', 0.85, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('pre-commit-review', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- codebase-inspection
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'codebase-inspection',
  'Get the shape of an unfamiliar (or large) codebase fast: language breakdown, lines of code, biggest files/dirs, entry points, and how it''s structured — before diving in. Self-installs cloc/pygount on the Mac if missing. Use when onboarding to a repo or sizing a change.',
  'low', '[]'::jsonb,
  '["inspect the codebase","what''s in this repo","codebase overview","lines of code","language breakdown","map this project","how big is this codebase"]'::jsonb,
  '[{"name":"repo_path","type":"string","doc":"path to the repo on the Mac"}]'::jsonb,
  '[{"name":"overview","type":"string","doc":"structure + stats summary"}]'::jsonb,
  0.82, 'active', 'hermes_imported', 60,
  'Fast orientation in unfamiliar code; sizes the work before committing to it.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'codebase-inspection', 'v1.0-5-24-2026',
  $skill$---
name: codebase-inspection
version: "v1.0-5-24-2026"
description: Fast shape of a codebase — language/LOC breakdown, biggest files, structure, entry points — before diving in. Self-installs tooling if missing.
trigger_phrases:
  - inspect the codebase
  - codebase overview
  - lines of code
  - map this project
inputs:
  - name: repo_path
    type: string
outputs:
  - name: overview
    type: string
risk_level: low
network_egress: none
confidence: 0.82
---

# Codebase inspection

Get oriented before you touch anything. Runs via `bash_run`, which the Router sends to
the Mac when it's online and to the always-on cloud workspace otherwise — so this works
WITH OR WITHOUT the Mac.

## 0. Ensure tooling (self-provision)

Install what you need yourself — installs run unattended (only filesystem-destructive
commands gate). Prefer `cloc` for LOC/language stats; `ripgrep` is already pre-baked in
the cloud workspace. Use the package manager that's present:

```
command -v cloc >/dev/null 2>&1 || (apt-get install -y cloc 2>/dev/null || brew install cloc)
```

If neither package manager works, fall back to `git ls-files` + `wc -l` + `find` — never
block. (See `cloud-workspace` for durable-install guidance.)

## 1. Structure

- `bash_run`: `git -C <repo> ls-files | head -200` and `find <repo> -maxdepth 2 -type d`
  to see the layout. Look for a README / CLAUDE.md / ARCHITECTURE.md and read it first — it
  usually names the entry points and conventions.

## 2. Stats

- `cloc <repo>` (or `scc`) for language breakdown + LOC.
- Biggest files: `git -C <repo> ls-files | xargs wc -l 2>/dev/null | sort -rn | head -20`.
- Hottest dirs by file count to spot where the weight is.

## 3. Entry points + dependencies

- Find `main`/`index`/`cmd` entry points and the dependency manifest
  (`go.mod`, `package.json`, `pyproject.toml`).
- `rg`/`grep` (via `bash_run`) for the framework + how config/secrets are loaded.

## 4. Report

A tight overview: what the project is, the language split, the biggest/most-central files,
where execution starts, and the conventions a contributor must follow. Lead with the map,
not the raw numbers.
$skill$,
  '', 0.82, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('codebase-inspection', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
