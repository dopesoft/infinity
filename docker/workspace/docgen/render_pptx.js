#!/usr/bin/env node
// render_pptx.js — spec (stdin JSON) → .pptx (argv[2]) via pptxgenjs.
//
// A THEMED, MULTI-LAYOUT deck engine. This is a generic building block
// (Infinity Rule #1): it knows nothing about any specific deck. It applies a
// default brand THEME (the dopesoft palette + logo chrome + the signature
// two-tone marker bar) and renders a vocabulary of slide LAYOUTS, each a
// generic template keyed by `slide.layout`. The JUDGMENT of which layout fits
// which data lives in the make-document skill, not here — add a layout and
// every deck can use it; the agent assembles the deck by choosing layouts.
//
// 16:9 widescreen (LAYOUT_WIDE = 13.333 × 7.5in). Office files auto-render an
// inline themed PDF preview via LibreOffice (docgen `also_pdf`), so a themed
// .pptx is also the boss's themed PDF for free.
//
// Spec (all fields optional; the renderer degrades gracefully):
//   {
//     "theme": "dopesoft" | { primary, ink, ... },   // default "dopesoft"
//     "title": "...", "subtitle": "...",              // → a cover slide
//     "slides": [ { "layout": "<type>", ...fields } ]
//   }
//
// Layout vocabulary (see LAYOUTS below for the per-layout fields):
//   cover · agenda · section · bullets · stats · statement · number_grid ·
//   timeline · metrics · team · profile · chart · thankyou
//
// Backward compatible: an old `{ title, bullets, body, notes }` slide with no
// `layout` is rendered as a `bullets` slide; a layout is also inferred from
// whichever data field is present, so the agent forgetting `layout` still
// produces the right slide (Rule #1b — mechanics don't depend on the model
// remembering prose).

const fs = require("fs");
const path = require("path");
const PptxGenJS = require("pptxgenjs");

// ── geometry (inches, 16:9 widescreen) ──────────────────────────────────────
const W = 13.333;
const H = 7.5;
const MX = 0.7;            // left/right margin
const CONTENT_W = W - MX * 2;
const HEAD_Y = 0.34;       // header chrome baseline
const BODY_TOP = 1.55;     // where a slide's title sits below the header

// ── themes ───────────────────────────────────────────────────────────────────
// A theme is a flat token bag. "dopesoft" is the default; a spec may pass a
// string key OR an object that is merged over the default, so a one-off deck
// can recolor without a code change.
// To ADD a theme: drop a new entry in this bag with the same token keys. The
// agent picks one via the spec's top-level `theme` ("midnight", "emerald", …)
// or by passing a partial object that merges over dopesoft (e.g.
// {"theme":{"primary":"FF5A1F"}}) — see resolveTheme. Every layout reads ONLY
// these tokens (no hardcoded colors), so a new theme recolors the whole deck.
const THEMES = {
  dopesoft: {
    dark: false,            // dark-mode theme? (drives logo + slide backgrounds)
    primary: "1E8CFF",      // brand azure — accents, stat numbers, marker, links
    primaryLight: "A9D2FF", // marker second segment / light fills
    ink: "1A1A1A",          // near-black headings / dark logo
    body: "5A5A5A",         // gray body copy
    muted: "9AA0A6",        // eyebrows, captions, labels
    slideBg: "FFFFFF",      // content-slide background (white in light themes)
    lightBg: "ECEFF1",      // cover / section / thankyou cool-gray field
    darkBg: "4D4D4D",       // statement-slide charcoal
    panel: "F4F6F8",        // soft card fill
    rule: "D9DEE2",         // hairlines / dotted dividers
    badge: "4D4D4D",        // numbered-circle fill
    barTrack: "ECEFF1",     // skill-bar track
    barFill: "5B9BD5",      // skill-bar fill (desaturated brand blue)
    accent2: "1B9E8F",      // secondary chart series
    headFont: "Calibri",    // Carlito twin baked in the workspace image
    bodyFont: "Calibri",
    logoLight: "dopesoft-black.png", // logo used ON light backgrounds
    logoDark: "dopesoft-white.png",  // logo used ON dark backgrounds
  },
  // ── light recolors: same chrome, different accent. Zero layout risk. ──
  emerald: { primary: "0F9D6A", primaryLight: "A7E8CE", barFill: "3DB587", accent2: "1E8CFF" },
  royal:   { primary: "4F46E5", primaryLight: "C7C4F5", barFill: "7C75ED", accent2: "F59E0B" },
  crimson: { primary: "DC2626", primaryLight: "F6B9B9", barFill: "E06A6A", accent2: "1F6FEB" },
  amber:   { primary: "D97706", primaryLight: "F6D8A8", barFill: "E0A24A", accent2: "1B9E8F" },
  slate:   { primary: "475569", primaryLight: "CBD5E1", barFill: "7689A1", accent2: "0EA5E9", lightBg: "F1F5F9" },
  // ── a full dark theme: dark fields + light ink + white logo. ──
  midnight: {
    dark: true,
    primary: "4FA3FF", primaryLight: "2C5C99",
    ink: "FFFFFF", body: "C7CDD6", muted: "8A93A3",
    slideBg: "0B0E14", lightBg: "11151D", darkBg: "0B0E14",
    panel: "1A2030", rule: "2A3142", badge: "4FA3FF",
    barTrack: "1A2030", barFill: "4FA3FF", accent2: "1FB6A6",
  },
};

