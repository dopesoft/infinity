#!/usr/bin/env node
// render_docx.js — spec (stdin JSON) → .docx (argv[2]) via docx-js.
//
// docx-js produces cleaner Word output than python-docx (matches Claude's
// stack). Note docx-js defaults to A4; we force US Letter (12240×15840 DXA).
//
// Typography + tables are pinned here so output is consistent across Word,
// Pages and LibreOffice instead of inheriting each renderer's wild defaults
// (that's what produced the giant-font / 1-character-wide-column jank):
//   • explicit document + heading styles (font, size, spacing),
//   • tables use a FIXED layout with real column widths, cell padding, and a
//     light grid — without these, Word/LibreOffice collapse every column to
//     ~1 char and the table explodes across dozens of pages.
//
// Spec:
//   { title?, subtitle?, blocks: [
//       { type:"heading", level:1|2|3, text },
//       { type:"paragraph", text },
//       { type:"bullets", items:[str] },
//       { type:"numbered", items:[str] },
//       { type:"table", columns:[str], rows:[[cell]] }
//   ] }

const fs = require("fs");
const {
  Document, Packer, Paragraph, TextRun, HeadingLevel,
  Table, TableRow, TableCell, WidthType, AlignmentType,
  TableLayoutType, BorderStyle, ShadingType,
} = require("docx");

// US Letter printable width with 1" margins: 12240 − 2×1440 = 9360 DXA (twips).
const CONTENT_W = 9360;

function readSpec() {
  return JSON.parse(fs.readFileSync(0, "utf8"));
}

function headingFor(level) {
  const l = Math.min(Math.max(parseInt(level, 10) || 1, 1), 3);
  return { 1: HeadingLevel.HEADING_1, 2: HeadingLevel.HEADING_2, 3: HeadingLevel.HEADING_3 }[l];
}

// columnWidths distributes CONTENT_W proportional to each column's longest
// cell (clamped so one long cell can't starve the rest, and short columns like
// "Rank" still get a usable floor). Proportional + FIXED layout is what makes
// text wrap inside a real column instead of collapsing to a vertical strip.
function columnWidths(cols, rows, ncols) {
  const FLOOR = 640; // ~0.44": every column stays readable ("Rank", "1").
  const weights = [];
  for (let c = 0; c < ncols; c++) {
    let maxLen = cols[c] ? String(cols[c]).length : 1;
    for (const r of rows) {
      const v = r && r[c] != null ? String(r[c]) : "";
      if (v.length > maxLen) maxLen = v.length;
    }
    weights.push(Math.max(5, Math.min(maxLen, 48))); // clamp influence per column
  }
  // Too many columns for floors to fit → even split.
  if (FLOOR * ncols >= CONTENT_W) {
    const even = Math.floor(CONTENT_W / ncols);
    return Array.from({ length: ncols }, () => even);
  }
  // Give every column the floor, then share the remainder proportionally. This
  // keeps short columns narrow and wide ones wide while summing to EXACTLY the
  // page width (no right-margin overflow, no fixed-layout collapse).
  const remaining = CONTENT_W - FLOOR * ncols;
  const total = weights.reduce((a, w) => a + w, 0) || ncols;
  const widths = weights.map((w) => FLOOR + Math.round((w / total) * remaining));
  const drift = CONTENT_W - widths.reduce((a, w) => a + w, 0); // fix rounding
  widths[widths.length - 1] += drift;
  return widths;
}

const GRID_BORDER = { style: BorderStyle.SINGLE, size: 4, color: "BFBFBF" };
const CELL_MARGINS = { top: 40, bottom: 40, left: 96, right: 96 };

function cell(text, width, header) {
  return new TableCell({
    width: { size: width, type: WidthType.DXA },
    margins: CELL_MARGINS,
    shading: header ? { type: ShadingType.CLEAR, color: "auto", fill: "F2F2F2" } : undefined,
    children: [new Paragraph({
      spacing: { before: 0, after: 0, line: 252 },
      children: [new TextRun({ text: String(text == null ? "" : text), bold: !!header })],
    })],
  });
}

function tableBlock(b) {
  const cols = b.columns || [];
  const rows = b.rows || [];
  let ncols = cols.length;
  for (const r of rows) ncols = Math.max(ncols, (r || []).length);
  if (!ncols) return null;

  const widths = columnWidths(cols, rows, ncols);
  const trows = [];

  if (cols.length) {
    trows.push(new TableRow({
      tableHeader: true,
      children: Array.from({ length: ncols }, (_, i) => cell(cols[i], widths[i], true)),
    }));
  }
  for (const r of rows) {
    const row = r || [];
    trows.push(new TableRow({
      children: Array.from({ length: ncols }, (_, i) => cell(row[i], widths[i], false)),
    }));
  }
  if (!trows.length) return null;

  return new Table({
    rows: trows,
    width: { size: CONTENT_W, type: WidthType.DXA },
    columnWidths: widths,
    layout: TableLayoutType.FIXED,
    borders: {
      top: GRID_BORDER, bottom: GRID_BORDER, left: GRID_BORDER, right: GRID_BORDER,
      insideHorizontal: GRID_BORDER, insideVertical: GRID_BORDER,
    },
  });
}

