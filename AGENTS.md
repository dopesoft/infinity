# AGENTS.md — operational rules for AI agents working in this repo

Audience: Claude Code, Cursor, Codex CLI, and any other coding agent invoked against the Infinity repo. CLAUDE.md is the full project guide; this file is the short list of "don't do this dumb shit" rules that have already burned us.

If you're an agent, **read this top-to-bottom before answering any question about schema, migrations, or deploy state.**

---

## Rule #1 — the agent assembles; you do not hardwire it

This is the product. Infinity has APIs, MCPs, tools, queues, memory, the internet, and a coding surface. The goal is an agent that takes a workflow in **natural language** and **assembles it** from those pieces. That assembly is the point — an agent that can't assemble is just a chatbot.

**Do not** build a feature as a hardwired Go vertical: a bespoke column + a Go function with the rubric frozen in a string `const` + a single-source widget. That's you doing the work, not the agent. It doesn't scale and it isn't an agent.

**Do** build it as a recipe: a `SKILL.md` whose body is the instruction ("hit the API, pull the data, analyze it, act"), orchestrated by the LLM over generic, schema-driven contracts the app renders automatically. Go is for building blocks — tools, queues, contracts, the loop that runs due skills — never for the cognition. **A prompt in a `.go` file means you built the wrong thing.**

Reference failure: `core/internal/proactive/followup_scoring.go` — ranking rubric baked into a `const`. Email triage is a recipe, not Go.

