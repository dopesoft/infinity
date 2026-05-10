# Infinity

Single-user AI agent with persistent memory. Two services on Railway plus a Postgres database.

- **Core** — Go binary (`core/`). Agent loop, MCP client, memory subsystem, WebSocket+HTTP server.
- **Studio** — Next.js 14 app (`studio/`). Mobile-first UI with Live, Sessions, Memory, and Settings tabs.
- **Database** — Postgres 16 + pgvector. Migrations in `db/migrations/`.

This repository implements the build plan in `~/.claude/plans/built-out-this-nextjs-noble-whistle.md`. Phase 0 (foundation) is complete; Phases 1-3 land the agent loop, MCP tools, and the agentmemory port respectively.

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

### 3. Migrate

```sh
cd core
DATABASE_URL=postgres://infinity:infinity@localhost:5432/infinity?sslmode=disable \
  go run ./cmd/infinity migrate --dir ../db/migrations
```

### 4. Run Core

```sh
cd core
go run ./cmd/infinity serve --addr :8080
# verify: curl localhost:8080/health
```

### 5. Run Studio

```sh
cd studio
pnpm install
pnpm dev
# open http://localhost:3000
```

### 6. Diagnose

```sh
cd core
go run ./cmd/infinity doctor
```

## Layout

```
infinity/
  core/                           # Go binary
    cmd/infinity/                 # cobra CLI: serve, migrate, doctor
    internal/{server,agent,llm,tools,memory,hooks,embed}/
    config/mcp.yaml               # MCP server registry (Phase 2)
    go.mod
  studio/                         # Next.js app router
    app/{live,sessions,memory,settings}/
    components/                   # TabFrame, ToolCallCard, MemoryCard, …
    components/ui/                # shadcn primitives
    lib/                          # ws client, utils
  db/migrations/                  # 001_init.sql; 002_memory.sql in Phase 3
  docker/{core,studio}.Dockerfile
  railway.toml
```

## Mobile

Designed mobile-first. Verified breakpoints: 375px (iPhone Safari), 390px, 768px, 1280px. Notes:

- `100dvh` everywhere, never `100vh`
- `viewport-fit=cover` + `env(safe-area-inset-*)` on every fixed/sticky surface
- 44×44 minimum touch targets
- `overscroll-behavior: contain` on body and scroll regions
- WebSocket auto-reconnects on `pageshow` + `visibilitychange` to survive iOS Safari background-tab kills

## Phase status

| Phase | What | Status |
|---|---|---|
| 0 | Foundation: repo, CLI, health, studio shell, Docker, Railway | ✅ done |
| 1 | Working text bot: agent loop, LLM provider, WebSocket, Live tab | ⏳ next |
| 2 | Tools and MCP: registry, websearch, filesystem, codeexec, httpfetch | — |
| 3 | Memory: agentmemory port, triple-stream retrieval, 12-hook pipeline, Memory tab | — |

## License

Private. Lifts and ports from `rohitg00/agentmemory` (Apache-2.0) per the build plan.