function resolveTheme(spec) {
  const base = THEMES.dopesoft;
  const t = spec && spec.theme;
  if (!t) return { ...base };
  // a named theme is merged OVER dopesoft so partial entries (the light
  // recolors above) inherit every unspecified token.
  if (typeof t === "string") return { ...base, ...(THEMES[t] || {}) };
  if (typeof t === "object") return { ...base, ...t }; // object overrides merge
  return { ...base };
}

// Document title for core metadata — the native doc's filename (no extension),
// e.g. "dfw-business-opportunity-deck". This is what the boss sees as the file
// name, and what the PDF preview must show — NOT pptxgenjs's "PptxGenJS
// Presentation" default that LibreOffice otherwise carries into the PDF title.
function docTitleFrom(outPath) {
  try { return path.basename(String(outPath), path.extname(String(outPath))) || "Presentation"; }
  catch (_) { return "Presentation"; }
}

// ── logo resolution ──────────────────────────────────────────────────────────
// Logos live in ./assets and are baked into the workspace image (Dockerfile
// COPY docgen/). dark=true picks the white logo for dark backgrounds. Returns a
// pptxgenjs image-source ({path}) or null → text wordmark fallback.
const ASSET_DIR = path.join(__dirname, "assets");
function logoSrc(t, dark) {
  const file = dark ? t.logoDark : t.logoLight;
  if (file) {
    const p = path.join(ASSET_DIR, file);
    if (fs.existsSync(p)) return { path: p };
  }
  return null;
}

// ── shared chrome ─────────────────────────────────────────────────────────────
// Logo top-left + a decorative hamburger top-right on every slide, matching the
// reference deck. Falls back to a styled text wordmark if the image is absent.
function addChrome(s, t, { dark = false } = {}) {
  const fg = dark ? "FFFFFF" : t.ink;
  const src = logoSrc(t, dark);
  if (src) {
    // logo art is 3:1 (3000×1000) — keep ratio inside a 1.5×0.5 box.
    s.addImage({ ...src, x: 0.5, y: HEAD_Y - 0.06, w: 1.5, h: 0.5, sizing: { type: "contain", w: 1.5, h: 0.5 } });
  } else {
    s.addText("dopesoft", { x: 0.5, y: HEAD_Y - 0.06, w: 3, h: 0.5, fontSize: 22, bold: true, italic: true, color: fg, fontFace: t.headFont, valign: "middle" });
  }
  // hamburger mark (decorative) — three short bars, top-right.
  const hb = dark ? "FFFFFF" : "B7BCC1";
  for (let i = 0; i < 3; i++) {
    s.addShape("rect", { x: 12.45, y: HEAD_Y + i * 0.1, w: 0.42, h: 0.03, fill: { color: hb }, line: { type: "none" } });
  }
}

// The signature two-tone marker bar (primary + light) above section/slide
// titles. Returns nothing; draws at (x,y).
function addMarker(s, t, x, y) {
  s.addShape("rect", { x, y, w: 0.55, h: 0.07, fill: { color: t.primary }, line: { type: "none" } });
  s.addShape("rect", { x: x + 0.55, y, w: 0.45, h: 0.07, fill: { color: t.primaryLight }, line: { type: "none" } });
}

