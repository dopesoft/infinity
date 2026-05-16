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

Phases 0-7 + AGI loops + Voice (Phase 8, GPT Realtime over WebRTC) + the assembly substrate (migrations 016–021) are done. The remaining work is deeper AGI-loop Studio surfaces, compaction recovery, and the container sandbox for high-risk skills. When you build, preserve the memory-first invariant and the new closed-loop invariant: every capability emits hooks, every artifact lands in the schema with provenance, every skill failure feeds curriculum.

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
      cron/            # Phase 6 — robfig scheduler + agent executor + HTTP
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

### Migrations — NEVER claim "all migrations applied" without verifying the live DB

**This bit us on 2026-05-13.** Prod was silently missing migrations 011 (AGI loops), 012 (OpenAI OAuth), 013 (session usage), and 014 (dashboard) for weeks. Dashboard handlers were spewing `relation "mem_tasks" does not exist` warnings; AGI-loop features had no tables to write to. A prior Claude session had asserted migrations were applied without checking.

Non-negotiable rules:

- **`infinity serve` does NOT auto-migrate.** The Railway start command is `infinity serve` — migrations only apply when `infinity migrate` is run explicitly. Merging a new `core/db/migrations/NNN_*.sql` file does NOTHING to prod on its own.
- **Verify against the live DB before answering ANY question about migration / schema state.** Never infer from `git log`, `ls core/db/migrations/`, or "I just merged it." Authoritative sources only:
  - `cd core && railway run --service core -- go run ./cmd/infinity migrate` — idempotent; prints `skip` for already-applied versions and `apply` for new ones. The output IS the source of truth.
  - `npx supabase db dump --linked --schema-only` — for inspecting actual table/column state.
  - Querying `schema_migrations` directly via Supabase MCP if available.
- **After merging a new migration, run it against prod the same session.** Pattern: merge → `cd core && railway run --service core -- go run ./cmd/infinity migrate` → confirm `apply NNN_*.sql` in output → only THEN tell the user it's live. Never split "merge" from "apply" across sessions — that's how 011-014 got stranded.
- **When debugging `relation does not exist` (SQLSTATE 42P01) errors, FIRST run the migrator.** Don't write fix code, don't propose schema changes, don't speculate — run `infinity migrate` and check the output. The fix is usually that someone forgot to apply.
- **If asked "are migrations applied?" the only acceptable answer is the output of `infinity migrate` run just now.** Anything else is a guess and guessing on this question has already caused production data loss equivalents (silent feature breakage for weeks). If you cannot run the migrator in the current session, say so explicitly — do not assert.

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

