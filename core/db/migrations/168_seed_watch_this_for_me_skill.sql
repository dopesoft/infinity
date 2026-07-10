-- 168_seed_watch_this_for_me_skill.sql
--
-- The recipe for "keep an eye on X" — a standing watch the boss sets up in
-- one sentence: recurring check → diff against last time → a brief in the
-- inbox only when something actually changed → a conversational push when
-- it's big enough to talk about.
--
-- Rule #1: the deterministic substrate is Go — cron_create_agent /
-- sentinel_create (the recurring trigger), state_get/state_set
-- (mem_agent_state, the "what did I see last time?" cell, migration 167),
-- surface_item (upsert-by-external_id makes re-briefs idempotent — one
-- card per watch, always current), notify with conversation=true (opens a
-- session the push deep-links into). This skill carries only the JUDGMENT:
-- what to watch, how often, what counts as a meaningful change, and how
-- loudly to say so.
--
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'watch-this-for-me',
  'Set up and run a standing watch when the boss says "keep an eye on X" / "watch X" / "let me know when Y": create a recurring check (cron or sentinel), diff each run against the last snapshot in mem_agent_state, surface a brief only when something meaningfully changed, and open a conversation with the boss when it''s big enough to act on. Also runs the per-fire check for existing watches.',
  'medium',
  '["web:*"]'::jsonb,
  '["keep an eye on","watch this for me","let me know when","let me know if","track this","monitor","alert me when","tell me when","keep tabs on","watch for","standing watch"]'::jsonb,
  '[{"name":"target","type":"string","doc":"what to watch - a topic, a URL, a repo, a price, a person, a metric"}]'::jsonb,
  '[{"name":"watch_slug","type":"string","doc":"stable slug identifying the watch (cron name, state key, surface external_id all derive from it)"},{"name":"summary","type":"string","doc":"what is being watched, at what cadence, and what counts as news"}]'::jsonb,
  0.85, 'active', 'boss_requested', 80,
  'Headline proactive capability: the boss delegates attention in one sentence and Jarvis keeps watching after the chat ends.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'watch-this-for-me', 'v1.0-7-10-2026',
  $skill$---
name: watch-this-for-me
version: "v1.0-7-10-2026"
description: Stand up and run standing watches - recurring check, diff vs last snapshot, brief only on real change, conversation when it matters.
trigger_phrases:
  - keep an eye on
  - watch this for me
  - let me know when
  - track this
  - monitor
  - alert me when
inputs:
  - name: target
    type: string
outputs:
  - name: watch_slug
    type: string
  - name: summary
    type: string
risk_level: medium
network_egress: web
confidence: 0.85
---

# Watch this for me

The boss delegates attention in one sentence; you keep watching after the chat
ends. Two modes: **setting up** a new watch (the boss just asked), and
**running** one (a cron fired and its prompt points here).

## Setting up a watch

1. **Pin down what "news" means.** From the boss's sentence, decide: what is
   the source of truth (a site, a search, a repo, an API, a mailbox), and what
   change is worth his attention? Only ask if genuinely ambiguous - "keep an
   eye on Claude model releases" is complete as stated.
2. **Pick a slug** - short kebab-case, e.g. `claude-releases`. Everything keys
   off it: cron name `watch: <slug>`, state key `watch:<slug>:last`, surface
   external_id `watch:<slug>`.
3. **Choose the trigger:**
   - The check needs judgment or multiple steps (search the web, read pages,
     compare, summarize) → `cron_create_agent`. This is most watches. Pick the
     cadence yourself from how fast the target moves: news/prices a few times
     a day, repos/blogs daily, slow-moving topics weekly. Say the cadence in
     your reply so the boss can correct it.
   - The check is ONE http endpoint whose response changing IS the news →
     `sentinel_create` with `external_api_poll` + `fire_on_change` (cheaper,
     no LLM per poll).
4. **Cron prompt template** (keep it thin - the judgment lives here, not in the
   cron): `This is the standing watch "<slug>": <one-line what and why>.
   Follow the watch-this-for-me skill's "Running a watch" steps for it.`
5. **Baseline now.** Run the check once immediately, `state_set`
   `watch:<slug>:last` with the snapshot, and reply with: what you'll watch,
   the cadence, what counts as news, and what the baseline looks like today.

## Running a watch (a cron fired)

1. `state_get watch:<slug>:last` - `found=false` means baseline was lost;
   treat this run as a new baseline, don't fabricate a diff.
2. **Gather the current state** from the same source(s) the watch was set up
   against. If the check itself fails (fetch error, auth dead, source gone),
   FAIL the run loudly - do NOT `state_set`, do NOT report "no news". A broken
   watch that says "all quiet" is the worst outcome there is.
3. **Diff for meaning, not bytes.** New items, changed facts, crossed
   thresholds count. Reordering, timestamps, and cosmetic rewording don't.
4. **Nothing meaningful?** Update `state_set` with the fresh snapshot and end
   quietly. No card, no push. Silence is the product working.
5. **Something new?** Write the brief - lead with what changed since last
   time, in plain English, with the link. Then:
   - `surface_item` with `surface: "watches"`, `kind: "watch_brief"`,
     `external_id: "watch:<slug>"`, a title like `Watch: <topic> - <headline>`,
     the diffed brief as body, the best URL, and importance you judge (60-90).
     Same external_id every time - the card updates in place, never piles up.
   - Judge the volume: **big or actionable** (the thing the boss was actually
     waiting for, or something he'd want to act on today) → `notify` with
     `conversation: true`, body = the one-paragraph version + "want me to
     <obvious next step>?". **Notable but not urgent** → `notify` normal.
     **Minor** → the card update alone is enough.
6. `state_set watch:<slug>:last` with the new snapshot - AFTER the brief is
   surfaced, so a crash before this line just re-briefs idempotently next run.

## Managing watches

- "What are you watching?" → `cron_list` (names starting `watch:`) +
  `state_list` prefix `watch:` - report each with its cadence and last update.
- "Stop watching X" → `cron_delete` (or `sentinel_delete`), `state_delete`
  the snapshot, and `surface_update` the card to done.
- The boss adjusting scope ("only major releases") → update the cron prompt's
  one-liner and say what changed.
$skill$,
  '', 0.85, 'boss_requested'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('watch-this-for-me', 'v1.0-7-10-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