// titleFit estimates a font size + box height for a heading so a long title
// wraps inside `box` inches instead of overflowing onto whatever sits below or
// beside it. ~1717/size chars fit per line at the bold head font in a 11.93"
// box; we scale that to the actual box width. Returns the chosen size, the
// estimated line count, and a box height that fits them.
function titleFit(text, { box = CONTENT_W, max = 32, mid, min } = {}) {
  const len = String(text || "").length;
  const cplAt = (sz) => Math.max(6, Math.floor((1717 / sz) * (box / CONTENT_W)));
  const sizes = [max, mid, min].filter((v) => typeof v === "number");
  let size = sizes[sizes.length - 1] || max;
  // pick the largest candidate size that keeps the title to ≤2 lines.
  for (const sz of sizes) { if (Math.ceil(len / cplAt(sz)) <= 2) { size = sz; break; } }
  const lines = Math.max(1, Math.ceil(len / cplAt(size)));
  const lineH = size * 0.0167; // ~inches per line
  return { size, lines, h: Math.max(0.8, lines * lineH + 0.1) };
}

// Marker + bold title — the standard slide header for content layouts.
function addTitle(s, t, text, { x = MX, y = BODY_TOP, w = CONTENT_W, color, size = 32 } = {}) {
  addMarker(s, t, x, y - 0.25);
  // auto-shrink + grow the box so a long title wraps in-place instead of
  // running under sibling content (the metrics/chart "lead" overlap bug).
  const fit = titleFit(text, { box: w, max: size, min: Math.max(20, size - 12) });
  s.addText(String(text || ""), { x, y, w, h: fit.h, fontSize: fit.size, bold: true, color: color || t.ink, fontFace: t.headFont, valign: "top", lineSpacingMultiple: 1.04 });
}

function addBullets(s, t, items, { x, y, w, h, fontSize = 16, color } = {}) {
  const runs = (items || []).map((b) => ({ text: String(b), options: { bullet: { indent: 18 }, breakLine: true } }));
  if (!runs.length) return;
  s.addText(runs, { x, y, w, h, fontSize, color: color || t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3, paraSpaceAfter: 6 });
}

// pptxgenjs image source from a path / URL / data-URI; null when absent.
function imgSrc(src) {
  if (!src || typeof src !== "string") return null;
  if (/^data:/.test(src)) return { data: src };
  return { path: src };
}

// A photo box that cover-fits an image, or a branded placeholder with initials.
function addPhoto(s, t, { x, y, w, h, src, label }) {
  const img = imgSrc(src);
  if (img) {
    try {
      s.addImage({ ...img, x, y, w, h, sizing: { type: "cover", w, h } });
      return;
    } catch (_) { /* fall through to placeholder */ }
  }
  s.addShape("rect", { x, y, w, h, fill: { color: t.panel }, line: { color: t.rule, width: 1 } });
  const initials = String(label || "")
    .split(/\s+/).filter(Boolean).slice(0, 2).map((p) => p[0] || "").join("").toUpperCase();
  s.addText(initials || "·", { x, y, w, h, fontSize: 40, bold: true, color: t.muted, fontFace: t.headFont, align: "center", valign: "middle" });
}

// Normalize a list of "stat" objects: accepts {value,label,caption} or strings.
function asStats(arr) {
  return (arr || []).map((v) => (typeof v === "object" && v ? v : { value: String(v) }));
}

// ── layouts ───────────────────────────────────────────────────────────────────
// Each renderer takes (pptx, t, sl) and draws one full slide. All fields are
// optional; missing fields are skipped, never crash.

function layoutCover(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.lightBg };
  addChrome(s, t, { dark: !!t.dark });
  // Optional hero image as a bottom band.
  const img = imgSrc(sl.image);
  if (img) {
    try { s.addImage({ ...img, x: 0, y: 4.6, w: W, h: H - 4.6, sizing: { type: "cover", w: W, h: H - 4.6 } }); } catch (_) {}
  }
  // Two-tone title — first word in brand blue, rest in ink (the "Agent Factory"
  // treatment), done generically so any deck title gets the brand look. The font
  // auto-shrinks for long titles and the subtitle is positioned BELOW the
  // measured title height, so a long cover title can never overlap the subtitle
  // (the bug on the DFW cover). See titleFit().
  const title = String(sl.title || "");
  const fit = titleFit(title, { box: CONTENT_W, max: 60, mid: 48, min: 40 });
  const titleY = 2.15;
  addMarker(s, t, MX, titleY - 0.25);
  const sp = title.indexOf(" ");
  const runs = sp > 0
    ? [{ text: title.slice(0, sp) + " ", options: { color: t.primary } }, { text: title.slice(sp + 1), options: { color: t.ink } }]
    : [{ text: title, options: { color: t.ink } }];
  s.addText(runs, { x: MX, y: titleY, w: CONTENT_W, h: fit.h, fontSize: fit.size, bold: true, fontFace: t.headFont, valign: "top", lineSpacingMultiple: 1.05 });
  if (sl.subtitle) {
    s.addText(String(sl.subtitle), { x: MX, y: titleY + fit.h + 0.12, w: CONTENT_W, h: 0.6, fontSize: 20, bold: true, color: t.body, fontFace: t.bodyFont });
  }
}

