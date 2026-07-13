---
name: Infinity
slug: infinity
tagline: A single-user AI agent that remembers, learns, and acts on its own
one_liner: An always-on personal AI agent that captures every interaction into a Postgres memory graph, retrieves it with hybrid search, and runs proactive and scheduled work across web, voice, phone, and a home-Mac coding bridge.
status: production
live_url: https://infinity.dopesoft.io
primary_stack:
  - Go 1.26.3
  - Next.js 14.2.35
  - React 18.3.1
  - TypeScript 5.9.3
  - PostgreSQL + pgvector 0.3.0
  - Tailwind CSS 3.4.19
  - Supabase (auth + realtime)
  - Anthropic SDK 1.41.0
categories:
  - ai
  - agent
  - personal-productivity
  - infra
last_extracted: 2026-07-10
---

# Infinity: Portfolio Extract

## Snapshot

Infinity is a single-user, always-on AI agent with persistent memory. It is split into two deployed services: a Go binary called Core (the agent loop, memory, tools, hooks, HTTP and WebSocket API) and a Next.js app called Studio (the phone-first UI). Postgres with the pgvector extension holds the memory graph. Both services and several sidecars deploy to Railway; the public UI is documented at infinity.dopesoft.io (`ARCHITECTURE.md:974`).

The access model is single-owner: the first authenticated Supabase user becomes the owner, and every later request must present a JWT whose subject matches that owner UUID (`core/internal/auth/auth.go:1-7`). This is a personal cognitive tool for one person, not a multi-tenant SaaS. The codebase is private (`README.md` License section).

The system is large. Core is 344 non-test Go files (about 92,676 lines by `wc -l`) plus 50 Go test files; Studio holds 213 TypeScript and TSX files. The database has 174 migration files defining roughly 80 `mem_*` tables (`core/db/migrations/`).

## Problem and Value

Most chat agents forget everything between sessions. Infinity is built around the opposite premise: every observation in the agent loop fires a hook that captures the event into Postgres, where it is compressed, linked, retrieved, and consolidated over time (`core/internal/hooks/`, `core/internal/memory/`). The stated goal is an agent whose understanding of its owner and their projects compounds instead of resetting.

Beyond memory, the app is designed to act without being asked: a heartbeat ticker raises findings, a cron scheduler runs agent tasks on a timetable, sentinels watch for events, and a trust queue holds any destructive action for one-tap approval (`core/internal/proactive/`, `core/internal/cron/`, `core/internal/sentinel/`). It also writes and runs code on the owner's home Mac through a bridge, and places phone calls. The intended user is a single technical owner who wants one agent that remembers, learns from its own runs, and carries out multi-step work across their tools.

## Tech Stack

Versions are pinned from lockfiles: `studio/pnpm-lock.yaml` (pnpm lockfile v9) for JS/TS and `core/go.mod` for Go. Go transitive versions come from the `require` blocks in `core/go.mod`.

### Frontend (`studio/`)
- Next.js 14.2.35 (App Router, `output: "standalone"`), React 18.3.1, react-dom 18.3.1
- TypeScript 5.9.3 with `strict: true` (`studio/tsconfig.json`)
- Tailwind CSS 3.4.19 with tailwindcss-animate 1.0.7
- Radix UI primitives: dialog 1.1.15, dropdown-menu 2.1.16, select 2.2.6, tabs 1.1.13, context-menu 2.2.16, plus alert-dialog, avatar, collapsible, hover-card, label, progress, scroll-area, separator, slot, switch, toggle, tooltip
- framer-motion 11.18.2, vaul 1.1.2 (bottom-sheet drawers), lucide-react 1.14.0, cmdk 1.1.1, sonner 2.0.7
- react-hook-form 7.76.0 + zod 4.4.3 + @hookform/resolvers 5.2.2
- react-markdown 10.1.0 + remark-gfm 4.0.1, dompurify 3.4.5
- @monaco-editor/react 4.7.0 (in-app code editor), @xterm/xterm 6.0.0 + @xterm/addon-fit 0.11.0 (terminal)
- onnxruntime-web 1.27.0 + @picovoice/web-voice-processor 4.0.10 (in-browser wake-word inference)
- @supabase/ssr 0.10.3 + @supabase/supabase-js 2.105.4

