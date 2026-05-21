-- 061_seed_document_skill.sql
--
-- Seed the `make-document` default skill — the cognition for turning data
-- and asks into real deliverables: spreadsheets, reports, decks, PDFs. The
-- rendering is a generic building block (the document_create tool → the
-- workspace bridge's baked helpers: openpyxl, docx-js, pptxgenjs,
-- reportlab — modeling Claude's file-ops skill stack). THIS skill carries
-- the judgment: what columns a leads sheet needs, how to structure a
-- report, when to also render a PDF, and how to surface the result.
--
-- Why a skill, not Go (Rule #1): document_create is deterministic
-- substrate. What goes IN the document is judgment that evolves via
-- Voyager/GEPA — so it lives here, not in a Go const.
--
-- Pairs with web-browsing: scrape leads → make a spreadsheet, in one flow.
-- Idempotent: ON CONFLICT DO NOTHING on all three skill tables.

BEGIN;

INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source)
VALUES (
  'make-document',
  'Turn data or an ask into a real file the boss can open: Excel spreadsheets, Word/PDF reports, PowerPoint decks, or Markdown. Uses the document_create tool (renders via the workspace bridge''s baked helpers). Knows what columns a leads sheet needs, how to structure a report, and when to also render a PDF. Pairs with web-browsing to turn scraped results into a deliverable.',
  'low',
  '[]'::jsonb,
  '["make me a spreadsheet","make a spreadsheet","build a spreadsheet","export to excel","put it in a spreadsheet","make me a report","write a report","generate a report","make a pdf","make me a deck","make a powerpoint","build a presentation","create a slide deck","put this in a doc","make a word doc","turn this into a spreadsheet","save this as a document"]'::jsonb,
  '[{"name":"format","type":"string","doc":"xlsx | docx | pptx | pdf | md — inferred from the ask if not given"},{"name":"data","type":"any","doc":"the content/data to put in the document"}]'::jsonb,
  '[{"name":"filename","type":"string","doc":"the file produced"},{"name":"artifact_id","type":"string","doc":"the indexed artifact"}]'::jsonb,
  0.85,
  'active',
  'manual'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'make-document',
  'v1.0-5-21-2026',
  $skill$---
name: make-document
version: "v1.0-5-21-2026"
description: Turn data or an ask into a real deliverable — Excel, Word/PDF report, PowerPoint deck, or Markdown — with document_create. Carries the judgment for structure; pairs with web-browsing.
trigger_phrases:
  - make me a spreadsheet
  - make me a report
  - make a powerpoint
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
and pick the right format. You never write Office-format code yourself —
that's exactly the jank `document_create` exists to prevent.

## 1. Pick the format

Infer from the ask if it isn't explicit:
- "spreadsheet", "excel", "leads list", "track", tabular data → **xlsx**
- "report", "write-up", "summary doc", "word doc" → **docx** (or **pdf** if
  they say PDF, or **md** if they just want it readable in chat/Studio)
- "deck", "slides", "presentation", "powerpoint" → **pptx**
- "just the markdown" / a quick readable artifact → **md**

When unsure between a Word doc and a PDF, make the **docx** and set
`also_pdf: true` so they get both.

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
  a `table` block, not prose.
- **pptx** — `{ "title", "subtitle", "slides": [ { "title", "bullets":[...] } ] }`.
  One idea per slide, 3-6 bullets max, short lines. Lead with a title slide.
- **md** — `{ "markdown": "# ...\n..." }`. Just clean markdown.

## 3. Generate

Call `document_create({ format, filename, content, also_pdf? })`. Pick a
clear, kebab-case `filename` with the right extension
(`frisco-plumbers.xlsx`, `q2-report.docx`). Set `also_pdf: true` for decks
and Word docs the boss will read so a PDF preview is ready.

## 4. Index it so it surfaces

After it's created, call `artifact_save` with `kind: "document"`, the
returned `path` as `storage_path`, `storage_kind: "workspace"`, a friendly
`name`, and a `virtual_path` (e.g. `reports/frisco-plumbers.xlsx`). This is
what makes it show up in the boss's artifacts and open in Studio.

## 5. Report

Tell the boss what you made in one line ("Made `frisco-plumbers.xlsx` — 23
leads with name, phone, rating, and website") — they'll open/download it in
Studio. Don't paste the whole dataset back into chat; the file IS the
deliverable.

## Pairing with web-browsing

The killer flow: `web-browsing` scrapes (e.g. plumbers in Frisco), then
`make-document` turns the scraped rows into a spreadsheet. Collect the data
first (browse → extract), hold it, then build the xlsx in one
`document_create` call.

## Hard rules

- **Real data only.** Never fabricate rows, phone numbers, or figures to
  fill a sheet. If you only found 12 leads, the sheet has 12 rows. Missing a
  field → leave the cell blank, don't invent it.
- **Match the requested format.** If they said spreadsheet, don't hand back
  markdown. If they said deck, make a .pptx.
- **One file per ask** unless they asked for several. Don't sprawl.
- **Keep columns/headers clean and consistent** — this is the difference
  between a usable export and a mess.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('make-document', 'v1.0-5-21-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
