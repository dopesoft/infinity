# Infinity — project guide for Claude

## What this is

Infinity is a single-user, always-on AI agent with persistent memory. It is built to be the user's permanent companion across every device — a personal cognitive substrate, not a chatbot. The differentiator vs. Hermes / nanobot / openclaw is the memory layer: every observation is captured, compressed, retrieved, and consolidated so the agent's understanding of the user, their projects, and their work compounds over time.

The build is split into a Go service (Core) and a Next.js service (Studio). Both deploy to Railway. Postgres + pgvector lives on Supabase. The architecture is documented in `~/.claude/plans/built-out-this-nextjs-noble-whistle.md` and was originally specified in the `infinity.pdf` brief.

## AGI focus — what we are reaching for

Infinity is an AGI-trajectory product. Every architectural decision should be evaluated against whether it moves the agent toward open-ended, self-improving, durable cognition. Concretely:

- **Memory is the substrate, not a feature.** The 12 `mem_*` tables are the brain. Every event in the agent loop fires a hook that captures into Postgres. Treat memory writes as load-bearing, not telemetry.
- **The agent should learn continuously.** Observations promote into the episodic tier via the Claude Haiku compressor (`core/internal/memory/compress.go`). Episodic clusters consolidate to semantic via the nightly `infinity consolidate` cron. Procedural memories will land in Phase 4 as Voyager-style skills the agent writes for itself.
- **Provenance is non-negotiable.** Every memory traces back to source observations via `mem_memory_sources`. Cascading staleness (`MarkSuperseded`) propagates through the graph. When the agent cites a fact, it must be able to surface the chain.
- **The agent should evolve its own toolset over time.** The `tools.Registry` is intentionally pluggable (native Go tools + MCP + Phase 4 skills). Don't hard-couple agent logic to specific tool implementations.
- **Privacy filtering is mandatory at the capture boundary.** `memory.StripSecrets` runs before any observation hits the database. Add new patterns when you discover new secret formats.
- **The Live tab is the present, the Memory tab is the brain, the Sessions tab is the history.** Don't conflate these. Each has a distinct mental model.

Phases 0-3 are done. Phases 4-8 (Skills system, Proactive Engine, Voyager self-evolution, Polish, Voice) are the path forward. When you build them, preserve the memory-first invariant.

## Architecture at a glance

The full wiring (boot sequence, package layout, HTTP API map, write/read paths,
phase-by-phase status with explicit gaps) lives in [`ARCHITECTURE.md`](ARCHITECTURE.md).
Read it before any non-trivial change. The summary:

```
infinity/
  core/              # Go 1.26 binary — agent loop, MCP client, memory, hooks, server
    cmd/infinity/      # cobra CLI: serve, migrate, doctor, consolidate
    db/migrations/     # 001..006 — embedded via go:embed
    internal/
      agent/loop.go    # intentionally-small loop (nanobot-inspired)
      llm/             # Provider interface + Anthropic, OpenAI, Google + Haiku summarizer
      tools/           # Registry, MCP client, native tools, memory tools
      memory/          # store, search (BM25+pgvector+graph), RRF, compress, forget, staleness, provenance
      hooks/           # 12-event pipeline + capture chain
      embed/           # Embedder interface (stub | http)
      skills/          # Phase 4 — registry, sandbox, runner, store, agent tools, HTTP
      intent/          # Phase 5 — IntentFlow detector (Haiku) + decision store
      proactive/       # Phase 5 — WAL, Working Buffer, Heartbeat, Trust queue, HTTP
      cron/            # Phase 6 — robfig scheduler + agent executor + HTTP
      sentinel/        # Phase 6 — manager + dispatcher (skill / log) + HTTP
      server/          # HTTP + WebSocket + JSON API + audit
  studio/            # Next.js 14 app router
    app/{live,sessions,memory,skills,heartbeat,trust,cron,audit,settings}/
    components/        # TabFrame, MobileNav, Drawer, ToolCallCard, MemoryCard, SkillCard, …
    components/ui/     # shadcn primitives + drawer (vaul)
    lib/               # ws client, api client, utils
  docker/            # codeexec + embed sidecar Dockerfiles (optional services)
  railway.toml
```

Two Dockerfiles at service roots (`core/Dockerfile`, `studio/Dockerfile`) — Railway auto-detects per-service. Migrations are embedded into the Go binary; the runtime container has no `db/` directory.

## Hard rules (in addition to the global ones in `~/.claude/CLAUDE.md`)

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

### Memory + capture invariants

- **Every event in the agent loop fires a hook.** When you add a new transition (e.g. a Phase 4 skill execution), call `hooks.Pipeline.Emit` with the right `EventName`. The pipeline is async — never block the loop on capture.
- **Privacy first.** All hook capture goes through `memory.StripSecrets` before persistence. Adding a new capture point? It must use the same path.
- **Compression is opt-in via `INFINITY_AUTO_COMPRESS=true`.** Don't enable by default — Haiku calls cost money. The `infinity consolidate --compress` command exists as the manual/cron path.
- **Provenance link is mandatory for every promoted memory.** `mem_memory_sources` rows must be written when an observation becomes a memory. Don't skip the bookkeeping.
- **No service-role secrets in the codebase.** Infinity Core connects to Postgres directly via `pgx`. We don't use Supabase's PostgREST — service_role and anon JWTs stay in the Supabase dashboard.

### Deployment + operations

