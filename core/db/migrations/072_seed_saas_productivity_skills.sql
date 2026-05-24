-- 072_seed_saas_productivity_skills.sql
--
-- Adopt Hermes's productivity bundled skills as Infinity recipes over OUR
-- substrate. Hermes assumes vendor CLIs (gws, ntn, linear GraphQL curl); we
-- re-point onto Composio verbs (loaded on demand via tool_search → load_tools)
-- and, for document text extraction, the doc/OCR stack ALREADY baked into the
-- cloud workspace (pdftotext, tesseract, libreoffice — see `cloud-workspace`).
--
-- NOTE on PowerPoint: NOT seeded as a separate skill — the existing
-- `make-document` skill (migration 061) already builds .pptx via document_create.
-- Don't fork it.
--
-- Rule #1: cognition in SKILL.md, never a hardcoded vendor slug in Go. The
-- recipes tell the agent to SEARCH the live Composio catalog for the right
-- verb (toolkit verb names vary per connected account).
-- Idempotent on all three tables.

BEGIN;

-- ─────────────────────────────────────────────────────────────────────────
-- google-workspace
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'google-workspace',
  'Operate Google Workspace via Composio: send/search Gmail, read/create Calendar events, manage Drive files, and read/write Docs & Sheets. Complements inbox-triage (which classifies the inbox) — this is the general "do something in Google" skill. Picks the right connected Google account for multi-account setups.',
  'medium',
  '["composio:gmail","composio:googlecalendar","composio:googledrive"]'::jsonb,
  '["send an email","email so-and-so","check my calendar","schedule a meeting","create a calendar event","make a google doc","update the spreadsheet","find a file in drive","share the doc"]'::jsonb,
  '[{"name":"task","type":"string","doc":"the Google Workspace action to perform"},{"name":"account","type":"string","doc":"which Google account, if more than one","required":false}]'::jsonb,
  '[{"name":"result","type":"string","doc":"what was sent/created/changed + link"}]'::jsonb,
  0.85, 'active', 'hermes_imported', 74,
  'The boss lives in Google Workspace; this is everyday executive-assistant reach across Gmail/Calendar/Drive/Docs/Sheets.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'google-workspace', 'v1.0-5-24-2026',
  $skill$---
name: google-workspace
version: "v1.0-5-24-2026"
description: Gmail + Calendar + Drive + Docs + Sheets via Composio. The general "do something in Google" skill; complements inbox-triage.
trigger_phrases:
  - send an email
  - check my calendar
  - schedule a meeting
  - make a google doc
  - update the spreadsheet
  - find a file in drive
inputs:
  - name: task
    type: string
  - name: account
    type: string
    required: false
outputs:
  - name: result
    type: string
risk_level: medium
network_egress: composio:google
confidence: 0.85
---

# Google Workspace

The verbs are Composio's, loaded on demand (they're dormant to save context):

1. **Load the right verbs.** `tool_search` for the action — e.g.
   `tool_search("gmail send email")`, `tool_search("google calendar create event")`,
   `tool_search("google sheets append row")`, `tool_search("google drive find file")` —
   then `load_tools([...])`. Don't guess slugs; the catalog has the exact names.
2. **Pick the account.** If more than one Google account is connected (the boss has
   several Gmails), resolve the right `connected_account_id` and `entity`/`user_id` for
   the task — see the `resolve-connector-identities` skill and the multi-account overlay.
   Composio execute needs both, or it errors.
3. **Do the task:**
   - *Gmail*: search/read with the list/search verb; to send, draft first for anything
     non-trivial. **Sending email is an external action — show the boss the exact
     recipient + subject + body and get a yes before send**, unless he said "just send."
   - *Calendar*: read availability before scheduling; create with attendees, time zone,
     and a clear title; check for conflicts.
   - *Drive*: find by name/query; respect sharing — don't change permissions without asking.
   - *Docs/Sheets*: read the current state before writing; for Sheets, append/update by
     range rather than clobbering.
4. **Report** what changed with a link.

## Rules

- External sends and calendar invites go to real people — confirm before firing unless
  told otherwise. Drafting is always safe.
