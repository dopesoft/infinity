-- 160_update_document_skill_inline_render.sql
--
-- Sweep the make-document skill prose to match the inline-render MECHANIC
-- (Rule #1b). The v1.0 body told the model to set `also_pdf: true` "for decks
-- and Word docs" — it never mentioned spreadsheets, so every xlsx shipped with
-- no sibling PDF and fell back to a bare download card instead of rendering
-- inline in the boss's canvas column. The boss caught exactly this: "the
-- spreadsheet is supposed to show in our browser tabs much like the .docx…
-- it should show an inline render… that's the point of the entire browser
-- column."
--
-- The real fix is in code (core/internal/tools/docgen.go now defaults
-- also_pdf=true for every office format, so the inline render is guaranteed),
-- and the docx table/typography fix is in docker/workspace/docgen/render_docx.js.
-- This migration just stops the skill prose from CONTRADICTING that mechanic
-- and the prose no longer leans on a droppable per-format flag:
--   • §1/§3 — drop the "set also_pdf for docs/decks" advice; note inline
--     preview is automatic for xlsx/docx/pptx.
--   • §5 — forbid hand-writing a download link/path in chat (there is no
--     public URL → it 404s, which is exactly what the boss saw). The canvas
--     tab IS the deliverable.
--
-- Ships a new version (v1.1) and repoints mem_skill_active so the live runtime
-- picks it up. Verifiable (active_version changes) rather than a brittle
-- in-place replace() that could silently no-op.

BEGIN;

INSERT INTO mem_skill_versions
  (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'make-document',
  'v1.1-6-25-2026',
  $skill$---
name: make-document
version: "v1.1-6-25-2026"
description: Turn data or an ask into a real deliverable — Excel, Word/PDF report, PowerPoint deck, or Markdown — with document_create. Carries the judgment for structure; pairs with web-browsing. Every office doc renders inline in the boss's canvas column automatically.
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

Every office format (xlsx/docx/pptx) renders inline in the boss's canvas
column automatically — a spreadsheet previews exactly like a report does — so
pick the format that fits the data, not the one that "shows" best.

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
- **pptx** — `{ "title", "subtitle", "slides": [ { "title", "bullets":[...] } ] }`.
  One idea per slide, 3-6 bullets max, short lines. Lead with a title slide.
- **md** — `{ "markdown": "# ...\n..." }`. Just clean markdown.

## 3. Generate

Call `document_create({ format, filename, content })`. Pick a clear,
kebab-case `filename` with the right extension (`frisco-plumbers.xlsx`,
`q2-report.docx`). You do NOT need to set `also_pdf` — every office file
(xlsx/docx/pptx) auto-renders inline in the boss's canvas column, so the
preview is handled for you.

## 4. Index it so it surfaces

After it's created, call `artifact_save` with `kind: "document"`, the
returned `path` as `storage_path`, `storage_kind: "workspace"`, a friendly
`name`, and a `virtual_path` (e.g. `reports/frisco-plumbers.xlsx`). This is
what makes it show up in the boss's artifacts and open in Studio.

## 5. Report

Tell the boss what you made in one line ("Made `frisco-plumbers.xlsx` — 23
leads with name, phone, rating, and website"). It is ALREADY open and
rendering inline in his canvas column — that tab IS the deliverable. **Never
write a file path or a download link in chat**: there is no public URL, so any
link you invent will 404 (the canvas tab and its Download button are the only
way to get the file). Don't paste the whole dataset back into chat either.

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
- **The canvas tab is the deliverable, not a chat link.** Don't hand back a
  URL or path; the file opens itself.
$skill$,
  '',
  0.85,
  'manual'
)
ON CONFLICT (skill_name, version) DO NOTHING;

-- Repoint the active version so the live runtime serves v1.1.
UPDATE mem_skill_active
   SET active_version = 'v1.1-6-25-2026'
 WHERE skill_name = 'make-document';

COMMIT;