### Backend (`core/`, Go 1.26.3)
- github.com/anthropics/anthropic-sdk-go v1.41.0, github.com/openai/openai-go v1.12.0
- github.com/jackc/pgx/v5 v5.9.2 (direct Postgres, no ORM), github.com/pgvector/pgvector-go v0.3.0
- github.com/gorilla/websocket v1.5.3 (chat transport)
- github.com/modelcontextprotocol/go-sdk v1.6.0 (MCP client)
- github.com/spf13/cobra v1.10.2 (CLI), github.com/robfig/cron/v3 v3.0.1 (scheduler)
- github.com/golang-jwt/jwt/v5 v5.3.1 + github.com/MicahParks/keyfunc/v3 v3.8.0 (JWKS verification)
- github.com/SherClockHolmes/webpush-go v1.4.0 (web push)
- golang.org/x/oauth2 v0.35.0, gopkg.in/yaml.v3 v3.0.1

### Data
- PostgreSQL with the pgvector extension, hosted on Supabase (`railway.toml:12-14`)
- 384-dimension embeddings, fixed across schema and code (`core/internal/embed/embed.go:24`)
- 174 SQL migrations embedded into the Go binary via go:embed (`core/db/migrations.go`, `core/db/migrations/`)

### Infra and deploy
- Railway, defined as a monorepo of eight services in `railway.toml`: core, studio, gepa, plasticity, workspace, browser, camofox, sentry
- Core container: distroless static (`gcr.io/distroless/static-debian12:nonroot`), CGO-disabled build (`core/Dockerfile`)
- Studio container: node:22-alpine, pnpm 11, Next.js standalone output, multi-stage with BuildKit cache mounts (`studio/Dockerfile`)
- GEPA sidecar: Python FastAPI 0.115.6 + uvicorn 0.32.1 + dspy-ai 2.5.39 + anthropic 0.45.2 (`docker/gepa/requirements.txt`)
- Go sidecars (each its own module): workspace with creack/pty, browser with chromedp 0.9.5, sentry, plus the tools/mcp-bridge module (`docker/workspace/go.mod`, `docker/browser/go.mod`, `docker/sentry/go.mod`, `tools/mcp-bridge/go.mod`)
- Honcho peer-modelling service and deriver worker, built from the upstream project (`docker/honcho/`, `docker/honcho-deriver/`)

### AI and LLM
- Providers: Anthropic, OpenAI (API key), OpenAI OAuth, Google Gemini (`core/internal/llm/anthropic.go`, `openai.go`, `openai_oauth.go`, `google.go`, selected in `factory.go`)
- Voice: OpenAI realtime model `gpt-realtime-2.1-mini` over WebRTC with `gpt-4o-transcribe` for speech-to-text (`core/internal/voice/realtime.go:48,64`)
- MCP servers: claude_code over SSE with Cloudflare Access, GitHub over streamable HTTP, Composio (`core/config/mcp.yaml`)

### Tooling and CI
- pnpm 11 package manager (`studio/Dockerfile`, `studio/pnpm-workspace.yaml`)
- ESLint 8.57.1 + eslint-config-next 14.2.35
- GitHub Actions CI: build and vet every Go module, run Core's tests, build Studio (`.github/workflows/ci.yml`)

## Architecture Overview

