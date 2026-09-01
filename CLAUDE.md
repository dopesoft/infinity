# Infinity — project guide for Claude

## Rule #1 — the agent ASSEMBLES; you do not hardwire it

**This is the whole point of Infinity. Read it before you write a single line.**

Infinity has APIs, MCP servers, native tools, queues, persistent memory, the internet, and a surface to write and run code. The goal is an agent that takes a workflow described in **natural language** and **assembles it** from those building blocks — fetch from an API, batch it through an LLM, rank it, write it somewhere, surface it. That assembly *is* the product. An agent that can't assemble is just a chatbot wired to a database — good for asking your horoscope, nothing more.

**The anti-pattern — do not do this.** Building a feature as a hardwired vertical slice in Go: a bespoke table column, a bespoke Go function with the *intelligence* frozen in a string constant, a bespoke widget that only understands one source. That is not the agent doing the work — that is you doing the work and leaving the agent with nothing to assemble. Every new source then needs its own Go file, its own prompt constant, its own migration, its own widget. It does not scale and it does not move us toward AGI.

The reference failure: `core/internal/proactive/followup_scoring.go` — a Go scorer with the ranking rubric ("hard rules") baked into a `const scoringSystem` string, writing to a bespoke `mem_followups.importance` column, rendered by a Gmail-shaped `FollowUpsCard`. Email triage is a **recipe**, not Go code.

**The pattern — do this.**

- **Capabilities are recipes — skills whose body is the instruction.** "Hit the API, pull the data, analyze it, act on it" is a `SKILL.md` the LLM reads and orchestrates using the tools it already has (MCP connectors, native tools, memory, queues). The judgment — the rubric, the "hard rules" — lives in the skill body: versioned, visible in the Skills tab, improvable by Voyager/GEPA. Never in a Go `const`.
- **Contracts are generic and schema-driven.** Anything the agent produces lands in a generic, typed contract (a surface table, a queue, a memory tier) that the app renders generically. Add a new capability → it surfaces automatically. No new widget, no new column, no new loader.
- **Go is for the substrate, not the cognition.** Write Go for the building blocks — the tool, the queue, the contract, the loop that runs due skills. Never write Go that *is* the intelligence. **If there is a prompt in a `.go` file, you have almost certainly built the wrong thing.**

**The test before you build:** *Could the agent have assembled this itself, from a natural-language request, using the tools it already has?* If yes — build it as a skill/recipe over generic contracts. If no — the missing piece is a **building block** (a tool, a queue, a contract), so build that, and keep it generic. If you are reaching for a bespoke Go function with embedded judgment, stop: you are hardwiring what should be assembled.

Anything less is just a pile of code, and a pile of code is not what we are building here.

### Rule #1a — ship AGI-out-of-the-box in the same PR; you pick the form

Corollary, made explicit because it kept getting missed: **when a feature obviously needs *something extra* for the agent to behave AGI-like the first time the boss tries it, build that thing in the same PR — don't propose it after. You decide the form. Don't ask.**

The form is whatever actually closes the loop best. Optimize for the boss's functionality, not for code size:

