#!/usr/bin/env python3
"""render_pdf.py — spec (stdin JSON) → .pdf (argv[1]) via reportlab Platypus.

Spec (same block vocabulary as docx):
  { title?, subtitle?, blocks: [
      {type:"heading", level, text}, {type:"paragraph", text},
      {type:"bullets", items:[str]}, {type:"table", columns, rows}
  ] }
"""
import json
import sys

from reportlab.lib import colors
from reportlab.lib.pagesizes import LETTER
from reportlab.lib.styles import getSampleStyleSheet
from reportlab.lib.units import inch
from reportlab.platypus import (
    ListFlowable, ListItem, Paragraph, SimpleDocTemplate, Spacer, Table, TableStyle,
)


def main():
    out = sys.argv[1]
    spec = json.load(sys.stdin)
    styles = getSampleStyleSheet()
    story = []

    if spec.get("title"):
        story.append(Paragraph(str(spec["title"]), styles["Title"]))
    if spec.get("subtitle"):
        story.append(Paragraph(str(spec["subtitle"]), styles["Italic"]))
        story.append(Spacer(1, 10))

    for b in spec.get("blocks", []):
        t = b.get("type")
        if t == "heading":
            lvl = min(int(b.get("level", 1)), 3)
            story.append(Paragraph(str(b.get("text", "")), styles[f"Heading{lvl}"]))
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
                    ("BACKGROUND", (0, 0), (-1, 0), colors.HexColor("#333333")),
                    ("TEXTCOLOR", (0, 0), (-1, 0), colors.white),
                    ("FONTSIZE", (0, 0), (-1, -1), 9),
                    ("GRID", (0, 0), (-1, -1), 0.5, colors.grey),
                    ("VALIGN", (0, 0), (-1, -1), "TOP"),
                ]))
                story.append(tbl)
        story.append(Spacer(1, 8))

    if not story:
        story = [Paragraph("", styles["BodyText"])]

    SimpleDocTemplate(
        out, pagesize=LETTER, topMargin=0.8 * inch, bottomMargin=0.8 * inch,
        leftMargin=0.8 * inch, rightMargin=0.8 * inch,
    ).build(story)


if __name__ == "__main__":
    main()
