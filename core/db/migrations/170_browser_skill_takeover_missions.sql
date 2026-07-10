-- 170_browser_skill_takeover_missions.sql
--
-- Bump `web-browsing` to v1.2 for TAKEOVER MODE + WEB MISSIONS.
--
-- v1.1's hard rules said "a login you don't have → STOP and ask the boss"
-- and "a real CAPTCHA → don't fight it, report and suggest another source".
-- Both were dead ends: the run died and the boss had to notice, come to the
-- desk, and restart. The mechanic that fixes this shipped in code with this
-- migration: `browser_request_takeover` flips the live session to human
-- control, pings the boss's phone (deep link into the session's Preview
-- pane, which is fully interactive - his clicks/keys go to the real
-- browser), BLOCKS until he hands back, and the agent's write verbs yield
-- while he's driving. A captcha is now a 30-second interruption, not a
-- mission-killer.
--
-- v1.2 also adds MISSION framing: a multi-step outcome job ("book X",
-- "order Y", "compile Z across sites") runs under a plan so progress shows
-- on the work board, and ends with a real deliverable - reusing plan_*,
-- surface_item, and notify rather than any new mission object (the
-- consolidate-surfaces rule; the durable substrate already exists).
--
-- Same pattern as 065: new immutable version + flip mem_skill_active.

BEGIN;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'web-browsing',
  'v1.2-7-10-2026',
  $skill$---
name: web-browsing
version: "v1.2-7-10-2026"
description: Drive a real anti-detect browser to do live web tasks and multi-step missions — find leads, search directories/maps, fill forms, book/order with approval, scrape results into a clean report, and get into JavaScript-heavy or bot-walled pages http_fetch can't reach. Observe → act → extract loop with auto-waiting. When a wall needs a human (captcha, login, 2FA), hand the live browser to the boss with browser_request_takeover and continue after he hands back. Mac-first (residential IP); the boss watches live in the Preview pane.
trigger_phrases:
  - find me leads
  - find businesses
  - look up online
  - search the web for
  - scrape
  - make me a report on
  - browse to
  - fill out the form at
  - it's blocking me
  - bypass the block
  - book me
  - order this
  - go get me
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
confidence: 0.87
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
load_tools(["browser_open","browser_navigate","browser_observe","browser_act","browser_extract","browser_request_takeover","browser_close"])
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

## 4. Blocked? Hand the browser to the boss (takeover)

Some walls only a human can clear: a CAPTCHA you'd have to *solve*, a login
for an account whose credentials you don't hold, a 2FA prompt, a "verify
you're human" that survives a retry. The move is **never** to fight it,
guess credentials, or abandon the task — it's a handoff:

1. Soft bot-check first: one `browser_navigate` retry (anti-detect passes
   most interstitials on reload). Still walled → step 2.
2. `browser_request_takeover` with a `reason` that tells him exactly what
   to do ("CAPTCHA on united.com checkout", "log into your Costco
   account"). His phone gets pinged; the Preview pane is his to click and
   type in — he enters credentials himself, they never pass through you.
3. The call blocks until he hands back. When it returns after a handback,
   **`browser_observe` before anything else** — the page changed under you.
   Then continue the task from where the wall was.
4. If he doesn't respond in the wait window, control returns to you but the
   wall remains: park the task, `notify` him what's blocked and what you'll
   do when he clears it, and move on. Don't loop on the wall.

If the boss grabs the mouse on his own mid-run (you'll see your acts refuse
with "the boss is driving"), same protocol: `browser_request_takeover` to
wait for his handback, then re-observe.

## 5. Missions — multi-step outcome jobs

"Find me the cheapest flight and hold it", "order the usual filters",
"compile pricing from these five vendors" — anything with **multiple stages
and a concrete outcome** is a mission, not a browse. Missions get structure:

- **Open a plan** (`plan_create`) with the real stages ("search fares",
  "compare + pick", "hold seat", "report"). Progress then shows on the
  boss's work board while you run, and a takeover pause doesn't lose your
  place.
- **Money and messages still stop for approval** — the hard rules below
  apply per-step, and takeover pauses don't change them. Getting the boss
  to solve a captcha is NOT approval to buy; ask separately, in words.
- **End with the deliverable, not a log**: the answer in chat if he's
  present; for a background mission, `surface_item` (kind `deliverable`,
  the link/confirmation number in `url`) + `notify` with
  `conversation: true` and the one-paragraph outcome.

## 6. Extract to scrape

When a page holds the data you want (a results list, a business detail, an
article), call `browser_extract`. It returns clean text — names, phones,
addresses, links — far more reliable than reading raw page structure.
Collect what you need across as many results/pages as the task warrants
(click into a result → extract → go back → next; or page through with a
"Next" click + extract each page). Track the source URL for each.

## 7. Close and report

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

## 8. Save the playbook (self-healing)

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
*that* too, as a one-line caution — and note whether a takeover cleared it,
so next time you ping the boss immediately instead of burning retries.

## Hard rules (safety — these are not optional)

- **Never type credentials.** If a page needs a login, that's a takeover
  (§4) — the boss types his own credentials into the live pane. Never
  invent, guess, or type anything into a password field yourself.
- **Never solve-or-fight a hard CAPTCHA.** One soft retry, then takeover
  (§4). Don't burn ten turns on a wall a human clears in ten seconds.
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
  0.87,
  'boss_requested'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('web-browsing', 'v1.2-7-10-2026')
ON CONFLICT (skill_name) DO UPDATE SET active_version = EXCLUDED.active_version;

COMMIT;
