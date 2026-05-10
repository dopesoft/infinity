# GEPA optimizer sidecar

A FastAPI service that wraps a Hermes-style Genetic-Pareto Prompt Evolution
loop so Infinity can have skill prompts *improve themselves* from execution
traces. Phase 1: SKILL.md optimization only — same scope Hermes ships
today.

## Why a sidecar

DSPy + the GEPA pattern are Python-native. Infinity's core is Go. We keep
the optimizer in its own container so:

- Core stays small and fast.
- Optimization runs are minutes-long; isolating them prevents stalls in the
  agent loop.
- The sidecar holds no state — it can crash, restart, or be wiped without
  losing anything.

## Deploy

### Railway (recommended)

```sh
railway service create gepa
railway variables --service gepa \
  --set ANTHROPIC_API_KEY="$ANTHROPIC_API_KEY"
# Build context = repo root, Dockerfile = docker/gepa.Dockerfile
```

Wire the URL into core:

```sh
railway variables --service core \
  --set GEPA_URL=https://gepa.up.railway.app
```

### Local

```sh
docker build -t infinity-gepa -f docker/gepa.Dockerfile .
docker run -p 8090:8090 -e ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY infinity-gepa
GEPA_URL=http://localhost:8090 go run ./cmd/infinity serve
```

## Use

The Voyager API exposes a `/api/voyager/optimize` endpoint:

```sh
curl -X POST $CORE/api/voyager/optimize \
  -H 'content-type: application/json' \
  -d '{ "skill": "weekly-standup-summary", "trace_limit": 20 }'
```

What happens:

1. Voyager pulls the last N runs of `weekly-standup-summary` from
   `mem_skill_runs` (failures + successes).
2. POSTs them to GEPA at `/optimize` with the current `SKILL.md`.
3. GEPA asks Haiku to summarise root causes of failure, mutates the
   prompt 6 times, scores each candidate, returns a Pareto-sorted list.
4. Voyager picks the winner (highest score, smallest size, valid
   frontmatter, ≤15KB).
5. Inserts it into `mem_skill_proposals` as a `candidate`.
6. The boss approves via the existing
   `/api/voyager/proposals/<id>/decide` endpoint or the Studio Skills tab.
7. On promotion, Voyager writes the new `SKILL.md` to disk and reloads
   the registry.

## Hard gates (in `core/internal/voyager/optimizer.go`)

| Gate | Where | Behaviour |
|---|---|---|
| Empty candidate | `pickWinner` | reject |
| `len > 15KB` | `pickWinner` | reject |
| Missing `---` frontmatter | `pickWinner` | reject |
| Identical to original | `pickWinner` | reject as no-op |
| Trust queue approval | existing `mem_skill_proposals.status` flow | the boss decides |

## Cost

Per optimization run, with default budget (`max_calls=24`):

- 1 root-cause summary call
- ~6 mutation calls
- ~6 scoring calls
- ≈$0.05–$0.20 in Haiku tokens at current pricing.

Trigger manually for now. A future Voyager extension can auto-trigger when
`mem_skill_failures` accumulates past a threshold.

## What this is NOT

- Not a full DSPy compiler — that's a much heavier dependency we haven't
  needed yet.
- Not a code-evolver — Phase 1 only mutates the SKILL.md (instructions).
  Phase 4 (Hermes's terminology) would mutate implementation files; not
  shipping that here.
- Not a runtime — skills still execute in `core/internal/skills/runner.go`.
  GEPA only proposes new prompts; it never runs them.