| Form | Use when |
|---|---|
| **Generic Go building block** (tool, contract, queue, writer) | Deterministic infrastructure with no judgment. Per Rule #1: zero per-vendor branches; one shape for everything. |
| **System-prompt update** (e.g. in `cache.SystemPromptBlock`, `soul.txt`, the agent loop's per-turn overlay) | A persistent nudge that applies every turn — naming a tool, pointing at a fact, framing how to approach a class of task. Costs zero tokens beyond the prompt. |
| **Default skill** (`mem_skills` row + `SKILL.md` body) | A multi-step recipe the agent should follow each time the situation recurs (search catalog → call verbs → persist → report). Voyager can evolve it. Seed via `core/db/migrations/NNN_seed_*_skill.sql` mirroring [`023_seed_self_improve_skill.sql`](core/db/migrations/023_seed_self_improve_skill.sql). |
| **Procedural memory rule** (`mem_memories` row, `tier='procedural'`) | A "always do X / never assume Y" lesson tied to a specific class of situation, retrieved via RRF when relevant. Cheaper than a full skill when the lesson is one sentence. |
| **Heartbeat checklist** (function in `core/internal/proactive/`) | A periodic deterministic check that emits Findings the agent acts on. Pairs naturally with a skill (the checklist notices, the skill resolves). |
| **Migration / schema change** | When persistence shape matters — a new column, a new table, an index for a hot query. Always paired with whatever Go / skill / prompt uses it. |
| **Studio surface** (existing card, sub-tab) | Only when there's something visual the boss needs. Prefer extending an existing card over a new one (see "Consolidate similar surfaces" memory). |

**Concrete test on every build:** *for the agent to behave AGI-like the first time the boss tries this feature, what gives him the best result — and what combination of forms gets us there?* Whatever it is — prompt, skill, memory rule, checklist, schema, combination of all of them — **ship it in the same PR**. The boss should never have to ask "now make it smart." If you can see what closes the loop with the best functionality, do it now. Don't trim the answer to "smallest"; trim it to "right."

**Reference (right way):** the connector-identity feature shipped (a) a generic tool `connector_identity_set` for the write-back, (b) a generic store (`connectors_identities` blob in `infinity_meta`) for the persistence, (c) a system-prompt nudge in `cache.SystemPromptBlock` so the agent sees what's missing every turn, (d) a heartbeat checklist (`proactive.ConnectorIdentityChecklist`) so the loop fires autonomously without a user prompt, AND (e) a default skill (migration `033`) carrying the actual cognition for *how* to find each toolkit's profile verb. Five pieces, one PR, all generic. The skill exists because the recipe is genuinely multi-step LLM cognition; the other four are infra that doesn't need a skill.

**Reference (wrong way, fixed same session):** the same feature was initially scaffolded with a Go path to hardcode `GMAIL_GET_PROFILE` for Gmail. That would have committed Infinity to a new Go branch for every toolkit (Slack `AUTH_TEST`, GitHub `GET_AUTHENTICATED_USER`, …) — death by per-vendor wiring. Wrong form for this work.

If you find yourself writing the sentence "we could also ship this as a [skill|prompt update|memory rule|checklist]" or "I'd recommend doing X next" in your reply to the boss — **stop, decide what gives him the best functionality, do that in this PR, then reply with it done**. Surface tradeoffs only when the form choice is genuinely ambiguous; when it's obvious, just pick — and pick for *quality*, not for *minimal diff*.

### Rule #1b — skills are JUDGMENT-only; mechanics live in code, never in prose

**This is the law for the entire skills system, not one skill.** A skill body is a recipe the LLM executes, and the runtime LLM (currently a gpt-5.x OAuth brain) **will drop instructions** — it does it routinely. So any behavior expressed as a *sentence in a skill* is behavior that can silently vanish on the next run. We have re-lived this repeatedly (the triage skill told to "capture the full email body" and "draft under a Trust batch" — it summarized instead of capturing, and the real email never showed; it depended on prose for a mechanic).

The fix is NOT "rewrite skills in Go" — that kills the assembly bet of Rule #1. The fix is the split:

- **Mechanics → tools, gates, contracts (deterministic Go). Never droppable.** A mechanic is anything where there is exactly one correct behavior and no judgment: *fetch/capture/store the real artifact, use a stable id, dedupe, retry, batch, never-send, never-auto-dismiss, gating, mark coverage, emit the default action set, RRF retrieval, hook capture.* These must be guaranteed by code that runs regardless of what the skill says.
- **Judgment → skill prose. The only thing the recipe contains.** Judgment is *which / whether / what / how-much*: which mail needs a reply, what the reply says, how to prioritise, how to classify, when to escalate. This is what the LLM is actually for.

**The test, applied to every line of every skill:** *if I delete this sentence, does a feature break?* If yes, it is a mechanic in the wrong place — **move it into the tool/gate/contract and delete the sentence.** A skill you can't break by dropping a line is a deterministic skill. Reference: this is exactly why `surface_item` now *always* fetches the real email body and stamps the default actions, and why `ComposioGate` ungates drafts — those mechanics were pulled out of the triage prose into code, so the run can no longer "forget" them.

**Applies to authoring AND evolution.** When you write or refactor a skill: audit each line with the test above; every mechanic moves to code; the skill shrinks to judgment. When Voyager/GEPA drafts a skill revision, the same holds — a candidate that encodes a mechanic in prose is a bug to fix at the tool, not to approve. New skills ship judgment-only or they don't ship.

**The standing migration of intent:** sweep the existing skills the same way (inbox-triage is the reference rewrite). Any skill still leaning on prose for a mechanic gets that mechanic moved into a tool/gate, then the prose deleted. The goal state: every skill is a short list of judgment calls, and the system behaves identically whether or not the LLM remembers the recipe word-for-word.

### Rule #1c — built-but-not-wired is NOT built; it's as good as never coded

**The boss's law, verbatim: "I consider it half built if it's built and then not actually used. It's as good as NOT having coded it."** A capability that exists in the tree but no live path calls is dead weight — worse than absent, because it reads as "done" in a status table and silently lies to the next session. Shipping the write side without the read side, the function without the caller, the gate without the chokepoint, the harness without the promotion path — every one of these is the SAME failure as hardwiring (Rule #1) or leaving a mechanic in prose (Rule #1b): the work looks finished and isn't.

**The reference failure (caught 2026-06-19):** the skill-verification harness (`core/internal/voyager/harness.go`) was fully built — it runs a proposed/self-rewritten skill read-only in an ephemeral session and proves it returns real data, the boss's exact "if the skill can't be verified, the test cleans itself up" rule. It was wired into the SessionEnd extractor… but the **GEPA self-rewrite auto-promoter** (`autotrigger.maybeAutoPromote`) and the **manual `/decide` API** both called `Manager.Decide("promoted")` directly, which never invoked it. So the loop that rewrites skills to be "better" could ship an unverified, broken skill — the verifier existed and sat unused on the exact path that needed it most. Fix: the gate moved to the SINGLE chokepoint inside `Decide`, so every promote path (autotrigger, extractor, API) is covered by construction. A second instance found AND fixed the same session: `voyager.SampleFromFrontier` — the GEPA Pareto frontier was computed, persisted, and rendered in Studio, but the runtime read half that A/B-samples a candidate had **zero callers** (write side shipped, read side orphaned; migration 011 line 82 always intended "the runner samples from the frontier"). Now wired at the single LLM-only invoke seam via a generic `skills.FrontierSampler` interface (voyager implements `SampleVariant`, serve wires `AttachFrontierSampler` — no import cycle, no parallel path, reuses the existing run ledger).

**The contract on every build:**
1. **Name the live path before you write the code.** What real, autonomous trigger calls this — a hook registration, a `memProviders` append, a cron seed, a chokepoint method, an HTTP route a Studio surface actually hits? If you can't name it, you're about to half-build.
2. **Wire it in the SAME PR, at the chokepoint.** If N paths reach the same outcome, gate the one method they all flow through (like `Decide`), not N copies. One wiring, every caller covered.
3. **Prove it RUNS, not just compiles.** `go build` passing is not "wired." Trace (or test) that the trigger actually reaches the new code on the live path. "Built and green" ≠ "used."
4. **If you find built-but-unwired code, fix it or delete it — don't leave it.** A persisted artifact nothing reads is either a missing caller (wire it) or dead weight (remove it). Surface which, and when the call is genuinely a behavior change with two legitimate answers, say so and recommend — don't silently leave it stranded.
5. **Status tables must reflect reality.** Don't mark a phase ✅ or write "SampleFromFrontier for runtime A/B" in a status line when only the write side ships. Stale "done" is how things rot for weeks (see the migrations rule). If it's half-wired, say half-wired.

The test: **could a live, autonomous run reach this code today?** If no, it isn't done — finish the wiring or cut the code. There is no "I'll wire it next session."

## What this is

Infinity is a single-user, always-on AI agent with persistent memory. It is built to be the user's permanent companion across every device — a personal cognitive substrate, not a chatbot. The differentiator vs. Hermes / nanobot / openclaw is the memory layer: every observation is captured, compressed, retrieved, and consolidated so the agent's understanding of the user, their projects, and their work compounds over time.

The build is split into a Go service (Core) and a Next.js service (Studio). Both deploy to Railway. Postgres + pgvector lives on Supabase. The architecture is documented in `~/.claude/plans/built-out-this-nextjs-noble-whistle.md` and was originally specified in the `infinity.pdf` brief.

## AGI focus — what we are reaching for

Infinity is an AGI-trajectory product. Every architectural decision should be evaluated against whether it moves the agent toward open-ended, self-improving, durable cognition. Concretely:

- **Memory is the substrate, not a feature.** The `mem_*` tables (now 15+ after migration 011) are the brain. Every event in the agent loop fires a hook that captures into Postgres. Treat memory writes as load-bearing, not telemetry.
- **The agent learns continuously, and the loops are CLOSED.** Migration 011 added the substrate for: procedural memory tier (CoALA — promoted skills go into `mem_memories` tier='procedural'), reflection (`mem_reflections` + `infinity reflect` CLI, MAR critic persona pattern), predict-then-act (`mem_predictions` Pre/Post pairing with Jaccard surprise), A-MEM auto-linking (top-4 cosine `associative` edges at compress time), sleep-time consolidation (8-op nightly regime: decay → hot-reset → cluster → contradiction resolve → associative prune → weak-edge purge → procedural reweight → forget), curiosity gap-scan (composed into heartbeat), GEPA Pareto frontier persistence (per ICLR 2026 Oral pattern), and Voyager autotrigger (background ticker that closes the failure → curriculum → skill → optimization cycle GEPA was missing). **Don't reintroduce the single-champion / no-reflection / no-prediction defaults; the substrate is in place and every new feature should compose with these loops.**
- **Provenance is non-negotiable.** Every memory traces back to source observations via `mem_memory_sources`. Cascading staleness (`MarkSuperseded`) propagates through the graph. The sleep-time consolidate now ALSO auto-resolves `'contradicts'` edges by marking the older memory superseded. When the agent cites a fact, it must be able to surface the chain.
- **The agent evolves its own toolset over time.** The `tools.Registry` is intentionally pluggable (native Go tools + MCP + skills + procedural-tier injection). Promoted skills materialize as procedural memories via the `voyager.Manager.OnSkillPromoted` callback; the agent retrieves them through the same RRF machinery as semantic facts. Don't hard-couple agent logic to specific tool implementations.
- **Privacy filtering is mandatory at the capture boundary.** `memory.StripSecrets` runs before any observation hits the database. Add new patterns when you discover new secret formats.
- **The Live tab is the present, the Memory tab is the brain, the Sessions tab is the history.** Don't conflate these. Each has a distinct mental model. Reflections, Predictions, and Curiosity questions all live under Memory conceptually (the AGI-loop outputs ARE memories) — surface them there when wiring Studio.

Phases 0-7 + AGI loops + Voice (Phase 8, GPT Realtime over WebRTC) + the assembly substrate (migrations 016–021) are done. Verified against code as of this commit: nightly cognition, AutoSkill repair, proposal draft/frontier metadata, Studio frontier review, compaction recovery, and the high/critical skill container path are implemented. When you build, preserve the memory-first invariant and the new closed-loop invariant: every capability emits hooks, every artifact lands in the schema with provenance, every skill failure feeds curriculum.

## Architecture at a glance

The full wiring (boot sequence, package layout, HTTP API map, write/read paths,
phase-by-phase status with explicit gaps) lives in [`ARCHITECTURE.md`](ARCHITECTURE.md).
Read it before any non-trivial change. The summary:

```
infinity/
  core/              # Go 1.26 binary — agent loop, MCP client, memory, hooks, server
    cmd/infinity/      # cobra CLI: serve, migrate, doctor, consolidate, reflect
    config/            # mcp.yaml + embed.go (//go:embed for distroless runtime)
    db/migrations/     # 001..011 — embedded via go:embed (011 = AGI loops)
    internal/
      agent/           # loop.go (nanobot-inspired) + gate.go (ToolGate) + composite_memory.go
      llm/             # Provider interface + Anthropic, OpenAI, Google + Haiku summarizer + critic (MAR persona)
      tools/           # Registry, MCP client (SSE+bearer/cloudflare_access), native tools, memory tools
      memory/          # store, search (BM25+pgvector+graph), RRF, compress (w/ A-MEM auto-link),
                       #   forget, staleness, provenance, procedural (CoALA tier), reflection (metacognition),
                       #   predictions (predict-then-act), consolidate (sleep-time 8-op)
      hooks/           # 12-event pipeline + capture chain + predict (Pre/Post recorder)
      honcho/          # Phase 7 — dialectic peer-modelling client + MemoryProvider
      embed/           # Embedder interface (stub | http)
      skills/          # Phase 4 — registry, sandbox, runner, store, agent tools, HTTP
      intent/          # Phase 5 — IntentFlow detector (Haiku) + decision store
      proactive/       # Phase 5 — WAL, Working Buffer, Heartbeat (w/ curiosity gap-scan composed in),
                       #   Trust queue, gate.go (ClaudeCodeGate), HTTP
      voyager/         # Phase 6 — discovery, extractor, source_extractor, verifier,
                       #   optimizer (GEPA + Pareto frontier persistence + SampleFromFrontier),
                       #   autotrigger (background ticker that closes the failure→GEPA loop), HTTP API
      cron/            # Phase 6 — robfig scheduler + agent/system-task executors + HTTP
      sentinel/        # Phase 6 — manager + dispatcher (skill / log) + HTTP
      server/          # HTTP + WebSocket + JSON API + audit
  studio/            # Next.js 14 app router
    app/{live,sessions,memory,gym,skills,heartbeat,trust,cron,code-proposals,audit,settings}/
    components/        # TabFrame, MobileNav, Drawer, ToolCallCard, MemoryCard, SkillCard, …
    components/ui/     # shadcn primitives + drawer (vaul)
    lib/               # ws client, api client, utils
  docker/            # codeexec, embed, gepa, honcho, honcho-deriver Dockerfiles
  docs/              # claude-code/ + honcho/ + gepa/ + agi-loops/ (migration 011 trail)
  railway.toml
```

Service Dockerfiles: `core/Dockerfile`, `studio/Dockerfile`, `docker/gepa/Dockerfile`, `docker/honcho/Dockerfile`, `docker/honcho-deriver/Dockerfile`. Plus Redis as a managed Railway addon. Migrations are embedded into the Go binary; the runtime container has no `db/` directory. `mcp.yaml` is also embedded via `core/config/embed.go` so the distroless runtime has the canonical MCP registry without source files.

## Operating rules

These apply to every task in this project unless explicitly overridden. Bias: caution over speed on non-trivial work. The project-specific "Hard rules" below sit on top of these.

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

## Hard rules (in addition to the global ones in `~/.claude/CLAUDE.md`)

### Self-healing & error visibility — Jarvis must SEE and FIX his own code failures, and never lie about a dead path

**This is the boss's law, and it is what makes Infinity a living machine instead of a chatbot wired to a DB.** The 2026-06-24/25 incident: a Composio v3 auth change made Infinity's own request send TWO auth headers (`x-api-key` + `Authorization: Bearer`), so every call 401'd with code `10401`. Inbox triage swallowed the error and reported a green "no new mail" for days. The boss was silently missing email. Worse: the enhancement that *fixed both the auth and the truthfulness* ([a8850a16]) was auto-reverted 8 minutes after deploy by the self-improve loop ([f064371]) — which re-broke the auth AND deleted the detector in one commit, so nothing was left to notice the failure when it recurred.

The standing law, non-negotiable:

- **A failure of YOUR OWN code is a first-class bug, not noise.** A 404 on a URL that never existed is fine. A 401 from a malformed request you sent, a cron that "ran clean" because it `continue`d past every error, a connector polling a dead endpoint — that is *your code failing* and it MUST be seen, named plainly, and fixed. The deadliest bug is the one that looks like success.
- **Empty-because-broken must NEVER read as empty-because-fine.** This is the same law as the [`feedback_never_hide_errors`] memory, stated at the substrate level. Any code path that can return "nothing" must distinguish "I looked and there was nothing" from "I couldn't look." If you can't prove a real success, fail LOUD (return an error) so the run is classified failed → surfaces in "Surfaced by Jarvis" → pings → feeds the self-improve backlog. A `log.Warn` + `continue` that ends in a rosy summary is the anti-pattern.
- **Stop retrying something dead. Diagnose the cause, fix the code, re-verify.** Repeating the same failing call is never the move. When the same request keeps failing the same way, find the actual cause (wrong header, stale token, dual auth) and patch it. This is human dev/assistant behavior and it is the bar.

**The error-visibility machinery is LOAD-BEARING. Do NOT revert, disable, or "simplify away" any of it:**

- `core/internal/httpx` — the instrumented `http.DefaultTransport` (`InstallDefault`, wired once at boot in [`serve.go`](core/cmd/infinity/serve.go)) records EVERY outbound `4xx/5xx`/transport error into `mem_http_failures` (migration `153`). One seam, all default-transport clients, zero per-vendor wiring.
- `cron.classifyOutcome`'s hard-HTTP-failure veto ([`core/internal/cron/outcome.go`](core/internal/cron/outcome.go)) — a run whose session logged a hard failure (`401/403/407/429/5xx`/transport, status 0) **cannot** be classified green, no matter what the executor stamped. `scheduler.finalizeOutcome` synthesizes an `execErr` from the recorded failure so the status line, ping, and `cron_failure` backlog all engage.
- The triage truthfulness gate ([`core/internal/inbox/triage.go`](core/internal/inbox/triage.go)) — a BLIND run (cache couldn't list accounts, or no mailbox answered) returns an error and `markCoverage`s `last_status='error'`; it never says "no new mail."
- `proactive.ConnectorCoverageChecklist` — raises a finding when the connector backend errors, instead of silently agreeing "0 accounts = nothing to cover."

**A run going RED because it correctly surfaced a real external failure is that guard WORKING — it is success, never a regression to revert.** If the self-improve loop (or any session) is tempted to revert a change because "a run went red" or "the board isn't green," that is almost always the honesty guard doing its job: fix the underlying failure, do NOT remove the guard. Reverting a whole multi-part commit because one part looked broken is what cost us this fix once already — isolate the real break, never throw out the truthfulness machinery with it.

### Migrations — NEVER claim "all migrations applied" without verifying the live DB

**This bit us on 2026-05-13.** Prod was silently missing migrations 011 (AGI loops), 012 (OpenAI OAuth), 013 (session usage), and 014 (dashboard) for weeks. Dashboard handlers were spewing `relation "mem_tasks" does not exist` warnings; AGI-loop features had no tables to write to. A prior Claude session had asserted migrations were applied without checking.

Non-negotiable rules:

- **`infinity serve` DOES auto-migrate at boot — but a deploy is the only thing that triggers it.** ([`serve.go:143`](core/cmd/infinity/serve.go#L143), changed after this rule was first written.) The schema is brought current synchronously *before* the listener opens, and a failure returns from `RunE` so the process exits non-zero without ever binding the port — nothing can report ready on an unmigrated schema, and Railway keeps the previous healthy container serving. That makes a **deploy** self-consistent by construction.

  What it does NOT do is help you now. **Merging a `core/db/migrations/NNN_*.sql` file still does NOTHING to prod on its own**, because deploys are the boss's and may not happen for hours or days. Until one lands, prod is running the old binary against the old schema. So the rules below are unchanged — the boot migration is a safety net for deploys, never a reason to skip verifying.
- **Verify against the live DB before answering ANY question about migration / schema state.** Never infer from `git log`, `ls core/db/migrations/`, "I just merged it", or "serve migrates at boot anyway." Authoritative sources only:
  - `cd core && railway run --service core -- go run ./cmd/infinity migrate` — idempotent; prints `skip` for already-applied versions and `apply` for new ones. The output IS the source of truth.
  - `GET /readyz` on core — reports `schema_version` (the newest migration the RUNNING BINARY carries) and `pending_migrations` (what the live DB hasn't recorded). Note it answers for the deployed binary, so it can't see a migration you merged but haven't deployed. `migrations: "unknown: …"` means the probe could not run and is never the same as `"current"`.
  - `npx supabase db dump --linked --schema-only` — for inspecting actual table/column state.
  - Querying `schema_migrations` directly via Supabase MCP if available.
- **After merging a new migration, run it against prod the same session.** Pattern: merge → `cd core && railway run --service core -- go run ./cmd/infinity migrate` → confirm `apply NNN_*.sql` in output → only THEN tell the user it's live. Never split "merge" from "apply" across sessions, and never leave it to the next deploy — that's how 011-014 got stranded.
- **A new column the code READS must be applied before that code deploys.** Adding a column to a `SELECT` list is a breaking change against an unmigrated database: every query using it fails until the migration lands. Applying it the same session (the rule above) is what keeps that ordering safe.
- **When debugging `relation does not exist` (SQLSTATE 42P01) errors, FIRST run the migrator.** Don't write fix code, don't propose schema changes, don't speculate — run `infinity migrate` and check the output. The fix is usually that someone forgot to apply.
- **If asked "are migrations applied?" the only acceptable answer is the output of `infinity migrate` run just now.** Anything else is a guess and guessing on this question has already caused production data loss equivalents (silent feature breakage for weeks). If you cannot run the migrator in the current session, say so explicitly — do not assert.

### Every repeated shape is a PRIMITIVE — when building AND when enhancing

**The boss's law: "why aren't the headers fucking standardized and made primitives?"** Settings had ten sections and five different header treatments: some drew a big title, some drew none, one wrapped itself in a `<Card>` nobody else used. Nothing was broken in any single file. The bug was that ten components each owned the same decision, so on mobile the header moved, appeared and vanished as you swiped between sections.

**A decision that appears on more than one screen belongs to a primitive, not to the screens.** If two consumers can disagree, they eventually will, and the drift is invisible in review because each file looks fine on its own. The reference fix is [`SettingsPanel`](studio/components/settings/SettingsPanel.tsx): every Settings section renders inside it, so the header shape, the sub-tab position and the spacing are decided once. Sections supply a count and an action; they no longer decide whether a header exists.

**An OPTIONAL slot is not a primitive — it is the same inconsistency with a nicer address.** This rule was learned twice on the same screen. First each Settings section drew its own header, giving five treatments across ten sections. The fix was a `SettingsPanel`... which took `title`, `meta` and `action`, all optional. Same complaint, new coat: some panels had a sentence above the sub-tabs and some had nothing, because an optional slot still leaves each consumer deciding. The boss, twice: *"I want uniformity and consistency."*

So the props ARE the structure. `SettingsPanel` takes sub-tabs and content, and nothing else, and the compiler now rejects the alternative. When you find yourself adding an optional prop so one consumer can put something extra at the top, ask where that thing actually belongs: a count belongs on the thing it counts (a tab badge, the group label above the list), an explanation belongs on the control it explains, an action belongs beside what it acts on. If every consumer would use the slot, it is not optional and should be required; if only some would, it does not belong in the primitive at all.

**A header never repeats the name of the tab that opened it.** "It's fucking clear what tab you're in." The panel titled *Dashboard* was the section the rail calls *Home layout* — the same thing under two names, which is how a repeated heading also silently drifts. The name comes from ONE table (`SECTIONS`) and the panel restates nothing. A heading further down that names a *group* is fine; a heading at the top that names the *section* is not.

**This applies to ENHANCING, not just to building.** The moment you touch a file that hand-rolls a shape a primitive already owns, route it through the primitive in that same edit. Do not add "one more" consumer to a pattern you can see repeated, and do not leave the old one behind because your change was small. "I'll refactor later" is how the fifth header treatment got written.

**The order of preference, always:**

1. **A primitive fits** → use it as-is.
2. **A primitive almost fits** → extend it (a prop, a variant, a slot) and migrate every existing consumer in the SAME PR, so everyone gets the fix. A `level="sub"` on the tab primitive is right; a second tab component is not.
3. **Genuinely new shape** → build the primitive first, in `components/ui/` or `components/<domain>/`, and route every consumer through it from day one.

**Spacing belongs to the primitive too, and to ONE of them.** The gap under a tab strip lives on the strip (`PageTabsList` carries `mb-6`), not on the page that renders it and not on the panel below it. When three layers each contributed some, the distance from tabs to content was different on every screen and "add a bit more padding" meant editing three files and still missing one. Same for row rhythm: a `SettingRow` owns its own vertical padding, and consumers do not add `pt-3` around it.

**Rules separate SECTIONS, never rows.** A hairline under every setting turns a short list into a ledger and drowns out the section breaks that carry real meaning. Rows separate by rhythm. `SettingRow` and `ListRow` draw no rule at all; a `Section` separates with its title rule or a `tone="band"` ground.

**Controls sit in the same place on every row.** A toggle goes in `SettingRow`'s `control` slot, to the right of the label, never under the field as `children`. One screen putting its switch somewhere else is exactly what the boss notices, and "consistent" means the eye lands in the same spot on every row of every section.

**Theme belongs to the primitive too.** A consumer must never patch a primitive's dark-mode problem locally. Our theme is true black (`--background` 0%, `--muted` 5%), which inverts shadcn's default of a muted strip with a `--background` active pill: the active tab came out darker than the strip it sat in and the strip all but vanished. The fix went into the tab primitive, so every tab strip in the app got it at once.

**The test before you write any UI:** *have I seen this shape before in this codebase?* If yes, and it is not already a primitive, making it one IS the task — not a follow-up.

### Two levels of tab, and they must not look alike

**The boss's law: "why on mobile aren't u making sub tabs different than the main tabs... that's so weird looking".** A page with a main section switcher AND a switcher inside the section it lands on rendered both as the same chip rail, so mobile showed two identical strips stacked with nothing saying which outranked which.

There are exactly **two** tab looks in this product, and both live in [`ui/page-tabs.tsx`](studio/components/ui/page-tabs.tsx):

| Level | Look | Use for |
|---|---|---|
| `level="primary"` (default) | shadcn's segmented control: one muted rounded container, the active tab a raised pill inside it. Scrolls sideways on mobile rather than wrapping. | The page's own section switcher. The only strip on the screen. |
| `level="sub"` | Material underline: no container, the active tab is a word with a line under it, on a hairline running the row. Sentence case at 13px, against primary's mono caps. | ANY strip that sits inside a screen that already has one above it. |

**The test:** is there already a tab strip above this one on this screen? Then it is `sub`. If it is the only one, it is `primary`. Nesting a third level means the page needs splitting, not a third look.

**Every tab strip in the app routes through `PageTabsList` / `PageTabsTrigger`.** Not raw `<TabsList>`, not a hand-rolled row of buttons, not a bespoke pill group. The layouts are exported as `TAB_LAYOUT_PRIMARY` / `TAB_LAYOUT_SUB` and style their children through `[&>button]:` selectors matching BOTH Radix's `data-state=active` and `aria-selected=true`, so a component that must own its own state (`ScopedTabs`, whose search field is scoped to the strip) still gets the identical look by applying the layout to its container. There is no reason left to hand-roll one.

`columns={2|3}` remains a niche fallback for short text-only labels that must fill the row, and it only applies to a primary strip.

**Never add a scrolling tab strip to the header.** That rule is unchanged: when navigation outgrows a phone, grow the drawer.

### One spinner, and the app mark is where "the app is loading" is said

**The boss's law: "what I want is a consistent spinner when things are loading."** Studio had one loading indicator, on the dashboard beside the greeting, and roughly thirty hand-rolled `Loader2 animate-spin` glyphs at four sizes everywhere else. So a tap on the rail was answered by nothing at all, and he tapped again.

- **`<Spinner>` ([`ui/spinner.tsx`](studio/components/ui/spinner.tsx)) is the only spinning glyph in the product.** Kibo's pinwheel (`LoaderPinwheel`) on the `pinwheel` keyframes in `tailwind.config.ts` (750ms/turn, faster than Tailwind's 1s `animate-spin`, because at 16-20px a slow turn reads as a static glyph). It owns the animation, so a consumer never writes `animate-spin` and never picks a different icon. No `Loader2`, no rotating `RefreshCw`, no bare `animate-spin` on a `<svg>`.
- **NEVER put an enter animation on a spinning element.** `animate-in` / `fade-in` set `animation-name: enter` and land LATER in the generated stylesheet than the rotation, so a spinner wearing both fades in and then sits perfectly still - the "why does it just flash, it doesn't even spin" bug, 2026-09-01. One CSS animation per element: fade the WRAPPER, spin the glyph. Same trap for `transition-colors` + `transition-transform` on one element, where the last class silently wins.
- **The hold is one full rotation minimum** (`MIN_VISIBLE_MS`, sized to the keyframe duration). A third of a turn is a flicker, not movement, and movement is the entire signal.
- **"A screen is loading" is said by the app mark, once, in the one place that is on every screen.** [`AppMark`](studio/components/nav/AppMark.tsx) is the Infinity logo, and it becomes the pinwheel while a screen is on its way in - top of the rail on desktop, top-left of the bar on mobile, both through the same component. Never add a second global loading surface (a top progress bar, a page overlay), and never draw the logo yourself.
- **The signal is [`lib/loading`](studio/lib/loading/index.tsx), and it has exactly two entry points.** `RouteLoadingWatcher` (mounted once in the root layout) sees EVERY internal anchor click through one capture-phase document listener and releases on the `usePathname()` change, so a new `<Link>` anywhere is covered by construction. `useAppRouter()` replaces `useRouter()` at every cross-page `push`/`replace`; a plain `useRouter()` is still right for a same-page query update (`useTabParam`, `<FocusSheet>`) - that is not a screen load.
- **A page declares its own first fetch with `usePageLoading(loading)`.** The hold releases the first time that flag goes false and is never taken again: a realtime push, a debounced search or a poll happens with the page already on screen and MUST NOT move the mark. A logo that spins whenever a row changes upstream is noise, and he stops reading it.
- **It never spins for the agent.** Jarvis thinking, a tool running, a reply streaming: the activity ledger, `<RunIndicator>` and the bridge pill own those. The mark answers one question, "did my tap do anything?"
- **Every hold ends.** The store holds for a 350ms minimum (so an instant load still registers as feedback rather than a strobe) and auto-releases on a TTL (so a hold nobody closed cannot spin forever). A stuck spinner is the UI version of a false green - see the self-healing rules above. Both are pinned by tests in [`lib/loading/store.test.ts`](studio/lib/loading/store.test.ts).

### A press is answered by the thing you pressed, instantly

**The boss: "it feels like a click takes about a second to even register... it just doesn't feel as tactile as it should."** Nothing was actually slow - the mark repaints ~8ms after a click. What was missing was any reaction *at the point of contact*: every nav target had a `hover:` wash, which a touchscreen never fires and which is already showing when a mouse presses, so mousedown changed nothing at all.

- **`:active` is the only feedback that can be instant**, because it is the only one the browser paints before the JavaScript for the navigation runs. Every interactive target gets one. It lives in [`ui/press.ts`](studio/components/ui/press.ts) as `PRESS_ICON` (square targets: rail icons, header buttons, the app mark) and `PRESS_ROW` (full-width rows). Two shapes because there are two - an icon can take a squeeze, a row cannot without looking like it wobbled. Do not write a third `active:` treatment.
- **A press must fully arrive inside the length of a tap.** `transition-colors` at the default 150ms means a 90ms tap shows half a colour and throws it away, which is indistinguishable from nothing. Press transitions are `duration-100` and cover `transform` as well as colour. The `<Button>` primitive carries the same rule for every button in the app.
- **Navigation answers on the PRESS, not on arrival.** The rail and drawer marker moves to the item you pressed the moment you press it, driven by `useNavTarget()` from [`lib/loading`](studio/lib/loading/index.tsx) - `lit` is where the app is pointing, `here` is where you are, and only `here` may set `aria-current`. The mobile drawer dismisses on the press too; it used to sit open for the whole navigation, so the one thing covering the screen was also the one thing giving no sign it had heard you.

### A control that cannot do anything is NOT SHOWN

**The boss's law: "why a fucking greyed out save button with nothing to even save vs only showing it when we're trying to actually save something?"** A disabled control is a promise the screen is not keeping. It sits in the spot a real action belongs, gives no clue what would enable it, and after the second time he taps it the page reads as broken.

**The default is to render nothing.** A Save button appears when there is an unsaved change and disappears when there is not. A Retry appears when something failed. A Remove appears on a row that exists. Nothing reserves space for a state it is not in.

**`disabled` is allowed in exactly two cases:**

1. **The action is one tap away and the blocker is visible on the same screen.** A Save inside an open form the boss is typing into may sit disabled until the required field has content, because the empty field explaining it is right there. The moment the enabling control is off-screen, in another tab, or a fact he cannot see, hide the button instead.
2. **The action is running.** A button showing a spinner mid-request must stay put and stay disabled, or the layout jumps and he taps twice. Per the server-tracked-progress rule this state comes from `mem_runs`, not local `useState`.

**Never disable to communicate.** If he cannot do a thing, the reason is a sentence where the thing would be, not a dead button he has to guess at. "Add a card first" beats a greyed-out Pay.

**The same rule for containers.** Do not render a section heading, an empty group, a filter row or a toolbar for content that is not there. The reference failure: Settings → Vault → Personal info could not reach the server, and rendered a warning, a second warning contradicting the first, four empty field groups, a "Paying by phone" heading and a dead Save. One sentence was the whole correct output.

**And when a fetch fails, say THAT, once, and render nothing else.** An empty shell after a failed load is the silent-green failure of the UI layer: it looks like "you have nothing" when it means "I could not look". Same law as the self-healing rules above, applied to a screen.

### Say it ONCE — no restating, no explaining yourself twice

**The boss's law: "you explain yourself a million times... you explain yourself more than once ALL the time."** A screen states each fact exactly one time, in the one place it belongs. Repetition is not reassurance; it is noise that buries the thing he actually came for, and it makes a short panel scroll.

The reference failure, Settings → Vault → Cards. Six elements, two facts:

```
CARDS                                                    ← tab
Cards                                                    ← heading, same word
The cards Jarvis can pay with… never the number.         ← what they are
Every purchase stops and asks you first…                 ← fine, new fact
SAVED CARDS  0                                           ← the count
No cards yet. Add one and Jarvis can buy things          ← the count AGAIN,
  and settle bills by phone.                               then the description AGAIN
[+ Add a card]                                           ← the action, a third time
```

**The checks, applied to every screen before it ships:**

1. **A section heading never repeats the active tab, the page title, or the nav label that got you here.** If the tab says Cards, the panel does not say Cards. Drop the heading, not the tab.
2. **A count appears once.** A badge, a group label, or a sentence — pick one. `SAVED CARDS 0` and "No cards yet" are the same fact twice.
3. **An empty state states the absence and stops.** "No cards yet." It does NOT re-explain what the feature is (the description did that) and does NOT describe an action a visible button already offers. The `[+ Add a card]` button IS the instruction. Only when there is no button and no obvious next step may an empty state carry one short clause saying how to get the first one.
4. **A description says what is NOT already obvious from the labels around it.** If every word of it can be inferred from the heading, the field names and the button, delete it. Keep the part that carries a real constraint or consequence ("he never sees the number", "every purchase stops for your approval") and cut the part that narrates.
5. **A per-row helper sentence repeated down a list is a column header, not a row.** Sixteen fields each captioned "He can give this out when a shop or a company asks" is the same sentence sixteen times. Say it once above the group and let the rows carry bare controls.
6. **Never caption a control with its own name.** A switch labelled "Enable" inside a row labelled "Notifications" adds nothing.

**The test:** read the panel top to bottom and mark every fact. If any fact is marked twice, delete the weaker instance. When in doubt about which to cut, keep the one closest to where he acts and cut the prose.

**This trades against clarity exactly once:** a genuine warning before a destructive or irreversible action may restate the consequence next to the button even if it appears above. Nothing else earns a second telling.

### Plain English in the UI — never developer language on a screen

**The boss's law, and he has had to say it twice: "I fucking hate dev language in a UI."** Studio is an interface for a person, not a database browser. Every word rendered to a screen is copy, and copy gets written the way he would say it out loud. The reference failure: Settings → Vault listed his secrets as `secret:vault.phone_passphrase`, `secret:vault.identity`, `secret:vault.payment_card`. Those are storage keys. Two of them were not even cards, and they were printed in a list captioned "cards Jarvis can pay with".

**Banned on any surface the boss reads** — labels, headings, buttons, empty states, placeholders, toasts, errors, tooltips, card titles, list rows, chart axes, notification text:

- `snake_case`, `camelCase`, `kebab-case`, `SCREAMING_CASE`, dotted keys (`vault.payment_card`), prefixed ids (`secret:`, `br_`, `mem_`)
- table, column, env-var, package or type names (`mem_followups`, `INFINITY_VAULT_KEY`, `WorkItem`, `ClaudeCodeGate`)
- our internal jargon where a plain word exists: **ward** → *file he can't open*, **obligation** → *purchase*, **surface item** → *what he brought you*, **contract** → *approval*, **provider** → *brain* or the vendor's actual name, **frontier / candidate** → *version he's trying*
- raw enum values (`needs_you`, `awaiting_3ds`, `pending_approval`) rendered as-is. Map them to a sentence: *waiting on you*, *your bank wants to check it's you*.
- restating the mechanism when the outcome is the point. "Blocked outright" is a state name; "He can never open this" is what happened.

**The one exception, and its shape.** A genuinely technical identifier may be *shown* when the boss needs the literal string to act on it: a tool name, a file path, a glob, a git SHA, a model id, an env var he has to set. Even then:

- the **listed name is the readable one**; the identifier goes in the **detail view, a secondary meta line, or on tap** — never as the row's title. A run card says "Checked your inbox", and `inbox-triage` lives inside it. See [[feedback_readable_names_engine_on_click]].
- a file path, glob or SHA renders in `font-mono` so it reads as a literal, and is never bent into prose.

**Naming in code is unaffected.** `ward`, `obligation`, `mem_surface_items` are good names for a Go type, a column and a prop, and renaming them buys nothing. This rule is about the *string that reaches a screen*. The two live in the same file constantly, and that is fine: `w.level === "private" ? "He can never open this" : ...`.

**The test before you ship a screen:** read every visible string aloud as if to him. If a word only makes sense because you have read the source, it is the wrong word. And if you are about to render a value straight out of the database because it "looks close enough", it isn't; map it.

### Reuse-first componentization — extend the primitive, don't re-roll it

**This is the same idea as Rule #1 applied to UI.** Studio is a single product, not a pile of one-off React files. Every modal, drawer, card, list row, form, button cluster, etc. must be a **named, reused primitive** with its own discipline baked in — not hand-rolled in each consumer. The recurring failure is the opposite: a new screen reaches for raw `<Dialog>` / `<Drawer>` / `<pre>` / `<a href>` / bare grid, copies whatever the last screen did, and silently drops a constraint (`min-w-0`, `break-all`, `pb-safe`, `dvh`, `truncate` chain). The boss then catches the same bug three times in a row in different components. This rule exists to make that physically harder.

**Reuse applies at the CONCEPT level too, not just the primitive level — this is the one the boss keeps catching.** Before you add a new *surface* (a page, a section, a dashboard card, a widget), ask: **what existing surface already models this concept's DATA SHAPE?** If a feature is "a unit of work with a status," it is a `WorkItem` on the **Agent Work board** (add a new `kind`, mirror the closest existing one) — NOT a new card. If it's a labeled identity fact, it's the **Memory boss-profile**. If it's an approval/permission, it's **Settings → Trust/Privacy**. If it's anything the agent surfaces for the boss, it's the generic **`surface_item`** contract. Fold in by extending that surface's data feed (a new `kind`, row, or `MemoryProvider`), never by authoring a sibling surface. The reference miss: a **Mandate** (definition-of-done with criteria + status) was first shipped as a bespoke `MandatesCard` on the dashboard, then correctly folded into the work board as `WorkItem kind:"mandate"` ([`core/internal/dashboard/mandates.go`](core/internal/dashboard/mandates.go) mirrors `plans.go`; criteria reuse the plan progress bar + ObjectViewer detail). When in doubt, fold in and say so — splitting later is cheap, making the boss hop around is not.

**The contract.** Before you build a UI surface, search the primitives directory:

```bash
ls studio/components/ui/         # base primitives (button, dialog, drawer, tabs, responsive-modal, modal-content, …)
ls studio/components/dashboard/  # dashboard primitives (Section, TileCard, …)
ls studio/components/            # higher-level shared widgets (TabFrame, MobileNav, ChatBubble, …)
```

If a primitive fits, **use it as-is**. If it almost fits, **extend the primitive** (add a variant, a prop, a sub-component) so every existing consumer benefits — don't fork a copy. If nothing fits and you genuinely need a new shape, **create a primitive in `studio/components/ui/` or `studio/components/<domain>/`** and route every consumer through it from day one. A new bespoke wrapper that lives next to its only caller is wrong — that's how the per-screen world gets recreated.

**Modal-specific contract (this kept burning us).** For ANY preview / info / action surface that is "modal-like":

- Use **`<ResponsiveModal>`** from [`studio/components/ui/responsive-modal.tsx`](studio/components/ui/responsive-modal.tsx). Do NOT import `<Dialog>` or `<Drawer>` directly. `ResponsiveModal` owns the Dialog-vs-Drawer split, the a11y title/description, the overflow chain (`overflow-hidden min-w-0 max-w-full` at every level), the pinned footer, `pb-safe`, the size scale (`sm` / `md` / `lg`).
- Use **`<ResponsiveModalHeader>`** when you need an icon + eyebrow + title row. Don't hand-roll header chrome — the default header in `ResponsiveModal` and the optional `ResponsiveModalHeader` cover every case we've needed.
- For body content use the primitives in [`studio/components/ui/modal-content.tsx`](studio/components/ui/modal-content.tsx): **`<ModalSection>`** (labeled context card), **`<ModalPre>`** (wrapping prose/JSON), **`<ModalCode>`** (whitespace-preserving diff/code with internal scroll), **`<ModalUrl>`** (bare URL with `break-all` and pinned icon), **`<ModalDl>`** (key/value metadata grid), **`<ModalChips>`** (eyebrow chip row). NEVER reach for a bare `<pre>`, `<a href={someUrl}>`, or `<dl>` inside a modal body — that's the smell that re-introduced mobile overflow three times.
- Only `<Dialog>` and `<Drawer>` primitives directly are still allowed for **non-modal** drawers: the global nav drawer (`MobileNav`), the sessions drawer (`SessionsDrawer`), the canvas git panel. These are persistent navigation, not previews. Everything else routes through `ResponsiveModal`.

**The same rule applies to other categories.** When you add a dashboard card or list row, route it through `Section` + `TileCard` from [`studio/components/dashboard/Section.tsx`](studio/components/dashboard/Section.tsx) which already carry `min-w-0 max-w-full overflow-hidden`. When you add a button cluster, use the `<Button>` primitive — never raw `<button className="h-10 …">`. When you add a form field, use the shadcn `<Input>` / `<Textarea>` primitives — they already enforce 16px iOS font, `inputMode`, the focus ring.

**The test before you build a UI surface:**
1. Does an existing primitive fit? → use it.
2. Does an existing primitive almost fit? → extend it (new prop/variant/slot) and migrate every existing consumer in the same PR.
3. Genuinely new shape? → build it in `studio/components/ui/` or `studio/components/<domain>/` with the same overflow / safe-area / typography discipline as the surrounding primitives, then route every consumer through it from day one.

**Anti-patterns that are bugs, not preferences:**
- Importing `<Dialog>` or `<Drawer>` from `@/components/ui/dialog` or `@/components/ui/drawer` in a new modal-style surface. Use `<ResponsiveModal>` instead.
- A `<pre>`, `<dl>`, or bare `<a href={url}>` inside any modal body. Use `ModalPre` / `ModalDl` / `ModalUrl`.
- A `useIsDesktop()` + `Dialog`/`Drawer` switch in a consumer. That logic belongs in the primitive.
- A "I'll just make it work for this one screen first and refactor later" copy of an existing primitive. There is no "later" — refactor in this PR or use the primitive as-is.
- Two hooks/utilities with the same name in different folders (we had two `useIsDesktop` files until 2026-05-16). Consolidate to one.

**Why this rule exists.** When primitives own the discipline (overflow, safe area, typography, a11y, motion), each consumer becomes trivially correct. When discipline lives in the consumer, the next consumer copies a buggy version and the bug ships. Reuse-first is how we keep mobile responsive, keep a11y intact, and keep the codebase from drifting into 30 different snowflake versions of the same surface.

### Server-tracked progress — every long action persists across focus, navigation, refresh, and device

**This bit us on 2026-05-16.** The boss fired a cron manually, tabbed to another page, came back, and the spinner was gone — even though the run was still in flight on the server. Root cause: a `useState<{status:"running"|"ok"|"error"}>` keyed by row id in [`studio/app/cron/page.tsx`](studio/app/cron/page.tsx). When the user navigated away, the page component unmounted, the state evaporated, and on return there was no signal that the run was still happening. The same anti-pattern was present in ~every screen that fires a long action (heartbeat run, skill invoke, voyager optimize, gym extract, etc).

**The boss's words: "REALTIME + PERSISTANCE ACROSS FOCUS AND EVERYTHING ELSE."** This is a hard rule, not a preference.

Non-negotiable rules:

- **Any server-side action that takes longer than ~250ms — or that the user might want to watch — MUST track its state in the database.** Use the [`mem_runs`](core/db/migrations/035_mem_runs.sql) substrate: a row is inserted with `status='running'` when the action starts, updated to `'ok'` / `'error'` (and `ended_at`) when it finishes. Every long action surface gets a `mem_runs` row, no exceptions: cron run, skill invoke, heartbeat scan, voyager optimize, gym extract, GEPA run, sentinel dispatch, anything else you add.
- **The Go side uses `runs.Track(ctx, kind, target_id, label, source, fn)`** in [`core/internal/runs`](core/internal/runs/runs.go). It books the row, runs your function, closes the row with the result. Never roll your own start/end UPDATE pair — use the helper. Adding a new kind is one new constant string; do NOT add a new column or table per kind.
- **The Studio side uses the generic `useRuns({kind?, targetId?})` hook** from [`studio/lib/runs`](studio/lib/runs/useRuns.ts) and the `<RunIndicator>` primitive. They subscribe via the existing realtime publication (`mem_runs` is replicated) so updates push live AND survive any navigation/refresh because they read server state on mount. NEVER track "is this running?" in component-local `useState`.
- **The spinner must work across:** route navigation, browser tab switch, browser refresh, app backgrounding, a second device opening the same screen. If your design can't do that, you've used the wrong primitive.
- **Optimistic local state is allowed for short interactions only** (input typing, dropdown open, form draft) — anything where the server has no opinion about "in progress" and the cost of being wrong is zero. The moment a request fires that the server should track, the source of truth is the server.
- **When you add a new long-action endpoint, wire it through `runs.Track` in the same PR.** Don't merge a `POST /api/foo/run` that doesn't book a `mem_runs` row — the next person to consume it will reintroduce the local-state anti-pattern because there's no server state to read.

The pattern this replaces (the bug): UI button onClick → `setLocal({running:true})` → `await fetch(...)` → `setLocal({running:false})`. Every one of these is a regression of this rule and should be migrated to `useRuns` + `<RunIndicator>` the moment you touch the file.

### Mobile-first responsiveness — iOS Safari + Chrome are the primary targets

The user lives on their phone. Every UI change must be designed for mobile first and verified at 375px. These rules are non-negotiable:

- **`100dvh` everywhere, never `100vh`.** iOS Safari's address bar makes `vh` unreliable. Use `min-h-app` / `h-app` / `dvh` / `svh` Tailwind utilities defined in `studio/app/globals.css`.
- **`viewport-fit=cover` + `interactiveWidget: "resizes-content"`** on every page. Both are set in `studio/app/layout.tsx`. The `resizes-content` hint is what makes iOS Safari shrink the layout viewport when the keyboard opens, so a sticky composer stays above the keyboard automatically.
- **`env(safe-area-inset-*)`** on every fixed/sticky surface. Use `pt-safe` / `pb-safe` / `px-safe`. Composer, top bar, and bottom drawers all need it.
- **16px minimum font on form fields.** Enforced globally via `font-size: max(16px, 1rem)` in `globals.css`. Do not override — iOS Safari auto-zooms below 16px and breaks the layout.
- **44×44 minimum touch targets.** Every `<Button>` defaults to `h-11`. The mobile drawer nav uses `min-h-12` rows. Don't shrink interactive elements below this.
- **`overscroll-behavior: contain`** on body and every scroll region. Already global; preserve it on new scrollers (`scroll-touch` utility wraps both that and `-webkit-overflow-scrolling: touch`).
- **WebSocket auto-reconnect on `pageshow` + `focus` + `visibilitychange`.** iOS Safari kills sockets when the tab is backgrounded. The reconnect logic lives in `studio/lib/ws/provider.tsx` — never strip those listeners.
- **Composer pattern: `position: sticky`, never `position: fixed` with keyboard open.** iOS Safari has a known bug where `fixed` elements jump on keyboard open. Use sticky inside a flex column. See `studio/components/Composer.tsx`.
- **No hover-only affordances.** Use long-press `ContextMenu` (Radix) for secondary actions on touch.
- **`inputMode` set on every Input/Textarea.** `text` for free-form, `search` on search boxes, `numeric` for amounts.
- **Test at 375px / 768px / 1280px.** Chrome DevTools mobile emulator with real iPhone UA covers most cases; verify on a real iPhone Safari before declaring a UI change shipped.
- **Lucide Icons via `lucide-react`** (the shadcn default). No Tabler, no Heroicons, no Material Icons. Stay consistent. Import as `import { Send, Plus } from "lucide-react"` and use `className="size-4"` for sizing.
- **Tailwind utility classes only — zero `style={}` props.** Already in the global rules; restating because it's especially load-bearing here. The tier palette / semantic colors / safe-area utilities are all Tailwind-native. The Composer's imperative `el.style.height` for textarea auto-resize is the only sanctioned exception (it sets a calculated value, not a styling concern).
- **Hydration discipline.** Never call `Math.random()` / `crypto.randomUUID()` / `Date.now()` inside a `useState` initializer — defer to `useEffect`. Every locale-dependent `<time>` or `<Badge>` rendering a date must use `suppressHydrationWarning` because UTC server vs client locale produces divergent text.

### Navigation pattern (mobile vs desktop)

- **Desktop (`lg:`+):** centered `<TabNav>` in the sticky header + `<ThemeToggle>` on the right.
- **Mobile (`<lg`):** logo on the left, right-hand hamburger that opens `<MobileNav>` — a draggable bottom-sheet drawer (vaul) with the full nav list and theme toggle. Tap a row to navigate; the drawer auto-dismisses.
- **Modals follow the same convention.** Anything that would be a desktop `<Dialog>` opens as a `<Drawer>` from the bottom on mobile. Use the `<Drawer>` primitive in `studio/components/ui/drawer.tsx` — it's a vaul wrapper that already wires `pb-safe`, max-height `92dvh`, the drag handle, and the popover token theming.
- **Don't add scrolling tab strips.** When you need more navigation than fits on a phone, grow the drawer — never put scrollable horizontal tabs in the header.

### Theme: true black, no slate undertones

The dark theme uses pure black (`hsl(0 0% 0%)`) backgrounds with neutral grays (no blue/slate hue rotation). When defining new tokens or components, keep that constraint — accent colors stay desaturated unless they're carrying meaning (info / success / warning / danger / tier palette). Don't reintroduce the shadcn-default `222 47%` slate.

### Logging — severity must match reality

**Railway's log shipper tags every line by stream: stdout → `severity:info`, stderr → `severity:error`.** Go's stdlib `log.Printf` writes to stderr by default, so a `log.Printf("wrote %d bytes", n)` shows up in Railway as a red `error` row even though it's a success. That's how you end up scrolling past dozens of fake errors looking for the real one — and eventually missing the real one. Non-negotiable rules:

- **Successes go to stdout. Failures go to stderr.** No exceptions. Either use a package-level `infoLog := log.New(os.Stdout, "", log.LstdFlags)` for the info lines and keep stdlib `log` for errors, or use `slog` with structured JSON output (preferred for new packages) so Railway picks up the explicit `level` field instead of falling back to stream-based severity.
- **Never use `log.Printf` for a "wrote / loaded / started / reconnected / promoted / queued / approved" line.** Those are all info-level. The reference fix lives in [`core/internal/skills/materialize.go`](core/internal/skills/materialize.go) — copy the `infoLog` pattern from there.
- **Real errors stay on stderr exactly as today.** `log.Printf("scan: %v", err)` / `log.Printf("write %s: %v", path, err)` is correct usage. Don't move failure logs to stdout to "clean up" Railway — that destroys the signal you actually need.
- **When in doubt, ask: would I want this page me at 3am?** If yes → stderr. If no → stdout. There is no third stream.
- **When you touch a package that uses stdlib `log` for both success and failure, split it.** Don't leave the next session to discover the same Railway noise pattern in a different package. Sweep the file you're editing while you're there.

### Memory + capture invariants

- **Every event in the agent loop fires a hook.** When you add a new transition (e.g. a Phase 4 skill execution), call `hooks.Pipeline.Emit` with the right `EventName`. The pipeline is async — never block the loop on capture.
- **Privacy first.** All hook capture goes through `memory.StripSecrets` before persistence. Adding a new capture point? It must use the same path.
- **Compression is opt-in via `INFINITY_AUTO_COMPRESS=true`.** Don't enable by default — Haiku calls cost money. The `infinity consolidate --compress` command exists as the manual/cron path.
- **Provenance link is mandatory for every promoted memory.** `mem_memory_sources` rows must be written when an observation becomes a memory. Don't skip the bookkeeping.
- **No service-role secrets in the codebase.** Infinity Core connects to Postgres directly via `pgx`. We don't use Supabase's PostgREST — service_role and anon JWTs stay in the Supabase dashboard.

### MemoryProvider — the canonical way to inject persistent context

When a substrate table needs to *shape the agent's reasoning every turn* (not just live on a dashboard), the right pattern is a new `agent.MemoryProvider` impl, NOT a special-case in `composite_memory.go` or the agent loop. `agent.CompositeMemory` is pure orchestration — it concatenates whatever `MemoryProvider`s `serve.go` hands it. Each provider implements `BuildSystemPrefix(ctx, sessionID, query) (string, error)` and returns either an XML-tagged block to inject or the empty string to stay silent.

Live providers (in registration order at [`serve.go:523`](core/cmd/infinity/serve.go#L523)):

1. **`memory.Searcher`** — RRF retrieval + boss-profile primer + procedural skill top-K.
2. **`plasticity.Provider`** — Gym reflex lessons (top-5 from `mem_training_examples`).
3. **`worldmodel.GoalsProvider`** — agent's own goals (`mem_agent_goals`), so the agent reasons knowing what it's pursuing.
4. **`memory.ReflectionChainsProvider`** — cross-session meta-lessons (`mem_reflection_chains`), round-robin so the same lesson doesn't recycle every turn.
5. **`memory.SelfModelProvider`** — `<self_model_alert>` emitted ONLY when a metric in `mem_agent_metrics` is >2σ off its 14-day baseline.
6. **`initiative.LoopAwarenessProvider`** — `<call_rate>` showing the session's own tool-call rate, emitted only once the session crosses 50% of the LoopGate ceiling so the agent self-throttles before the gate trips.
7. **`honcho.MemoryProvider`** — peer dialectic representation when `HONCHO_BASE_URL` is set.
8. **`bridge.MemoryProvider`** — active bridge (Mac / Cloud) tool-availability overlay.

When you add a new persistent-context surface, follow the pattern:

1. New file: `<package>/provider_<name>.go` with a `BuildSystemPrefix` implementation. Return `""` when there's nothing to say — providers MUST stay silent when their substrate is empty so the system prompt doesn't bloat.
2. Wire it in at the `memProviders := []agent.MemoryProvider{}` block in [`serve.go`](core/cmd/infinity/serve.go).
3. Wrap the XML tag in a one-line caption that tells the agent how to interpret it (`"Lessons distilled from prior sessions. Apply when the situation matches."`) — naked tags get ignored.
4. If the data needs background rollup (like `mem_agent_metrics`), register the rollup as a `memory.RegisterConsolidateHook(...)` callback so it runs as part of nightly cognition. Don't add a 9th op to `ConsolidateNightly` itself — the hook seam is the contract.

### Runaway-loop defense (two layers, one source of truth)

The agent should NEVER spin in retry loops or burn through hundreds of tool calls without progress. We defend in two layers, both backed by the same in-memory call tracker in [`core/internal/initiative/loop_gate.go`](core/internal/initiative/loop_gate.go):

- **Primary (the agent itself):** `LoopAwarenessProvider` injects a `<call_rate>` block into the system prompt once the session crosses ~50% of the call ceiling. The agent sees `calls_in_300s=27/50` and `most_repeated_in_60s=tool_x × 2 (hard-blocked at 3)` and is expected to consolidate, batch, or stop on its own.
- **Safety net (the gate):** `LoopGate` hard-blocks any (tool + canonicalised input) hash that repeats ≥3 times in 60s with an explanatory tool-result the model sees. Sessions exceeding 50 tool calls in 5 min route through the Trust queue so the boss can approve "keep going" or stop the session.

Tunables live as constants in [`loop_gate.go`](core/internal/initiative/loop_gate.go) (`repeatLimit`, `repeatWindow`, `sessionCallCeiling`, `sessionWindow`). Bump them if a legitimate heavy workflow legitimately needs more headroom — but the right fix is usually a better skill, not a wider window.

### Coding via Claude Code (Max-subscription, ToS-clean)
Full wiring in [`ARCHITECTURE.md` §10](ARCHITECTURE.md#10-coding-bridge--claude-code-over-mcp--cloudflare-tunnel). Operational invariants:

- **The brain contract (updated 2026-08-30): conversation = WHATEVER THE BOSS PICKED IN STUDIO SETTINGS; coding on the Mac bridge = Claude Code on his Claude MAX SUBSCRIPTION, always, no matter what Settings says; coding on the Cloud bridge = the Settings brain.** The Settings choice is the `setting.provider` / `setting.model` pair in `infinity_meta` (see [[reference_runtime_brain_override]]), and it is not just chat: `activeModelProvider` resolves it for every auxiliary call too (compression, reflection, intent, triage, session naming, skill drafting, Voyager). Vendors are added by pasting an API key in Settings, which stores it in `mem_provider_keys` and registers the provider in the live registry with no redeploy. As of 2026-08-30 the vendors are Claude (API key), ChatGPT (API key), ChatGPT (plan, OAuth) and DeepSeek; Gemini is listed but `core/internal/llm/google.go` is a STUB, so the registry refuses to register it and Settings says "no client yet". The Mac exception is absolute and is enforced in code, not by convention: every Mac coding run (`code_agent`, and `background_build` when the Mac is active) goes through `tools.ClaudeCodeRunner` ([`core/internal/tools/code_agent.go`](core/internal/tools/code_agent.go)), which PROVES the subscription before launching: it reads the Mac's `~/.claude.json` `oauthAccount` (`organizationType: claude_max`, `billingType: stripe_subscription`), refuses an `apiKeyHelper` or a missing sign-in, unsets `ANTHROPIC_API_KEY` for the run, and stamps `meta.auth` ("Max subscription · email") on the `mem_runs` row (bridge pill + dock + result footer show it). Never add a path that lets the chat model author code on the Mac by default, and never tell it to "do it yourself" when Claude is out of usage: that is how the ChatGPT plan got spent on 2026-08-26 and 2026-08-28.
- **Coding tools are wired through MCP, not raw shell-out.** The `claude_code` server in `core/config/mcp.yaml` connects over SSE to a home-Mac bridge (existing `jarvis-mac` Cloudflare Tunnel → mcp-proxy → `claude mcp serve`). 25 tools register as `claude_code__Bash`, `claude_code__Edit`, etc.
- **OAuth tokens never leave the Mac.** Anthropic's Feb 2026 ToS restricts subscription OAuth to Claude Code itself. Infinity orchestrates the CLI via the supported `mcp serve` path. Never copy `~/.claude/.credentials.json` anywhere.
- **Cloudflare Access service token is the only credential Railway holds.** `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET` envs on core; `tools/mcp.go` attaches them via `headerRoundTripper` on the SSE transport. Two auth modes are supported in `mcp.yaml`: `bearer` and `cloudflare_access`.
- **Destructive tool calls route through the Trust queue; writing code does not.** `core/internal/proactive/gate.go` (`ClaudeCodeGate`) lets `claude_code__write` / `claude_code__edit` through unattended (source edits are git-reversible) and is **content-aware on `claude_code__bash`**: read-only git and any non-destructive command (`go build`, `npm test`, `git commit`, `mkdir`, …) run unattended, only **filesystem-destructive** commands (`rm`/`rmdir`/`shred`/`dd`/`mkfs`/`truncate`/`find -delete`/`mv …/dev/null`, incl. ones hidden behind `&&`/`|`/`xargs`/`sudo`/subshells) insert a `mem_trust_contracts` row. This is what lets the boss walk away during a code change. Git history/remote, DB drops, and deploys are deliberately NOT gated here (git is the boss's; deploys are blocked by the no-deploy rule). Tunables: `INFINITY_CLAUDE_CODE_AUTOAPPROVE` (suffixes that always allow — e.g. `bash` for full box trust), `INFINITY_CLAUDE_CODE_BLOCK` (suffixes subject to gating, default `bash`), `INFINITY_CLAUDE_CODE_BASH_GATE_ALL=true` (restore legacy gate-every-bash), `INFINITY_CLAUDE_CODE_BASH_DESTRUCTIVE` (extra destructive substrings). Per-session approvals are durable in `mem_trust_contracts` over an 8h sliding window.
- **Chat does NOT ride `LLM_PROVIDER`, and the Anthropic API key is never the chat brain.** The env var is only a boot fallback; the running brain follows Settings, and `serve.go` explicitly overrides the boot provider with the persisted Settings choice ("boot provider follows Settings: env LLM_PROVIDER ignored"). The boss's standing order, said more than once: Claude is his MAX plan through Claude Code on the Mac and nothing else, so `ANTHROPIC_API_KEY` is never to carry a conversation and `LLM_STANDBY_PROVIDERS` stays unset unless he sets it himself. Claude Code on the Mac only wakes when the model picks `code_agent`, `background_build` with the Mac active, or a `claude_code__*` tool.
- **`claude mcp serve` does NOT take `--dangerously-skip-permissions`.** In MCP-serve mode the parent client (Infinity) is the permission authority — no CLI prompts to skip. The launchd plist (`docs/claude-code/launchd/dev.dopesoft.mcp-proxy.plist`) reflects this.
- **`mcp.yaml` is embedded into the binary** via `core/config/embed.go` (`//go:embed mcp.yaml`) so Railway's distroless runtime finds the registry without source files. Local dev still reads the on-disk copy first.

### Honcho (dialectic peer modelling)
Full wiring in [`ARCHITECTURE.md` §11](ARCHITECTURE.md#11-honcho--dialectic-peer-modelling). Operational invariants:

- **Honcho complements `mem_*`, doesn't replace it.** Set `HONCHO_BASE_URL` to enable. The `agent.CompositeMemory` chains Infinity's `Searcher` (RRF retrieval, primary) with `honcho.MemoryProvider` (peer representation). Hooks mirror user/assistant messages into Honcho async; the representation is cached for 60s and folded into the system prompt under "About the boss (Honcho dialectic)".
- **Privacy holds.** `memory.StripSecrets` runs *before* the hook fires, so Honcho only ever sees redacted text — same redaction Infinity stores in `mem_observations`.
- **Two services: `honcho` (FastAPI) + `honcho-deriver` (worker)**, both built from `plastic-labs/honcho` main. The deriver consumes the Redis queue and refreshes peer reps async. Without it, the API still works — reps just don't update.
- **The Honcho Dockerfile CMD rewrites the DB URL scheme at startup** (`postgresql://` → `postgresql+psycopg://`) so Railway reference variables (`${{core.DATABASE_URL}}`) keep working without leaking the secret through Claude logs.

### GEPA (Hermes-style skill self-evolution)
Full wiring in [`ARCHITECTURE.md` §12](ARCHITECTURE.md#12-voyager--gepa--skill-self-evolution). Operational invariants:

- **Phase 1 only — SKILL.md optimization.** No code mutation, no full DSPy compilation. Same scope Hermes ships today.
- **Sidecar at `docker/gepa/Dockerfile`** runs a Genetic-Pareto loop over Anthropic Haiku. `POST /api/voyager/optimize { "skill": "<name>" }` triggers a run.
- **Hard gates in `core/internal/voyager/optimizer.go`**: ≤15KB, valid frontmatter, non-empty, non-identical, ≥1 candidate scored. Winners land in `mem_skill_proposals` and route through the existing Trust/decide flow.
- **Triggered manually for now**, not auto on failure rate. Cost ~$0.05–$0.20 per run.

### Voyager source extractor — code self-noticing
Fourth Voyager hook (alongside extract/discover/verify). Drafts source-refactor proposals when the boss visibly fought the same file. Operational invariants:

- **SessionEnd hook** registered in [`serve.go`](core/cmd/infinity/serve.go) as `voyager.source_extract` → `Manager.OnSessionEndSource`. Lives in [`source_extractor.go`](core/internal/voyager/source_extractor.go).
- **Heuristic:** scan ≤200 observations per session; flag any file with ≥3 `claude_code__edit`/`__write` calls AND either ≥1 failure attributed to that file or ≥1 session-wide bash failure. Up to 3 files per session draft proposals.
- **Drafts** via Haiku → `mem_code_proposals` rows with `{title, rationale, proposed_change, risk_level, evidence}`. LLM-less path inserts a stub row so the signal is preserved.
- **Approval is intent only.** The `mem_code_proposals.status` column does NOT auto-apply edits. Any actual `claude_code__edit/__write/__bash` still routes through `ClaudeCodeGate` → `mem_trust_contracts` → boss approval per call. Voyager is doing autonomous *noticing*, not autonomous writing.
- **Studio surface:** `/code-proposals` tab in `NAV_OVERFLOW`. Realtime via `mem_code_proposals` publication entry in migration 010.
- **APIs:** `GET /api/voyager/code-proposals?status=` · `POST /api/voyager/code-proposals/:id/decide` (`approved` | `rejected` | `applied`).

### Deployment + operations
Full diagram in [`ARCHITECTURE.md` §14](ARCHITECTURE.md#14-deployment). Operational invariants:

- **Six Railway services**: `core`, `studio`, `gepa`, `honcho`, `honcho-deriver`, `redis`. Each has its own root directory pinned by `railway.toml`. Only `core` and `studio` expose public ingress; everything else runs on the Railway private network (`<service>.railway.internal:<PORT>`).
- **Studio's public URL is `https://infinity.dopesoft.io`** (CNAME via Cloudflare → `studio-production-2ca0.up.railway.app`). DNS lives in Cloudflare (Namecheap is just the registrar).
- **Postgres lives on Supabase.** Session pooler at `aws-1-us-west-1.pooler.supabase.com:5432` (IPv4) — direct connection is IPv6-only on free tier and unreachable from Railway. Honcho shares this DB (separate tables, same schema for now).
- **`infinity migrate` reads embedded migrations by default.** Pass `--dir core/db/migrations` only when iterating on schema locally.
- **`mcp.yaml` is embedded into the core binary.** Editing it requires a rebuild + push. For local dev the on-disk copy takes priority.
- **Never commit `.env`.** Already gitignored. Set production vars via `railway variables --service <name> --set KEY=VALUE`.
- **Don't run git or deployment commands unless the user explicitly asks.** Inherits from the global rules.

### Railway CLI — use it for debugging, do NOT speculate

**You have full `railway` CLI access from this repo.** Project: `Infinity` · environment: `production`. When a production service is misbehaving (timeouts, blank metrics, weird behaviour) — *check Railway directly before guessing*. Do not write "you should check the Deployments tab" or "looks like it might be sleeping" — pull the data yourself.

Standard debug recipe when a service is acting up:

```bash
railway status                                 # confirm project/env/service
railway logs --service <name> --lines 200 -d   # last 200 lines of DEPLOY logs (runtime)
railway logs --service <name> --lines 200 -b   # BUILD logs (Dockerfile failures)
railway logs --service <name> --http           # HTTP request/response logs
railway deployment list --service <name>       # recent deploys, SUCCESS / FAILED / REMOVED
railway variables --service <name> --kv        # env var NAMES (the values are secret — never paste back in responses)
```

Useful refinements:
- `-f "@level:error"` or `-f "context deadline"` — Railway log filter syntax (text + `@level:` selectors).
- `--json` — structured output when you need timestamps + attributes for analysis.
- `--lines N` disables streaming (one-shot fetch); without it, the command streams forever.
- Logs are bound to a **deployment ID**. If `--lines` returns only "Starting Container" the container booted then died before producing app stdout — that's a crash, not silence. Look at the build logs and the env vars next.

Allowed without asking:
- `railway logs ...` (any flag)
- `railway status`, `railway service`, `railway deployment list`
- `railway variables --service X --kv` (treat the values as opaque secrets — never echo them; redact when summarising)
- `railway run <cmd>` (executes locally with prod env injected — fine for read-only diagnostics like `curl honcho.railway.internal/health`)

Always require explicit user authorisation:
- `railway deployment redeploy` / `railway up` — those are deploys, blocked by the same global rule that gates `git push`.
- `railway variables --set KEY=VALUE` — already pre-authorised per memory (`feedback_railway_env_authorized.md`), but never set keys whose names look secret unless the user told you the value verbatim.
- `railway down`, deleting services, deleting volumes — destructive, ask first.

Redaction discipline: when you paste log lines back to the user, scrub anything that looks like a JWT, API key, Bearer token, full DSN, or PII. Names of env vars (left side of `=`) are fine to surface. Values are not. The `--kv` view we use is for *understanding what's configured*, not for echoing the values to anywhere.

**Failure mode to avoid:** writing a response that ends with "check your Deployments tab" or "looks like it might be X" when one `railway logs --lines 200 -d` would have answered the question. The user has explicitly empowered you to run this CLI — guessing instead is the worst-of-both-worlds option.

### NEVER `set -x` in any container entrypoint or shell that runs in prod

**This bit us on 2026-05-13.** A diagnostic entrypoint with `set -e -u -x` on the Honcho services traced every command — including `[ -n "$LLM_OPENAI_API_KEY" ]` and case-match on `$DB_CONNECTION_URI` — to stderr. Railway captures stderr, so the full OpenAI API key and the Supabase Postgres password ended up in the deploy logs verbatim. Both had to be rotated.

Rules:

- **No `set -x` (or `set -xv`, `bash -x`, `PS4` tracing) in any shell that touches secrets.** This is non-negotiable. Even one `set -x` line in a startup script is enough to leak everything in the environment.
- **Never compare secret env vars directly in shell.** `[ -n "$SECRET" ]` is safe under `set +x` but expands the value under `set -x`. Use `test -n "${SECRET:-}" && echo set || echo unset` patterns and *only* echo the boolean result.
- **Never `echo $SOMETHING_URI` where URI could contain credentials.** Use `printf '%s' "$URI" | cut -d: -f1` to surface just the scheme. Treat *every* connection string as a credential, not a URL.
- **If a diagnostic entrypoint is needed, use explicit `echo` lines with redaction baked in.** `echo "boot: DB_SCHEME=$(printf '%s' "${URI:-}" | cut -d: -f1)://[redacted]"` is the canonical form. Never the raw value.
- **`docker/honcho/Dockerfile` and `docker/honcho-deriver/Dockerfile` are the reference shape** — copy those entrypoints when adding a new sidecar service.

If you ever need full command tracing for one-shot debugging, do it on a *local* container with throwaway secrets, never on Railway. And revert before deploying.

## Common gotchas

- **`pnpm-workspace.yaml` is sensitive on pnpm 11.** It must contain `allowBuilds: { unrs-resolver: false }` or installs fail with `ERR_PNPM_IGNORED_BUILDS`. Don't strip that key.
- **Studio Dockerfile needs Node 22+ and `CI=true`.** pnpm 11 imports `node:sqlite` (Node 22+ only) and runs an interactive `confirmModulesPurge` prompt during `pnpm build` unless `CI=true` is set.
- **The compressor only activates when `LLM_PROVIDER=anthropic`.** It needs an `*llm.Anthropic` to construct the Haiku summarizer. With OpenAI or Google providers the capture pipeline still runs but observations don't promote to memories until you switch back or build the equivalent summarizer.
- **`vector(384)` is hardcoded.** Embedding dim is fixed across schema, embedder interface, and HNSW index. Changing the embedding model means changing the schema.
- **The `infinity_search` FTS configuration falls back gracefully on managed Postgres.** Synonym dictionaries can't load on Supabase (no FS access). The migration logs a NOTICE and uses plain `english` stemming. Functional, just no `db→database` synonym expansion.

## Where to look first

When asked to add a feature, **first re-read [Rule #1](#rule-1--the-agent-assembles-you-do-not-hardwire-it)** — most "features" are skills the agent should assemble, or generic building blocks, not hardwired Go. Then read these files in this order to understand the relevant slice:

- **The assembly substrate (Rule #1 build-out)**: start at [`docs/substrate/README.md`](docs/substrate/README.md) — the surface contract (`mem_surface_items` + `surface_item`/`surface_update` tools + generic `SurfaceCard`) and the skill-authoring loop (`skill_create` + `Registry.Put`). This is the canonical example of "building block, not vertical."
- Agent loop end-to-end: `core/internal/agent/loop.go` → `core/internal/server/ws.go` → `studio/hooks/useChat.ts` → `studio/components/ConversationStream.tsx`
- Adding a tool: `core/internal/tools/registry.go` → `core/internal/tools/{httpfetch,websearch,memory_tools}.go` → `core/internal/tools/defaults.go`
- Memory write path: `core/internal/hooks/capture.go` → `core/internal/memory/store.go` → `core/internal/memory/compress.go`
- Memory read path: `core/internal/memory/search.go` → `core/internal/memory/rrf.go` → `core/internal/server/memory_api.go` → `studio/app/memory/page.tsx`
- LLM provider boundary: `core/internal/llm/provider.go` → `core/internal/llm/anthropic.go` (reference impl)
- Mobile UI conventions: `studio/app/globals.css` → `studio/components/TabFrame.tsx` → `studio/components/MobileNav.tsx` → `studio/components/ui/drawer.tsx` → `studio/components/Composer.tsx`
- Skills end-to-end: `core/internal/skills/loader.go` → `registry.go` → `runner.go` → `registry_tools.go` → `studio/app/skills/page.tsx`
- Proactive engine: `core/internal/intent/flow.go` → `core/internal/proactive/{wal,buffer,heartbeat,trust}.go` → `studio/app/{heartbeat,trust}/page.tsx`
- Cron + Sentinels: `core/internal/cron/{scheduler,executor_agent}.go` → `core/internal/sentinel/{manager,dispatcher}.go` → `studio/app/cron/page.tsx`
- Claude Code coding bridge: `core/config/mcp.yaml` → `core/internal/tools/mcp.go` (bearer auth) → `core/internal/agent/{gate,loop}.go` → `core/internal/proactive/gate.go` → `docs/claude-code/SETUP.md`
- Honcho user modelling: `core/internal/honcho/{client,provider}.go` → `core/internal/agent/composite_memory.go` → `core/cmd/infinity/serve.go` → `docs/honcho/SETUP.md`
- GEPA skill optimizer: `docker/gepa.Dockerfile` + `docker/gepa/server.py` → `core/internal/voyager/optimizer.go` → `core/internal/voyager/api.go` (`POST /api/voyager/optimize`) → `docs/gepa/README.md`
- Voyager source extractor (code proposals): `core/internal/voyager/source_extractor.go` (`OnSessionEndSource`, file-fight detection, Haiku draft) → `core/internal/voyager/api.go` (`/api/voyager/code-proposals` + `/decide`) → `core/db/migrations/010_code_proposals.sql` → `studio/app/code-proposals/page.tsx`
- **AGI loops (migration 011)**: start at [`docs/agi-loops/README.md`](docs/agi-loops/README.md) for the trail + citations. Then by loop:
  - Procedural tier: `core/internal/memory/procedural.go` → `core/internal/memory/search.go` (AttachProcedural + BuildSystemPrefix) → `core/internal/voyager/voyager.go` (OnSkillPromoted callback in `Decide`) → wired in `core/cmd/infinity/serve.go`
  - Reflection: `core/internal/llm/critic.go` (MAR persona) → `core/internal/memory/critic_adapter.go` → `core/internal/memory/reflection.go` → `core/cmd/infinity/reflect.go` (CLI)
  - Predict-then-act: `core/internal/memory/predictions.go` (store + Jaccard SurpriseFor) → `core/internal/hooks/predict.go` (PredictionRecorder) → `core/internal/agent/loop.go` (emits `tool_call_id` in Pre/Post payloads)
  - A-MEM auto-linking: `core/internal/memory/compress.go` → `autoLinkNeighbours` (async, top-4, cosine ≥ 0.65, writes `relation_type='associative'` to `mem_relations`)
  - Sleep-time consolidate: `core/internal/memory/consolidate.go` → `ConsolidateNightly` (8-op) → invoked by `core/cmd/infinity/consolidate.go`
  - Curiosity gap-scan: `core/internal/proactive/curiosity.go` (4 detectors + `CuriosityChecklist` + `ComposeChecklists`) → wired into heartbeat in `serve.go`
  - GEPA Pareto frontier: `core/internal/voyager/optimizer.go` (`paretoFrontier`, `insertFrontierProposal`, `SampleFromFrontier`)
  - Voyager autotrigger: `core/internal/voyager/autotrigger.go` (background ticker) → started in `serve.go` when `GEPA_URL` is set

## Phase status

See `ARCHITECTURE.md` § 12 for the granular gap list. Summary:

| Phase | Status | What |
|---|---|---|
| 0 | ✅ | Foundation: repo, CLI, health, studio shell |
| 1 | ✅ | Working text bot: agent loop, LLM provider, WebSocket, Live tab |
| 2 | ✅ | Tools and MCP: registry, websearch, filesystem, codeexec, httpfetch, Settings tab |
| 3 | ✅ | Memory: agentmemory port, triple-stream retrieval, 12-hook pipeline, compression, Memory tab, provenance |
| 4 | ✅ substrate | Skills system: schema, registry, process-jail sandbox, Docker-backed container sandbox for high/critical risk, Trust gate for critical skills, agent tools, HTTP, Studio Skills tab. **Gaps:** Tests sub-tab, "+ New skill" / Import buttons. |
| 5 | ✅ | Proactive Engine: IntentFlow detector (Haiku), WAL, Working Buffer, compaction recovery on "where were we"/resume/summary turns, Heartbeat ticker, Trust queue, full schema, all HTTP APIs, Heartbeat + Trust tabs. **WS-handler integration is live** — `ws.go` fires IntentFlow per turn, appends to WAL on user input, captures WorkingBuffer pairs after each turn. **Curiosity gap-scan composed into heartbeat** — `mem_curiosity_questions` populated automatically, with approval/dismissal and stale cleanup. **Remaining gaps:** 3-column Live, sub-tabs in Heartbeat. |
| 6 | ✅ | Cron + Sentinels + Voyager: robfig scheduler with agent executor plus `system_task` executor, seeded nightly cognition cron, sentinel manager + skill dispatcher, schemas, HTTP APIs, combined Cron+Sentinels tab. **All six sentinel watch types live** (`webhook`, `file_change`, `memory_event`, `external_api_poll`, `threshold`, plus `goal_event` — wired in [`core/internal/sentinel/runtimes.go`](core/internal/sentinel/runtimes.go)). **Voyager is on by default** (`INFINITY_VOYAGER=false` to opt out): SessionEnd → skill extractor + verifier (auto-promotes instruction-only candidates), PostToolUse → triplet discovery, **GEPA optimizer with Pareto frontier persistence** (`frontier_run_id` + `pareto_rank` per candidate, `SampleFromFrontier` for runtime A/B), **Voyager autotrigger**, AutoSkill repair recipe, proposal draft merge metadata, and source extractor for `mem_code_proposals`. Studio groups merged drafts, GEPA frontiers, and standalone candidates. **Remaining gaps:** curriculum generator, NaturalLanguageScheduleInput live parser, Verification log sub-tab, auto-apply path for approved code proposals. |
| 7 | ⚠️ partial | Audit log endpoint + viewer. **Gaps:** command palette (cmd+K), sessions rewind, settings depth, knowledge graph viewer, backup/export, full doctor suite. |
| **AGI** | ✅ | **Migrations 011 + 043/044/047/053/054 — AGI loops shipped, scheduled, AND closing.** Procedural memory tier (CoALA — promoted skills → `tier='procedural'` rows, injected into system prompt via RRF). Reflection / metacognition (`infinity reflect` CLI + `mem_reflections` with MAR critic persona). Predict-then-act (`mem_predictions` Pre/Post pairing with Jaccard surprise scoring plus gated Haiku prediction text for expensive/high-risk calls). A-MEM auto-linking (top-4 cosine `associative` edges at compress time). Sleep-time consolidate (8-op `ConsolidateNightly`: decay → hot-reset → cluster → contradiction resolve → associative prune → weak-edge purge → procedural reweight → forget). Nightly cognition cron runs reflection, compression, consolidation, Gym extraction, world-model entity extraction, reflection-chain building, agent-metric rollup, stale sweep, and a surface report. Curiosity scanner composed into heartbeat. **Loops are now closed via the MemoryProvider pattern**: agent goals (`worldmodel.NewGoalsProvider`), cross-session lessons (`memory.NewReflectionChainsProvider`), self-model drift (`memory.NewSelfModelProvider`), and daily cost burn (`initiative.NewCostBudgetProvider`) all enter the system prompt every turn — see [`core/cmd/infinity/serve.go`](core/cmd/infinity/serve.go) memProviders block. Heartbeat composes `GoalEntityUrgencyChecklist` + `CalendarPrepChecklist` for pre-emptive nudges; sentinels watch `goal_event`. Studio surfaces reflections, high-surprise predictions, Memory graph, curiosity approval/dismissal, procedural badges, GEPA frontier comparison, and Lab → Gym (candidates/adapter eval/routes scaffolding). See [`docs/agi-loops/README.md`](docs/agi-loops/README.md) for the trail and [`docs/agi-loops/PHASE_9_GYM_TRAINING.md`](docs/agi-loops/PHASE_9_GYM_TRAINING.md) for the adapter-training plan when GPU is greenlit. |
| 8 | ✅ | **Voice: GPT Realtime over WebRTC** — `core/internal/voice/realtime.go` mints short-lived OpenAI `client_secret`s; the browser does the WebRTC SDP exchange P2P with `api.openai.com` (audio never touches Core); tool calls round-trip through `/api/voice/tool` so voice shares the registry + Trust gate with text, and `/api/voice/turn` fires the same memory-capture hooks. Model `gpt-realtime-2.1-mini`, server-VAD barge-in. **Gaps:** Studio mic-button polish, wake-word activation (currently tap-to-talk). |
| **Substrate** | ✅ | **Migrations 016–021 — the assembly substrate.** Generic surface contract, runtime skill-authoring, durable workflow engine, runtime self-extension, eval scorecards, world model + agent goals, initiative + economics. See [`docs/substrate/README.md`](docs/substrate/README.md) and [`ARCHITECTURE.md` §18](ARCHITECTURE.md#18-the-assembly-substrate-migrations-016021). |
| **Gym** | ✅ substrate | **Migration 022 — plasticity control surface.** `core/internal/plasticity` reads training examples, distillation datasets/runs, model adapters, adapter evals, and policy routes. Deterministic extraction mines evals/reflections/high-surprise predictions into `mem_training_examples` via POST `/api/gym`, `infinity gym extract`, or the nightly cognition cron; the Gym provider injects top lessons into the agent through `CompositeMemory`, so learning can change future behavior immediately. `/api/gym` feeds Studio `/gym`; `/audit` redirects into Gym's audit tab. `docker/plasticity` is the deployable worker skeleton. **Gaps:** sidecar train/eval implementation, eval replay + Trust-gated adapter promotion, learned policy router integration. |

When implementing Phase 4+, preserve the memory-first invariant: every new capability emits hooks, every artifact lives in the schema with provenance.