The test: *could the agent have assembled this itself from a natural-language request using the tools it has?* If yes → skill over generic contracts. If no → the missing piece is a generic building block; build that. Full rationale in [CLAUDE.md](CLAUDE.md#rule-1--the-agent-assembles-you-do-not-hardwire-it).

---

## Rule #1a — ship AGI-out-of-the-box in the same PR; you pick the form

Corollary of Rule #1, made explicit because it kept getting missed: **when a feature obviously needs *something extra* for the agent to behave AGI-like the first time the boss tries it, build that thing in the same PR — don't propose it after. You decide the form. Don't ask.**

The form is whatever closes the loop best. Optimize for the boss's functionality, not for code size:

- **Generic Go building block** (tool, contract, queue, writer) — deterministic infra with no judgment. Per Rule #1: zero per-vendor branches.
- **System-prompt update** — a persistent nudge that applies every turn (in `cache.SystemPromptBlock`, `soul.txt`, the agent loop's per-turn overlay). Costs zero tokens beyond the prompt.
- **Default skill** (`mem_skills` row + `SKILL.md`) — a multi-step recipe the agent follows when the situation recurs (catalog search → call verbs → persist → report). Seed via `core/db/migrations/NNN_seed_*_skill.sql` mirroring `023_seed_self_improve_skill.sql` (three idempotent INSERTs).
- **Procedural memory rule** — `mem_memories` row with `tier='procedural'`, retrieved via RRF when relevant. Cheaper than a full skill when the lesson is one sentence.
- **Heartbeat checklist** (function in `core/internal/proactive/`) — periodic deterministic check that emits Findings. Pairs naturally with a skill: checklist notices, skill resolves.
- **Migration / schema change** — when persistence shape matters. Always paired with whatever Go / skill / prompt uses it.
- **Studio surface** — only when there's something visual the boss needs. Prefer extending an existing card over a new one.

**Concrete test on every build:** *for the agent to behave AGI-like the first time the boss tries this feature, what gives him the best result — and what combination of forms gets us there?* Whatever it is — prompt, skill, memory rule, checklist, schema, combo — **ship it in the same PR**. The boss should never have to ask "now make it smart." Trim to *right*, not to *small*.

**Reference (right way, 2026-05-16):** the connector-identity feature shipped (a) a generic tool `connector_identity_set`, (b) a generic store (`connectors_identities` blob in `infinity_meta`), (c) a system-prompt nudge, (d) a heartbeat checklist, AND (e) the default skill in migration `033`. Five pieces, one PR, all generic. The skill exists because the recipe is genuinely multi-step LLM cognition; the other four are infra that doesn't need a skill.

**Reference (wrong way, caught and fixed before merge):** the same feature was initially scaffolded with a Go path to hardcode `GMAIL_GET_PROFILE` for Gmail — would have committed Infinity to a new Go branch for every toolkit. Wrong form for this work; the cognition belonged in a SKILL.md.

If you find yourself writing "we could also ship this as a [skill|prompt update|memory rule|checklist]" or "I'd recommend doing X next" in a reply — **stop, decide what gives the best functionality, do that in this PR, then reply with it done**. Surface tradeoffs only when the form choice is genuinely ambiguous; when it's obvious, pick — and pick for *quality*, not for *minimal diff*.

---

## Operating rules

These apply to every task in this repo unless explicitly overridden. Bias: caution over speed on non-trivial work.

1. **Think before coding.** State assumptions explicitly. Ask rather than guess. Push back when a simpler approach exists. Stop when confused.
2. **Simplicity first.** Minimum code that solves the problem. Nothing speculative. No abstractions for single-use code.
3. **Surgical changes.** Touch only what you must. Don't improve adjacent code. Match existing style. Don't refactor what isn't broken.
4. **Goal-driven execution.** Define success criteria up front and loop until verified. Strong success criteria let you loop independently.
5. **Use the model only for judgment calls.** Use for classification, drafting, summarization, extraction. Do NOT use for routing, retries, deterministic transforms. If code can answer, code answers.
6. **Token budgets are not advisory.** Per-task: 4,000 tokens. Per-session: 30,000 tokens. If you're approaching the budget, summarize and start fresh. Surface the breach — do not silently overrun.
7. **Surface conflicts, don't average them.** If two patterns contradict, pick one (more recent / more tested), explain why, and flag the other for cleanup.
8. **Read before you write.** Before adding code, read exports, immediate callers, and shared utilities. If you don't understand why existing code is structured a certain way, ask.
9. **Tests verify intent, not just behavior.** Tests must encode WHY behavior matters, not just WHAT it does. A test that can't fail when business logic changes is wrong.
10. **Checkpoint after every significant step.** Summarize what was done, what's verified, what's left. Don't continue from a state you can't describe back.
11. **Match the codebase's conventions, even if you disagree.** Conformance > taste inside the codebase. If you think a convention is harmful, surface it — don't fork silently.
12. **Fail loud.** "Completed" is wrong if anything was skipped silently. "Tests pass" is wrong if any were skipped. Default to surfacing uncertainty, not hiding it.

---

## Migrations — verify, never assert

**Burned us on 2026-05-13.** Prod silently ran without migrations 011 (AGI loops), 012 (OpenAI OAuth), 013 (session usage), and 014 (dashboard) for weeks. Dashboard endpoints logged `relation "mem_tasks" does not exist`; AGI-loop features had nowhere to write. A prior agent session had told the boss "all migrations applied" without checking. That was a lie by omission and it cost real time.

Rules:

1. **`infinity serve` does NOT auto-migrate.** Railway's start command is `infinity serve`. New migration files merged to `core/db/migrations/` do NOTHING in prod until `infinity migrate` is run.
2. **Verify against the live DB. Always.** Never infer migration state from git, `ls`, or "I just merged it." Run the migrator and read its output:
   ```bash
   cd core && railway run --service core -- go run ./cmd/infinity migrate
   ```
   `skip` = already applied. `apply` = it just ran. The output is the source of truth.
3. **Merge and apply in the same session.** After landing a new migration: merge → run the migrator against prod → confirm `apply NNN_*.sql` → THEN tell the boss it's live. Never end a session with a merged-but-unapplied migration.
4. **`relation does not exist` (SQLSTATE 42P01) errors → run the migrator FIRST.** Before proposing schema changes, before writing fix code, before speculating. The fix is almost always "someone forgot to apply."
5. **"Are migrations applied?" has exactly one valid answer: the output of `infinity migrate` you just ran.** Anything else is a guess. If you can't run the migrator in the current session, say so explicitly. Do not assert.

---

## Other rules that have bitten us

These are documented in detail in [CLAUDE.md](CLAUDE.md). Restated here because agents skim:

- **No git or deploy commands without explicit ask.** No `git add/commit/push`, no `railway up`, no `railway deployment redeploy`. The boss runs git and deploys himself.
- **No destructive SQL without explicit approval.** No `DELETE`, `DROP`, `TRUNCATE` without "yes, delete it." Prefer soft deletes.
- **Never query the prod DB with credentials inlined from `railway variables --kv`.** Use the Supabase CLI / MCP or `railway run -- go run ./cmd/infinity ...` instead. Don't leak DSNs into the transcript.
- **No inline CSS.** Tailwind utility classes only. The Composer's textarea auto-resize is the sole sanctioned `el.style.height` exception.
- **Mobile-first.** 375px is the target. `100dvh` not `100vh`. `pt-safe` / `pb-safe` on sticky surfaces. 16px minimum on inputs. 44×44 touch targets.
- **Lucide icons only.** No Tabler, no Heroicons, no Material.
- **`SECURITY DEFINER` + explicit `search_path`** on every Supabase function/RPC. RLS on every table.
- **Embeddings are `vector(384)` — hardcoded across schema, embedder, and HNSW index.** Don't change the dim without a coordinated migration.
- **Compressor needs `LLM_PROVIDER=anthropic`.** Capture still runs with OpenAI/Google but observations don't promote.

---

## Default debugging recipe for prod

Before guessing, run the actual tools:

```bash
railway status                                 # confirm project/env/service
railway logs --service <name> --lines 200 -d   # last 200 lines of runtime logs
railway logs --service <name> --lines 200 -b   # build logs
railway deployment list --service <name>       # recent deploys
railway variables --service <name> --kv        # env var NAMES only (values are secret — never echo)
```

For schema/migration questions specifically, prepend:

```bash
cd core && railway run --service core -- go run ./cmd/infinity migrate
```

If you find yourself typing "you should check the Deployments tab" or "looks like it might be X" — STOP. Run the CLI command instead.