- Never change file/calendar sharing or delete anything without explicit approval.
- For pure inbox triage (classify + surface follow-ups), use `inbox-triage` instead.
$skill$,
  '', 0.85, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('google-workspace', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- notion
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'notion',
  'Work with Notion via Composio: create/read/update pages and database rows, append markdown content, query databases. Use to capture notes, update a tracker, or pull info out of Notion.',
  'medium',
  '["composio:notion"]'::jsonb,
  '["add to notion","create a notion page","update notion","notion database","log this in notion","find in notion"]'::jsonb,
  '[{"name":"task","type":"string","doc":"the Notion action"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"page/row link + summary"}]'::jsonb,
  0.82, 'active', 'hermes_imported', 60,
  'Notion is a common knowledge/tracker home; lets Jarvis read and write it directly.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'notion', 'v1.0-5-24-2026',
  $skill$---
name: notion
version: "v1.0-5-24-2026"
description: Notion pages + databases via Composio — create/read/update pages, append markdown, query databases.
trigger_phrases:
  - add to notion
  - create a notion page
  - update notion
  - notion database
inputs:
  - name: task
    type: string
outputs:
  - name: result
    type: string
risk_level: medium
network_egress: composio:notion
confidence: 0.82
---

# Notion

1. **Load verbs.** `tool_search("notion create page")` /
   `tool_search("notion query database")` / `tool_search("notion append block")` →
   `load_tools([...])`.
2. **Find the target.** Search for the parent page or database first; resolve its id.
   Don't create a duplicate page when one exists — update it.
3. **Write:** create pages/rows with proper title + properties; append content as
   markdown blocks. Match the database's existing property schema (don't invent
   properties).
4. **Read/query:** filter the database by the relevant property; return the rows the boss
   asked for, summarized.

Report the page/row URL. Don't archive/delete Notion content without approval.
$skill$,
  '', 0.82, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('notion', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- linear
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'linear',
  'Manage Linear via Composio: create/update issues, move them across states, set priority/assignee/labels, and query a team''s board. Use to file work, update status, or pull a backlog view.',
  'medium',
  '["composio:linear"]'::jsonb,
  '["create a linear issue","add to linear","update the linear ticket","move to in progress","linear backlog","assign in linear","what''s in my linear"]'::jsonb,
  '[{"name":"task","type":"string","doc":"the Linear action"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"issue link + summary"}]'::jsonb,
  0.82, 'active', 'hermes_imported', 60,
  'Linear is a common dev tracker; lets Jarvis file/update work items directly.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'linear', 'v1.0-5-24-2026',
  $skill$---
name: linear
version: "v1.0-5-24-2026"
description: Linear issues/projects via Composio — create/update issues, change state, set priority/assignee/labels, query the board.
trigger_phrases:
  - create a linear issue
  - update the linear ticket
  - move to in progress
  - linear backlog
inputs:
  - name: task
    type: string
outputs:
  - name: result
    type: string
risk_level: medium
network_egress: composio:linear
confidence: 0.82
---

# Linear

1. **Load verbs.** `tool_search("linear create issue")` /
   `tool_search("linear update issue state")` / `tool_search("linear list issues")` →
   `load_tools([...])`.
2. **Resolve the team/project** id first (verbs need it). Search existing issues to avoid
   duplicates.
3. **Create/update:** clear title + description; set priority, assignee, labels, and
   state. When moving state, use the team's real workflow states (don't invent names).
4. **Query:** filter by team/assignee/state; return a tidy list (id, title, state,
   priority).

