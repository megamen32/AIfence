#!/usr/bin/env python3
"""Export weekly truth aggregates to the customer's ``Продажи`` workbook shape."""
from __future__ import annotations

import argparse
import json
import math
import zipfile
from pathlib import Path
from xml.sax.saxutils import escape


HEADERS = ["week_start", "product_family", "orders_index", "orders_index_4w", "base_period",
           "channel", "comment", "orders", "revenue_rub"]


def rows_from_truth(path: Path) -> list[dict]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    rows = raw.get("weeks") if isinstance(raw, dict) else raw
    if not isinstance(rows, list):
        raise ValueError("truth JSON must be a list or {weeks:[...]}")
    result = []
    for row in rows:
        if not isinstance(row, dict):
            continue
        result.append({"week_start": str(row["week_start"]), "product_family": str(row["product_family"]),
                       "orders": float(row.get("orders", row.get("orders_full", 0))),
                       "revenue_rub": float(row.get("revenue_rub", row.get("revenue", 0))),
                       "comment": str(row.get("comment", "")), "channel": str(row.get("channel", "Самостоятельные заказы клиентов"))})
    return result


def aggregate(rows: list[dict]) -> list[dict]:
    grouped: dict[tuple[str, str], dict] = {}
    for row in rows:
        key = (row["week_start"], row["product_family"])
        grouped.setdefault(key, {**row, "orders": 0.0, "revenue_rub": 0.0})
        grouped[key]["orders"] += row["orders"]
        grouped[key]["revenue_rub"] += row["revenue_rub"]
    by_product: dict[str, list[dict]] = {}
    for row in sorted(grouped.values(), key=lambda r: (r["product_family"], r["week_start"])):
        by_product.setdefault(row["product_family"], []).append(row)
    out = []
    for product, product_rows in by_product.items():
        baseline = sum(r["orders"] for r in product_rows[:4]) / max(1, min(4, len(product_rows)))
        base_start = product_rows[0]["week_start"]
        base_end = product_rows[min(3, len(product_rows) - 1)]["week_start"]
        for i, row in enumerate(product_rows):
            previous = [r["orders"] for r in product_rows[max(0, i - 3):i + 1]]
            rolling = sum(previous) / max(1, len(previous))
            out.append({"week_start": row["week_start"], "product_family": product,
                        "orders_index": round(row["orders"] / baseline * 100, 2) if baseline else None,
                        "orders_index_4w": round(rolling / baseline * 100, 2) if baseline else None,
                        "base_period": f"{base_start}-{base_end} = 100", "channel": row["channel"],
                        "comment": row["comment"], "orders": round(row["orders"], 4),
                        "revenue_rub": round(row["revenue_rub"], 2)})
    return sorted(out, key=lambda r: (r["week_start"], r["product_family"]))


def cell(value: object, row: int, col: int) -> str:
    letters = ""
    n = col
    while n:
        n, rem = divmod(n - 1, 26); letters = chr(65 + rem) + letters
    ref = f"{letters}{row}"
    if value is None or value == "":
        return f'<c r="{ref}" t="inlineStr"><is><t></t></is></c>'
    if isinstance(value, (int, float)) and not isinstance(value, bool) and math.isfinite(float(value)):
        return f'<c r="{ref}"><v>{value}</v></c>'
    return f'<c r="{ref}" t="inlineStr"><is><t>{escape(str(value))}</t></is></c>'


def write_xlsx(rows: list[dict], path: Path) -> None:
    sheet_rows = [HEADERS] + [[row.get(h, "") for h in HEADERS] for row in rows]
    body = []
    for i, values in enumerate(sheet_rows, 1):
        body.append(f'<row r="{i}">' + "".join(cell(value, i, j) for j, value in enumerate(values, 1)) + "</row>")
    sheet = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?>' + '<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>' + "".join(body) + "</sheetData></worksheet>"
    workbook = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Продажи" sheetId="1" r:id="rId1"/></sheets></workbook>'
    rels = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>'
    workbook_rels = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>'
    content = '<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>'
    path.parent.mkdir(parents=True, exist_ok=True)
    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as z:
        z.writestr("[Content_Types].xml", content); z.writestr("_rels/.rels", rels)
        z.writestr("xl/workbook.xml", workbook); z.writestr("xl/_rels/workbook.xml.rels", workbook_rels); z.writestr("xl/worksheets/sheet1.xml", sheet)


def main() -> int:
    parser = argparse.ArgumentParser(); parser.add_argument("--truth", type=Path, required=True); parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args(); rows = aggregate(rows_from_truth(args.truth)); write_xlsx(rows, args.out)
    print(json.dumps({"rows": len(rows), "out": str(args.out)}, ensure_ascii=False)); return 0


if __name__ == "__main__":
    raise SystemExit(main())
