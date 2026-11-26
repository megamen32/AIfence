#!/usr/bin/env python3
"""Turn authorized source rows into watchdog input files. Do not upload the workbook."""
import argparse
import datetime as dt
import hashlib
import json
from pathlib import Path
import re
import sys
import tempfile
import os

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / "apps/product-manager/research"),
                str(ROOT / "apps/metrika-analyst/demo_sources")]
from xlsx import Workbook
import aifence_demo_sources as metrika


def fingerprint(path):
    with path.open("rb") as source:
        return "sha256:" + hashlib.file_digest(source, "sha256").hexdigest()


def day(value):
    if isinstance(value, (float, int)):
        return (dt.date(1899, 12, 30) + dt.timedelta(days=int(value))).isoformat()
    return dt.date.fromisoformat(str(value)[:10]).isoformat()


def batch(source, cutoff, facts, status="ok", warnings=()):
    return dict(schema_version="aifence.v1", source=source, as_of=cutoff + "T23:59:59Z",
                status=status, observations=facts, warnings=list(warnings))


def safe_row(row):
    return not any("эталонный_кейс" in str(cell).strip().casefold() for cell in row.values())


def records(book, sheet):
    rows = book.rows(sheet)
    header = rows.pop(1)
    return {n: {header[c]: v for c, v in fields.items() if c in header}
            for n, fields in sorted(rows.items()) if safe_row(fields)}


def fact(kind, key, value, start, end, source, locator, **details):
    return dict(id=f"{kind}/{metrika.digest([source, locator, key])}", kind=kind,
                product_key="all", metric_key=key, value=value, unit="text",
                period={"from": start, "to": end}, status="observed",
                source_ref=dict(source, locator=locator),
                details=dict(details, synthetic=False, origin="imported_historical",
                             evidence_direction="unknown"))


def links(value):
    return [v.strip() for v in str(value or "").split(",") if v.strip()]


def workbook_context(book, source, cutoff):
    definitions = {
        r["metric_id"]: {"metric_key": r["metric_id"], "name": r["metric_name"],
                         "source_ref": dict(source, locator=f"Метрики_справочник!row {n}")}
        for n, r in records(book, "Метрики_справочник").items() if r.get("metric_id")
    }
    releases, tasks = [], []
    for n, r in records(book, "Релизы").items():
        if not r.get("release_id") or not r.get("release_date"):
            continue
        date = day(r["release_date"])
        if date <= cutoff:
            releases.append(fact("release", "release_log", r.get("description"), date, date, source,
                                 f"Релизы!row {n}", release_id=r["release_id"], release_type=r.get("release_type"),
                                 release_status=r.get("status"), metric_keys=links(r.get("affected_metrics")),
                                 related_task_ids=links(r.get("related_task_ids"))))
    for n, r in records(book, "Бэклог_задачи").items():
        if not r.get("task_id") or not r.get("created_date"):
            continue
        start = day(r["created_date"])
        if start > cutoff:
            continue
        closed = day(r["closed_date"]) if r.get("closed_date") not in (None, "", "нет данных", "—", "-") else None
        keys = links(r.get("linked_metric_ids"))
        tasks.append(fact("task", "backlog_item", r.get("title"), start,
                          min(closed or cutoff, cutoff), source, f"Бэклог_задачи!row {n}",
                          task_id=r["task_id"], state=r.get("status"), priority=r.get("priority"),
                          description=r.get("description_short"), release_id=r.get("release_id"),
                          closed_date=closed, metric_keys=keys,
                          metric_definitions=[definitions[k] for k in keys if k in definitions]))
    return {"release": batch("release", cutoff, releases), "backlog": batch("backlog", cutoff, tasks)}


def break_facts(book, specs, source, sales):
    facts = []
    for spec in specs:
        sheet, row, column = spec["sheet"], spec["row"], spec["column"]
        if sheet not in {"Метрики_справочник", "Релизы", "Бэклог_задачи", "Метрики_данные", "Продажи"}:
            raise ValueError("data break must reference a recognized source sheet")
        fields = book.rows(sheet)[row]
        quote = fields[column]
        if not isinstance(quote, str) or not quote.strip() or not safe_row(fields):
            raise ValueError("data break source is empty or excluded")
        effect = spec["effect"]
        if effect not in {"exclude", "boundary", "missing"}:
            raise ValueError("invalid data break effect")
        f = fact("metric", "data_break", quote if effect != "missing" else None,
                 day(spec["from"]), day(spec["to"]), source, f"{sheet}!{column}{row}",
                 effect=effect, metric_keys=spec["metric_keys"], reason=spec["reason"],
                 known_from=day(spec["known_from"]) if "known_from" in spec else "")
        f["product_key"] = spec.get("product_key", "all")
        f["segment"] = spec.get("segment", "all")
        f["id"] += "/" + metrika.digest([f["product_key"], f["segment"], f["period"], effect])
        f["status"] = "missing" if effect == "missing" else "incomparable"
        facts.append(f)
    for row in sales:
        if row["details"].get("partial_week"):
            f = fact("metric", "data_break", None, row["period"]["from"], row["period"]["to"], source,
                     row["source_ref"]["locator"], effect="exclude", metric_keys=[row["metric_key"]],
                     reason="incomplete_week")
            f.update(product_key=row["product_key"], segment=row["segment"], status="missing")
            facts.append(f)
    return facts