- **Two services on Railway: `core` and `studio`.** Their root directories in Railway are `core/` and `studio/` respectively. Auto-detected Dockerfiles.
- **Postgres lives on Supabase.** Connection string is the **session pooler** at `aws-1-us-west-1.pooler.supabase.com:5432` (IPv4) — direct connection is IPv6-only on free tier and unreachable from Railway.
- **`infinity migrate` reads embedded migrations by default.** Pass `--dir core/db/migrations` only when iterating on schema locally.
- **Never commit `.env`.** Already gitignored. Set production vars via `railway variables --service <name> --set KEY=VALUE`.
- **Don't run git or deployment commands unless the user explicitly asks.** Inherits from the global rules.

## Common gotchas

- **`pnpm-workspace.yaml` is sensitive on pnpm 11.** It must contain `allowBuilds: { unrs-resolver: false }` or installs fail with `ERR_PNPM_IGNORED_BUILDS`. Don't strip that key.
- **Studio Dockerfile needs Node 22+ and `CI=true`.** pnpm 11 imports `node:sqlite` (Node 22+ only) and runs an interactive `confirmModulesPurge` prompt during `pnpm build` unless `CI=true` is set.
- **The compressor only activates when `LLM_PROVIDER=anthropic`.** It needs an `*llm.Anthropic` to construct the Haiku summarizer. With OpenAI or Google providers the capture pipeline still runs but observations don't promote to memories until you switch back or build the equivalent summarizer.
- **`vector(384)` is hardcoded.** Embedding dim is fixed across schema, embedder interface, and HNSW index. Changing the embedding model means changing the schema.
- **The `infinity_search` FTS configuration falls back gracefully on managed Postgres.** Synonym dictionaries can't load on Supabase (no FS access). The migration logs a NOTICE and uses plain `english` stemming. Functional, just no `db→database` synonym expansion.

## Where to look first

When asked to add a feature, read these files in this order to understand the relevant slice:

- Agent loop end-to-end: `core/internal/agent/loop.go` → `core/internal/server/ws.go` → `studio/hooks/useChat.ts` → `studio/components/ConversationStream.tsx`
- Adding a tool: `core/internal/tools/registry.go` → `core/internal/tools/{httpfetch,websearch,memory_tools}.go` → `core/internal/tools/defaults.go`
- Memory write path: `core/internal/hooks/capture.go` → `core/internal/memory/store.go` → `core/internal/memory/compress.go`
- Memory read path: `core/internal/memory/search.go` → `core/internal/memory/rrf.go` → `core/internal/server/memory_api.go` → `studio/app/memory/page.tsx`
- LLM provider boundary: `core/internal/llm/provider.go` → `core/internal/llm/anthropic.go` (reference impl)
- Mobile UI conventions: `studio/app/globals.css` → `studio/components/TabFrame.tsx` → `studio/components/MobileNav.tsx` → `studio/components/ui/drawer.tsx` → `studio/components/Composer.tsx`
- Skills end-to-end: `core/internal/skills/loader.go` → `registry.go` → `runner.go` → `registry_tools.go` → `studio/app/skills/page.tsx`
- Proactive engine: `core/internal/intent/flow.go` → `core/internal/proactive/{wal,buffer,heartbeat,trust}.go` → `studio/app/{heartbeat,trust}/page.tsx`
- Cron + Sentinels: `core/internal/cron/{scheduler,executor_agent}.go` → `core/internal/sentinel/{manager,dispatcher}.go` → `studio/app/cron/page.tsx`

## Phase status

See `ARCHITECTURE.md` § 12 for the granular gap list. Summary:

| Phase | Status | What |
|---|---|---|
| 0 | ✅ | Foundation: repo, CLI, health, studio shell |
| 1 | ✅ | Working text bot: agent loop, LLM provider, WebSocket, Live tab |
| 2 | ✅ | Tools and MCP: registry, websearch, filesystem, codeexec, httpfetch, Settings tab |
| 3 | ✅ | Memory: agentmemory port, triple-stream retrieval, 12-hook pipeline, compression, Memory tab, provenance |
| 4 | ✅ substrate | Skills system: schema, registry, process-jail sandbox, agent tools, HTTP, Studio Skills tab. **Gaps:** container sandbox for high/critical, Tests sub-tab, "+ New skill" / Import buttons. |
| 5 | ✅ substrate | Proactive Engine: IntentFlow detector (Haiku), WAL, Working Buffer, Heartbeat ticker, Trust queue, full schema, all HTTP APIs, Heartbeat + Trust tabs. **Gaps:** WS-handler integration of IntentFlow/WAL/Buffer (currently API-only), Compaction Recovery flow, Curiosity/Pattern/Surprise loops, 3-column Live, sub-tabs in Heartbeat. |
| 6 | ✅ substrate | Cron + Sentinels: robfig scheduler with agent executor, sentinel manager + skill dispatcher, schemas, HTTP APIs, combined Cron+Sentinels tab. **Gaps:** Voyager curriculum/skill-generator/verifier/AutoSkill loops, skill discovery hooks, sentinel runtimes for non-webhook watch types. |
| 7 | ⚠️ partial | Audit log endpoint + viewer. **Gaps:** command palette (cmd+K), sessions rewind, settings depth, knowledge graph viewer, backup/export, full doctor suite. |
| 8 | — | Voice (skipped per direction) |
| 6 | — | Voyager Self-Evolution (skill curriculum, automated improvement) |
| 7 | — | Polish (token budgets, multi-provider failover, full benchmarks) |
| 8 | — | Voice (always-on phone-first interface) |

When implementing Phase 4+, preserve the memory-first invariant: every new capability emits hooks, every artifact lives in the schema with provenance.
