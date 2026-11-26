#!/usr/bin/env python3
"""Build and serve the AIfence Metrika demo sources.

The workbook is treated as an immutable imported source.  The sandbox is a
separate synthetic source with explicit scenario IDs.  The script uses only
the Python standard library so it runs on a clean Mac M1 installation.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import sqlite3
import sys
import zipfile
from pathlib import Path
from xml.etree import ElementTree as ET

ROOT = Path(__file__).resolve().parent
DATA = ROOT / "data"
COUNTER_ID = 114698578
HOLDOUT_SHEETS = {"Эталонный_кейс"}  # organizers' answer key: never agent input

NS = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main",
      "r": "http://schemas.openxmlformats.org/officeDocument/2006/relationships",
      "p": "http://schemas.openxmlformats.org/package/2006/relationships"}


def iso_day(day: dt.date) -> str:
    return day.isoformat()


def digest(value: object) -> str:
    return hashlib.sha256(json.dumps(value, ensure_ascii=False, sort_keys=True).encode()).hexdigest()[:16]


def ref(source_id: str, locator: str) -> dict:
    out = {"source_id": source_id, "locator": locator}
    if source_id != "softline-workbook-imported":
        out["revision"] = "fixture:aifence-sandbox-v1"
    return out


def obs(product: str, metric: str, value: float | None, unit: str, start: str, end: str,
        source_id: str, locator: str, status: str = "observed", segment: str = "all",
        caveats: list[str] | None = None, details: dict | None = None) -> dict:
    payload = {"kind": "metric", "product_key": product, "metric_key": metric, "period": {"from": start, "to": end},
               "segment": segment, "value": value, "unit": unit, "status": status,
               "source_ref": ref(source_id, locator), "caveats": caveats or []}
    payload["id"] = "obs-" + digest(payload)
    if details:
        payload["details"] = details
    return payload


def evidence(source: str, observations: list[dict], warnings: list[str]) -> dict:
    return {"schema_version": "aifence.v1", "source": "metrika",
            "as_of": dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
            "status": "ok", "observations": observations, "warnings": warnings}


SALES_SHEET = "Продажи"
SALES_PRODUCT_KEYS = {"Kaspersky Endpoint Security": "kaspersky_endpoint_security",
                      "Astra Linux Special Edition": "astra_linux_special_edition"}
SALES_CAVEATS = ["orders_index is an index, not a count of orders or revenue.",
                 "Only self-service orders (Самостоятельные заказы клиентов) are included."]


def sales_index_observations(sheets: dict[str, list[list[str]]]) -> list[dict]:
    """Weekly orders index per product family from the workbook sheet «Продажи».

    One observation per week per family; the period spans seven days from
    week_start.  Values are copied from the sheet, never derived or interpreted here.
    """
    rows = sheets.get(SALES_SHEET, [])
    if not rows:
        return []
    pos = {name: i for i, name in enumerate(rows[0])}

    def cell(row: list[str], name: str) -> str:
        i = pos.get(name, len(row))
        return row[i].strip() if i < len(row) else ""

    out: list[dict] = []
    for row_no, row in enumerate(rows[1:], 2):
        week, family = cell(row, "week_start"), cell(row, "product_family")
        if not week or not family:
            continue
        product_key = SALES_PRODUCT_KEYS.get(family)
        if product_key is None:
            raise ValueError(f"unmapped product family in {SALES_SHEET}!row {row_no}: {family}")
        start = dt.date.fromisoformat(week)
        raw = cell(row, "orders_index")
        value = float(raw.replace(",", ".")) if raw else None
        caveats = list(SALES_CAVEATS)
        comment = cell(row, "comment")
        if comment:
            caveats.append(comment)
        details = {"workbook_sheet": SALES_SHEET, "workbook_row": row_no, "product_family": family,
                   "base_period": cell(row, "base_period"), "channel": cell(row, "channel"),
                   "partial_week": "неполн" in comment.casefold(), "synthetic": False}
        out.append(obs(product_key, "orders_index", value, "index", start.isoformat(),
                       (start + dt.timedelta(days=6)).isoformat(), "softline-workbook-imported",
                       f"{SALES_SHEET}!row {row_no}", "observed" if value is not None else "missing",
                       "self_service", caveats, details))
    return out


SCENARIOS = {
    "kes_regression": {
        "title": "Сценарий A: индекс заказов по семействам",
        "description": "Синтетический ряд заказов двух семейств в двух окнах.",
    },
    "tracking_gap": {
        "title": "Сценарий B: метрика корзины",
        "description": "Синтетический ряд добавлений в корзину за окно.",
    },
    "mobile_regression": {
        "title": "Сценарий C: checkout по сегментам",
        "description": "Синтетический checkout rate по устройствам в двух окнах.",
    },
    "recommendation_ctr": {
        "title": "Сценарий D: блок рекомендаций",
        "description": "Синтетические показы и клики блока в двух окнах.",
    },
    "false_anomaly": {
        "title": "Сценарий E: последний день",
        "description": "Синтетическое значение заказов за последний день периода.",
    },
}


def sandbox_observations() -> list[dict]:
    sid = "aifence-sandbox-synthetic"
    out: list[dict] = []
    def add(scenario: str, product: str, metric: str, value: float | None, unit: str,
            start: str, end: str, segment: str = "all", status: str = "observed", caveats: list[str] | None = None):
        out.append(obs(product, metric, value, unit, start, end, sid, f"scenario:{scenario}", status, segment,
                       caveats, {"scenario_id": scenario, "synthetic": True}))
    # Equal seven-day windows make compare_periods deterministic.
    add("kes_regression", "kaspersky_endpoint_security", "orders", 150, "count", "2026-09-14", "2026-09-20")
    add("kes_regression", "kaspersky_endpoint_security", "orders", 100, "count", "2026-10-05", "2026-10-11")
    add("kes_regression", "astra_control", "orders", 90, "count", "2026-09-14", "2026-09-20")
    add("kes_regression", "astra_control", "orders", 87, "count", "2026-10-05", "2026-10-11")
    add("tracking_gap", "kaspersky_endpoint_security", "add_to_cart", None, "count", "2026-10-05", "2026-10-11", status="missing",
        caveats=["Synthetic scenario: no value recorded for this window."])
    add("mobile_regression", "store", "checkout_rate", 0.42, "ratio", "2026-10-05", "2026-10-11", "mobile")
    add("mobile_regression", "store", "checkout_rate", 0.33, "ratio", "2026-10-12", "2026-10-18", "mobile")
    add("mobile_regression", "store", "checkout_rate", 0.55, "ratio", "2026-10-05", "2026-10-11", "desktop")
    add("mobile_regression", "store", "checkout_rate", 0.56, "ratio", "2026-10-12", "2026-10-18", "desktop")
    add("recommendation_ctr", "store", "lk_reco_shown", 1000, "count", "2026-10-05", "2026-10-11")
    add("recommendation_ctr", "store", "lk_reco_click", 81, "count", "2026-10-05", "2026-10-11")
    add("recommendation_ctr", "store", "lk_reco_shown", 1250, "count", "2026-10-12", "2026-10-18")
    add("recommendation_ctr", "store", "lk_reco_click", 40, "count", "2026-10-12", "2026-10-18")
    add("false_anomaly", "store", "orders", None, "count", "2026-10-26", "2026-10-26", status="missing",
        caveats=["Period is not closed at snapshot time."])
    return out


def _xlsx_values(path: Path) -> dict[str, list[list[str]]]:
    """Small XLSX reader used only for provenance/row counts (no third-party deps)."""
    with zipfile.ZipFile(path) as z:
        shared: list[str] = []
        if "xl/sharedStrings.xml" in z.namelist():
            root = ET.fromstring(z.read("xl/sharedStrings.xml"))
            for si in root.findall("m:si", NS):
                shared.append("".join(t.text or "" for t in si.iter("{%s}t" % NS["m"])))
        wb = ET.fromstring(z.read("xl/workbook.xml"))
        rel = ET.fromstring(z.read("xl/_rels/workbook.xml.rels"))
        targets = {x.attrib["Id"]: x.attrib["Target"] for x in rel}
        out: dict[str, list[list[str]]] = {}
        for sheet in wb.findall("m:sheets/m:sheet", NS):
            name = sheet.attrib["name"]
            if name.strip().casefold() in {item.casefold() for item in HOLDOUT_SHEETS}:
                continue
            target = targets[sheet.attrib["{http://schemas.openxmlformats.org/officeDocument/2006/relationships}id"]]
            target = target.lstrip("/")
            if not target.startswith("xl/"):
                target = "xl/" + target
            root = ET.fromstring(z.read(target))
            rows = {}
            for row in root.findall("m:sheetData/m:row", NS):
                fields = {}
                for cell in row.findall("m:c", NS):
                    val = cell.find("m:v", NS)
                    text = "" if val is None else (val.text or "")
                    if cell.attrib.get("t") == "s" and text:
                        text = shared[int(text)]
                    elif cell.attrib.get("t") == "inlineStr":
                        text = "".join(t.text or "" for t in cell.findall(".//m:t", NS))
                    fields[re.match(r"[A-Z]+", cell.attrib["r"]).group()] = text
                rows[int(row.attrib["r"])] = fields
            out[name] = xlsx_rows(rows)
        return out


def xlsx_rows(rows, usable=lambda row: True):
    """Keep empty row/column positions so observations retain actual worksheet coordinates."""
    values = [[] for _ in range(max(rows, default=0))]
    for n, row in rows.items():
        if not usable(row) or any(h.casefold() in str(v).casefold() for v in row.values() for h in HOLDOUT_SHEETS):
            continue
        vals = values[n-1]
        for column, value in row.items():
            index = 0
            for ch in column:
                index = index * 26 + ord(ch) - ord("A") + 1
            vals.extend([""] * (index - len(vals)))
            vals[index - 1] = str(value)
    return values


def workbook_observations(sheets: dict[str, list[list[str]]]) -> list[dict]:
    rows = sheets.get("Метрики_данные", [])
    if not rows:
        return []
    header = rows[0]
    pos = {name: i for i, name in enumerate(header)}
    out: list[dict] = []
    for row_no, row in enumerate(rows[1:], 2):
        if not row:
            continue
        row = row + [""] * max(0, len(header) - len(row))
        if not row[pos["date"]] or not row[pos["metric_id"]]:
            continue
        date = row[pos["date"]]
        metric = row[pos["metric_id"]]
        product = row[pos["product"]] or "store"
        product_key = {"ЛК": "lk", "Конфигуратор Kaspersky": "kaspersky_configurator", "Конфигуратор Астра": "astra_configurator"}.get(product, product)
        unit = row[pos.get("unit", 7)] or "unknown"
        raw = row[pos.get("value", 6)]
        try:
            value = float(raw.replace(",", ".")) if raw else None
        except (ValueError, AttributeError):
            value = None
        status = "observed" if value is not None else "missing"
        comment = row[pos.get("comment", 10)] if len(row) > pos.get("comment", 10) else ""
        sample = row[pos.get("sample_size", 8)] if len(row) > pos.get("sample_size", 8) else ""
        details = {"workbook_sheet": "Метрики_данные", "workbook_row": row_no,
                   "release_id": row[pos.get("release_id", 5)] if len(row) > pos.get("release_id", 5) else "",
                   "period_type": row[pos.get("period_type", 4)] if len(row) > pos.get("period_type", 4) else "",
                   "sample_size": sample, "product_label": product, "synthetic": False}
        out.append(obs(product_key, metric, value, unit, date, date, "softline-workbook-imported",
                        f"Метрики_данные!row {row_no}", status, row[pos.get("segment", 3)] or "all",
                        [comment] if comment else [], details))
    return out


def build_db(workbook: Path | None = None) -> dict:
    DATA.mkdir(exist_ok=True)
    db_path = DATA / "aifence_sources.sqlite3"
    con = sqlite3.connect(db_path)  # rebuild in place: tables are dropped, the file is not deleted
    con.executescript("""
      drop table if exists source_catalog; drop table if exists raw_rows;
      drop table if exists observations; drop table if exists scenarios;
      create table source_catalog(source_id text primary key, source_kind text not null, label text not null,
        immutable integer not null, provenance_json text not null);
      create table raw_rows(source_id text not null, sheet_name text not null, row_number integer not null,
        row_json text not null, primary key(source_id,sheet_name,row_number));
      create table observations(id text primary key, source_id text not null, scenario_id text,
        product_key text not null, metric_key text not null, value real, unit text not null,
        date_from text not null, date_to text not null, segment text not null, status text not null,
        caveats_json text not null, locator text not null, details_json text not null);
      create table scenarios(scenario_id text primary key, title text not null, description text not null);
    """)
    workbook_rows = 0
    skipped_refs = 0
    sheet_counts: dict[str, int] = {}
    sheets: dict[str, list[list[str]]] = {}
    if workbook and workbook.exists():
        sheets = {k: v for k, v in _xlsx_values(workbook).items() if k not in HOLDOUT_SHEETS}
        for sheet, rows in sheets.items():
            sheet_counts[sheet] = len(rows)
            for n, row in enumerate(rows, 1):
                if not row:
                    continue
                if any(h in cell for h in HOLDOUT_SHEETS for cell in row):
                    skipped_refs += 1  # rows that name the answer-key sheet are not agent input
                    continue
                con.execute("insert into raw_rows values (?,?,?,?)",
                            ("softline-workbook-imported", sheet, n, json.dumps(row, ensure_ascii=False)))
                workbook_rows += 1
    workbook_ref = {}
    if workbook and workbook.exists():
        with workbook.open("rb") as source_file:
            workbook_ref = {"source_id": workbook.name, "revision": "sha256:" + hashlib.file_digest(source_file, "sha256").hexdigest()}
    provenance = {"kind": "imported_historical", "immutable": True, "workbook": workbook_ref,
                  "sheet_counts": sheet_counts,
                  "complete_workbook_available": bool(workbook and workbook.exists()),
                  "holdout_reference_rows_skipped": skipped_refs,
                  "snapshot_note": "Workbook rows and the weekly sales index are ingested when --workbook is supplied; holdout sheet excluded."}
    con.execute("insert into source_catalog values (?,?,?,?,?)",
                ("softline-workbook-imported", "imported_historical", "Softline workbook — immutable original", 1,
                 json.dumps(provenance, ensure_ascii=False)))
    sandbox_prov = {"kind": "synthetic_fixture", "immutable": True, "counter_id": COUNTER_ID,
                    "note": "Synthetic problem scenarios; never presented as customer evidence."}
    con.execute("insert into source_catalog values (?,?,?,?,?)",
                ("aifence-sandbox-synthetic", "synthetic_fixture", "AIfence sandbox scenarios", 1,
                 json.dumps(sandbox_prov, ensure_ascii=False)))
    for sid, data in SCENARIOS.items():
        con.execute("insert into scenarios values (?,?,?)", (sid, data["title"], data["description"]))
    imported = workbook_observations(sheets)
    sales = sales_index_observations(sheets)
    for item in imported + sales + sandbox_observations():
        source_id = item["source_ref"]["source_id"]
        details = item.get("details") or {}
        con.execute("insert into observations values (?,?,?,?,?,?,?,?,?,?,?,?,?,?)", (
            item["id"], source_id, details.get("scenario_id"), item["product_key"], item["metric_key"], item["value"],
            item["unit"], item["period"]["from"], item["period"]["to"], item.get("segment", "all"), item["status"],
            json.dumps(item.get("caveats", []), ensure_ascii=False), item["source_ref"]["locator"], json.dumps(details, ensure_ascii=False)))
    con.commit()
    con.close()
    for item in imported + sales:
        item["source_ref"].update(workbook_ref)
    original = evidence("original", imported + sales, [
        "Imported historical source: preserve as evidence; no live fallback.",
        "Kaspersky cart additions are unknown because T-08 tracking is absent; unknown is not zero.",
    ])
    sandbox = evidence("sandbox", sandbox_observations(), [
        "Synthetic problem scenarios only; never customer evidence.",
        "Live Yandex counter for recent replay/smoke events: " + str(COUNTER_ID) + ".",
    ])
    (DATA / "original-evidence.json").write_text(json.dumps(original, ensure_ascii=False, indent=2) + "\n")
    (DATA / "sandbox-evidence.json").write_text(json.dumps(sandbox, ensure_ascii=False, indent=2) + "\n")
    manifest = {"schema_version": "aifence.sources.v1", "counter_id": COUNTER_ID,
                "sources": [{"source_id": "softline-workbook-imported", "mode": "original", "kind": "imported_historical", "path": "original-evidence.json", "immutable": True},
                            {"source_id": "aifence-sandbox-synthetic", "mode": "sandbox", "kind": "synthetic_fixture", "path": "sandbox-evidence.json", "immutable": True}],
                "scenarios": SCENARIOS, "sqlite": "aifence_sources.sqlite3", "workbook_rows_ingested": workbook_rows,
                "sheet_counts": sheet_counts}
    (DATA / "source-manifest.json").write_text(json.dumps(manifest, ensure_ascii=False, indent=2) + "\n")
    return {"db": str(db_path), "original_observations": len(original["observations"]),
            "sandbox_observations": len(sandbox["observations"]), "workbook_rows": workbook_rows, "sheet_counts": sheet_counts}


def replay_plan() -> dict:
    """Return a safe Measurement Protocol plan; token is intentionally never stored."""
    events = []
    for item in sandbox_observations():
        if item["status"] != "observed" or item["value"] is None:
            continue
        events.append({"date": item["period"]["to"], "product_key": item["product_key"],
                       "metric_key": item["metric_key"], "value": item["value"],
                       "scenario_id": item.get("details", {}).get("scenario_id")})
    return {"counter_id": COUNTER_ID, "transport": "Yandex Measurement Protocol",
            "token_required": True, "token_storage": "interactive local prompt only",
            "historical_limit": "Yandex MP accepts recent events; original historical aggregates stay in SQLite/evidence-v1.",
            "events": events, "events_skipped": ["missing observations", "KES add_to_cart tracking gap"]}


def command(args: argparse.Namespace) -> int:
    if args.command == "build":
        print(json.dumps(build_db(Path(args.workbook) if args.workbook else None), ensure_ascii=False, indent=2))
    elif args.command == "replay-plan":
        print(json.dumps(replay_plan(), ensure_ascii=False, indent=2))
    elif args.command == "list-sources":
        print((DATA / "source-manifest.json").read_text())
    elif args.command == "query":
        path = DATA / ("original-evidence.json" if args.source == "original" else "sandbox-evidence.json")
        payload = json.loads(path.read_text())
        if args.scenario:
            payload["observations"] = [x for x in payload["observations"] if x.get("details", {}).get("scenario_id") == args.scenario]
        print(json.dumps(payload, ensure_ascii=False, indent=2))
    return 0


def main() -> int:
    p = argparse.ArgumentParser(description=__doc__)
    sub = p.add_subparsers(dest="command", required=True)
    b = sub.add_parser("build"); b.add_argument("--workbook", help="optional local XLSX for complete raw-row ingest")
    sub.add_parser("replay-plan")
    sub.add_parser("list-sources")
    q = sub.add_parser("query"); q.add_argument("--source", choices=["original", "sandbox"], required=True); q.add_argument("--scenario")
    return command(p.parse_args())


if __name__ == "__main__":
    raise SystemExit(main())