CONTACT = re.compile(r"@|(?:\+?\d[\s().-]*){10,}|https?://", re.I)


def interview_facts(specs, spec_path, cutoff):
    for spec in specs:
        path = spec_path.parent / spec["file"]
        source = {"source_id": path.name, "revision": fingerprint(path)}
        book = Workbook(path)
        try:
            for sheet, columns in spec["sheets"].items():
                if "эталон" in sheet.casefold():
                    raise ValueError("excluded interview sheet")
                for n, row in sorted(book.rows(sheet).items()):
                    if n < spec.get("first_response_row", 2) or not safe_row(row):
                        continue
                    for col in columns:
                        text = row.get(col)
                        if not isinstance(text, str) or len(text.strip()) < 12 or CONTACT.search(text):
                            continue
                        f = fact("research", "interview_excerpt", text.strip(), cutoff, cutoff, source,
                                 f"{sheet}!{col}{n}", period_basis="import_snapshot_interview_date_unknown")
                        yield f
        finally:
            book.close()


def apply_breaks(rows, breaks):
    for row in rows:
        period = row["period"]
        for f in breaks:
            meta = f["details"]
            if row["metric_key"] not in meta["metric_keys"] or f["product_key"] not in ("all", row["product_key"]):
                continue
            if f["segment"] not in ("all", row.get("segment", "all")):
                continue
            affected = f["period"]
            if meta["effect"] == "boundary":
                if period["from"] >= affected["from"]:
                    row["comparison_key"] = affected["from"]
                elif period["to"] >= affected["from"]:
                    row["status"] = "incomparable"
                else:
                    continue
            elif period["from"] <= affected["to"] and period["to"] >= affected["from"] and meta["reason"] != "incomplete_week":
                row["status"] = "missing" if meta["effect"] == "missing" else "incomparable"
                if row["status"] == "missing":
                    row["value"] = None
            else:
                continue
            row["details"].setdefault("data_break_refs", []).append(f["source_ref"])


def write_json(path, value):
    with tempfile.NamedTemporaryFile(mode="w", encoding="utf-8", dir=path.parent, delete=False) as out:
        tmp = Path(out.name)
        try:
            json.dump(value, out, ensure_ascii=False, indent=2, allow_nan=False)
            out.write("\n")
            out.close()
            os.replace(tmp, path)
        finally:
            tmp.unlink(missing_ok=True)


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--workbook", required=True, type=Path)
    ap.add_argument("--as-of", required=True, type=day)
    ap.add_argument("--breaks", required=True, type=Path, help="Operator-reviewed private JSON array of data breaks")
    ap.add_argument("--interviews", type=Path, help="Private JSON array of selected interview files/sheets/response columns")
    ap.add_argument("--out", required=True, type=Path, help="Ignored local directory for generated evidence")
    args = ap.parse_args()
    out = args.out.resolve()
    if not any(out.is_relative_to(root) for root in (ROOT / "data", metrika.DATA)):
        ap.error("--out must be under an ignored data directory")
    source = {"source_id": args.workbook.name, "revision": fingerprint(args.workbook)}
    book = Workbook(args.workbook)
    try:
        batches = workbook_context(book, source, args.as_of)
        selected = {name: metrika.xlsx_rows(book.rows(name), safe_row)
                    for name in ("Продажи", "Метрики_данные")}
        raw_metrics = metrika.workbook_observations(selected)
        raw_sales = metrika.sales_index_observations(selected)
        metrics = [o for o in raw_metrics if o["period"]["to"] <= args.as_of]
        sales = [o for o in raw_sales if o["period"]["to"] <= args.as_of or
                 o["period"]["from"] <= args.as_of and o["details"].get("partial_week")]
        for row in sales + metrics:
            row["source_ref"].update(source)
            if row["details"].get("partial_week"):
                row["details"]["available_on"] = args.as_of
        breaks = break_facts(book, json.loads(args.breaks.read_text()), source, sales)
    finally:
        book.close()
    apply_breaks(sales + metrics, breaks)
    batches["metrika"] = batch("metrika", args.as_of, breaks)
    if args.interviews:
        specs = json.loads(args.interviews.read_text())
        quotes = list(interview_facts(specs, args.interviews, args.as_of))
        batches["research"] = batch("research", args.as_of, quotes, "ok" if quotes else "unavailable",
                                    ["Interview excerpts are qualitative and may refer to a brand rather than a specific edition."])
    out.mkdir(mode=0o700, parents=True, exist_ok=True)
    write_json(out / "watch-context.json", dict(schema_version="aifence.watch-context.v1", synthetic=False, batches=batches))
    write_json(out / "watch-series.json", batch("metrika", args.as_of, sales + metrics))
    print("Built watchdog inputs with row-level provenance.")


if __name__ == "__main__":
    main()
