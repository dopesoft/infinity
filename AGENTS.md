# AGENTS.md — operational rules for AI agents working in this repo

Audience: Claude Code, Cursor, Codex CLI, and any other coding agent invoked against the Infinity repo. CLAUDE.md is the full project guide; this file is the short list of "don't do this dumb shit" rules that have already burned us.

If you're an agent, **read this top-to-bottom before answering any question about schema, migrations, or deploy state.**

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