function layoutAgenda(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.lightBg };
  addChrome(s, t, { dark: !!t.dark });
  addTitle(s, t, sl.title || "agenda", { color: t.primary, size: 36 });
  const items = (sl.items || []).map((v, i) => (typeof v === "object" ? v : { text: String(v), n: i + 1 }));
  const mid = Math.ceil(items.length / 2);
  const cols = [items.slice(0, mid), items.slice(mid)];
  const colW = (CONTENT_W - 0.6) / 2;
  cols.forEach((col, ci) => {
    const runs = col.map((it, i) => {
      const n = it.n != null ? it.n : ci * mid + i + 1;
      return { text: `${String(n).padStart(1, "0")}. ${it.text || it}`, options: { breakLine: true, paraSpaceAfter: 16 } };
    });
    if (runs.length) {
      s.addText(runs, { x: MX + ci * (colW + 0.6), y: 2.7, w: colW, h: 3.8, fontSize: 18, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.2 });
    }
  });
}

function layoutSection(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.lightBg };
  addChrome(s, t, { dark: !!t.dark });
  const img = imgSrc(sl.image);
  const hasImg = !!img;
  if (hasImg) {
    try { s.addImage({ ...img, x: W * 0.5, y: 0, w: W * 0.5, h: H, sizing: { type: "cover", w: W * 0.5, h: H } }); } catch (_) {}
  }
  const w = hasImg ? W * 0.5 - MX - 0.3 : CONTENT_W;
  addMarker(s, t, MX, 3.0);
  s.addText(String(sl.title || ""), { x: MX, y: 3.25, w, h: 2.2, fontSize: 46, bold: true, color: t.ink, fontFace: t.headFont, valign: "top", lineSpacingMultiple: 1.05 });
  if (sl.subtitle) s.addText(String(sl.subtitle), { x: MX, y: 5.4, w, h: 0.8, fontSize: 18, color: t.body, fontFace: t.bodyFont });
}

function layoutBullets(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  const img = imgSrc(sl.image);
  const hasImg = !!img;
  if (hasImg) {
    try { s.addImage({ ...img, x: W * 0.55, y: 1.7, w: W * 0.45 - 0.3, h: 4.6, sizing: { type: "cover", w: W * 0.45 - 0.3, h: 4.6 } }); } catch (_) {}
  }
  const w = hasImg ? W * 0.55 - MX - 0.3 : CONTENT_W;
  addTitle(s, t, sl.title, hasImg ? { w } : {}); // keep title clear of the side image
  let y = 2.55;
  if (sl.tagline) {
    s.addText(String(sl.tagline), { x: MX, y, w, h: 0.6, fontSize: 22, bold: true, italic: true, color: t.primary, fontFace: t.headFont });
    y += 0.9;
  }
  const items = sl.bullets || sl.items;
  if (items && items.length) {
    addBullets(s, t, items, { x: MX, y, w, h: 6.6 - y, fontSize: 17 });
  } else if (sl.body) {
    s.addText(String(sl.body), { x: MX, y, w, h: 6.6 - y, fontSize: 17, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3 });
  }
}

