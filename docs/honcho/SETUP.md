# Honcho — dialectic peer modelling for Infinity

[Honcho](https://github.com/plastic-labs/honcho) is an open-source memory
library that derives a continually-updated *peer representation* — "who the
boss is" — from interaction traces. Infinity treats it as a complement to
the 12 `mem_*` tables, not a replacement: facts + provenance stay in
Postgres; Honcho contributes the *who*-layer to the system prompt.

## Architecture

```
                ┌──────────────────────────┐
  user msg ────►│ infinity-core (Railway)  │
                │  ├─ hooks pipeline       │
                │  │  └─ honcho.user/ass.   ├──── async POST /v1/messages ──┐
                │  ├─ Composite memory     │                                 │
                │  │  ├─ Searcher (RRF)    │                                 ▼
                │  │  └─ Honcho provider   │◄── GET /representation ── ┌──────────────┐
                │  └─ agent loop           │                            │ honcho       │
                └──────────────────────────┘                            │ FastAPI +    │
                                                                        │ Postgres     │
                                                                        │ (separate    │
                                                                        │  schema)     │
                                                                        └──────────────┘
```

## Deploy options

### Option A — Railway service (recommended)

Honcho ships a Docker image. Add a new Railway service from
`plastic-labs/honcho`, point it at the **same Supabase Postgres** with a
distinct schema (`honcho`):

```sh
railway service create honcho
railway variables --service honcho \
  --set DATABASE_URL="$INFINITY_DATABASE_URL" \
  --set DATABASE_SCHEMA=honcho \
  --set ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY" \
  --set HONCHO_AUTH_USE_AUTH=false           # internal-only access; rely on private network
```

Get the internal URL (`https://honcho.up.railway.app`) and wire it into core:

```sh
railway variables --service core \
  --set HONCHO_BASE_URL=https://honcho.up.railway.app \
  --set HONCHO_WORKSPACE=infinity \
  --set HONCHO_PEER=boss
```

On boot you'll see `honcho: enabled (workspace=infinity peer=boss)`.

### Option B — Local Docker Compose (dev)

```sh
git clone https://github.com/plastic-labs/honcho ~/Dev/honcho
cd ~/Dev/honcho
cp .env.example .env
# set ANTHROPIC_API_KEY
docker compose up -d
```

Then point Infinity at `http://localhost:8080`:

```sh
echo "HONCHO_BASE_URL=http://localhost:8080" >> core/.env
```

### Option C — Honcho Cloud (managed)

Sign up at app.honcho.dev, create a workspace + peer, copy the API key.

```sh
HONCHO_BASE_URL=https://app.honcho.dev
HONCHO_API_KEY=<your key>
HONCHO_WORKSPACE=<your workspace id>
HONCHO_PEER=<your peer id>
```

Trade-off: data leaves your box. Avoid unless you trust Plastic Labs.

## What Infinity does with Honcho

1. **Mirror messages**: every `UserPromptSubmit` and `TaskCompleted` hook
   POSTs the text to `/v1/workspaces/<ws>/messages` with the right `role`.
   Honcho's async reasoning pipeline updates the peer representation.
2. **Inject representation**: `agent.CompositeMemory` calls
   `Honcho.Representation()` on every system-prefix build (60s TTL cache),
   appending it under "About the boss (Honcho dialectic):".
3. **Optional dialectic queries**: `client.Ask(ctx, "what does the boss
   prefer for X?")` returns Honcho's reasoned answer. Used by future
   features (proactive surfacing, intent disambiguation). Not on the hot
   path today.

## Privacy

`memory.StripSecrets` runs **before** the hook fires, so secrets never reach
Honcho. The hook payload uses the same redacted text Infinity stores in
`mem_observations`. If you self-host Honcho on the same Supabase project
you're already trusting with `mem_*`, the trust model is identical.

## When to disable

- API budget pressure: Honcho's reasoning pipeline calls Anthropic on a
  schedule. Set `HONCHO_BASE_URL=` (empty) to disable cleanly.
- Sandboxing tests: bypass Honcho when iterating on Infinity's own RRF
  retrieval; the searcher remains the primary memory provider.
