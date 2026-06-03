-- 119_skills_judgment_only.sql
--
-- Rule #1b sweep, wave 2. surface_item now derives a stable external_id and
-- upserts on its own (dedup-on-rerun is a tool MECHANIC, not the recipe's job).
-- So the "use a stable external_id" / "track seen ids so it never surfaces
-- twice" prose in these two surfacing skills is now redundant — and worse, it
-- was a mechanic the LLM could drop. Rewrite both to JUDGMENT-only: WHAT to
-- surface and WHY, with dedup left to the tool.
--
-- (The GitHub / calendar / airtable items the audit flagged stay as prose on
-- purpose: their dangerous verbs — create/merge/delete/send — are already
-- routed through the generic Trust gate, and building per-vendor input
-- validators would reintroduce the per-toolkit Go branching Rule #1 forbids.
-- The "revert once, no infinite loop" concern is already covered generically by
-- the LoopGate. So no per-vendor gates were added.)
--
-- Idempotent: ON CONFLICT updates the body; active pointer set unconditionally.

BEGIN;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'batch-surface-items',
  'v1.1-deterministic',
  $SKILL$# batch-surface-items

## Purpose
Surface several related findings onto the dashboard in one pass.

## What you decide (the judgment)
- Which findings are worth surfacing — drop obvious duplicates and noise.
- A short, actionable title, a rank, and a one-line "why it matters" for each.
- The canonical surface that matches the dashboard card (don't invent a new surface name when one already exists).

## How
For each finding, call `surface_item` with the title, body, rank, surface, and compact metadata chips (account, intent, mode). You do NOT manage deduplication — `surface_item` derives a stable id and upserts, so reruns refresh the same rows instead of piling up duplicates. After surfacing, summarize what you added and why.
$SKILL$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO UPDATE SET skill_md = EXCLUDED.skill_md;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('batch-surface-items', 'v1.1-deterministic')
ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version, updated_at = NOW();

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'feed-monitor',
  'v1.1-deterministic',
  $SKILL$# Feed monitor

Make "watch this for me" a durable, autonomous loop — not a one-off check.

## What you decide (the judgment)
- The best source: a direct RSS/Atom URL, a site's `/feed` or `/rss`, or a `web_search` query each run when there is no feed.
- The cadence — hourly/daily by how fast the source moves.
- Which new items are genuinely RELEVANT and worth the boss's attention, plus a tight "why it matters" for each.
- Whether a hit is high-value enough to `notify` the boss, or routine (dashboard only).

## How
Schedule it with `cron_create_agent`: a recurring prompt that fetches the source and, for relevant items, calls `surface_item` on the `insights`/`alerts` surface with a title, link, and why-it-matters. You do NOT need to track "seen" ids — `surface_item` derives a stable id and upserts, so the same post never surfaces twice on rerun. When there is nothing relevant, surface nothing.

Confirm the job back to the boss: what it watches, how often, and where results show up. Offer `cron_delete` to stop it.
$SKILL$,
  '',
  0.9,
  'manual'
)
ON CONFLICT (skill_name, version) DO UPDATE SET skill_md = EXCLUDED.skill_md;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('feed-monitor', 'v1.1-deterministic')
ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version, updated_at = NOW();

COMMIT;