// Left: title + tagline + Features/bullets + "Best for". Right: stacked stat
// callouts. Matches the Voice/Chat agent pages.
function layoutStats(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  const stats = asStats(sl.stats);
  const hasStats = stats.length > 0;
  const leftW = hasStats ? 8.4 : CONTENT_W;
  addTitle(s, t, sl.title, { w: leftW }); // keep title clear of the right stat rail
  let y = 2.5;
  if (sl.tagline) {
    s.addText(String(sl.tagline), { x: MX, y, w: leftW, h: 0.6, fontSize: 22, bold: true, italic: true, color: t.primary, fontFace: t.headFont });
    y += 0.85;
  }
  const bullets = sl.bullets || sl.features;
  if (bullets && bullets.length) {
    s.addText("Features:", { x: MX, y, w: leftW, h: 0.35, fontSize: 14, bold: true, color: t.ink, fontFace: t.headFont });
    y += 0.45;
    addBullets(s, t, bullets, { x: MX, y, w: leftW, h: 2.6, fontSize: 15 });
    y += Math.min(2.6, bullets.length * 0.42 + 0.2);
  } else if (sl.body) {
    s.addText(String(sl.body), { x: MX, y, w: leftW, h: 2.6, fontSize: 15, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3 });
    y += 2.6;
  }
  if (sl.best_for || sl.bestFor) {
    s.addText("Best for:", { x: MX, y, w: leftW, h: 0.35, fontSize: 14, bold: true, color: t.ink, fontFace: t.headFont });
    s.addText(String(sl.best_for || sl.bestFor), { x: MX, y: y + 0.4, w: leftW, h: 1.0, fontSize: 14, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.25 });
  }
  // Right rail of stat callouts.
  if (hasStats) {
    const railX = 9.4, railW = W - railX - 0.5;
    const top = 1.4, bottom = 6.9;
    const block = (bottom - top) / stats.length;
    stats.forEach((st, i) => {
      const by = top + i * block;
      if (st.label) s.addText(String(st.label), { x: railX, y: by + 0.05, w: railW, h: 0.4, fontSize: 12, bold: true, color: t.muted, align: "center", fontFace: t.headFont });
      s.addText(String(st.value || ""), { x: railX, y: by + 0.4, w: railW, h: 0.9, fontSize: 44, bold: true, color: t.primary, align: "center", fontFace: t.headFont });
      if (st.caption) s.addText(String(st.caption), { x: railX, y: by + 1.3, w: railW, h: 0.8, fontSize: 11, color: t.body, align: "center", fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.15 });
      if (i < stats.length - 1) s.addShape("line", { x: railX + 0.3, y: by + block - 0.02, w: railW - 0.6, h: 0, line: { color: t.rule, width: 1, dashType: "dash" } });
    });
  }
}

// Dark slide: eyebrow + one huge statement. For a single bold takeaway / quote.
function layoutStatement(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.darkBg };
  addChrome(s, t, { dark: true });
  addMarker(s, t, MX, 2.2);
  if (sl.eyebrow) s.addText(String(sl.eyebrow), { x: MX, y: 2.4, w: CONTENT_W, h: 0.4, fontSize: 14, bold: true, color: "FFFFFF", fontFace: t.headFont });
  s.addText(String(sl.text || sl.title || ""), { x: MX, y: 2.95, w: CONTENT_W, h: 3.6, fontSize: 40, color: "FFFFFF", fontFace: t.headFont, valign: "top", lineSpacingMultiple: 1.15 });
}

// Numbered points with circle badges, 1 or 2 columns.
function layoutNumberGrid(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  addTitle(s, t, sl.title);
  const items = (sl.items || []).map((v, i) => (typeof v === "object" ? v : { text: String(v) }));
  const twoCol = items.length > 2;
  const cols = twoCol ? 2 : 1;
  const colW = (CONTENT_W - (cols - 1) * 0.8) / cols;
  const perCol = Math.ceil(items.length / cols);
  const rowH = Math.min(1.9, (6.7 - 2.6) / perCol);
  items.forEach((it, i) => {
    const c = Math.floor(i / perCol), r = i % perCol;
    const x = MX + c * (colW + 0.8);
    const y = 2.7 + r * rowH;
    const n = it.n != null ? it.n : i + 1;
    s.addShape("ellipse", { x, y, w: 0.55, h: 0.55, fill: { color: t.badge }, line: { type: "none" } });
    s.addText(String(n), { x, y, w: 0.55, h: 0.55, fontSize: 16, bold: true, color: "FFFFFF", align: "center", valign: "middle", fontFace: t.headFont });
    if (it.title) s.addText(String(it.title), { x: x + 0.75, y: y - 0.02, w: colW - 0.75, h: 0.4, fontSize: 15, bold: true, color: t.ink, fontFace: t.headFont });
    s.addText(String(it.text || ""), { x: x + 0.75, y: y + (it.title ? 0.38 : 0), w: colW - 0.75, h: rowH - 0.4, fontSize: 13, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.2 });
  });
}

