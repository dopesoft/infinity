-- 073_seed_research_content_skills.sql
--
-- Adopt Hermes's research + creative bundled skills as Infinity recipes over
-- OUR substrate: http_fetch / web_search for the data, document_create for
-- deliverables, cron_create_agent for monitoring, and fs_save / artifact_save
-- (Mac or cloud workspace) for HTML/SVG artifacts. Pure-cognition writing
-- skills (humanizer, ideation) carry no tools — the rubric IS the skill.
--
-- Rule #1: cognition in SKILL.md, not Go. Idempotent on all three tables.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────
-- arxiv-research
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'arxiv-research',
  'Search and retrieve academic papers from arXiv by keyword/author/category/id via the public arXiv API, then summarize findings or compile a reading list. Use for literature scans on ML/CS/physics topics.',
  'low', '["http_fetch"]'::jsonb,
  '["search arxiv","find papers on","latest research on","arxiv paper","literature review","recent papers about","pull the paper"]'::jsonb,
  '[{"name":"query","type":"string","doc":"topic, author, or arXiv id"}]'::jsonb,
  '[{"name":"summary","type":"string"},{"name":"papers","type":"array"}]'::jsonb,
  0.83, 'active', 'hermes_imported', 58,
  'Keeps Jarvis current on research; the boss builds AI systems and tracks the field.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'arxiv-research', 'v1.0-5-24-2026',
  $skill$---
name: arxiv-research
version: "v1.0-5-24-2026"
description: Search/retrieve arXiv papers via the public API; summarize or compile a reading list.
trigger_phrases:
  - search arxiv
  - find papers on
  - latest research on
  - literature review
inputs:
  - name: query
    type: string
outputs:
  - name: summary
    type: string
  - name: papers
    type: array
risk_level: low
network_egress: http_fetch
confidence: 0.83
---

# arXiv research

Use the free arXiv API — no key needed.

1. **Query** `http_fetch` GET `http://export.arxiv.org/api/query?search_query=<q>&start=0&max_results=20&sortBy=submittedDate&sortOrder=descending`.
   Build `search_query` with field prefixes: `all:`, `ti:` (title), `au:` (author),
   `cat:` (e.g. `cat:cs.LG`). For a known id use `id_list=<arxiv_id>`.
2. **Parse** the Atom XML: title, authors, summary, published date, pdf link, arXiv id.
3. **Rank/filter** by recency + relevance to the actual question. Drop near-duplicates.
4. **Deliver:** a concise summary of what the papers collectively say + a list (title,
   authors, date, one-line takeaway, link). For a deep dive, `http_fetch` the abstract
   page or hand off the PDF to `extract-document-text`.
5. For a formal output, `document_create` a markdown/PDF reading list.

