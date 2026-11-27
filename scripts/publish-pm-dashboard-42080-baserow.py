#!/usr/bin/env python3
"""Project Product Manager hypotheses into a reproducible Baserow dashboard.

The default source is the owner's read-only Product Manager list_hypotheses tool.
For a repeatable demo, --fixture accepts a PM-shaped JSON export. Rows created
from a fixture carry the visible ДЕМО label; source values and links remain in
separate columns so synthetic origin is never mistaken for customer data.
"""

from __future__ import annotations

import argparse
import importlib.util
import json
import os
import subprocess
from pathlib import Path
from urllib.parse import urlparse


ROOT = Path(__file__).resolve().parents[1]
SYNC_PATH = ROOT / "apps/market-watch/baserow-sync/sync.py"
AIR = Path.home() / "AIfence/.runtime/bin/market-air"
URL = os.environ.get("AIRLOCK_URL", "http://localhost:42080")
DATABASE = "Market Watch"
TABLE = "Сигналы и гипотезы"

PRODUCT_NAMES = {
    "kaspersky_small_office_security": "Kaspersky Small Office Security",
    "kaspersky_endpoint_security": "Kaspersky Endpoint Security",
    "dr_web_desktop_security_suite": "Dr.Web Desktop Security Suite",
    "astra_linux": "Astra Linux",
    "myoffice": "МойОфис",
    "microsoft_365": "Microsoft 365",
}
CONFIDENCE = {"path_verified": 90, "correlation": 60, "signal_only": 35}
STATUS = {
    "proposed": "Предложена",
    "in_backlog": "В бэклоге",
    "supported": "Подтверждена",
    "awaiting_measurement": "Ждёт измерения",
    "rejected": "Отклонена",
}

FIELDS = (
    ("title", "Гипотеза", "text", 300),
    ("product", "Товар", "text", 210),
    ("status", "Статус", "text", 130),
    ("confidence", "Уверенность, %", "number", 120),
    ("evidence", "Уровень доказанности", "text", 150),
    ("delay", "Стоимость задержки, RUB/нед.", "number", 170),
    ("redmine", "Задача Redmine", "url", 155),
    ("pm_card", "Карточка PM", "url", 145),
    ("signal", "Сигнал", "text", 170),
    ("label", "Маркировка", "text", 95),
    ("source", "Источник PM", "text", 220),
    ("key", "Ключ строки", "text", 0),
)


def load_sync():
    spec = importlib.util.spec_from_file_location("market_baserow_sync", SYNC_PATH)
    module = importlib.util.module_from_spec(spec)
    assert spec and spec.loader
    spec.loader.exec_module(module)
    module.ROOT = ROOT
    module.SDK = AIR
    module.DIRECT_URL = URL
    return module


def invoke_pm() -> dict:
    result = subprocess.run(
        [
            str(AIR),
            "tools",
            "invoke",
            "list_hypotheses",
            "--json",
            "--agent",
            "product-manager",
            "--remote",
            "default",
            "--target",
            "production",
            "--url",
            URL,
            "--timeout",
            "60s",
        ],
        cwd=ROOT / "apps/product-manager",
        capture_output=True,
        text=True,
        check=True,
    )
    envelope = json.loads(result.stdout)
    return json.loads(envelope["data"]["result"]["output"])


def product_name(record: dict) -> str | None:
    hypothesis = record.get("hypothesis", {})
    key = hypothesis.get("product_key", "")
    if key in PRODUCT_NAMES:
        return PRODUCT_NAMES[key]
    title = hypothesis.get("title", "")
    for name in PRODUCT_NAMES.values():
        if name.casefold() in title.casefold():
            return name
    return None


def redmine_url(record: dict) -> str:
    candidates = [record.get("task", {}).get("url", ""), record.get("hypothesis", {}).get("backlog_ref", {}).get("url", "")]
    for value in candidates:
        parsed = urlparse(value)
        if parsed.scheme in ("http", "https") and ("redmine" in parsed.netloc or "/issues/" in parsed.path):
            return value
    return ""


def rows(payload: dict, label: str, source: str) -> tuple[list[dict], int]:
    output = []
    skipped = 0
    for record in payload.get("records", []):
        name = product_name(record)
        if not name:
            skipped += 1
            continue
        hypothesis = record.get("hypothesis", {})
        level = hypothesis.get("evidence_level") or "signal_only"
        assessment = record.get("assessment") or {}
        raw_status = assessment.get("status") or hypothesis.get("status", "")
        planning = record.get("planning") or {}
        delay = planning.get("weekly_potential_rub")
        if not isinstance(delay, (int, float)):
            delay = None
        card = record.get("card_url", "")
        if not card:
            card = hypothesis.get("backlog_ref", {}).get("url", "")
        signal_ids = hypothesis.get("signal_ids") or []
        hypothesis_id = hypothesis.get("id", "")
        key = f"{label}|{hypothesis_id}"
        output.append(
            {
                "Гипотеза": hypothesis.get("title", "Без названия"),
                "Товар": name,
                "Статус": STATUS.get(raw_status, raw_status or "Не указан"),
                "Уверенность, %": CONFIDENCE.get(level, 20),
                "Уровень доказанности": {"path_verified": "Путь проверен", "correlation": "Наблюдаемая связь", "signal_only": "Сигнал"}.get(level, "Не доказано"),
                "Стоимость задержки, RUB/нед.": delay,
                "Задача Redmine": redmine_url(record),
                "Карточка PM": card,
                "Сигнал": ", ".join(str(x) for x in signal_ids),
                "Маркировка": label,
                "Источник PM": source,
                "Ключ строки": key,
            }
        )
    return output, skipped


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
        table = sync.api("POST", tables_path, {"name": TABLE, "data": [["Гипотеза"]], "first_row_header": True})
    fields_path = f"database/fields/table/{table['id']}/"
    fields = {row["name"]: row for row in sync.api("GET", fields_path)}
    ids = {}
    for key, name, kind, _ in FIELDS:
        field = fields.get(name)
        if not field:
            payload = {"name": name, "type": kind}
            if kind == "number":
                payload.update(number_decimal_places=0, number_negative=False)
            field = sync.api("POST", fields_path, payload)
        if field["type"] != kind:
            raise RuntimeError(f"Baserow field {name!r} has type {field['type']!r}, expected {kind!r}")
        ids[key] = field["id"]
    views_path = f"database/views/table/{table['id']}/"
    views = sync.api("GET", views_path)
    default = next((row for row in views if row["name"] == TABLE), None)
    if not default:
        default = sync.api("POST", views_path, {"name": TABLE, "type": "grid"})
    for name in ("Требуют подтверждения", "Есть задача Redmine"):
        if not any(row["name"] == name for row in views):
            sync.api("POST", views_path, {"name": name, "type": "grid"})
    return table["id"], ids


