# Majordomo, the Studio design language

Approved 2026-08-28 from the mockup at
https://claude.ai/code/artifact/f4d6500f-371a-48ca-9784-65dac5adc6b4 (the
mockup is the visual source of truth; this file is the buildable contract).

A majordomo runs the house without being seen. Jarvis speaks in one register
and the machine in another, his work reads as a quiet ledger, and every page
is a title, one line of context, then the thing itself. No boxes inside boxes.

This document is the contract for every Studio surface. It sits on top of the
rules in `CLAUDE.md` (reuse-first componentization, mobile-first, no inline
styles, server-tracked progress) and never overrides them.

## 1. Principles

1. **Voice, chrome, data.** Jarvis's words are set larger and lighter in ink
   (`font-voice`). The interface is medium weight, one size down (`font-sans`).
   Data is mono (`font-mono`). One family (Geist) carries voice and chrome; the
   difference is size, weight, and colour, never a second typeface.
2. **Tone, not boxes.** Sections separate by ground, not by chrome. A section
   sits either on the page (`plain`, hairline under its title), on a quiet
   full-width band of `muted` (`band`, the "web style" alternation that keeps a
   long page from reading as one wall of text), or, when it is one object you
   act on, in a card (`card`: tinted or 1px bordered, radius 16). **One level
   of tone.** Never a card inside a card or a card on a band, never a header
   bar inside a card, never bordered list rows, never a bordered code block
   inside a section, never a bordered empty state. Cards are fine; stacked
   cards with header bars are the vibe-coded look this replaces.
3. **One title per surface.** A page has one title and one line of context. A
   section has a title and a count. Nothing repeats what the row above said.
4. **One alive signal.** Emerald (`brand`) marks the one thing happening right
   now. Amber (`warning`) marks the thing waiting on the boss. Red (`danger`)
   marks the thing that broke. Everything else is grey.
5. **Descriptions are decision aids, not furniture.** Keep a grey sentence on a
   setting row (what turning it on does), an empty state (what will fill it),
   an approval (why he is asking). Cut it under page titles, under section
   titles, in nav rails, inside eyebrows, anywhere it restates the title.
6. **Eyebrows** (mono, uppercase, letter-spaced) label a group of rows or a
   modal section. Never above a title.

## 2. The depth rule

Every surface is at most three levels deep: **surface → sections → rows**.

- **A page**: `PageHeader` (title, one meta line, actions) → `Section` (title
  + count on a hairline) → rows (`ListRow`, `WorkRow`, etc.). A tap opens a
  modal or a detail; it never opens another box inside the row.
- **A modal**: header (title in the voice face, one grey context line, close)
  → labelled rows (`ModalSection`: mono label in a left column, content beside
  it, hairline between) → content. Only sections with data render. Footer:
  destructive/secondary left, one primary right.
- **Inset** (a tinted, borderless block, `bg-muted`, radius 10) is the only
  container allowed inside a row: opened detail, terminal, diff, quote, schema.

Definition of done for any page: zero bordered containers inside bordered
containers, one `<h1>`, and no horizontal scroll at 375px.

## 3. Tokens

Variable names in `studio/app/globals.css` stay exactly as they are so every
Tailwind class keeps working. Only the values change (warm neutrals, true black
stays). Convert these hex values to the existing `H S% L%` triplet format.

| Token | Light | Dark | Role |
|---|---|---|---|
| `--background` | `#fffefb` | `#000000` | page and device ground (true black in dark, house rule) |
| `--foreground` | `#1a1916` | `#f1eee7` | ink: primary text, primary button |
| `--muted` | `#f5f3ee` | `#0e0d0c` | inset ground (opened detail, terminal, quote, schema) |
| `--muted-foreground` | `#5f5b53` | `#a8a49b` | secondary text |
| `--foreground-quiet` (new) | `#918c82` | `#6f6b63` | meta, timestamps, resting glyphs |
| `--card` | `#fffefb` | `#0e0d0c` | the rare real card (modal, proposal) |
| `--popover` | `#fffefb` | `#0a0a09` | popovers, drawers |
| `--border` / `--input` | `#e4e1d9` | `#262523` | hairline |
| `--hairline` (new) | `#edebe5` | `#171615` | the fainter hairline between rows |
| `--accent` | `#e9e6df` | `#1a1917` | hover / active row ground |
| `--secondary` | `#f5f3ee` | `#1a1917` | secondary button ground |
| `--primary` / `--primary-foreground` | ink / ground | ink / ground | unchanged semantics |
| `--ring` | `#918c82` | `#6f6b63` | focus ring |
| `--brand` | `#1f9660` | `#35c98a` | alive |
| `--info` | `#2a6fd6` | `#6a9df5` | informational |
| `--warning` | `#d98a0c` | `#f0a83a` | needs you |
| `--danger` | `#d93a3a` | `#ef6363` | failed |

Tailwind additions: `text-quiet` / `bg-quiet` → `hsl(var(--foreground-quiet))`,
`border-hairline` → `hsl(var(--hairline))`. Keep `--success` and the tier
palette as they are.

