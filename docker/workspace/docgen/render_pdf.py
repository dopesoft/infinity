#!/usr/bin/env python3
"""render_pdf.py — spec (stdin JSON) → .pdf (argv[1]) via reportlab Platypus.

Spec (same block vocabulary as docx):
  { title?, subtitle?, blocks: [
      {type:"heading", level, text}, {type:"paragraph", text},
      {type:"bullets", items:[str]}, {type:"table", columns, rows}
  ] }

Font: Calibri everywhere (the boss's pick for every generated document).
reportlab ships only Helvetica/Times/Courier, so we register Carlito — the
metric-identical open twin of Calibri, installed in the workspace image — as
"Calibri". If the font files aren't present (e.g. local dev) we fall back to
Helvetica so rendering never breaks.
"""
import json
import os
import sys

from reportlab.lib import colors
from reportlab.lib.enums import TA_CENTER
from reportlab.lib.pagesizes import LETTER
from reportlab.lib.styles import getSampleStyleSheet
from reportlab.lib.units import inch
from reportlab.pdfbase import pdfmetrics
from reportlab.pdfbase.pdfmetrics import registerFontFamily
from reportlab.pdfbase.ttfonts import TTFont
from reportlab.platypus import (
    HRFlowable, ListFlowable, ListItem, Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle,
)

# ── font registration ────────────────────────────────────────────────────────
# Register Carlito (Calibri twin) under the name "Calibri". Fall back to the
# built-in Helvetica family if the TTFs aren't installed.
CARLITO_DIR = "/usr/share/fonts/truetype/crosextra"
FONT = "Helvetica"
FONT_BOLD = "Helvetica-Bold"
FONT_ITALIC = "Helvetica-Oblique"
try:
    pdfmetrics.registerFont(TTFont("Calibri", f"{CARLITO_DIR}/Carlito-Regular.ttf"))
    pdfmetrics.registerFont(TTFont("Calibri-Bold", f"{CARLITO_DIR}/Carlito-Bold.ttf"))
    pdfmetrics.registerFont(TTFont("Calibri-Italic", f"{CARLITO_DIR}/Carlito-Italic.ttf"))
    pdfmetrics.registerFont(TTFont("Calibri-BoldItalic", f"{CARLITO_DIR}/Carlito-BoldItalic.ttf"))
    registerFontFamily(
        "Calibri", normal="Calibri", bold="Calibri-Bold",
        italic="Calibri-Italic", boldItalic="Calibri-BoldItalic",
    )
    FONT, FONT_BOLD, FONT_ITALIC = "Calibri", "Calibri-Bold", "Calibri-Italic"
except Exception as e:  # noqa: BLE001 — degrade to Helvetica, never crash
    sys.stderr.write(f"render_pdf: Calibri/Carlito unavailable, using Helvetica: {e}\n")

INK = colors.HexColor("#1A1A1A")
NAVY = colors.HexColor("#1F3864")

# Heading sizes/colors mirror render_docx.js so a report looks the same whether
# it's exported as .docx or .pdf.
HEADING_SPECS = {
    "Title": dict(font=FONT_BOLD, size=22, color=INK, align=TA_CENTER, space_after=6, leading=26),
    "Heading1": dict(font=FONT_BOLD, size=16, color=INK, space_before=16, space_after=3, leading=20),
    "Heading2": dict(font=FONT_BOLD, size=13, color=NAVY, space_before=13, space_after=5, leading=16),
    "Heading3": dict(font=FONT_BOLD, size=11.5, color=INK, space_before=10, space_after=3, leading=14),
}


def styled():
    styles = getSampleStyleSheet()
    # Body + italic in Calibri at a readable 11pt.
    styles["BodyText"].fontName = FONT
    styles["BodyText"].fontSize = 11
    styles["BodyText"].leading = 15
    styles["BodyText"].textColor = INK
    styles["Italic"].fontName = FONT_ITALIC
    styles["Italic"].fontSize = 11
    styles["Italic"].textColor = colors.HexColor("#5A5A5A")
    for name, s in HEADING_SPECS.items():
        st = styles[name]
        st.fontName = s["font"]
        st.fontSize = s["size"]
        st.textColor = s["color"]
        st.leading = s.get("leading", s["size"] + 4)
        if "align" in s:
            st.alignment = s["align"]
        if "space_before" in s:
            st.spaceBefore = s["space_before"]
        if "space_after" in s:
            st.spaceAfter = s["space_after"]
    return styles


def main():
    out = sys.argv[1]
    spec = json.load(sys.stdin)
    styles = styled()
    story = []

    if spec.get("title"):
        story.append(Paragraph(str(spec["title"]), styles["Title"]))
    if spec.get("subtitle"):
        story.append(Paragraph(str(spec["subtitle"]), styles["Italic"]))
        story.append(Spacer(1, 12))

    for b in spec.get("blocks", []):
        t = b.get("type")
        if t == "heading":
            lvl = min(int(b.get("level", 1)), 3)
            story.append(Paragraph(str(b.get("text", "")), styles[f"Heading{lvl}"]))
            if lvl == 1:  # thin rule under H1 — mirrors the docx heading style
                story.append(HRFlowable(
                    width="100%", thickness=0.75, color=colors.HexColor("#AAAAAA"),
                    spaceBefore=2, spaceAfter=8,
                ))
        elif t == "paragraph":
            story.append(Paragraph(str(b.get("text", "")), styles["BodyText"]))
        elif t == "bullets":
            items = [ListItem(Paragraph(str(i), styles["BodyText"])) for i in b.get("items", [])]
            if items:
                story.append(ListFlowable(items, bulletType="bullet"))
        elif t == "table":
            data = []
            if b.get("columns"):
                data.append([str(c) for c in b["columns"]])
            for r in b.get("rows", []):
                data.append([str(c) for c in r])
            if data:
                tbl = Table(data, hAlign="LEFT")
                tbl.setStyle(TableStyle([
                    ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#F2F2F2")),
                    ("TEXTCOLOR", (0, 0), (-1, 0), INK),
                    ("FONTNAME", (0, 0), (-1, 0), FONT_BOLD),
                    ("FONTNAME", (0, 1), (-1, -1), FONT),
                    ("FONTSIZE", (0, 0), (-1, -1), 10.5),
                    ("GRID", (0, 0), (-1, -1), 0.5, colors.HexColor("#BFBFBF")),
                    ("VALIGN", (0, 0), (-1, -1), "TOP"),
                    ("TOPPADDING", (0, 0), (-1, -1), 4),
                    ("BOTTOMPADDING", (0, 0), (-1, -1), 4),
                    ("LEFTPADDING", (0, 0), (-1, -1), 6),
                    ("RIGHTPADDING", (0, 0), (-1, -1), 6),
                ]))
                story.append(tbl)
        story.append(Spacer(1, 8))

    if not story:
        story = [Paragraph("", styles["BodyText"])]

    # PDF /Title metadata → the file's own name, so the canvas preview's title
    # reads the native doc name instead of an empty/default title.
    doc_title = os.path.splitext(os.path.basename(out))[0] or "Document"

    SimpleDocTemplate(
        out, pagesize=LETTER, topMargin=0.8 * inch, bottomMargin=0.8 * inch,
        leftMargin=0.8 * inch, rightMargin=0.8 * inch,
        title=doc_title, author="dopesoft",
    ).build(story)


if __name__ == "__main__":
    main()