// Horizontal process timeline; steps alternate above/below the line.
function layoutTimeline(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  addTitle(s, t, sl.title || "Process");
  const steps = (sl.steps || []).map((v, i) => (typeof v === "object" ? v : { title: `Step ${i + 1}`, text: String(v) }));
  const n = steps.length || 1;
  const lineY = 4.1;
  const x0 = MX + 0.2, x1 = W - MX - 0.2;
  s.addShape("line", { x: x0, y: lineY, w: x1 - x0, h: 0, line: { color: t.primaryLight, width: 2 } });
  const span = (x1 - x0) / n;
  steps.forEach((st, i) => {
    const cx = x0 + span * (i + 0.5);
    const above = i % 2 === 0;
    s.addShape("ellipse", { x: cx - 0.09, y: lineY - 0.09, w: 0.18, h: 0.18, fill: { color: t.ink }, line: { type: "none" } });
    s.addText(`Step ${st.n != null ? st.n : i + 1}`, { x: cx - span / 2, y: above ? lineY - 0.55 : lineY + 0.25, w: span, h: 0.4, fontSize: 18, bold: true, color: t.ink, align: "center", fontFace: t.headFont });
    const ty = above ? lineY - 2.3 : lineY + 0.7;
    if (st.title) s.addText(String(st.title), { x: cx - span / 2 + 0.1, y: ty, w: span - 0.2, h: 0.35, fontSize: 13, bold: true, color: t.ink, align: "center", fontFace: t.headFont });
    s.addText(String(st.text || ""), { x: cx - span / 2 + 0.1, y: ty + 0.38, w: span - 0.2, h: 1.5, fontSize: 11, color: t.body, align: "center", fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.15 });
  });
}

// Row of headline numbers with captions, dotted vertical dividers.
function layoutMetrics(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  // Title on the left, optional lead paragraph on the right — constrain the
  // title so it can't slide under the lead (the "Best Opportunity, Economics at
  // a Glance" / "The numbers are not bargain-basement…" overlap).
  const hasLead = !!sl.lead;
  addTitle(s, t, sl.title || "Deliverables", hasLead ? { w: 4.4 } : {});
  if (hasLead) s.addText(String(sl.lead), { x: 5.4, y: 1.55, w: W - 5.4 - MX, h: 1.6, fontSize: 14, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3 });
  const metrics = asStats(sl.metrics || sl.stats);
  const n = metrics.length || 1;
  const colW = CONTENT_W / n;
  metrics.forEach((m, i) => {
    const x = MX + i * colW;
    if (m.label) s.addText(String(m.label), { x, y: 3.7, w: colW, h: 0.4, fontSize: 13, bold: true, color: t.primary, align: "center", fontFace: t.headFont });
    s.addText(String(m.value || ""), { x, y: 4.05, w: colW, h: 1.1, fontSize: 56, bold: true, color: t.primary, align: "center", fontFace: t.headFont });
    if (m.caption) s.addText(String(m.caption), { x: x + 0.2, y: 5.25, w: colW - 0.4, h: 0.9, fontSize: 14, color: t.body, align: "center", fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.2 });
    if (i < n - 1) s.addShape("line", { x: x + colW, y: 3.8, w: 0, h: 2.0, line: { color: t.rule, width: 1, dashType: "dash" } });
  });
}

// Intro text + row of member photo cards with name/role overlay.
function layoutTeam(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  addTitle(s, t, sl.title || "Team", { w: 3.6 }); // keep title clear of the photo cards
  const members = sl.members || [];
  if (sl.intro) s.addText(String(sl.intro), { x: MX, y: 2.6, w: 3.4, h: 3, fontSize: 14, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3 });
  const gx = MX + 3.9, gw = W - gx - 0.5;
  const n = Math.max(members.length, 1);
  const cardW = (gw - (n - 1) * 0.2) / n;
  const cardH = 4.4, cy = 1.9;
  members.forEach((m, i) => {
    const x = gx + i * (cardW + 0.2);
    addPhoto(s, t, { x, y: cy, w: cardW, h: cardH, src: m.photo, label: m.name });
    // gradient-ish footer band for legibility
    s.addShape("rect", { x, y: cy + cardH - 1.0, w: cardW, h: 1.0, fill: { color: "000000", transparency: 55 }, line: { type: "none" } });
    if (m.role) s.addText(String(m.role), { x: x + 0.15, y: cy + cardH - 0.85, w: cardW - 0.3, h: 0.3, fontSize: 10, color: "FFFFFF", fontFace: t.headFont });
    if (m.name) s.addText(String(m.name), { x: x + 0.15, y: cy + cardH - 0.55, w: cardW - 0.3, h: 0.4, fontSize: 18, bold: true, color: "FFFFFF", fontFace: t.headFont });
  });
}