### Coding via Claude Code (Max-subscription, ToS-clean)
Full wiring in [`ARCHITECTURE.md` §10](ARCHITECTURE.md#10-coding-bridge--claude-code-over-mcp--cloudflare-tunnel). Operational invariants:

- **Coding tools are wired through MCP, not raw shell-out.** The `claude_code` server in `core/config/mcp.yaml` connects over SSE to a home-Mac bridge (existing `jarvis-mac` Cloudflare Tunnel → mcp-proxy → `claude mcp serve`). 25 tools register as `claude_code__Bash`, `claude_code__Edit`, etc.
- **OAuth tokens never leave the Mac.** Anthropic's Feb 2026 ToS restricts subscription OAuth to Claude Code itself. Infinity orchestrates the CLI via the supported `mcp serve` path. Never copy `~/.claude/.credentials.json` anywhere.
- **Cloudflare Access service token is the only credential Railway holds.** `CF_ACCESS_CLIENT_ID` + `CF_ACCESS_CLIENT_SECRET` envs on core; `tools/mcp.go` attaches them via `headerRoundTripper` on the SSE transport. Two auth modes are supported in `mcp.yaml`: `bearer` and `cloudflare_access`.
- **High-risk tool calls route through the Trust queue.** `core/internal/proactive/gate.go` (`ClaudeCodeGate`) intercepts `claude_code__bash`, `claude_code__write`, `claude_code__edit` by default and inserts a `mem_trust_contracts` row. The synthetic tool result tells the model to ask the boss to approve in Studio's Trust tab. Override the verb list with `INFINITY_CLAUDE_CODE_BLOCK` / `INFINITY_CLAUDE_CODE_AUTOAPPROVE`.
- **Non-coding chat keeps using the Anthropic API.** The brain is `LLM_PROVIDER` (default Anthropic Sonnet 4.5 via API key). Claude Code on the Mac only wakes when the model picks a `claude_code__*` tool. API billing for chat, Max-subscription billing for coding.
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
| 4 | ✅ substrate | Skills system: schema, registry, process-jail sandbox, agent tools, HTTP, Studio Skills tab. **Gaps:** container sandbox for high/critical, Tests sub-tab, "+ New skill" / Import buttons. |
| 5 | ✅ | Proactive Engine: IntentFlow detector (Haiku), WAL, Working Buffer, Heartbeat ticker, Trust queue, full schema, all HTTP APIs, Heartbeat + Trust tabs. **WS-handler integration is live** — `ws.go` fires IntentFlow per turn, appends to WAL on user input, captures WorkingBuffer pairs after each turn. **Curiosity gap-scan composed into heartbeat** (NEW) — `mem_curiosity_questions` populated automatically. **Remaining gaps:** Compaction Recovery flow, 3-column Live, sub-tabs in Heartbeat, Studio approval/dismissal UI for curiosity questions. |
| 6 | ✅ | Cron + Sentinels + Voyager: robfig scheduler with agent executor, sentinel manager + skill dispatcher, schemas, HTTP APIs, combined Cron+Sentinels tab. **Voyager is on by default** (`INFINITY_VOYAGER=false` to opt out): SessionEnd → skill extractor + verifier (auto-promotes instruction-only candidates), PostToolUse → triplet discovery, **GEPA optimizer with Pareto frontier persistence** (NEW — per ICLR 2026 Oral; `frontier_run_id` + `pareto_rank` per candidate, `SampleFromFrontier` for runtime A/B), **Voyager autotrigger** (NEW — background ticker auto-fires GEPA on failing skills, closes the loop), and source extractor for `mem_code_proposals`. Studio: `/code-proposals` tab. **Remaining gaps:** curriculum generator, AutoSkill failure-reflection-patch loop, sentinel runtimes for non-webhook watch types, frontier-comparison UI in Studio, auto-apply path for approved code proposals. |
| 7 | ⚠️ partial | Audit log endpoint + viewer. **Gaps:** command palette (cmd+K), sessions rewind, settings depth, knowledge graph viewer, backup/export, full doctor suite. |
| **AGI** | ✅ | **Migration 011 — AGI loops shipped.** Procedural memory tier (CoALA — promoted skills → `tier='procedural'` rows, injected into system prompt via RRF). Reflection / metacognition (`infinity reflect` CLI + `mem_reflections` with MAR critic persona). Predict-then-act (`mem_predictions` Pre/Post pairing with Jaccard surprise scoring). A-MEM auto-linking (top-4 cosine `associative` edges at compress time). Sleep-time consolidate (8-op `ConsolidateNightly`: decay → hot-reset → cluster → contradiction resolve → associative prune → weak-edge purge → procedural reweight → forget). Curiosity scanner composed into heartbeat. Studio now surfaces reflections, high-surprise predictions, curiosity approval/dismissal, and procedural badges. See [`docs/agi-loops/README.md`](docs/agi-loops/README.md) for the trail. **Remaining gaps:** A-MEM graph viz, Pareto frontier comparison, cron the CLI loops (`infinity reflect` + `infinity consolidate`), LLM-driven prediction text for high-cost tools, cross-session reflection chains. |
| 8 | ✅ | **Voice: GPT Realtime over WebRTC** — `core/internal/voice/realtime.go` mints short-lived OpenAI `client_secret`s; the browser does the WebRTC SDP exchange P2P with `api.openai.com` (audio never touches Core); tool calls round-trip through `/api/voice/tool` so voice shares the registry + Trust gate with text, and `/api/voice/turn` fires the same memory-capture hooks. Model `gpt-realtime-1.5`, server-VAD barge-in. **Gaps:** Studio mic-button polish, wake-word activation (currently tap-to-talk). |
| **Substrate** | ✅ | **Migrations 016–021 — the assembly substrate.** Generic surface contract, runtime skill-authoring, durable workflow engine, runtime self-extension, eval scorecards, world model + agent goals, initiative + economics. See [`docs/substrate/README.md`](docs/substrate/README.md) and [`ARCHITECTURE.md` §18](ARCHITECTURE.md#18-the-assembly-substrate-migrations-016021). |
| **Gym** | ✅ substrate | **Migration 022 — plasticity control surface.** `core/internal/plasticity` reads training examples, distillation datasets/runs, model adapters, adapter evals, and policy routes. Deterministic extraction mines evals/reflections/high-surprise predictions into `mem_training_examples` via POST `/api/gym` or `infinity gym extract`; the Gym provider injects top lessons into the agent through `CompositeMemory`, so learning can change future behavior immediately. `/api/gym` feeds Studio `/gym`; `/audit` redirects into Gym's audit tab. `docker/plasticity` is the deployable worker skeleton. **Gaps:** nightly extraction scheduling, train/eval implementation, eval replay + Trust-gated adapter promotion, learned policy router integration. |

When implementing Phase 4+, preserve the memory-first invariant: every new capability emits hooks, every artifact lives in the schema with provenance.