## 4. Type

- **Family:** Geist for voice and chrome, Geist Mono for data. Load with the
  `geist` npm package (`geist/font/sans`, `geist/font/mono`, Vercel's own
  package, works with next/font on Next 14). Read the package README before
  wiring it. Expose as CSS variables `--font-geist-sans`, `--font-geist-mono`.
- **Tailwind `fontFamily`:** `sans` → Geist, `voice` → Geist (same family; the
  utility exists so voice can be retuned later in one place), `mono` → Geist
  Mono. **Stop aliasing `mono` to the sans.**
- **Registers:**
  - Voice (`font-voice text-[15.5px] leading-[1.55] text-foreground`): Jarvis's
    replies, the working headline, thoughts, notes he saves, the reason behind
    an approval or a failure, page titles, section titles.
  - Chrome (`font-sans text-[13.5px] font-medium`): step verbs, labels, nav,
    buttons, the boss's own messages, meta.
  - Data (`font-mono text-[12px] tabular-nums`): commands, diffs, ids, schemas,
    raw JSON, group labels.
- **Scale:** 12 meta · 13.5 rows/buttons · 15 body · 15.5 voice · 18 section
  title (`font-voice font-medium tracking-tight`) · 26 page title
  (`font-voice font-medium tracking-tight`).
- **Radius:** 8 chips/inputs/insets · 10 buttons/opened rows · 16 modals and
  proposals · pill for toggles and the model chip. `rounded-xl` everywhere is
  over.
- **Motion:** fades and the shimmer only. Rows open in place with no
  transition; chevrons rotate in 150ms. `prefers-reduced-motion` turns the
  shimmer into plain ink.

## 5. Primitives (build these once, compose everywhere)

Every shape below lives in `studio/components/ui/` (or `components/chat/` for
the ledger) and is the only way that shape appears. A consumer that hand-rolls
one of these is a bug.

| Primitive | File | Contract |
|---|---|---|
| `PageHeader` | `ui/page-header.tsx` | `{ title, meta?: ReactNode, actions?: ReactNode, live?: boolean }`. Renders the one `<h1>` (voice, 26), a 12.5px quiet meta line, actions right-aligned. `live` adds the pulsing brand dot before the title. Every route uses it. |
| `Section` | `dashboard/Section.tsx` | Existing export, new prop `tone?: "plain" \| "band" \| "card"` (default `plain`). `plain`: title row (`SectionTitle`: voice 18 + count/meta in quiet + optional link) on a hairline, then children, no container. `band`: the same on a full-bleed `bg-muted` band with its own vertical padding (negative horizontal margins so it runs edge to edge of the page column). `card`: a radius-16 `bg-card border` container for one actionable object (a reflection, a proposal). Never nest tones. Keep `Icon`, `badge`, `action`, `headerExtra`, `noPad` props working. The dashboard should alternate `plain` / `band` down the page so neighbouring sections read as distinct areas without any inner chrome. |
| `ListRow` | `ui/list-row.tsx` | `{ leading?: ReactNode (7px status dot by tone or glyph), title, meta?, trailing?, onClick?, tone? }`. Hairline-separated, `min-h-11`, hover `bg-accent/60`. Title in voice when the row is something Jarvis said, chrome otherwise (`voice?: boolean`). |
| `GroupLabel` | `ui/list-row.tsx` | `{ label, count? }` mono uppercase quiet row used to group rows (replaces Kanban columns and rail sections). |
| `WorkRow` | `ui/list-row.tsx` | kind eyebrow + title + status chip + optional progress bar (brand). Used by the Agent Work board and the work-item modal. |
| `Inset` | `ui/inset.tsx` | `{ variant?: "plain" \| "terminal" \| "diff" \| "quote" \| "kv" \| "schema" }`. **API shape, read this before using it:** `diff`, `kv`, `terminal` and `schema` are DATA-driven — pass `text` / `items` / `command` / `fields`, not children. `plain` and `quote` are children-driven. Passing children to a data-driven variant renders a silently empty block. Tinted `bg-muted` radius-10 block, never bordered. Owns `min-w-0 max-w-full overflow-wrap:anywhere`. `terminal` renders `$ cmd` lines + trimmed output with "Show all"; `diff` reuses the existing `DiffPre` line tinting; `quote` sets voice with curly quotes; `kv` is a `dt/dd` grid; `schema` is a mono table of fields. |
| `TileCard` | `dashboard/Section.tsx` | Keep exported for existing consumers but implement as `ListRow` styling (hairline, no border, no icon tile). |
| `MetricRow` | `ui/metric-row.tsx` | Replaces `MetricCard` boxes on /memory: label + tabular number on a hairline. |

## 6. The activity ledger (Live tab)

Replaces the folded stack of `ToolCallCard`s with one quiet ledger per turn.

