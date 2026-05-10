# Infinity

Single-user AI agent with persistent memory. Two services on Railway plus a Postgres database.

- **Core** — Go binary (`core/`). Agent loop, MCP client, memory subsystem, WebSocket+HTTP server. Migrations are embedded into the binary.
- **Studio** — Next.js 14 app (`studio/`). Mobile-first UI with Live, Sessions, Memory, and Settings tabs.
- **Database** — Postgres 16 + pgvector. Recommended host: **Supabase** (managed, free tier, pgvector enabled out of the box). Railway-managed Postgres also works; local development uses the `pgvector/pgvector:pg16` Docker image.

## Quickstart (local)

Prereqs: Go 1.22+, pnpm 11+, Postgres 16 with the `vector` extension, Docker (optional).

### 1. Database

```sh
docker run -d --name infinity-pg \
  -e POSTGRES_USER=infinity \
  -e POSTGRES_PASSWORD=infinity \
  -e POSTGRES_DB=infinity \
  -p 5432:5432 \
  pgvector/pgvector:pg16
```

### 2. Environment

```sh
cp .env.example .env
# fill in ANTHROPIC_API_KEY at minimum
```

### 3. Migrate + Run Core

```sh
cd core
DATABASE_URL=postgres://infinity:infinity@localhost:5432/infinity?sslmode=disable \
  go run ./cmd/infinity migrate
go run ./cmd/infinity serve --addr :8080
# verify: curl localhost:8080/health
```

`infinity migrate` reads migrations from the binary itself (no `--dir` flag needed in production). Pass `--dir core/db/migrations` to use the on-disk copy if you're iterating on schema.

### 4. Run Studio

```sh
cd studio
pnpm install
pnpm dev
# open http://localhost:3000
```

### 5. Diagnose

```sh
cd core
go run ./cmd/infinity doctor
```

### 6. Nightly consolidation (cron)

```sh
go run ./cmd/infinity consolidate           # runs decay, hot-reset, cluster, auto-forget
go run ./cmd/infinity consolidate --dry-run # preview deletions only
```

## Layout

```
infinity/
  core/                           # Go binary — self-contained, embedded migrations
    Dockerfile                    # build context = core/
    cmd/infinity/                 # cobra CLI: serve, migrate, doctor, consolidate
    config/mcp.yaml               # MCP server registry (Phase 2)
    db/migrations/                # 001_init, 002_memory, 003_search (embedded)
    db/synonyms.syn               # FTS synonym dictionary (best-effort load)
    internal/{server,agent,llm,tools,memory,hooks,embed}/
    go.mod
  studio/                         # Next.js app router — self-contained
    Dockerfile                    # build context = studio/
    app/{live,sessions,memory,settings}/
    components/                   # TabFrame, ToolCallCard, MemoryCard, …
    components/ui/                # shadcn primitives
    lib/                          # ws client, utils
    public/
  docker/embed.Dockerfile         # optional FastAPI embed sidecar
  railway.toml
```

## Deploying to Railway

The repo is a monorepo with two services. In your Railway project:

1. **Create a Postgres database**. Easiest path: use **Supabase** for Postgres (recommended) — it has pgvector enabled and a generous free tier. In your Supabase project: Settings → Database → Connection string → use the "Transaction" pooler URL with `sslmode=require`. Set this as `DATABASE_URL` on the Core service.

   If you'd rather host Postgres on Railway, add the `pgvector/pgvector:pg16` template service and set its connection string the same way.

2. **Add the Core service**. Source from this GitHub repo. In Settings → Source → **Root Directory**: `core`. Railway auto-detects `core/Dockerfile`. Wire env: `DATABASE_URL`, `ANTHROPIC_API_KEY`, `LLM_PROVIDER`, `LLM_MODEL`, `HTTP_FETCH_ALLOWED_DOMAINS`, optional `TAVILY_API_KEY` / `MCP_CONFIG`.

3. **Add the Studio service**. Source from the same repo. Settings → Source → **Root Directory**: `studio`. Wire env: `NEXT_PUBLIC_CORE_URL=https://core.up.railway.app`, `NEXT_PUBLIC_CORE_WS_URL=wss://core.up.railway.app/ws`.

4. **Run migrations** from your dev machine with the production `DATABASE_URL` once: `cd core && DATABASE_URL=… go run ./cmd/infinity migrate`. (Or one-shot deploy the core service with `infinity migrate && infinity serve` as the start command.)

`railway.toml` in the repo root pins `rootDirectory` for each service so the auto-detection lines up. If you change service names or roots, update the file accordingly.

## Mobile

Designed mobile-first. Verified breakpoints: 375px (iPhone Safari), 390px, 768px, 1280px. Notes:

- `100dvh` everywhere, never `100vh`
- `viewport-fit=cover` + `env(safe-area-inset-*)` on every fixed/sticky surface
- 44×44 minimum touch targets
- `overscroll-behavior: contain` on body and scroll regions
- WebSocket auto-reconnects on `pageshow` + `focus` + `visibilitychange` to survive iOS Safari background-tab kills

## Phase status

| Phase | What | Status |
|---|---|---|
| 0 | Foundation: repo, CLI, health, studio shell, Docker, Railway | ✅ done |
| 1 | Working text bot: agent loop, LLM provider, WebSocket, Live tab | ✅ done |
| 2 | Tools and MCP: registry, websearch, filesystem, codeexec, httpfetch | ✅ done |
| 3 | Memory: agentmemory port, triple-stream retrieval, 12-hook pipeline, Memory tab | ✅ done |
| 4-8 | LLM-based observation compression, voice mode, Voyager self-evolution, polish | — Part 2 |

## License

Private. Lifts and ports from `rohitg00/agentmemory` (Apache-2.0) per the build plan.