// Level-1 headings get a thin rule beneath them (like "Part 1: …" in a clean
// study guide) for strong visual sectioning.
const H1_RULE = { bottom: { style: BorderStyle.SINGLE, size: 6, color: "AAAAAA", space: 3 } };

function children(spec) {
  const out = [];
  if (spec.title) {
    out.push(new Paragraph({
      text: String(spec.title),
      heading: HeadingLevel.TITLE,
      alignment: AlignmentType.CENTER,
    }));
  }
  if (spec.subtitle) {
    out.push(new Paragraph({
      alignment: AlignmentType.CENTER,
      spacing: { after: 280 },
      children: [new TextRun({ text: String(spec.subtitle), italics: true, color: "5A5A5A", size: 24 })],
    }));
  }
  for (const b of spec.blocks || []) {
    switch (b.type) {
      case "heading": {
        const lvl = Math.min(Math.max(parseInt(b.level, 10) || 1, 1), 3);
        const opts = { text: String(b.text || ""), heading: headingFor(lvl) };
        if (lvl === 1) opts.border = H1_RULE;
        out.push(new Paragraph(opts));
        break;
      }
      case "paragraph":
        out.push(new Paragraph({ children: [new TextRun(String(b.text || ""))] }));
        break;
      case "bullets":
        (b.items || []).forEach((it) =>
          out.push(new Paragraph({ text: String(it), bullet: { level: 0 } })));
        break;
      case "numbered":
        (b.items || []).forEach((it) =>
          out.push(new Paragraph({ text: String(it), numbering: { reference: "nums", level: 0 } })));
        break;
      case "table": {
        const t = tableBlock(b);
        if (t) {
          out.push(t);
          // A table can't be the last block in a docx section cleanly; a
          // trailing empty paragraph also gives breathing room after it.
          out.push(new Paragraph({ text: "" }));
        }
        break;
      }
      default:
        if (b.text) out.push(new Paragraph({ children: [new TextRun(String(b.text))] }));
    }
  }
  if (!out.length) out.push(new Paragraph(""));
  return out;
}

// Typography tuned for IMMEDIATE readability, modeled on a clean study-guide:
// a sane centered title (not a billboard), a strong hierarchy (big bold H1
// with a rule, bold navy H2, bold H3), 11pt body with breathing room.
//
// Font: "Calibri" (the boss's pick). The workspace container can't ship the
// proprietary Calibri, so it installs Carlito — the metric-IDENTICAL open
// twin — which LibreOffice auto-substitutes for Calibri in the inline PDF
// preview, so it looks like true Calibri. The downloaded .docx still NAMES
// Calibri, so Word on the boss's Mac uses the real face. See the Dockerfile
// font install + docker/workspace/fonts-local.conf.
const BODY_FONT = "Calibri";
const HEAD_FONT = "Calibri";
const NAVY = "1F3864";  // H2 accent — readable, not loud
const INK = "1A1A1A";   // near-black body/heading ink

const STYLES = {
  default: {
    document: {
      run: { font: BODY_FONT, size: 22, color: INK }, // 11pt
      paragraph: { spacing: { after: 180, line: 276 } }, // ~9pt after, 1.15 line
    },
    title: {
      run: { font: HEAD_FONT, size: 44, bold: true, color: INK }, // 22pt
      paragraph: { spacing: { after: 60 } },
    },
    heading1: {
      run: { font: HEAD_FONT, size: 32, bold: true, color: INK }, // 16pt
      paragraph: { spacing: { before: 320, after: 140 }, keepNext: true },
    },
    heading2: {
      run: { font: HEAD_FONT, size: 26, bold: true, color: NAVY }, // 13pt navy
      paragraph: { spacing: { before: 260, after: 90 }, keepNext: true },
    },
    heading3: {
      run: { font: HEAD_FONT, size: 23, bold: true, color: INK }, // ~11.5pt
      paragraph: { spacing: { before: 200, after: 60 }, keepNext: true },
    },
  },
};

(async () => {
  const out = process.argv[2];
  const spec = readSpec();
  const doc = new Document({
    styles: STYLES,
    numbering: {
      config: [{
        reference: "nums",
        levels: [{ level: 0, format: "decimal", text: "%1.", alignment: AlignmentType.START }],
      }],
    },
    sections: [{
      properties: { page: { size: { width: 12240, height: 15840 } } }, // US Letter
      children: children(spec),
    }],
  });
  const buf = await Packer.toBuffer(doc);
  fs.writeFileSync(out, buf);
})().catch((e) => {
  process.stderr.write(String((e && e.stack) || e) + "\n");
  process.exit(1);
});
