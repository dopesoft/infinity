# app/fonts

`InstrumentSerif-Regular.woff2` / `InstrumentSerif-Italic.woff2` — the Majordomo
**display** register (docs/studio/MAJORDOMO.md §4, amended 2026-08-30). Used by
exactly two elements: the dashboard greeting and the daily quote.

Committed rather than pulled from Google Fonts at build time, for the same
reason the `geist` package is used for the sans: the production build must not
depend on a network round trip to fonts.googleapis.com, and the runtime must
never round-trip to fonts.gstatic.com.

These are Google's **latin** subset files (U+0000-00FF plus the U+2000-206F
punctuation block, which is where the curly quotes live). Full coverage would
need the latin-ext subset too, and `next/font/local` has no per-file
`unicode-range`, so registering both would let the wrong file win. The two
consumers are an English greeting and an English quote corpus, so latin is the
correct trade; a glyph outside it falls back to Georgia for that character
only.

SIL Open Font License 1.1 — see OFL.txt. Source:
https://github.com/Instrument/instrument-serif
