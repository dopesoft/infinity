-- 065_seed_browser_skill_stealth.sql
--
-- Bump the `web-browsing` skill to v1.1 now that the browser_* tools are
-- backed by an ANTI-DETECT engine (Camoufox / camofox-browser) running
-- Mac-first on a residential IP with a Cloud fallback. The verb contract is
-- unchanged — this is pure cognition (Rule #1): teach the agent that
--
--   (a) the browser now gets through bot walls that block http_fetch, so it
--       should ESCALATE to the browser when a fetch/search hits a 403, a
--       challenge page, or an empty JS shell — instead of giving up;
--   (b) per-site know-how is a DOMAIN PLAYBOOK: when it cracks a tricky site
--       it saves the trick to procedural memory via `remember`, and checks
--       memory for an existing playbook before browsing a known-hard site.
--       This is the self-healing loop the boss asked for — playbooks accrue
--       from real runs, no new Go, no speculative seeds.
--
-- Why a new version instead of editing v1.0: skill versions are immutable
-- and Voyager/GEPA evolve them; we add v1.1 and flip mem_skill_active. The
-- inserts are idempotent so re-running migrate is safe.

BEGIN;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'web-browsing',
  'v1.1-5-22-2026',
  $skill$---
name: web-browsing
version: "v1.1-5-22-2026"
description: Drive a real anti-detect browser to do live web tasks — find leads/businesses, search directories/maps, fill forms, scrape results into a clean report, and get into JavaScript-heavy or bot-walled pages that http_fetch can't reach. Observe → act → extract loop with auto-waiting. Mac-first (residential IP) so it passes most bot detection; the boss watches live in the Preview pane (column 3).
trigger_phrases:
  - find me leads
  - find businesses
  - find plumbers in
  - look up online
  - search the web for
  - scrape
  - make me a report on
  - browse to
  - fill out the form at
  - it's blocking me
  - bypass the block
inputs:
  - name: task
    type: string
    doc: what the boss wants found or done on the web
  - name: start_url
    type: string
    doc: optional URL to start from; default a search engine
outputs:
  - name: report
    type: string
  - name: sources
    type: array
risk_level: medium
network_egress: browser
confidence: 0.85
---

# Web browsing

You are driving a real browser to get something done on the live web. This
is for tasks `http_fetch` and `web_search` can't do: pages that need
JavaScript, results behind a search box, directories and maps, multi-step
forms, anything where you have to *interact* — and anything that blocks a
plain fetch.

**Your browser is anti-detect.** It's a Camoufox (Firefox) engine with
engine-level fingerprint spoofing, and it runs Mac-first on the boss's home
**residential IP** (Cloud is the fallback when the Mac is offline). In
practice that means it gets through Cloudflare / DataDome / "are you a
human" soft-walls that return a 403 or a challenge page to `http_fetch`. So
when a fetch or search gets blocked, **don't give up — escalate to the
browser.** The boss is watching you drive in Studio's Preview pane (column
3), so move deliberately.

The whole contract is one loop, repeated:

> **observe → act → extract**

You never guess CSS selectors. You `browser_observe` to get a numbered list
of interactive elements, you `browser_act` on one BY INDEX, the page
auto-waits to settle, and you observe again. When a page has what you came
for, you `browser_extract` to pull clean markdown.

## 0. Load the tools — and check for a playbook

The `browser_*` verbs are dormant to save context. First call:

```
load_tools(["browser_open","browser_navigate","browser_observe","browser_act","browser_extract","browser_close"])
```

If the task targets a **specific site you've handled before** (a directory,
a portal, a store), check memory first — you may have saved a *domain
playbook* for it (see §6). A 5-second memory check beats re-deriving the
flow. Your retrieved memories already surface relevant ones; if you recall a
playbook for this domain, follow it.

## 1. Open and go

`browser_open` (optionally with `url`). If you didn't pass a URL, or you
want to search, `browser_navigate` to a search engine
(`https://duckduckgo.com/?q=...` or `https://www.google.com/search?q=...`)
with your query already in the URL — that skips a typing round-trip.

For a lead-gen task like "find plumbers in Frisco, Texas", a good first
move is a maps/directory query: navigate straight to
`https://www.google.com/maps/search/plumbers+in+frisco+tx` or a search for
"plumbers frisco tx", then observe.

## 2. Observe before every action

`browser_observe` returns:
- **Interactive elements**, each `[index] tag "label" (extra)`. You act on
  these by index.
