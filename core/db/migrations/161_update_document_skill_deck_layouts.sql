-- 161_update_document_skill_deck_layouts.sql
--
-- Teach the make-document skill the new THEMED, multi-layout deck engine
-- (render_pptx.js). The deck renderer now applies a default brand theme (logo,
-- palette, title chrome) and renders a vocabulary of slide LAYOUTS — cover,
-- agenda, section, bullets, stats, statement, number_grid, timeline, metrics,
-- team, profile, chart, thankyou. The boss's ask: "intelligently put together
-- the slides based on what type of data is being rendered by choosing the
-- proper slide type."
--
-- Per Rule #1b this migration ships JUDGMENT only — *which* layout fits *which*
-- data, how to split content into slides, one idea per slide, don't fabricate
-- figures. The MECHANICS (theme, logo, marker bar, per-layout rendering,
-- graceful fallbacks, layout inference) live in render_pptx.js, so the deck
-- looks right even if the runtime LLM drops a line of this recipe.
--
-- NOTE: the renderer is baked into the workspace image (Dockerfile COPY
-- docgen/), so the new layouts only render after a workspace image rebuild +
-- redeploy. Until then the deployed renderer ignores `layout` and falls back to
-- title+bullets — a graceful degrade, not a break.
--
-- Ships v1.2 and repoints mem_skill_active so the live runtime serves it.
-- Verifiable (active_version changes) rather than a brittle in-place replace().

BEGIN;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'make-document',
  'v1.2-6-25-2026',
  $skill$---
name: make-document
version: "v1.2-6-25-2026"
description: Turn data or an ask into a real deliverable — Excel, Word/PDF report, themed PowerPoint deck, or Markdown — with document_create. Carries the judgment for structure and (for decks) for choosing the right slide layout per data shape. Every office doc renders inline in the boss's canvas column automatically.
trigger_phrases:
  - make me a spreadsheet
  - make me a report
  - make a powerpoint
  - make a deck
  - make a pdf
  - put this in a doc
  - export to excel
  - turn this into a spreadsheet
inputs:
  - name: format
    type: string
    doc: xlsx | docx | pptx | pdf | md — infer from the ask if not stated
  - name: data
    type: any
    doc: the content/data to put in the document
outputs:
  - name: filename
    type: string
  - name: artifact_id
    type: string
risk_level: low
network_egress: ""
confidence: 0.85
---

# Make a document