def configure_views(sync, table_id, ids):
    path = f"database/views/table/{table_id}/"
    views = {row["name"]: row for row in sync.api("GET", path)}
    widths = {key: width for key, _, _, width in FIELDS}
    options = {
        str(ids[key]): {"width": width or 100, "hidden": not bool(width), "order": order}
        for order, (key, _, _, width) in enumerate(FIELDS, start=1)
    }
    for name, filter_rule in ((TABLE, None), ("Требуют подтверждения", ("confidence", "lower_than", "70")), ("Есть задача Redmine", ("redmine", "not_empty", ""))):
        view = views.get(name)
        if not view:
            continue
        view_id = view["id"]
        current = sync.api("GET", f"database/views/{view_id}/field-options/").get("field_options", {})
        if any(not isinstance(current.get(field_id), dict) or any(current[field_id].get(prop) != value for prop, value in option.items()) for field_id, option in options.items()):
            sync.api("PATCH", f"database/views/{view_id}/field-options/", {"field_options": options})
        filters_path = f"database/views/{view_id}/filters/"
        filters = sync.api("GET", filters_path)
        if filter_rule:
            field, kind, value = filter_rule
            if not any(item["field"] == ids[field] and item["type"] == kind and item.get("value", "") == value for item in filters):
                sync.api("POST", filters_path, {"field": ids[field], "type": kind, "value": value})
        sort_path = f"database/views/{view_id}/sortings/"
        sortings = sync.api("GET", sort_path)
        delay_sort = next((item for item in sortings if item["field"] == ids["delay"]), None)
        if not delay_sort:
            sync.api("POST", sort_path, {"field": ids["delay"], "order": "DESC"})


def publish(sync, table_id, rows_to_publish):
    base = f"database/rows/table/{table_id}/"
    existing = {}
    page = 1
    while True:
        payload = sync.api("GET", base + f"?user_field_names=true&size=200&page={page}")
        for row in payload.get("results", []):
            existing[row.get("Ключ строки")] = row
        if len(existing) >= payload.get("count", 0):
            break
        page += 1
    updated = created = 0
    for row in rows_to_publish:
        prior = existing.get(row["Ключ строки"])
        if prior:
            sync.api("PATCH", base + f"{prior['id']}/?user_field_names=true", row)
            updated += 1
        else:
            sync.api("POST", base + "?user_field_names=true", row)
            created += 1
    readback = sync.api("GET", base + "?user_field_names=true&size=200")
    visible = [row for row in readback.get("results", []) if row.get("Маркировка") == "ДЕМО"]
    keys = [row.get("Ключ строки") for row in visible]
    expected = {row["Ключ строки"] for row in rows_to_publish if row["Маркировка"] == "ДЕМО"}
    if not expected.issubset(set(keys)) or len(keys) != len(set(keys)):
        raise RuntimeError(f"Baserow read-back mismatch: expected demo keys {len(expected)}, got {len(keys)}")
    return {"rows": len(rows_to_publish), "created": created, "updated": updated, "demo_readback": len(visible), "table_id": table_id}


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture", type=Path, help="PM-shaped JSON export for a reproducible labelled demo")
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    fixture_path = args.fixture.resolve() if args.fixture else None
    payload = json.loads(fixture_path.read_text(encoding="utf-8")) if fixture_path else invoke_pm()
    label = "ДЕМО" if args.fixture else "PM"
    source = str(fixture_path.relative_to(ROOT)) if fixture_path else "product-manager:list_hypotheses"
    prepared, skipped = rows(payload, label, source)
    if args.dry_run:
        print(json.dumps({"rows": len(prepared), "skipped": skipped, "labels": sorted({r["Маркировка"] for r in prepared}), "sample": prepared[:2]}, ensure_ascii=False))
        return
    sync = load_sync()
    db_id = database(sync)
    table_id, ids = table_and_fields(sync, db_id)
    configure_views(sync, table_id, ids)
    result = publish(sync, table_id, prepared)
    result.update({"database": db_id, "skipped": skipped, "source": source})
    print(json.dumps(result, ensure_ascii=False))


if __name__ == "__main__":
    main()