- **Page text** — the readable structure, for deciding what to do next.

Read it. Decide the single next action. Don't fire actions blind.

## 3. Act by index

`browser_act` with `index`, `action`, optional `value`, and ALWAYS a short
`label` describing what you're doing ("Search button", "Next page",
"Result: ABC Plumbing"). Actions:
- `click` — links, buttons, results
- `type` — `value` is the text (it clears the field first)
- `select` — `value` is the option
- `press` — `value` is a key like `Enter` (great after typing a query)
- `scroll` — pull more results into view (many directories lazy-load)
- `clear` — empty a field

The page auto-waits for the network to go idle after each act. **Refs reset
after every navigation**, so always `browser_observe` again after a click
that changed the page before you act on the new page — don't reuse old
indices.

## 4. Extract to scrape

When a page holds the data you want (a results list, a business detail, an
article), call `browser_extract`. It returns clean text — names, phones,
addresses, links — far more reliable than reading raw page structure.
Collect what you need across as many results/pages as the task warrants
(click into a result → extract → go back → next; or page through with a
"Next" click + extract each page). Track the source URL for each.

## 5. Close and report

When you have enough, `browser_close`. Then assemble the answer the boss
actually asked for — usually a tidy markdown report:

```
## Plumbers in Frisco, TX

1. **ABC Plumbing** — (469) 555-0142 — 4.8★ (210 reviews)
   123 Main St · abcplumbing.com
2. ...
```

Lead with the deliverable, then list `sources`. If the boss asked for a
specific format (CSV, a table, "just the phone numbers"), match it.

If the task is big (50 leads), do a solid first batch (10–15), report it,
and offer to continue rather than grinding silently for ten minutes.

## 6. Save the playbook (self-healing)

The first time you work out how to handle a specific site — the search box
that needed an Enter not a click, the cookie wall you dismiss first, the
"load more" that lazy-loads, the exact maps URL shape that skips the
splash — **save it** so next time is instant. Use `remember` with:

- `tier: "procedural"`
- content written as a short, reusable rule that names the **domain**, e.g.
  *"Playbook — yellowpages.com: results are behind a search at [the top
  form]; type the trade + city, press Enter (clicking Search opens an ad).
  Each result card has the phone inline, no need to click in. Page through
  with the numbered pager at the bottom."*

These domain playbooks are retrieved by domain on future tasks, so the agent
gets better at the sites the boss actually uses — without anyone editing
this skill. If a site beat you (hard wall, layout you couldn't crack), save
*that* too, as a one-line caution, so you don't burn time the same way next
time.

## Hard rules (safety — these are not optional)

- **Never type credentials.** If a page needs a login you don't have
  saved, STOP and ask the boss — do not invent or guess a username or
  password, and do not type anything into a password field.
- **Hard human-verification → stop and report. Soft bot-check → one retry.**
  Because the browser is anti-detect, a lot of "are you human" interstitials
  just pass on a reload. If you hit one, a single `browser_navigate` retry
  (or wait + observe) is fine. But a real CAPTCHA you'd have to *solve*
  (pick the traffic lights, slide the puzzle) — don't fight it, don't try to
  solve it. Report which site walled you and suggest another source.
- **Never spend money or destroy anything without explicit confirmation.**
  Before any act that buys, pays, checks out, places an order, subscribes,
  books, transfers money, or deletes/closes an account: STOP, summarise
  exactly what you're about to do and what it costs, and ask the boss in
  chat. (The gate also stops these for Trust approval — but surface it in
  conversation first, don't rely on the gate alone.)
- **Don't submit a form that sends a message as the boss** (contact forms,
  "request a quote", emails) without showing the boss the exact content and
  getting a yes. Filling the fields is fine; the final submit is the line.
- **Stay on task.** One task, one browsing session, then close it.
- **Respect the budget.** If the daily-cost overlay shows >80% spent, keep
  the run short — a focused first batch, then report.

## When NOT to use the browser

- A plain fact you can get from `web_search` → use `web_search`, it's
  cheaper and faster.
- A known API/JSON endpoint or a static page that isn't blocked →
  `http_fetch`.
- The browser is for *interaction*, *JavaScript-rendered* pages, and
  *getting past blocks*. If `http_fetch` already returned the content
  cleanly, you're done — don't open the browser to re-do it.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Flip the active version to v1.1.
INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('web-browsing', 'v1.1-5-22-2026')
ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version;

COMMIT;