- **Frontend:** Next.js 14 App Router. The root route renders a dashboard cockpit (`studio/app/page.tsx` to `components/dashboard/DashboardClient`). A same-origin rewrite proxies `/api/*` from Studio's Node server to Core so the browser never makes a cross-origin call (`studio/next.config.mjs`).
- **Backend:** A single Go binary with a cobra CLI. Subcommands include `serve`, `migrate`, `doctor`, `consolidate`, `reflect`, `gym`, `backfill` (`core/cmd/infinity/`). `serve` starts the HTTP + WebSocket server; the start command is `infinity serve` and does not auto-migrate (`railway.toml:29`).
- **Data stores:** One Postgres database with pgvector on Supabase (`railway.toml:12-14`). Redis is used by the optional Honcho sidecar (`README.md` production table). No ORM; queries go through pgx directly.
- **Real-time transport, two mechanisms:** (1) A gorilla/websocket connection carries the agent chat stream, upgraded in `core/internal/server/ws.go:233,364`, consumed by `studio/lib/ws/`. (2) Supabase Realtime channels (postgres_changes) push table updates into the UI for nav badges, run indicators, and dashboard cards (`studio/lib/realtime/provider.tsx`). The client auto-reconnects on `pageshow` and `visibilitychange` (`studio/lib/ws/provider.tsx:71-78`).
- **Memory retrieval:** Three streams run in parallel (BM25 full-text, pgvector cosine, and a graph stream) and are fused with Reciprocal Rank Fusion at k=60 (`core/internal/memory/search.go:47-87`, `core/internal/memory/rrf.go:12-22`).
- **AI/LLM integration:** Provider is chosen at boot from `LLM_PROVIDER` (`core/internal/llm/factory.go`). A separate Haiku-class summarizer compresses observations, and a critic persona drives reflection (`core/internal/llm/summarize.go`, `critic.go`).
- **Auth model:** Supabase-issued JWTs verified against the project JWKS; single-owner gate keyed on the owner's subject UUID (`core/internal/auth/auth.go:1-9`). Studio middleware adds a UX-level session guard but is explicitly not the security boundary (`studio/middleware.ts:4-6`).
- **Tool system:** A pluggable registry of native Go tools plus MCP-registered tools, with a lazy load pattern (`tool_search` / `load_tools`) so only pinned tools ship their schemas every turn (`core/internal/tools/registry.go`, `defaults.go`).
- **Skill sandboxing:** Skills run in a process jail on Unix or a Docker container for higher-risk skills (`core/internal/skills/sandbox_process_unix.go`, `sandbox_container.go`).
- **Privacy at capture:** `memory.StripSecrets` runs before any observation is persisted (`core/internal/memory/privacy.go`).

## Key Features

