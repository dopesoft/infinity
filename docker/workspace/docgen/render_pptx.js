#!/usr/bin/env node
// render_pptx.js — spec (stdin JSON) → .pptx (argv[2]) via pptxgenjs.
//
// pptxgenjs is the cleanest free PowerPoint generator (matches Claude's
// stack; python-pptx makes you hand-write layout). 16:9 widescreen.
//
// Spec:
//   { title?, subtitle?, slides: [
//       { title?, bullets?:[str], body?:str, notes?:str }
//   ] }

const fs = require("fs");
const PptxGenJS = require("pptxgenjs");

const spec = JSON.parse(fs.readFileSync(0, "utf8"));
const out = process.argv[2];

const pptx = new PptxGenJS();
pptx.layout = "LAYOUT_WIDE"; // 13.333 × 7.5in, 16:9

// Title slide.
if (spec.title) {
  const s = pptx.addSlide();
  s.background = { color: "111111" };
  s.addText(String(spec.title), {
    x: 0.6, y: 2.7, w: 12.1, h: 1.6, fontSize: 40, bold: true, color: "FFFFFF", align: "center",
  });
  if (spec.subtitle) {
    s.addText(String(spec.subtitle), {
      x: 0.6, y: 4.3, w: 12.1, h: 0.8, fontSize: 20, color: "AAAAAA", align: "center",
    });
  }
}

for (const sl of spec.slides || []) {
  const s = pptx.addSlide();
  if (sl.title) {
    s.addText(String(sl.title), { x: 0.5, y: 0.35, w: 12.3, h: 0.9, fontSize: 28, bold: true, color: "222222" });
  }
  if (Array.isArray(sl.bullets) && sl.bullets.length) {
    s.addText(
      sl.bullets.map((b) => ({ text: String(b), options: { bullet: true, fontSize: 18, breakLine: true } })),
      { x: 0.7, y: 1.5, w: 12, h: 5.4, valign: "top", color: "333333" },
    );
  } else if (sl.body) {
    s.addText(String(sl.body), { x: 0.7, y: 1.5, w: 12, h: 5.4, fontSize: 18, valign: "top", color: "333333" });
  }
  if (sl.notes) s.addNotes(String(sl.notes));
}

if (!pptx.slides || pptx.slides.length === 0) {
  pptx.addSlide().addText("(empty)", { x: 1, y: 1, w: 8, h: 1, fontSize: 18 });
}

pptx.writeFile({ fileName: out }).catch((e) => {
  process.stderr.write(String((e && e.stack) || e) + "\n");
  process.exit(1);
});