You're producing a real file the boss can open and download — a spreadsheet,
a report, a deck, a PDF. The rendering is done by the `document_create` tool
(it's loaded by default); your job is to assemble the right **content spec**
and pick the right format. You never write Office-format code or styling
yourself — that's exactly the jank `document_create` exists to prevent, and a
deck is themed for you automatically.

## 1. Pick the format

Infer from the ask if it isn't explicit:
- "spreadsheet", "excel", "leads list", "track", tabular data → **xlsx**
- "report", "write-up", "summary doc", "word doc" → **docx** (or **pdf** if
  they say PDF, or **md** if they just want it readable in chat/Studio)
- "deck", "slides", "presentation", "powerpoint" → **pptx**
- "just the markdown" / a quick readable artifact → **md**

Every office format (xlsx/docx/pptx) renders inline in the boss's canvas
column automatically — a spreadsheet previews exactly like a report does, and a
.pptx also gives a themed PDF preview — so pick the format that fits the data,
not the one that "shows" best.

## 2. Build the content spec

Shape `content` for the format (see document_create's schema for the exact
fields):

- **xlsx** — `{ "sheets": [ { "name", "columns": [...], "rows": [[...]] } ] }`.
  First row is a bold header from `columns`; `rows` is your data. Use clean
  column names ("Name", "Phone", "Rating", "Website"). One sheet unless the
  data clearly splits.
- **docx / pdf** — `{ "title", "subtitle", "blocks": [...] }` where blocks are
  `heading` (level 1-3), `paragraph`, `bullets` (items[]), and `table`
  (columns + rows). Open with a title, then sections. Put structured data in
  a `table` block, not prose — the renderer sizes table columns for you.
- **pptx** — a themed deck: `{ "title", "subtitle", "slides": [ { "layout", ...fields } ] }`.
  A top-level title/subtitle makes a branded cover. **See §3 for how to choose
  the layout for each slide** — this is the heart of a good deck.
- **md** — `{ "markdown": "# ...\n..." }`. Just clean markdown.

## 3. Decks — choose the layout that fits the data

A deck is **themed automatically** (brand logo, palette, title chrome) — you
NEVER describe colors, fonts, or styling. Your job is to split the content into
slides and, for each, pick the `layout` whose **data shape** matches. One idea
per slide; short lines.

Match the data to a layout (fields are skipped when absent):

- **cover** — the opening, or a major part-opener. `{title, subtitle, image?}`.
- **agenda** — a table of contents / list of sections. `{title, items:[...]}`.
- **section** — a divider announcing a new topic. `{title, image?}`.
- **statement** — ONE bold takeaway, objective, or quote and nothing else.
  `{eyebrow, text}`. Use sparingly, for impact.
- **bullets** — a plain list of points. `{title, tagline?, bullets:[...] | body, image?}`.
- **stats** — an offering/feature backed by metrics. `{title, tagline?, bullets:[...], best_for?, stats:[{value,label,caption}]}` (e.g. value "85%", "$126K").
- **number_grid** — 3-6 discrete, parallel points. `{title, items:[{title,text}]}`.
- **timeline** — a sequential PROCESS with ordered steps. `{title, steps:[{title,text}]}`.
- **metrics** — headline KPIs / dollar figures on their own. `{title, lead?, metrics:[{value,label,caption}]}`.
- **team** — several people. `{title, intro?, members:[{name,role,photo?}]}`.
- **profile** — one person in depth. `{role, name, bio, skills:[{label,pct}], photo?}`.
- **chart** — comparison/trend data over categories. `{title, lead?, chart:{type:"bar"|"line", categories:[...], series:[{name,values:[...]}]}}` — a real chart, not a screenshot.
- **thankyou** — the closing slide. `{title}`.

Choose by the data, not by decoration: a list of metrics is `metrics`/`stats`,
not bullets; a process is `timeline`, not a numbered list; a single punchy line
is `statement`, not a slide full of text. If a slide is just prose, it's
`bullets`. Don't force every layout into one deck — use what the content needs,
and lead with a cover, close with a thankyou.

Photos/images are optional: omit them and the renderer draws clean
placeholders. Never invent an image URL.

## 4. Generate

Call `document_create({ format, filename, content })`. Pick a clear,
kebab-case `filename` with the right extension (`frisco-plumbers.xlsx`,
`agent-factory.pptx`). You do NOT need to set `also_pdf` — every office file
(xlsx/docx/pptx) auto-renders inline in the boss's canvas column, so the
preview is handled for you.

## 5. Index it so it surfaces

After it's created, call `artifact_save` with `kind: "document"`, the
returned `path` as `storage_path`, `storage_kind: "workspace"`, a friendly
`name`, and a `virtual_path` (e.g. `decks/agent-factory.pptx`). This is
what makes it show up in the boss's artifacts and open in Studio.

## 6. Report

Tell the boss what you made in one line ("Made `agent-factory.pptx` — 14
themed slides: cover, agenda, three agent types with stat callouts, process
timeline, and team"). It is ALREADY open and rendering inline in his canvas
column — that tab IS the deliverable. **Never write a file path or a download
link in chat**: there is no public URL, so any link you invent will 404 (the
canvas tab and its Download button are the only way to get the file). Don't
paste the whole dataset back into chat either.

## Pairing with web-browsing

The killer flow: `web-browsing` scrapes (e.g. plumbers in Frisco), then
`make-document` turns the scraped rows into a spreadsheet or a deck. Collect
the data first (browse → extract), hold it, then build in one
`document_create` call.

## Hard rules

- **Real data only.** Never fabricate rows, phone numbers, figures, stats,
  metrics, or chart values to fill space. If you only found 12 leads, the
  sheet has 12 rows. A `stats`/`metrics`/`chart` slide uses numbers you
  actually have — missing a field → leave it out, don't invent it.
- **Match the requested format.** If they said spreadsheet, don't hand back
  markdown. If they said deck, make a .pptx.
- **One file per ask** unless they asked for several. Don't sprawl.
- **Decks: one idea per slide, layout chosen by data shape.** Don't dump a
  wall of text on a slide; split it, or pick `statement`.
- **Keep columns/headers clean and consistent** — this is the difference
  between a usable export and a mess.
- **The canvas tab is the deliverable, not a chat link.** Don't hand back a
  URL or path; the file opens itself.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint the active version so the live runtime serves v1.2.
UPDATE mem_skill_active
   SET active_version = 'v1.2-6-25-2026'
 WHERE skill_name = 'make-document';

COMMIT;