// Single person: role eyebrow + name + bio + skill bars; photo on the right.
function layoutProfile(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  addMarker(s, t, MX, 1.5);
  if (sl.role) s.addText(String(sl.role), { x: MX, y: 1.7, w: 6, h: 0.4, fontSize: 14, color: t.muted, fontFace: t.headFont });
  s.addText(String(sl.name || sl.title || ""), { x: MX, y: 2.1, w: 6, h: 0.9, fontSize: 40, bold: true, color: t.ink, fontFace: t.headFont });
  if (sl.bio) s.addText(String(sl.bio), { x: MX, y: 3.1, w: 5.8, h: 1.6, fontSize: 14, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3 });
  // skill bars
  const skills = (sl.skills || []).map((v) => (typeof v === "object" ? v : { label: String(v), pct: 0 }));
  let y = 4.8;
  const barX = MX + 1.1, barW = 3.6;
  skills.forEach((sk) => {
    const pct = Math.max(0, Math.min(100, Number(sk.pct) || 0));
    s.addText(String(sk.label || ""), { x: MX, y: y - 0.04, w: 1.05, h: 0.3, fontSize: 11, color: t.ink, fontFace: t.bodyFont, valign: "middle" });
    s.addShape("rect", { x: barX, y: y + 0.02, w: barW, h: 0.18, fill: { color: t.barTrack }, line: { type: "none" } });
    s.addShape("rect", { x: barX, y: y + 0.02, w: barW * (pct / 100), h: 0.18, fill: { color: t.barFill }, line: { type: "none" } });
    s.addText(`${pct}%`, { x: barX + barW + 0.15, y: y - 0.04, w: 0.8, h: 0.3, fontSize: 11, color: t.body, fontFace: t.bodyFont, valign: "middle" });
    y += 0.42;
  });
  addPhoto(s, t, { x: 7.0, y: 1.5, w: W - 7.0 - 0.5, h: 5.2, src: sl.photo, label: sl.name });
}

// Native chart (bar/line) + optional lead paragraph. categories + series.
function layoutChart(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.slideBg };
  addChrome(s, t, { dark: !!t.dark });
  const hasLead = !!sl.lead;
  addTitle(s, t, sl.title || "Trend analysis", hasLead ? { w: 4.4 } : {}); // clear of the lead
  const c = sl.chart || {};
  if (hasLead) s.addText(String(sl.lead), { x: 5.4, y: 1.55, w: W - 5.4 - MX, h: 1.4, fontSize: 13, color: t.body, fontFace: t.bodyFont, valign: "top", lineSpacingMultiple: 1.3 });
  const cats = c.categories || [];
  const series = (c.series || []).map((sr) => ({ name: String(sr.name || ""), labels: cats, values: (sr.values || []).map(Number) }));
  if (!series.length) return;
  const palette = [t.accent2 || "1B9E8F", t.primary, "E8741E", t.badge];
  const type = (c.type === "line") ? "line" : "bar";
  try {
    s.addChart(type, series, {
      x: MX, y: 3.0, w: CONTENT_W, h: 3.8,
      chartColors: palette,
      showLegend: true, legendPos: "b", legendFontFace: t.bodyFont, legendFontSize: 11,
      catAxisLabelFontFace: t.bodyFont, valAxisLabelFontFace: t.bodyFont,
      catAxisLabelFontSize: 10, valAxisLabelFontSize: 10,
      showValAxisTitle: false, barGapWidthPct: 60,
    });
  } catch (_) { /* chart libs vary; skip silently rather than crash the deck */ }
}

function layoutThankyou(pptx, t, sl) {
  const s = pptx.addSlide();
  s.background = { color: t.lightBg };
  addChrome(s, t, { dark: !!t.dark });
  const img = imgSrc(sl.image);
  if (img) { try { s.addImage({ ...img, x: 0, y: 4.4, w: W, h: H - 4.4, sizing: { type: "cover", w: W, h: H - 4.4 } }); } catch (_) {} }
  addMarker(s, t, MX, 2.2);
  s.addText(String(sl.title || sl.text || "Thank you."), { x: MX, y: 2.5, w: CONTENT_W, h: 1.6, fontSize: 60, bold: true, color: t.ink, fontFace: t.headFont });
}