- **Streaming tool-calling agent loop.** The core conversational loop with tool calls, thinking, and streamed output; surfaced over WebSocket to the chat UI (evidence: `core/internal/agent/loop.go`, `core/internal/server/ws.go`, `studio/app/live/page.tsx`).
- **Persistent hybrid-search memory.** BM25, vector, and graph streams fused by RRF, with a Memory tab in the UI (evidence: `core/internal/memory/search.go`, `core/internal/memory/rrf.go`, `studio/app/memory/page.tsx`, `/api/memory/search`).
- **Memory provenance.** Every promoted memory links back to source observations, and a cite endpoint returns the chain (evidence: `mem_memory_sources` in `core/db/migrations/002_memory.sql`, `/api/memory/cite/` in `core/internal/server/memory_api.go`).
- **Secret stripping before storage.** Observations pass through a redaction step before hitting the database (evidence: `core/internal/memory/privacy.go`).
- **Nightly sleep-time consolidation.** A multi-operation nightly pass decays, clusters, resolves contradictions, prunes edges, and forgets (evidence: `core/internal/memory/consolidate.go`, migration `core/db/migrations/043_nightly_cognition_cron.sql`).
- **Reflection and metacognition.** A critic persona reflects on past sessions and stores lessons, runnable via CLI (evidence: `core/internal/memory/reflection.go`, `core/internal/llm/critic.go`, `core/cmd/infinity/reflect.go`, `mem_reflections`).
- **Predict-then-act.** Pre/Post tool-call prediction pairs scored for surprise (evidence: `core/internal/memory/predictions.go`, `core/internal/hooks/predict.go`, `mem_predictions`).
- **Skills system with self-authoring.** A skill registry, sandboxed execution, and a runtime skill-authoring tool, with default skills seeded through migrations (evidence: `core/internal/skills/`, `skill_propose` tool, `core/db/migrations/037_seed_skill_self_authoring.sql`).
- **GEPA skill self-optimization.** A Python sidecar runs a genetic-Pareto loop over a skill body; winners route through the trust flow with hard gates (evidence: `docker/gepa/`, `core/internal/voyager/optimizer.go`, `/api/voyager/optimize`).
- **Proactive engine.** Heartbeat findings, curiosity gap questions, and a trust approval queue (evidence: `core/internal/proactive/`, `studio/app/heartbeat/page.tsx`, `studio/app/trust/page.tsx`).
- **Cron scheduler and sentinels.** robfig cron with agent and system-task executors, plus event/threshold/webhook watchers (evidence: `core/internal/cron/`, `core/internal/sentinel/`, `studio/app/cron/page.tsx`).
- **Voice over WebRTC.** Core mints a short-lived OpenAI realtime key; the browser does the SDP exchange peer-to-peer with OpenAI so audio never transits Core (evidence: `core/internal/voice/realtime.go`, `/api/voice/session`).
- **Outbound phone calls.** Twilio places a call whose SIP leg bridges into OpenAI's realtime voice endpoint (evidence: `core/internal/phone/tools.go:195-235`, `mem_scheduled_calls`, migrations `172`-`174`).
- **In-browser wake word.** A "hey jarvis" model plus ONNX runtime assets run detection in the browser (evidence: `studio/public/wake/hey_jarvis_v0.1.onnx`, `studio/public/wake/ort/`, `@picovoice/web-voice-processor` in `studio/package.json`).
- **Coding bridge to the home Mac.** Claude Code is driven over MCP (SSE) behind a Cloudflare Access service token, registering file and shell tools (evidence: `core/config/mcp.yaml:31-37`, `core/internal/tools/mcp.go`).
- **Cloud workspace and browser bridges.** A files/bash/git workspace sidecar and a chromedp browser sidecar, with an anti-detect Camoufox variant (evidence: `docker/workspace/`, `docker/browser/`, `docker/camofox/`, `core/internal/bridge/`, `core/internal/browser/`).
- **In-app canvas: editor, terminal, git.** Monaco editor and xterm terminal wired to canvas file, terminal, and git endpoints (evidence: `/api/canvas/fs/*`, `/api/canvas/terminal/pty`, `/api/canvas/git/*` in `core/internal/server/canvas_api.go`, `terminal_pty.go`).
- **Trust-gated destructive actions.** Destructive tool calls insert a durable trust contract for approval; safe commands pass unattended (evidence: `core/internal/proactive/gate.go`, `mem_trust_contracts`, `/api/trust-contracts`).
- **Generic surface contract.** Anything the agent wants the owner to see lands in a typed surface table rendered by one generic card, with tap-through actions (evidence: `mem_surface_items` in `core/db/migrations/016_surface_items.sql`, `surface_item` tool, `/api/surface/action`).
- **Durable workflows and plans.** A resumable workflow engine and a verifiable step-by-step plan substrate, both surfaced in chat and dashboard (evidence: `core/internal/workflow/`, `mem_workflows`; `core/internal/plan/`, `mem_plans`, migrations `116`-`117`).
- **Planning and verification primitives.** Compass (owner mission injected every turn), Mandate (binary done-criteria with a Go-enforced gate), Crosscheck (adversarial re-verification), Gauge (effort sizing), Wards (private-path gate) (evidence: `core/internal/compass/`, `core/internal/mandate/`, `core/internal/crosscheck/`, `core/internal/gauge/`, `core/internal/proactive/ward_gate.go`, migrations `128`-`132`).
- **Server-tracked run state.** Long actions book a run row so progress survives navigation, refresh, and device switch (evidence: `core/internal/runs/runs.go`, `mem_runs` in `core/db/migrations/035_mem_runs.sql`, `studio/lib/runs/`).
- **Web push notifications.** VAPID-based push subscribe/send path (evidence: `core/internal/push/sender.go`, `core/internal/push/store.go`, `/api/push/*`, `webpush-go` in `core/go.mod`).
- **Self-improvement loop and external deploy watchdog.** A nightly self-edit path plus a separate sentry service that polls Core from outside and rolls back a bad deploy (evidence: `docker/sentry/`, `railway.toml:189-226`, migrations `087`, `096`, `152`).
- **Outbound HTTP failure instrumentation.** An instrumented default transport records every 4xx/5xx or transport error, and a cron outcome veto prevents false "green" runs (evidence: `core/internal/httpx/`, `mem_http_failures` in `core/db/migrations/153_http_failures.sql`, `core/internal/cron/outcome.go`).

## Engineering Highlights

