-- 060_seed_browser_skill.sql
--
-- Seed the `web-browsing` default skill. This is the cognition that makes
-- the cloud browser behave like a real agent the first time the boss says
-- "go find me some plumbers in Frisco" — the observe → act → extract loop,
-- how to scrape a directory into a report, and the hard safety rules.
--
-- Why a skill, not Go (Rule #1): the building blocks are generic Go (the
-- browser_* tools + the BrowserGate + the screencast). The *judgment* —
-- when to extract vs observe, how to page through results, never autofill
-- credentials, stop on CAPTCHA, summarise-and-confirm before spending money
-- — lives here so Voyager/GEPA can evolve it without a deploy.
--
-- The browser_* tools are dormant by default (lazy-loaded to protect the
-- per-turn context budget); this skill's step 0 loads them. Retrieved via
-- RRF when the opening message looks like a browsing/lead-gen task.
--
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'web-browsing',
  'Drive a real cloud browser to do live web tasks: find leads/businesses, search directories and maps, look things up that need JavaScript or a login, fill forms, and scrape results into a clean report. Uses the observe → act → extract loop (browser_open/observe/act/extract/close) with auto-waiting so it does not break on page 2. The boss watches it drive live in Studio''s Preview pane (column 3). Reads run unattended; transactional actions (buy/pay/checkout/delete-account) stop for Trust approval.',
  'medium',
  '["browser:*"]'::jsonb,
  '["find me leads","find leads","find businesses","find plumbers","find <profession> in <city>","look up online","search the web for","scrape","make me a report on","go to website","browse to","navigate to","fill out the form at","look this up","find contact info","find phone numbers"]'::jsonb,
  '[{"name":"task","type":"string","doc":"what the boss wants found/done on the web"},{"name":"start_url","type":"string","doc":"optional URL to start from; if omitted, start from a search engine"}]'::jsonb,
  '[{"name":"report","type":"string","doc":"the markdown report / answer assembled from what was scraped"},{"name":"sources","type":"array","doc":"URLs visited that back the report"}]'::jsonb,
  0.85,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'web-browsing',
  'v1.0-5-20-2026',
  $skill$---
name: web-browsing
version: "v1.0-5-20-2026"
description: Drive a real cloud browser to do live web tasks — find leads/businesses, search directories/maps, fill forms, and scrape results into a clean report. Observe → act → extract loop with auto-waiting. The boss watches live in the Preview pane (column 3).
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

You are driving a real browser in the cloud to get something done on the
live web. This is for tasks `http_fetch` and `web_search` can't do: pages
that need JavaScript, results behind a search box, directories and maps,
multi-step forms, anything where you have to *interact*. The boss is
watching you drive in Studio's Preview pane (column 3), so move deliberately.

The whole contract is one loop, repeated:

> **observe → act → extract**

You never guess CSS selectors. You `browser_observe` to get a numbered list
of the interactive elements, you `browser_act` on one BY INDEX, the page
auto-waits to settle, and you observe again. When a page has what you came
for, you `browser_extract` to pull clean markdown.

## 0. Load the tools

The `browser_*` verbs are dormant to save context. First call:

```
load_tools(["browser_open","browser_navigate","browser_observe","browser_act","browser_extract","browser_close"])
```

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
- **Page text** — the readable content, for deciding what to do next.

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

The page auto-waits for the network to go idle after each act, so the next
`browser_observe` sees the settled page. If an element moved, `browser_act`
re-observes and retries once automatically.

## 4. Extract to scrape

When a page holds the data you want (a results list, a business detail, an
article), call `browser_extract`. It returns clean markdown — names,
phones, addresses, links — far more reliable than reading raw page text.
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

## Hard rules (safety — these are not optional)

- **Never type credentials.** If a page needs a login you don't have
  saved, STOP and ask the boss — do not invent or guess a username or
  password, and do not type anything into a password field.
- **CAPTCHA / "are you human" / bot wall → stop and report.** Don't fight
  it. Tell the boss which site blocked you and suggest an alternative
  source. Many directories (Yelp, some maps) gate aggressively; pivot to
  another source rather than hammering one.
- **Never spend money or destroy anything without explicit confirmation.**
  Before any act that buys, pays, checks out, places an order, subscribes,
  books, transfers money, or deletes/closes an account: STOP, summarise
  exactly what you're about to do and what it costs, and ask the boss in
  chat. (The gate also stops these for Trust approval — but you should
  surface it in conversation first, not rely on the gate alone.)
- **Don't submit a form that sends a message as the boss** (contact forms,
  "request a quote", emails) without showing the boss the exact content
  and getting a yes. Filling the fields is fine; the final submit is the
  line.
- **Stay on task.** Don't wander to unrelated pages. One task, one
  browsing session, then close it.
- **Respect the budget.** If the daily-cost overlay shows >80% spent, keep
  the run short — a focused first batch, then report.

## When NOT to use the browser

- A plain fact you can get from `web_search` → use `web_search`, it's
  cheaper and faster.
- Fetching a known API/JSON endpoint or a static page → `http_fetch`.
- The browser is for *interaction* and *JavaScript-rendered* pages. If a
  task doesn't need either, don't open it.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('web-browsing', 'v1.0-5-20-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
