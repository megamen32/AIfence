#!/usr/bin/env python3
"""Publish the 42080 KES demo price history as a native Baserow table."""

from __future__ import annotations

import argparse
import importlib.util
import json
import subprocess
from datetime import date, timedelta
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
SYNC_PATH = ROOT / "apps/market-watch/baserow-sync/sync.py"
AIR = Path.home() / "AIfence/.runtime/bin/market-air"
APP_DIR = Path.home() / "AIfence/apps/market-watch"
MARKET_URL = "http://localhost:42080"
MARKET_AGENT = "market-watch"
DATABASE = "Market Watch"
TABLE = "Цены по неделям"
SKU = "KL4863RAKFS"
FROM = "2026-11-09"
TO = "2026-11-22"

FIELDS = (
    ("license", "Лицензия", "text"),
    ("label", "Маркировка", "text"),
    ("seller", "Продавец", "text"),
    ("sku", "SKU", "text"),
    ("week_from", "Неделя с", "date"),
    ("week_to", "Неделя по", "date"),
    ("price", "Цена, RUB", "number"),
    ("delta", "Δ, %", "number"),
    ("source", "Источник", "url"),
    ("key", "Ключ", "text"),
)


def load_sync():
    spec = importlib.util.spec_from_file_location("market_baserow_sync", SYNC_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec and spec.loader
    spec.loader.exec_module(module)
    # The native Baserow helper is kept in the shared protected checkout and
    # runtime, while this reproducible script is committed in the worktree.
    module.ROOT = Path.home() / "AIfence"
    module.SDK = AIR
    module.DIRECT_URL = MARKET_URL
    return module


def market_history() -> dict:
    args = [
        str(AIR), "tools", "invoke", "price_history", "--json",
        "--agent", MARKET_AGENT, "--remote", "default", "--target", "production",
        "--url", MARKET_URL, "--input", json.dumps({
            "product_key": SKU, "from": FROM, "to": TO,
        }, ensure_ascii=False),
    ]
    result = subprocess.run(args, cwd=APP_DIR, capture_output=True, text=True, check=True)
    envelope = json.loads(result.stdout)
    return json.loads(envelope["data"]["result"]["output"])


def database(sync):
    databases = [row for row in sync.api("GET", "applications/") if row["type"] == "database"]
    match = next((row for row in databases if row["name"] == DATABASE), None)
    if not match:
        raise RuntimeError(f"Baserow database {DATABASE!r} is unavailable")
    return match["id"]


def table_and_fields(sync, db_id):
    tables_path = f"database/tables/database/{db_id}/"
    tables = sync.api("GET", tables_path)
    table = next((row for row in tables if row["name"] == TABLE), None)
    if not table:
        table = sync.api("POST", tables_path, {
            "name": TABLE,
            "data": [["Лицензия"]],
            "first_row_header": True,
        })
    fields_path = f"database/fields/table/{table['id']}/"
    fields = {row["name"]: row for row in sync.api("GET", fields_path)}
    ids = {}
    for key, name, kind in FIELDS:
        field = fields.get(name)
        if not field:
            payload = {"name": name, "type": kind}
            if kind == "number":
                payload.update(number_decimal_places=2, number_negative=True)
            if kind == "date":
                payload.update(date_include_time=False, date_format="EU", date_time_format="24")
            field = sync.api("POST", fields_path, payload)
        if field["type"] != kind:
            raise RuntimeError(f"Baserow field {name!r} has type {field['type']!r}, expected {kind!r}")
        ids[key] = field["id"]
    views_path = f"database/views/table/{table['id']}/"
    views = sync.api("GET", views_path)
    view = next((row for row in views if row["name"] == TABLE), None)
    if not view:
        view = sync.api("POST", views_path, {"name": TABLE, "type": "grid"})
    return table["id"], ids


def rows(history):
    result = []
    for series in history["series"]:
        offer = series["offer"]
        points = series["points"]
        if len(points) != 2:
            raise RuntimeError(f"Expected two observations for {offer['details']['name']}")
        previous = points[0]
        previous_price = previous["value"]
        seller = offer["details"]["name"].rsplit(" — ", 1)[-1]
        for point in points:
            day = date.fromisoformat(point["captured_at"][:10])
            week_from = day - timedelta(days=day.weekday())
            week_to = week_from + timedelta(days=6)
            delta = None
            if point is not previous:
                delta = round((point["value"] - previous_price) / previous_price * 100, 2)
            result.append({
                "Лицензия": offer["details"]["name"],
                "Маркировка": "ДЕМО",
                "Продавец": seller,
                "SKU": offer["product_key"],
                "Неделя с": week_from.isoformat(),
                "Неделя по": week_to.isoformat(),
                "Цена, RUB": point["value"],
                "Δ, %": delta,
                "Источник": offer["source_ref"]["url"],
                "Ключ": f"{seller}|{offer['product_key']}|{week_from.isoformat()}",
            })
    return result


def publish(sync, table_id, ids, new_rows):
    base = f"database/rows/table/{table_id}/"
    field_ids = {"field_" + str(value): name for key, name, _ in FIELDS for value in [ids[key]]}
    existing = {}
    page = 1
    while True:
        payload = sync.api("GET", base + f"?user_field_names=true&size=200&page={page}")
        for row in payload["results"]:
            existing[row.get("Ключ")] = row
        if len(existing) >= payload["count"]:
            break
        page += 1
    for row in new_rows:
        prior = existing.get(row["Ключ"])
        if prior:
            sync.api("PATCH", base + f"{prior['id']}/?user_field_names=true", row)
        else:
            sync.api("POST", base + "?user_field_names=true", row)
    readback = sync.api("GET", base + "?user_field_names=true&size=200")
    visible = [row for row in readback["results"] if row.get("Маркировка") == "ДЕМО"]
    keys = [row.get("Ключ") for row in visible]
    if len(visible) != len(new_rows) or len(set(keys)) != len(keys):
        raise RuntimeError(f"Baserow read-back mismatch: expected {len(new_rows)} unique ДЕМО rows, got {len(visible)}")
    if any(row.get("SKU") != SKU for row in visible):
        raise RuntimeError("Baserow read-back contains an unexpected SKU")
    return len(new_rows), sum(1 for row in new_rows if row["Ключ"] in existing), len(visible)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    history = market_history()
    new_rows = rows(history)
    if args.dry_run:
        print(json.dumps({"rows": len(new_rows), "labels": sorted({r["Маркировка"] for r in new_rows}), "sample": new_rows[:2]}, ensure_ascii=False))
        return
    sync = load_sync()
    db_id = database(sync)
    table_id, ids = table_and_fields(sync, db_id)
    count, updated, readback = publish(sync, table_id, ids, new_rows)
    print(json.dumps({"database": db_id, "table": table_id, "rows": count, "readback": readback, "updated": updated, "created": count - updated}, ensure_ascii=False))


if __name__ == "__main__":
    main()
