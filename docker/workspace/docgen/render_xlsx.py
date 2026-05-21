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
            cell.font = Font(bold=True, color="FFFFFF")
            cell.fill = PatternFill("solid", fgColor="333333")
            cell.alignment = Alignment(horizontal="left", vertical="center")
        ws.freeze_panes = "A2"
        row_cursor = 2

    for row in rows:
        for c, val in enumerate(row, start=1):
            ws.cell(row=row_cursor, column=c, value=val)
        row_cursor += 1

    # Width auto-fit (capped) so columns aren't tofu-narrow or absurdly wide.
    ncols = len(cols) if cols else (max((len(r) for r in rows), default=0))
    for c in range(1, ncols + 1):
        width = 10
        for r in range(1, ws.max_row + 1):
            v = ws.cell(row=r, column=c).value
            if v is not None:
                width = max(width, min(60, len(str(v)) + 2))
        ws.column_dimensions[get_column_letter(c)].width = width


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
