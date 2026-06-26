#!/usr/bin/env python3
"""render_xlsx.py — spec (stdin JSON) → .xlsx (argv[1]) via openpyxl.

Spec shapes accepted:
  { "sheets": [ { "name", "columns": [str], "rows": [[cell]], "number_formats": {colIdx: "fmt"} } ] }
  { "columns": [str], "rows": [[cell]] }            # single-sheet shorthand
  { "title", "rows" }                                # bare rows

openpyxl writes formulas as strings without computed values; the workspace
/docgen endpoint runs `soffice --headless --convert-to xlsx` afterwards when
the spec sets recalc=true so cached values exist (matches Claude's stack).
"""
import json
import sys

from openpyxl import Workbook
from openpyxl.styles import Alignment, Font, PatternFill
from openpyxl.utils import get_column_letter
from openpyxl.worksheet.properties import PageSetupProperties

# Calibri everywhere — the boss's pick for every generated document. openpyxl
# already defaults to Calibri 11, but we set it explicitly so the file (and its
# LibreOffice PDF preview) is guaranteed Calibri regardless of the renderer's
# defaults. Carlito (the metric twin) renders it faithfully in the container.
FONT = "Calibri"


def add_sheet(wb, spec, first):
    name = (spec.get("name") or "Sheet")[:31]
    ws = wb.active if first else wb.create_sheet()
    ws.title = name
    cols = spec.get("columns") or []
    rows = spec.get("rows") or []

    row_cursor = 1
    if cols:
        for c, col in enumerate(cols, start=1):
            cell = ws.cell(row=1, column=c, value=str(col))
            cell.font = Font(name=FONT, bold=True, color="FFFFFF")
            cell.fill = PatternFill("solid", fgColor="333333")
            cell.alignment = Alignment(horizontal="left", vertical="center")
        ws.freeze_panes = "A2"
        row_cursor = 2

    for row in rows:
        for c, val in enumerate(row, start=1):
            cell = ws.cell(row=row_cursor, column=c, value=val)
            cell.font = Font(name=FONT, size=11)
            # Wrap long text instead of letting one cell balloon a column —
            # keeps both the live grid AND the PDF preview tidy.
            cell.alignment = Alignment(wrap_text=True, vertical="top")
        row_cursor += 1

    # Width auto-fit, capped so a long cell wraps within a sane column rather
    # than stretching the sheet off the page (which wrecks the print preview).
    ncols = len(cols) if cols else (max((len(r) for r in rows), default=0))
    for c in range(1, ncols + 1):
        width = 12
        for r in range(1, ws.max_row + 1):
            v = ws.cell(row=r, column=c).value
            if v is not None:
                width = max(width, min(44, len(str(v)) + 2))
        ws.column_dimensions[get_column_letter(c)].width = width

    # Print/preview layout: the inline preview is a LibreOffice xlsx→PDF render,
    # so without this a wide sheet spills columns onto extra pages or shrinks to
    # tofu. Fit all columns to one page width, repeat the header row on every
    # page, and go landscape once it's wide. The .xlsx stays fully editable —
    # these are just print settings, the same ones a clean deliverable carries.
    if ncols:
        ws.page_setup.orientation = "landscape" if ncols > 6 else "portrait"
        ws.page_setup.fitToWidth = 1
        ws.page_setup.fitToHeight = 0
        ws.sheet_properties.pageSetUpPr = PageSetupProperties(fitToPage=True)
        if cols:
            ws.print_title_rows = "1:1"


def main():
    out = sys.argv[1]
    spec = json.load(sys.stdin)

    sheets = spec.get("sheets")
    if not sheets:
        sheets = [{
            "name": spec.get("title", "Sheet1"),
            "columns": spec.get("columns", []),
            "rows": spec.get("rows", []),
        }]

    wb = Workbook()
    for i, s in enumerate(sheets):
        add_sheet(wb, s, first=(i == 0))
    wb.save(out)


if __name__ == "__main__":
    main()