- **Hybrid retrieval with rank fusion, not a single vector search.** Three retrieval streams run concurrently over channels and merge with RRF at k=60, matching the documented port spec (`core/internal/memory/search.go:47-87`, `core/internal/memory/rrf.go:12-22`).
- **Two-tier skill sandboxing.** Lower-risk skills run in an OS process jail; higher-risk skills run in a Docker container that fails loudly when Docker is absent rather than silently degrading (`core/internal/skills/sandbox_process_unix.go`, `core/internal/skills/sandbox_container.go:15-18`).
- **Lazy tool loading to keep context small.** The registry ships only pinned tool schemas each turn and exposes `tool_search` / `load_tools` so a large tool surface (110 distinct native tool names) does not blow the context window (`core/internal/tools/defaults.go`, count derived from `core/internal/tools/*.go` `Name()` methods).
- **Voice that never routes audio through the backend.** Core only mints an input-only ephemeral key; the browser exchanges SDP directly with OpenAI, keeping the media path peer-to-peer (`core/internal/voice/realtime.go:17-20`).
- **A polyrepo inside a monorepo.** Five independent Go modules plus the Next.js app, each built and vetted separately in CI, with Core's hermetic tests run without a database (`.github/workflows/ci.yml`, `core/go.mod`, `docker/*/go.mod`, `tools/mcp-bridge/go.mod`).
- **Migrations embedded into the binary.** All 174 SQL files are compiled into the distroless runtime via go:embed, so the production image carries no `db/` directory (`core/db/migrations.go`, `core/Dockerfile`).
- **TypeScript strict mode and a componentized UI.** `strict: true` in `studio/tsconfig.json`, with 29 shared UI primitives and 53 higher-level components under `studio/components/`.
- **Deliberate reconnect and mobile hardening.** WebSocket reconnect on `pageshow` / `focus` / `visibilitychange` for iOS Safari (`studio/lib/ws/provider.tsx:71-78`), and a same-origin API proxy to avoid CORS failures during deploys (`studio/next.config.mjs`).
- **Provider abstraction across four LLM backends.** A single `Provider` interface with streaming events backs Anthropic, OpenAI, OpenAI OAuth, and Google, with per-vendor model resolution to prevent cross-vendor model id leakage (`core/internal/llm/provider.go:129-156`, `core/internal/llm/factory.go`).
- **Honesty guards against silent failure.** A cron outcome classifier vetoes a green result when the run logged a hard HTTP failure, and the triage path returns an error rather than reporting empty when it could not actually look (`core/internal/cron/outcome.go`, `core/internal/httpx/`, `mem_http_failures`).
- **Test presence.** 50 Go test files across Core, including tests for the workflow executor, loop gates, sandbox container command rewriting, and OAuth compaction and retry (`core/**/*_test.go`).

## Integrations and External Services

Integration names taken only from `.env.example`; real usage confirmed in code.

- **Anthropic** (LLM, and Haiku summarizer/critic): `ANTHROPIC_API_KEY` in `.env.example`; `core/internal/llm/anthropic.go`, `anthropic-sdk-go` in `core/go.mod`.
- **OpenAI** (LLM, realtime voice, transcription, OAuth): `OPENAI_API_KEY` in `.env.example`; `core/internal/llm/openai.go`, `core/internal/voice/realtime.go`, `openai-go` in `core/go.mod`.
- **Google Gemini** (LLM): `GOOGLE_API_KEY` in `.env.example`; `core/internal/llm/google.go`.
- **Supabase** (Postgres host, JWKS auth, realtime, browser auth): `SUPABASE_URL` and `NEXT_PUBLIC_SUPABASE_*` in `.env.example`; `core/internal/auth/auth.go`, `studio/lib/realtime/provider.tsx`, `@supabase/*` in `studio/package.json`.
- **Tavily** (web search, optional): `TAVILY_API_KEY` in `.env.example`; `core/internal/tools/websearch.go`, commented server in `core/config/mcp.yaml:109-115`.
- **Claude Code MCP over Cloudflare Access** (home-Mac coding): `core/config/mcp.yaml:31-37`, Cloudflare Access service-token headers in `core/internal/tools/mcp.go`.
- **GitHub MCP** (repo and PR/issue API): `core/config/mcp.yaml:62-68`, endpoint `api.githubcopilot.com/mcp/`.
- **Composio** (SaaS toolkit gateway, Gmail/Slack/etc via REST verb registrar): `core/config/mcp.yaml:100-107`, `core/internal/tools/composio_verbs.go`, `core/internal/connectors/`.
- **Twilio** (outbound phone calls, SIP-bridged to OpenAI): `TWILIO_ACCOUNT_SID` and related read in `core/internal/phone/phone.go:291-293`, call creation in `core/internal/phone/tools.go`.
- **Railway** (hosting for eight services): `railway.toml`.
- **Honcho** (dialectic peer modelling, optional sidecar): `core/internal/honcho/`, `docker/honcho/`, `docker/honcho-deriver/`.
- **Web Push / VAPID** (browser notifications): `core/internal/push/`, `webpush-go` in `core/go.mod`.

