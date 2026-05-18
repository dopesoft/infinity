# Why Infinity does NOT fine-tune models

**Status:** decision artifact. Replaces the earlier `PHASE_9_GYM_TRAINING.md` (which scoped GPU adapter training before we realized the math didn't work).

## TL;DR

Infinity's learning loop is **reflex injection + skills + GEPA-evolved recipes**. Not fine-tunes. Reasoning below.

## What we actually run

Every turn the agent receives:

1. **Reflex lessons** — top-K rows from `mem_training_examples` (split into AVOID / APPLY buckets) injected into the system prompt by [`plasticity.Provider`](../../core/internal/plasticity/provider.go).
2. **Procedural skills** — Voyager-promoted skills sit in `mem_memories` at `tier='procedural'` and get RRF-retrieved alongside semantic memories.
3. **Cross-session lessons** — meta-lessons clustered across sessions land in [`mem_reflection_chains`](../../core/db/migrations/047_reflection_chains.sql) and surface via [`memory.ReflectionChainsProvider`](../../core/internal/memory/provider_reflection_chains.go).

When the boss needs **strict consistency** (an exact format, an exact tone, an exact sequence of API calls), the answer is a **skill with an attached script**. The SKILL.md describes when/why, the script enforces the deterministic shape. 100% format fidelity, zero token cost per execution, fully auditable.

That covers the entire "the agent should learn from experience and behave better" use case.

## Why not fine-tune?

We considered three flavors of fine-tuning. None earn their complexity:

### OpenAI/Anthropic hosted fine-tunes (~$5–20/run)

- Saves the ~500–1000 tokens/turn the reflex layer costs. At our volume, $5–10/month savings.
- Adapter dies on model upgrade. When Sonnet 5 ships, every adapter trained on Sonnet 4.7 is obsolete.
- Requires an eval harness + Trust-gated promotion + rollback monitoring. All real work for that ~$10/mo benefit.
- For "consistency" use cases the boss already prefers skills+scripts — deterministic beats 95%-consistent.

### Self-hosted LoRA on Modal/Runpod (~$30–200/mo infra)

- Requires base model selection (Qwen / Llama / etc.) which lags frontier capability by months.
- Adds a vLLM serving sidecar — another moving piece.
- Modal/Runpod costs scale with usage; the boss is on ChatGPT plan + Composio free tier specifically to keep marginal cost at zero.

### Our own GPU

- High capex, ops overhead, and zero benefit over the hosted options at this scale.

## What changes if we ship a new task that "needs training"?

We almost certainly don't need training. The decision tree:

1. **Need deterministic format/tone/sequence?** → Skill with attached script.
2. **Want the agent to internalize a behavior pattern?** → New row(s) in `mem_training_examples`, labeled appropriately (`accepted` for positive, `rejected`/`corrected` for negative). Reflex layer surfaces them on every turn.
3. **Want the recipe itself to improve over time?** → Voyager + GEPA already evolves `SKILL.md` bodies via Pareto optimization (no GPU; just text mutation against Haiku-graded evals).
4. **Genuinely, demonstrably, need fine-tuning?** → Revisit this doc.

If we ever hit step 4, the work to add it is small: OpenAI's fine-tune API is the right path, the substrate tables (`mem_distillation_*`, `mem_model_adapters`, `mem_adapter_evals`, `mem_policy_routes`) are already in migration 022, and the LLM provider can be taught to honor `mem_policy_routes.active_adapter_id` per call. **None of that needs to ship before we need it.**

## Implications for the codebase

- `mem_distillation_datasets`, `mem_distillation_runs`, `mem_model_adapters`, `mem_adapter_evals` are kept in the schema as optionality — they cost nothing to leave alone.
- `mem_policy_routes` may eventually be repurposed for **cost-class model routing** (Haiku for cheap classifications, Sonnet for chat, Opus only when explicitly requested). Same table, totally different semantics — no adapters in the picture.
- The Studio `/lab` "Gym" tab shows only **Candidates** (real training-example data driving the reflex layer). The earlier scaffolded "Adapter Eval" and "Routes" sub-sections have been removed since they advertised a path we no longer plan to walk.
- `docker/plasticity/server.py` stays as a stub. It returns 501s for training endpoints, which is now the accurate behaviour, not a TODO.

## When to reconsider

Three signals that would justify revisiting fine-tunes:

1. **Reflex layer saturates context** — when top-K injection consistently bloats the system prompt past comfort and we can't trim by relevance, fine-tuning becomes the way to bake those lessons into weights.
2. **Per-turn token cost grows to where $10–50/mo savings move the needle** — i.e. agent activity scales 10–100x.
3. **A narrow, repeatable task emerges where a hosted fine-tune demonstrably beats reflex + script** — measured on real evals, not vibes.

Until then: reflex injection is the loop. Skills+scripts are the consistency tool. GEPA is the recipe evolver. No GPU, no fine-tunes, no eval gates to babysit.