Report the issue URL. Don't delete issues without approval.
$skill$,
  '', 0.82, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('linear', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- airtable
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'airtable',
  'Work with Airtable bases via Composio (or the REST API through http_fetch): list/filter records, create and update rows, and upsert. Use to read or maintain an Airtable tracker/CRM table.',
  'medium',
  '["composio:airtable","http_fetch"]'::jsonb,
  '["airtable","add a record to airtable","update airtable","read the airtable base","airtable table","upsert into airtable"]'::jsonb,
  '[{"name":"task","type":"string","doc":"the Airtable action"}]'::jsonb,
  '[{"name":"result","type":"string","doc":"records affected + summary"}]'::jsonb,
  0.8, 'active', 'hermes_imported', 56,
  'Airtable is a common lightweight CRM/tracker; lets Jarvis read and maintain it.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'airtable', 'v1.0-5-24-2026',
  $skill$---
name: airtable
version: "v1.0-5-24-2026"
description: Airtable records via Composio or the REST API — list/filter, create/update, upsert.
trigger_phrases:
  - airtable
  - add a record to airtable
  - update airtable
  - upsert into airtable
inputs:
  - name: task
    type: string
outputs:
  - name: result
    type: string
risk_level: medium
network_egress: composio:airtable
confidence: 0.8
---

# Airtable

Prefer Composio if Airtable is a connected toolkit; otherwise use the REST API via
`http_fetch` with the boss's Airtable token.

1. **Load verbs / set up the call.** `tool_search("airtable list records")` /
   `tool_search("airtable create record")` → `load_tools([...])`. For the REST path,
   the base id + table name + a Bearer token go to `api.airtable.com/v0/...`.
2. **Read:** list/filter records with a `filterByFormula` or field filter; return the
   fields asked for. Page through if the result set is large.
3. **Write:** create or update records matching the table's existing field schema. For
   upsert, look up by the key field first, then update-or-create.
4. **Report** how many records were read/created/updated and any record links.

Don't delete records or change the schema without approval.
$skill$,
  '', 0.8, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('airtable', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

-- ─────────────────────────────────────────────────────────────────────────
-- extract-document-text
-- ─────────────────────────────────────────────────────────────────────────
INSERT INTO mem_skills
  (name, description, risk_level, network_egress, trigger_phrases, inputs, outputs,
   confidence, status, source, importance, importance_reason)
VALUES (
  'extract-document-text',
  'Pull clean text out of PDFs, scans, and Office docs — including OCR for image-only/scanned pages. Uses the doc/OCR stack already baked into the cloud workspace (pdftotext, tesseract, libreoffice) via bash_run; no install needed. Use to read a document the boss hands over before summarizing or acting on it.',
  'low',
  '["workspace:*"]'::jsonb,
  '["read this pdf","extract text from","ocr this","get the text out of","what does this document say","scan this document","parse the pdf"]'::jsonb,
  '[{"name":"path","type":"string","doc":"path/URL to the document"}]'::jsonb,
  '[{"name":"text","type":"string","doc":"extracted text (markdown where structure matters)"}]'::jsonb,
  0.85, 'active', 'hermes_imported', 64,
  'Lets Jarvis actually read the documents the boss shares — including scans — using pre-baked cloud tooling.'
)
ON CONFLICT (name) DO NOTHING;

INSERT INTO mem_skill_versions (skill_name, version, skill_md, implementation, confidence, source)
VALUES (
  'extract-document-text', 'v1.0-5-24-2026',
  $skill$---
name: extract-document-text
version: "v1.0-5-24-2026"
description: Clean text out of PDFs/scans/Office docs incl. OCR, using the cloud workspace's pre-baked pdftotext/tesseract/libreoffice via bash_run.
trigger_phrases:
  - read this pdf
  - extract text from
  - ocr this
  - what does this document say
  - parse the pdf
inputs:
  - name: path
    type: string
outputs:
  - name: text
    type: string
risk_level: low
network_egress: workspace
confidence: 0.85
---

# Extract document text

Runs via `bash_run` on the cloud workspace, where the whole toolchain is **already
installed** — no provisioning needed (see `cloud-workspace`). Get the file into
`/workspace` first (`fs_save` from a paste, `http_fetch`/`curl` from a URL).

## Pick the tool by document type

- **Text-based PDF:** `pdftotext -layout <file> -` (poppler) → clean text preserving
  layout. Fast, lossless.
- **Scanned / image-only PDF or image:** OCR with `tesseract`. For a PDF, rasterize
  pages first (`pdftoppm -r 300 <file> page` → PNGs) then `tesseract page-N.png stdout`.
  Tesseract is pre-installed.
- **Office docs (.docx/.pptx/.xlsx/.odt):** convert with LibreOffice headless —
  `libreoffice --headless --convert-to txt --outdir /workspace/out <file>` (or
  `--convert-to pdf` then `pdftotext`).
- **Tables/spreadsheets:** keep structure — convert to CSV (`libreoffice --convert-to
  csv`) rather than flattening to prose.

## Then

- Return clean text; use markdown when structure (headings, tables) matters.
- For a long doc, extract fully but summarize up front — lead with what the boss needs.
- If a PDF is partly text + partly scanned, pdftotext the text pages and OCR the rest;
  don't silently drop the image pages.

`code_exec` with `pymupdf`/`pdfplumber` is an alternative for structured extraction, but
the workspace CLIs above are the default since they're pre-baked and handle OCR.
$skill$,
  '', 0.85, 'hermes_imported'
)
ON CONFLICT (skill_name, version) DO NOTHING;

INSERT INTO mem_skill_active (skill_name, active_version)
VALUES ('extract-document-text', 'v1.0-5-24-2026')
ON CONFLICT (skill_name) DO NOTHING;

COMMIT;