## Screenshot Shot List

Screens ordered most impressive first. Component paths are the client entry for each route.

1. **Dashboard cockpit** — route `/`, `studio/components/dashboard/DashboardClient` (via `studio/app/page.tsx`). Shows the surfaced-items inbox, work board, and memory primers in one cockpit view.
2. **Live workspace** — route `/live`, `studio/app/live/page.tsx`. Chat plus canvas with a Monaco editor, xterm terminal, and git panel; demonstrates the agent editing and running code.
3. **Memory** — route `/memory`, `studio/app/memory/page.tsx`. The brain: memory graph, reflections, predictions, and procedural skill badges with provenance.
4. **Skills** — route `/skills`, `studio/app/skills/page.tsx`. Skill catalog, versions, and proposals from the self-authoring and GEPA loops.
5. **Cron and Sentinels** — route `/cron`, `studio/app/cron/page.tsx`. Scheduled agent runs and event watchers with live run indicators.
6. **Heartbeat** — route `/heartbeat`, `studio/app/heartbeat/page.tsx`. Proactive findings and curiosity questions the agent raised on its own.
7. **Trust** — route `/trust`, `studio/app/trust/page.tsx`. The one-tap approval queue for gated destructive actions.
8. **Gym** — route `/gym`, `studio/app/gym/page.tsx`. Training examples, adapter evals, and policy routes for the plasticity surface.
9. **Lab** — route `/lab`, `studio/app/lab/page.tsx`. GEPA candidates and Pareto frontier comparison.
10. **Code proposals** — route `/code-proposals`, `studio/app/code-proposals/page.tsx`. Self-noticed source-refactor drafts.
11. **Logs** — route `/logs` and `/logs/[turnId]`, `studio/app/logs/page.tsx`. Run history with a per-run narrative and turn traces.
12. **Sessions** — route `/sessions`, `studio/app/sessions/page.tsx`. Conversation history.
13. **Settings** — route `/settings`, `studio/app/settings/page.tsx`. Connectors, Compass, model selection, trust and privacy.
14. **Audit** — route `/audit`, `studio/app/audit/page.tsx`. Audit log viewer.

## Existing Visual Assets

No `screenshots/` directory or marketing image set exists in the repo. The committed image assets are logos, icons, and machine-learning model files.

- `infinity_symbol_PNG102471.png` (repo root) — the Infinity symbol graphic.
- `studio/public/dopesoft-white.png` — DopeSoft logo in white.
- `studio/public/apple-touch-icon.png`, `apple-touch-icon.svg` — iOS home-screen icon.
- `studio/public/icon-192.png`, `icon-512.png`, `icon-maskable-192.png`, `icon-maskable-512.png`, `icon.svg`, `icon-maskable.svg` — PWA app icons.
- `studio/public/manifest.webmanifest` — PWA manifest.
- `studio/public/sw.js` — service worker.
- `studio/public/wake/hey_jarvis_v0.1.onnx`, `melspectrogram.onnx`, `embedding_model.onnx`, and `studio/public/wake/ort/` — wake-word ML models and ONNX runtime WASM (functional assets, not marketing images).

## Metrics and Status

Only facts sourced from repo files.

- **Live URL (documented):** infinity.dopesoft.io, described as the Studio custom domain via a Cloudflare CNAME to Railway (`ARCHITECTURE.md:974`, `CLAUDE.md` Deployment section). Reachability was not tested in this extraction.
- **Deployment target:** Railway, eight services defined in `railway.toml` (core, studio, gepa, plasticity, workspace, browser, camofox, sentry); Postgres on Supabase (`railway.toml:12-14`).
- **Database size:** 174 migration files (`core/db/migrations/`, counted), roughly 80 `mem_*` and `infinity_*` tables (counted from `CREATE TABLE` statements).
- **Code size (repo-derived by `wc -l` / `find`):** about 92,676 lines of Go across 344 non-test files, 50 Go test files, 213 TypeScript/TSX files in Studio, 82 Studio components. Treat these as rough line counts, not curated metrics.
- **Native tool surface:** 110 distinct native tool `Name()` values across `core/internal/tools/`.
- **CI:** GitHub Actions builds and vets five Go modules, runs Core tests, and builds Studio on pushes and PRs to `main` (`.github/workflows/ci.yml`).