const LAYOUTS = {
  cover: layoutCover,
  agenda: layoutAgenda,
  section: layoutSection,
  bullets: layoutBullets,
  content: layoutBullets,
  stats: layoutStats,
  statement: layoutStatement,
  quote: layoutStatement,
  number_grid: layoutNumberGrid,
  numbers: layoutNumberGrid,
  timeline: layoutTimeline,
  process: layoutTimeline,
  metrics: layoutMetrics,
  team: layoutTeam,
  profile: layoutProfile,
  chart: layoutChart,
  thankyou: layoutThankyou,
};

// A slide with NO renderable content produces a blank page (the boss's "empty
// pages"). The model occasionally over-generates these — an `integrations`
// section header with nothing after it, or a stray `{}`. We drop them in code
// (Rule #1b mechanic) so a forgotten line of the recipe can't ship blank
// slides. A slide counts as content if it has ANY text, list, chart, or image.
function slideHasContent(sl) {
  if (!sl || typeof sl !== "object") return false;
  const TEXT = ["title", "subtitle", "text", "eyebrow", "tagline", "body", "lead", "intro", "name", "role", "bio"];
  for (const f of TEXT) if (sl[f] != null && String(sl[f]).trim() !== "") return true;
  const LIST = ["items", "bullets", "features", "stats", "metrics", "steps", "members", "skills"];
  for (const f of LIST) if (Array.isArray(sl[f]) && sl[f].length) return true;
  if (sl.chart && Array.isArray(sl.chart.series) && sl.chart.series.length) return true;
  if (sl.image && String(sl.image).trim() !== "") return true;
  return false;
}

// Infer a layout from whichever data field is present so a slide without an
// explicit `layout` still renders correctly (mechanic-in-code, not prose).
function inferLayout(sl) {
  if (sl.layout && LAYOUTS[String(sl.layout).toLowerCase()]) return String(sl.layout).toLowerCase();
  if (sl.chart) return "chart";
  if (sl.metrics) return "metrics";
  if (sl.stats) return "stats";
  if (sl.steps) return "timeline";
  if (sl.members) return "team";
  if (sl.skills) return "profile";
  if (sl.items) return "number_grid";
  if (sl.eyebrow && sl.text) return "statement";
  return "bullets";
}

// ── main ───────────────────────────────────────────────────────────────────
const spec = JSON.parse(fs.readFileSync(0, "utf8"));
const out = process.argv[2];
const t = resolveTheme(spec);

const pptx = new PptxGenJS();
pptx.layout = "LAYOUT_WIDE"; // 13.333 × 7.5in, 16:9
pptx.theme = { headFontFace: t.headFont, bodyFontFace: t.bodyFont };

// Core metadata → the file's own name, so the inline PDF preview's title reads
// "dfw-business-opportunity-deck" (the native doc), not pptxgenjs's default
// "PptxGenJS Presentation" that LibreOffice would otherwise carry into the PDF.
const docTitle = docTitleFrom(out);
pptx.title = docTitle;
pptx.subject = docTitle;
pptx.author = "dopesoft";
pptx.company = "dopesoft";

// Top-level title/subtitle → a branded cover slide.
if (spec.title) layoutCover(pptx, t, { title: spec.title, subtitle: spec.subtitle, image: spec.image });

for (const sl of spec.slides || []) {
  if (!slideHasContent(sl)) {
    process.stderr.write(`render_pptx: skipped an empty slide\n`);
    continue;
  }
  const layout = inferLayout(sl);
  const fn = LAYOUTS[layout] || layoutBullets;
  try {
    fn(pptx, t, sl);
  } catch (e) {
    // Never let one bad slide kill the whole deck — render a minimal fallback.
    process.stderr.write(`render_pptx: slide (${layout}) failed: ${(e && e.message) || e}\n`);
    const s = pptx.addSlide();
    addChrome(s, t, { dark: !!t.dark });
    if (sl.title) addTitle(s, t, sl.title);
    if (sl.body) s.addText(String(sl.body), { x: MX, y: 2.6, w: CONTENT_W, h: 4, fontSize: 16, color: t.body, fontFace: t.bodyFont, valign: "top" });
  }
  if (sl.notes) pptx.slides[pptx.slides.length - 1].addNotes(String(sl.notes));
}

if (!pptx.slides || pptx.slides.length === 0) {
  pptx.addSlide().addText("(empty)", { x: 1, y: 1, w: 8, h: 1, fontSize: 18, fontFace: t.bodyFont });
}

pptx.writeFile({ fileName: out }).catch((e) => {
  process.stderr.write(String((e && e.stack) || e) + "\n");
  process.exit(1);
});