Cite every claim to a specific paper. Don't overstate — summarize what's in the abstract,
flag when you'd need the full text.
$skill$,
  '', 0.83, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('arxiv-research', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- youtube-to-content
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'youtube-to-content',
  'Turn a YouTube video into useful text: pull the transcript, then produce a summary, a thread, a blog post, or key timestamps. Uses http_fetch for the transcript + document_create for the deliverable.',
  'low', '["http_fetch","web_search"]'::jsonb,
  '["summarize this youtube","youtube video","transcript of","turn this video into a thread","blog post from this video","what does this video say","key points from the video"]'::jsonb,
  '[{"name":"url","type":"string","doc":"YouTube URL or id"},{"name":"format","type":"string","doc":"summary | thread | blog | timestamps","required":false}]'::jsonb,
  '[{"name":"output","type":"string"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 58,
  'Content repurposing for the boss''s channels; video → text in his voice.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'youtube-to-content', 'v1.0-5-24-2026',
  $skill$---
name: youtube-to-content
version: "v1.0-5-24-2026"
description: YouTube transcript → summary / thread / blog / timestamps via http_fetch + document_create.
trigger_phrases:
  - summarize this youtube
  - transcript of
  - turn this video into a thread
  - blog post from this video
inputs:
  - name: url
    type: string
  - name: format
    type: string
    required: false
outputs:
  - name: output
    type: string
risk_level: low
network_egress: http_fetch
confidence: 0.8
---

# YouTube to content

1. **Get the video id** from the URL.
2. **Pull the transcript.** Try the timedtext endpoint
   (`http_fetch` `https://www.youtube.com/api/timedtext?...`) or fetch the watch page and
   extract the caption track. If captions are unavailable, say so — don't fabricate a
   transcript. (The cloud `browser` can open the page if `http_fetch` is blocked.)
3. **Produce the requested format:**
   - *summary* — tight TL;DR + the main points in order.
   - *thread* — hook + 5–10 punchy posts, one idea each, in the boss's voice
     (run it through `humanizer`).
   - *blog* — structured post with headings; `document_create` a markdown/PDF.
   - *timestamps* — key moments as `mm:ss — topic`.
4. Attribute to the video; quote sparingly and accurately.

Don't invent quotes or claims not in the transcript.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('youtube-to-content', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- maps-geo
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'maps-geo',
  'Geocode addresses, find points of interest, compute routes/distances/ETAs, and look up timezones using free OpenStreetMap services (Nominatim + OSRM) via http_fetch. Use for "where is", "how far", "route between", or nearby-POI questions.',
  'low', '["http_fetch"]'::jsonb,
  '["geocode","how far is","distance between","route from","directions to","find nearby","what timezone","coordinates of","nearest"]'::jsonb,
  '[{"name":"query","type":"string","doc":"the geo question"}]'::jsonb,
  '[{"name":"answer","type":"string"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 52,
  'Free geo/routing without an API key; useful for logistics and lead-location work.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'maps-geo', 'v1.0-5-24-2026',
  $skill$---
name: maps-geo
version: "v1.0-5-24-2026"
description: Geocode, POIs, routes/ETAs, timezones via OpenStreetMap (Nominatim + OSRM) over http_fetch.
trigger_phrases:
  - geocode
  - how far is
  - route from
  - find nearby
  - what timezone
inputs:
  - name: query
    type: string
outputs:
  - name: answer
    type: string
risk_level: low
network_egress: http_fetch
confidence: 0.8
---

# Maps & geo

Free OSM services, no key. Send a descriptive User-Agent (Nominatim requires it) and
don't hammer — one request at a time.

- **Geocode:** `http_fetch` `https://nominatim.openstreetmap.org/search?q=<addr>&format=json&limit=1`
  → lat/lon + display name. Reverse: `/reverse?lat=&lon=&format=json`.
- **POIs nearby:** Overpass API (`https://overpass-api.de/api/interpreter`) with an
  `amenity`/`shop` filter around a lat/lon radius.
- **Route / distance / ETA:** OSRM
  `https://router.project-osrm.org/route/v1/driving/<lon1>,<lat1>;<lon2>,<lat2>?overview=false`
  → distance (m) + duration (s). Convert to mi/km + minutes.
- **Timezone:** infer from coordinates / country, or note if you need a TZ service.

Report a clean human answer (distance, ETA, address) — not raw JSON. State the data is
from OpenStreetMap. For an interactive map task (clicking around Google Maps), use the
`web-browsing` skill instead.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('maps-geo', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- feed-monitor
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'feed-monitor',
  'Stand up a recurring watch on blogs, RSS/Atom feeds, or a search topic: poll on a schedule, detect new items since last check, and surface the notable ones to the dashboard. Uses cron_create_agent + http_fetch/web_search + surface_item. Use for "keep an eye on" / "alert me when" requests.',
  'low', '["http_fetch","web_search"]'::jsonb,
  '["monitor","keep an eye on","watch this blog","alert me when","track new posts","follow this feed","notify me about new"]'::jsonb,
  '[{"name":"source","type":"string","doc":"feed URL, site, or search topic"},{"name":"cadence","type":"string","doc":"how often to check","required":false}]'::jsonb,
  '[{"name":"job","type":"string","doc":"the scheduled watch + what it surfaces"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 62,
  'Turns "watch X for me" into a durable autonomous loop — core proactive behavior.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'feed-monitor', 'v1.0-5-24-2026',
  $skill$---
name: feed-monitor
version: "v1.0-5-24-2026"
description: Recurring watch on feeds/blogs/topics — poll on a schedule, detect new items, surface the notable ones to the dashboard.
trigger_phrases:
  - keep an eye on
  - watch this blog
  - alert me when
  - track new posts
inputs:
  - name: source
    type: string
  - name: cadence
    type: string
    required: false
outputs:
  - name: job
    type: string
risk_level: low
network_egress: http_fetch
confidence: 0.8
---

# Feed monitor

Make "watch this for me" a durable, autonomous loop — not a one-off check.

1. **Resolve the source.** A direct RSS/Atom URL is best; for a site, look for its
   `/feed` or `/rss`; for a topic with no feed, use a `web_search` query each run.
2. **Schedule it.** `cron_create_agent` with a clear recurring prompt and a cadence
   (default hourly/daily by how fast the source moves). The prompt should: fetch the
   source, compare against what was seen last run (persist the last-seen ids/timestamps
   with `remember`/`mem_act`), and for genuinely NEW + relevant items call `surface_item`
   on the `insights`/`alerts` surface with a title, link, and why-it-matters.
3. **Dedupe.** Track seen items in memory so the same post never surfaces twice.
4. **Be quiet when there's nothing.** No new items → surface nothing. Only notify the
   boss (`notify`) for high-value hits; routine finds just go to the dashboard.

Confirm the job back to the boss: what it watches, how often, where results show up.
Offer `cron_delete` to stop it.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('feed-monitor', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- polymarket-query
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'polymarket-query',
  'Query Polymarket prediction markets via the public API: find markets, read current prices/odds, order books, and resolution status. Read-only — surfaces market sentiment, does not place trades. Use for "what are the odds on" / "prediction market for".',
  'low', '["http_fetch"]'::jsonb,
  '["polymarket","prediction market","what are the odds","market odds for","betting odds on","probability the market gives"]'::jsonb,
  '[{"name":"query","type":"string","doc":"the market/topic"}]'::jsonb,
  '[{"name":"answer","type":"string"}]'::jsonb,
  0.78, 'active', 'hermes_imported', 48,
  'Surfaces market-implied probabilities as a signal; read-only, no trading.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'polymarket-query', 'v1.0-5-24-2026',
  $skill$---
name: polymarket-query
version: "v1.0-5-24-2026"
description: Read Polymarket markets/prices/orderbooks via the public API. Read-only — no trading.
trigger_phrases:
  - polymarket
  - prediction market
  - what are the odds
  - market odds for
inputs:
  - name: query
    type: string
outputs:
  - name: answer
    type: string
risk_level: low
network_egress: http_fetch
confidence: 0.78
---

# Polymarket query

Read-only market lookups via the public API.

1. **Find the market.** `http_fetch` the Gamma API
   (`https://gamma-api.polymarket.com/markets?...`) filtered by the topic/keyword; get the
   market + outcome token ids.
2. **Read prices/odds.** Use the CLOB API
   (`https://clob.polymarket.com/...`) for current prices / order book / midpoint.
3. **Report** the market question, the implied probability for each outcome (price ≈
   probability), volume/liquidity, and resolution date. Frame it as market-implied
   sentiment, not fact.

Hard rule: this skill NEVER places, signs, or funds a trade. It reads. Any trading
request stops and asks the boss explicitly.
$skill$,
  '', 0.78, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('polymarket-query', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- humanizer
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'humanizer',
  'Rewrite text to strip AI-isms and add a real human voice: kill the clichés ("delve", "in today''s fast-paced world", "it''s important to note"), vary rhythm, cut filler, and match the boss''s tone. Pure-cognition editing rubric. Use when copy reads robotic or "too AI".',
  'low', '[]'::jsonb,
  '["humanize this","make it sound human","strip the ai-isms","less robotic","sounds too ai","make it sound like me","rewrite in my voice","de-slop"]'::jsonb,
  '[{"name":"text","type":"string","doc":"the copy to humanize"}]'::jsonb,
  '[{"name":"rewrite","type":"string"}]'::jsonb,
  0.82, 'active', 'hermes_imported', 60,
  'High value for the boss''s marketing/content output; kills the AI-slop tell.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'humanizer', 'v1.0-5-24-2026',
  $skill$---
name: humanizer
version: "v1.0-5-24-2026"
description: Strip AI-isms and add real human voice — kill clichés, vary rhythm, cut filler, match the boss's tone.
trigger_phrases:
  - humanize this
  - make it sound human
  - strip the ai-isms
  - sounds too ai
  - rewrite in my voice
inputs:
  - name: text
    type: string
outputs:
  - name: rewrite
    type: string
risk_level: low
network_egress: none
confidence: 0.82
---

# Humanizer

Make the text sound like a sharp human wrote it, not a model. The rubric:

## Kill the tells

- Ban the slop vocabulary: *delve, tapestry, testament, in today's fast-paced world,
  it's important to note, navigate the landscape, unlock/unleash, elevate, seamless,
  robust, leverage (as a verb), embark on a journey, in conclusion, moreover/furthermore*
  as connective tissue.
- No "Not only X but also Y" scaffolding. No tricolon-of-three on every line. No
  em-dash-everywhere tic. No bulleted lists where prose is better.
- Drop hedging filler: *arguably, it's worth noting, in many ways, when it comes to.*

## Add the voice

- **Vary sentence length.** Short punches between longer lines. Read it aloud in your head.
- **Be specific and concrete** over abstract and general. Name the thing.
- **One clear idea per sentence.** Cut throat-clearing intros — start where it gets interesting.
- **Match the boss's tone** if you have samples in memory (recall them); else default to
  direct, confident, plainspoken — no corporate gloss.
- Keep the meaning and the facts exactly; you're changing voice, not content.

## Output

Return the rewrite only (unless asked for a diff). If the original was already clean,
say so and make minimal changes rather than churning it.
$skill$,
  '', 0.82, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('humanizer', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- ideation
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'ideation',
  'Generate genuinely varied ideas using creative constraints rather than the obvious first five: force different angles, combine unrelated domains, invert assumptions, then cluster and pick. Pure-cognition. Use for brainstorms, naming, concepts, content angles.',
  'low', '[]'::jsonb,
  '["brainstorm","give me ideas","ideate","come up with concepts","name ideas for","angles for","what could we do for"]'::jsonb,
  '[{"name":"prompt","type":"string","doc":"what to ideate on"},{"name":"count","type":"string","doc":"how many","required":false}]'::jsonb,
  '[{"name":"ideas","type":"array"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 52,
  'Better divergent thinking for the boss''s product/content work than default listing.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'ideation', 'v1.0-5-24-2026',
  $skill$---
name: ideation
version: "v1.0-5-24-2026"
description: Generate varied ideas via creative constraints — different angles, domain mashups, inversion — then cluster and pick.
trigger_phrases:
  - brainstorm
  - give me ideas
  - ideate
  - come up with concepts
  - angles for
inputs:
  - name: prompt
    type: string
  - name: count
    type: string
    required: false
outputs:
  - name: ideas
    type: array
risk_level: low
network_egress: none
confidence: 0.8
---

# Ideation

Don't just list the obvious five. Force range with constraints, then converge.

## Diverge (use several lenses)

- **Different personas:** how would a skeptic / a kid / a luxury brand / a hacker approach it?
- **Domain mashup:** borrow a mechanic from an unrelated field (games, biology, finance).
- **Invert it:** what would guarantee the OPPOSITE outcome? Flip those into ideas.
- **Constrain hard:** "if it had to be free / done in a day / one sentence / physical not digital."
- **Scale extremes:** the 10x bigger version and the deliberately tiny version.

Generate more than asked, deliberately including a few weird ones — the obvious ideas are
cheap; the value is in the surprising-but-viable.

## Converge

- Cluster into themes. Cut the duplicates and the truly unworkable.
- Surface the top few with a one-line "why this could work" each, and flag the boldest
  bet. If the boss gave a count, hit it after pruning.

Tie ideas to the boss's actual context (recall what you know about his projects) so
they're usable, not generic.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('ideation', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- html-artifact-design
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'html-artifact-design',
  'Design polished one-off HTML artifacts — a landing page, a pitch deck, a prototype, a mockup — written as self-contained HTML/CSS and saved as an artifact. Avoids generic AI aesthetics; draws on real design-system references. Uses fs_save (Mac or cloud workspace) + artifact_save. Use for "design a page/deck/prototype".',
  'low', '["workspace:*"]'::jsonb,
  '["design a landing page","make a deck","build a prototype","html mockup","design a page","one pager","pitch deck","quick mockup of"]'::jsonb,
  '[{"name":"brief","type":"string","doc":"what to design + audience/vibe"}]'::jsonb,
  '[{"name":"artifact","type":"string","doc":"saved HTML artifact + path"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 56,
  'Lets Jarvis produce real visual deliverables (pages/decks/prototypes), not just text.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'html-artifact-design', 'v1.0-5-24-2026',
  $skill$---
name: html-artifact-design
version: "v1.0-5-24-2026"
description: Polished one-off HTML artifacts (landing/deck/prototype/mockup) as self-contained HTML/CSS, saved as an artifact. Avoids generic AI look.
trigger_phrases:
  - design a landing page
  - make a deck
  - build a prototype
  - html mockup
  - pitch deck
inputs:
  - name: brief
    type: string
outputs:
  - name: artifact
    type: string
risk_level: low
network_egress: workspace
confidence: 0.8
---

# HTML artifact design

Produce a distinctive, production-grade artifact — not the default AI gradient-card look.

1. **Pin the direction.** Audience, purpose, vibe. Anchor to a real design language the
   brief implies (e.g. the crisp restraint of Stripe/Linear/Vercel, an editorial look, a
   brutalist look) rather than a generic template. State the choice in one line.
2. **Write self-contained HTML** with inline `<style>` (no external build). Use a real
   type scale, intentional spacing, a small disciplined color palette, and a clear visual
   hierarchy. Responsive: mobile-first, looks right at 375px and scales up.
3. **For a deck**, one section/slide per screen; for a landing page, a strong hero →
   value → proof → CTA flow; for a prototype, make the key interactions actually work.
4. **Save it.** `fs_save` the `.html` under `/workspace` (works on the Mac or the cloud
   workspace — see `cloud-workspace`), then `artifact_save` it (kind `document`/`project`)
   so it lands in the boss's library with a virtual path. Offer 2–3 variants to compare
   when the direction is open.

## Avoid

- The AI tells: purple→blue gradients on everything, three identical feature cards, emoji
  bullets, `lorem ipsum`, centered everything. Make deliberate, opinionated choices.
- Don't ship broken layout — sanity-check at mobile width.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('html-artifact-design', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- diagram-as-code
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'diagram-as-code',
  'Produce diagrams as code/markup — architecture/flow/sequence as SVG or Mermaid, or hand-drawn-style Excalidraw JSON — and save them as artifacts. Uses fs_save (Mac or cloud workspace) + artifact_save. Use for "draw a diagram of", "architecture diagram", "flowchart".',
  'low', '["workspace:*"]'::jsonb,
  '["draw a diagram","architecture diagram","flowchart","sequence diagram","diagram of","sketch the flow","excalidraw","mermaid"]'::jsonb,
  '[{"name":"subject","type":"string","doc":"what to diagram"},{"name":"style","type":"string","doc":"svg | mermaid | excalidraw","required":false}]'::jsonb,
  '[{"name":"artifact","type":"string","doc":"saved diagram + path"}]'::jsonb,
  0.78, 'active', 'hermes_imported', 52,
  'Visual explanation on demand; pairs with planning/architecture work.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'diagram-as-code', 'v1.0-5-24-2026',
  $skill$---
name: diagram-as-code
version: "v1.0-5-24-2026"
description: Diagrams as code — SVG / Mermaid / Excalidraw JSON — saved as artifacts.
trigger_phrases:
  - draw a diagram
  - architecture diagram
  - flowchart
  - sequence diagram
  - mermaid
inputs:
  - name: subject
    type: string
  - name: style
    type: string
    required: false
outputs:
  - name: artifact
    type: string
risk_level: low
network_egress: workspace
confidence: 0.78
---

# Diagram as code

Pick the format by use:

- **Mermaid** — fastest for flowcharts/sequence/ER/state. Write a fenced `mermaid` block
  for inline rendering, or save a `.md`/`.html` with the Mermaid script for a standalone file.
- **SVG** — when you want full control of layout/typography/branding (architecture/infra
  diagrams). Dark-theme friendly. Hand-write clean SVG with a sane grid and labels.
- **Excalidraw JSON** — for a hand-drawn, informal look the boss can keep editing in
  Excalidraw.

1. **Map the entities + relationships first** (nodes, edges, direction). Get the logical
   structure right before styling.
2. **Render** in the chosen format; keep labels legible, spacing even, flow left→right or
   top→down consistently.
3. **Save.** `fs_save` under `/workspace` then `artifact_save` (kind `image`/`document`)
   so it's in the boss's library. For SVG, you can also convert to PNG via the workspace's
   tooling if a raster is needed.

Keep it accurate to the system being drawn — a pretty diagram that's wrong is worse than
an plain one that's right.
$skill$,
  '', 0.78, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('diagram-as-code', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