No user counts, revenue, uptime, or latency figures appear in any committed file. None are asserted here.

## Unverified / Needs Kai Confirmation

- **Status field.** `status: production` is inferred from committed deploy config plus documentation (`railway.toml`, `ARCHITECTURE.md`, `README.md`). It is a private, single-user deployment, not a public multi-tenant product. Confirm whether "production" or "internal" is the label you want on the portfolio.
- **Live URL reachability.** infinity.dopesoft.io is documented in repo files but was not fetched or pinged during extraction (guardrail: do not run the app). Confirm it is currently live.
- **Service count discrepancy.** `README.md` says "Six services on Railway," while `railway.toml` defines eight (`core`, `studio`, `gepa`, `plasticity`, `workspace`, `browser`, `camofox`, `sentry`), and `docker/honcho` plus `docker/honcho-deriver` add two more Python services not listed in `railway.toml`. Confirm the actual deployed service set.
- **"25 Claude Code tools" figure.** The 25-tool count for the coding bridge appears in `ARCHITECTURE.md` and `CLAUDE.md` prose, not counted in code during this pass. Confirm the current number.
- **Competitor comparison claims.** The Hermes / OpenClaw / Nanobot comparison table in `README.md` is the project's own assessment and was not independently verified. Do not present it as neutral fact on the site.
- **Which features are live versus scaffolded.** `README.md` and `CLAUDE.md` mark some areas as partial (for example Phase 7 polish, the Gym sidecar train/eval implementation, wake-word activation still tap-to-talk). Confirm which capabilities should be shown as shipped in the portfolio.
- **Any metric worth featuring.** No usage, performance, or reliability numbers exist in the repo. If you want metrics on the site, they must come from you, not from this codebase.

## Source Map

- Single-owner auth model: `core/internal/auth/auth.go:1-9`; Studio session guard is UX-only: `studio/middleware.ts:4-6`.
- Live URL and custom domain: `ARCHITECTURE.md:974`.
- Railway eight-service definition: `railway.toml` (service blocks).
- Postgres + pgvector on Supabase: `railway.toml:12-14`.
- 384-dim embeddings: `core/internal/embed/embed.go:24`.
- RRF k=60 over three streams: `core/internal/memory/rrf.go:12-22`, `core/internal/memory/search.go:47-87`.
- Voice model and peer-to-peer WebRTC: `core/internal/voice/realtime.go:17-20,48,64,96,103`.
- Phone via Twilio SIP into OpenAI: `core/internal/phone/tools.go:195-235`, `core/internal/phone/phone.go:291-293`.
- Wake-word models in browser: `studio/public/wake/`, `@picovoice/web-voice-processor` in `studio/package.json`.
- MCP servers (claude_code, github, composio): `core/config/mcp.yaml`.
- Composio REST verb registrar: `core/internal/tools/composio_verbs.go`.
- Two-tier skill sandbox: `core/internal/skills/sandbox_process_unix.go`, `sandbox_container.go:15-18`.
- Secret stripping at capture: `core/internal/memory/privacy.go`.
- Migrations embedded in binary: `core/db/migrations.go`, `core/Dockerfile`.
- Polyrepo CI: `.github/workflows/ci.yml`.
- Same-origin API proxy: `studio/next.config.mjs`.
- Server-tracked runs: `core/internal/runs/runs.go`, `core/db/migrations/035_mem_runs.sql`.
- HTTP failure guard and cron veto: `core/internal/httpx/`, `core/internal/cron/outcome.go`, `core/db/migrations/153_http_failures.sql`.
- External deploy watchdog: `docker/sentry/`, `railway.toml:189-226`.
- Resolved JS versions: `studio/pnpm-lock.yaml` importers block. Resolved Go versions: `core/go.mod`.
