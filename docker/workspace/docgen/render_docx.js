#!/usr/bin/env node
// render_docx.js — spec (stdin JSON) → .docx (argv[2]) via docx-js.
//
// docx-js produces cleaner Word output than python-docx (matches Claude's
// stack). Note docx-js defaults to A4; we force US Letter (12240×15840 DXA).
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
} = require("docx");

function readSpec() {
  return JSON.parse(fs.readFileSync(0, "utf8"));
}

function headingFor(level) {
  const l = Math.min(Math.max(parseInt(level, 10) || 1, 1), 3);
  return { 1: HeadingLevel.HEADING_1, 2: HeadingLevel.HEADING_2, 3: HeadingLevel.HEADING_3 }[l];
}

function tableBlock(b) {
  const cols = b.columns || [];
  const rows = b.rows || [];
  const trows = [];
  if (cols.length) {
    trows.push(new TableRow({
      tableHeader: true,
      children: cols.map((c) => new TableCell({
        children: [new Paragraph({ children: [new TextRun({ text: String(c), bold: true })] })],
      })),
    }));
  }
  for (const r of rows) {
    trows.push(new TableRow({
      children: (r || []).map((c) => new TableCell({
        children: [new Paragraph(String(c == null ? "" : c))],
      })),
    }));
  }
  if (!trows.length) return null;
  return new Table({ rows: trows, width: { size: 100, type: WidthType.PERCENTAGE } });
}

function children(spec) {
  const out = [];
  if (spec.title) out.push(new Paragraph({ text: String(spec.title), heading: HeadingLevel.TITLE }));
  if (spec.subtitle) {
    out.push(new Paragraph({
      alignment: AlignmentType.LEFT,
      children: [new TextRun({ text: String(spec.subtitle), italics: true, color: "666666" })],
    }));
  }
  for (const b of spec.blocks || []) {
    switch (b.type) {
      case "heading":
        out.push(new Paragraph({ text: String(b.text || ""), heading: headingFor(b.level) }));
        break;
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
        if (t) out.push(t);
        break;
      }
      default:
        if (b.text) out.push(new Paragraph({ children: [new TextRun(String(b.text))] }));
    }
  }
  if (!out.length) out.push(new Paragraph(""));
  return out;
}

(async () => {
  const out = process.argv[2];
  const spec = readSpec();
  const doc = new Document({
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