- `studio/lib/chat/activity.ts` (pure, tested, no React):
  - `describeStep(call, result?) → { verb, verbPast, glyph, meta, kind }`. One
    table maps tool ids to plain-English verbs, present and past tense, a
    Lucide glyph, the input field that becomes the meta, and a detail `kind`
    (`kv | terminal | diff | note | thought | search | files | plan | goal |
    commit | approval | failure`). Unknown tools humanize `server__VERB` to
    "Server · verb". Never show a raw tool id in a row.
  - `coalesce(messages) → ActivityItem[]`: consecutive calls with the same
    verb fold into one item with a count ("Ran 3 commands", "Read 5 files",
    "Edited 2 files"). A different verb, a failure, an approval, or a stop
    breaks the run.
  - `headlineFor(items, plan?) → string`: first sentence of the turn's interim
    narration, else the plan's current step, else "Working".
  - `summaryFor(items) → string`: "Worked for 2m 14s · revised the plan, ran 3
    commands, edited 2 files" (duration + the three most telling verbs).
- `components/chat/ActivityLedger.tsx` replaces `TurnWorkBlock` (keep the file
  name exporting the new component so `ConversationStream` changes minimally).
  Owns: headline row (pulsing brand dot + shimmer in the voice face + count and
  elapsed right, tabular), the faint left rail, the settled summary row, the
  live → settled fold (auto-collapse when the turn settles unless the boss
  touched it). Absorbs `WorkingIndicator` (still rendered standalone when a
  turn has no steps yet).
- `components/chat/ActivityStep.tsx` replaces `ToolCallCard` and
  `ThinkingBlock` rendering. One line: 18px glyph, verb (chrome), meta (quiet,
  truncating, tabular), chevron. States: `running` (brand spinner, the only
  spinner on screen), `done` (static glyph, no green check), `error` (red glyph,
  opens itself, explanation in the voice face), `approval` (amber lock, opens
  itself, Approve / Deny inline, the reason in the voice face), `stopped` (ban
  glyph, quiet). Detail opens in an `Inset` by `kind`; "Raw" is the last link in
  the detail footer and reveals the JSON in place.
- **Preserve every behaviour of the current cards**: awaiting-approval flow
  and `contract_id`, `BLOCKED:` gated detection and the Trust link, code-write
  live preview (a running code write opens itself and streams the diff), the
  `delegate` label, `interrupted` / stopped state, `useNow` live timers, the
  hydration rules, `data-message` attributes the scroller relies on, and the
  decision cards that never fold (`PlanProposalCard`, `SkillProposalCard`,
  `AgentTeamCard`).
- Jarvis's reply bubble (`ChatBubble`, assistant role) takes the voice face.
  The plan dock gains one line naming the current step under the bar. The
  composer placeholder becomes "Ask me anything".

## 7. Modal anatomy

- `ResponsiveModal` default header becomes: title (voice, 19, medium) + one
  quiet context line (`description`) + close. No icon chip, no eyebrow.
  `ResponsiveModalHeader` keeps its export but drops eyebrow/subtitle chrome
  in favour of the same title + context line (props stay accepted so consumers
  compile; unused ones are ignored with a code comment saying why).
- `ModalSection` becomes a labelled row: mono uppercase label in a 92px left
  column, content on the right, hairline between sections, **no border**. Same
  prop names. `ModalDiff`, `ModalHtml`, `ModalPre`, `ModalCode` render inside
  `Inset`. `ModalChips` becomes plain comma-separated text unless the chips are
  interactive.
- Footer: `justify-between`; secondary/destructive left, primary right, one
  primary.
- `ObjectViewer` sweep: only sections with data render; plan steps render with
  `ActivityStep`; the eyebrow + title header collapses to title + context line
  for every kind.

## 8. Page sweeps (in order)

Home → Settings (Tools, Connectors, Chat) → Skills → Memory → Work → Cron.
Each page: `PageHeader`, sections alternating `plain` and `band` tone down the
page (a `card` only where a section is one object you act on), group labels instead of
columns, insets instead of code boxes, descriptions only where §1.5 keeps
them, chip rows (`PageTabsList scrollable`) instead of stacked tab strips,
candidates and similar side rails folded into the main list as a labelled
group. Check at 375 / 768 / 1280 in both themes before moving to the next page.

## 9. Build phases

| Phase | Scope | Done when |
|---|---|---|
| 1 | Tokens (§3) + type (§4) + primitives (§5) | Both themes render, `font-mono` is mono, primitives exist with stories in a scratch page or tests, typecheck + lint + build pass |
| 2 | Activity ledger (§6) | A turn with 7+ tool calls reads as one headline and under 8 lines; no raw id or JSON without a tap; approval row still approves; tests for `activity.ts` |
| 3 | Modal anatomy (§7) | Work-item viewer shows one bordered container (the modal); every ObjectViewer kind opens without an empty section |
| 4 | Page sweeps (§8) | Every route passes the depth rule; no horizontal scroll at 375px |
| 5 | Review | code-quality pass; no hand-rolled copies of any primitive in §5 |
