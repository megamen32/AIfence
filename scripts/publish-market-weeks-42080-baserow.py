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
BACKLOG_TABLE = "Задачи бэклога · Redmine"
SIGNALS_TABLE = "Сигналы и гипотезы"
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


def find_table(sync, db_id, name):
    tables = sync.api("GET", f"database/tables/database/{db_id}/")
    table = next((row for row in tables if row["name"] == name), None)
    if not table:
        raise RuntimeError(f"Baserow table {name!r} is unavailable")
    fields = {row["name"]: row for row in sync.api("GET", f"database/fields/table/{table['id']}/")}
    return table["id"], fields


def ensure_select(sync, table_id, fields, name, options):
    field = fields.get(name)
    if not field:
        field = sync.api("POST", f"database/fields/table/{table_id}/", {
            "name": name,
            "type": "single_select",
            "select_options": [{"value": value, "color": color} for value, color in options],
        })
        fields[name] = field
    if field["type"] != "single_select":
        raise RuntimeError(f"Baserow field {name!r} is not a single-select")
    return field["id"]


def rows_for(sync, table_id):
    return sync.api("GET", f"database/rows/table/{table_id}/?user_field_names=true&size=200")["results"]


def set_color_values(sync, table_id, rows, field_name, value_for):
    base = f"database/rows/table/{table_id}/"
    for row in rows:
        value = value_for(row)
        if value and row.get(field_name) != value:
            sync.api("PATCH", base + f"{row['id']}/?user_field_names=true", {field_name: value})


def ensure_view(sync, table_id, name, fields, *, filter_field=None, filter_type=None,
                filter_value="", group_field=None, sort_field=None, sort_order="ASC", field_map=None):
    path = f"database/views/table/{table_id}/"
    views = sync.api("GET", path)
    view = next((row for row in views if row["name"] == name), None)
    if not view:
        view = sync.api("POST", path, {"name": name, "type": "grid"})
    view_id = view["id"]
    field_options = {}
    if field_map is None:
        field_map = {row["name"]: row for row in sync.api("GET", f"database/fields/table/{table_id}/")}
    mapped_fields = fields_by_name(field_map or {})
    for order, (field_name, width, hidden) in enumerate(fields, start=1):
        if field_name in mapped_fields:
            field_options[str(mapped_fields[field_name])] = {
                "order": order, "width": width, "hidden": hidden,
            }
    if field_options:
        sync.api("PATCH", f"database/views/{view_id}/field-options/", {"field_options": field_options})
    if filter_field:
        existing = sync.api("GET", f"database/views/{view_id}/filters/")
        if not any(x["field"] == filter_field and x["type"] == filter_type and x.get("value", "") == filter_value for x in existing):
            sync.api("POST", f"database/views/{view_id}/filters/", {
                "field": filter_field, "type": filter_type, "value": filter_value,
            })
    if group_field:
        groups = sync.api("GET", f"database/views/{view_id}/group_bys/")
        if not any(x["field"] == group_field for x in groups):
            sync.api("POST", f"database/views/{view_id}/group_bys/", {"field": group_field, "order": "ASC", "width": 220})
    if sort_field:
        sorts = sync.api("GET", f"database/views/{view_id}/sortings/")
        if not any(x["field"] == sort_field for x in sorts):
            sync.api("POST", f"database/views/{view_id}/sortings/", {"field": sort_field, "order": sort_order})
    return view_id


def fields_by_name(fields):
    if isinstance(fields, dict):
        return {name: value["id"] if isinstance(value, dict) else value for name, value in fields.items()}
    return {}


def configure_visuals(sync, db_id, weekly_table_id, weekly_fields):
    weekly_color = ensure_select(sync, weekly_table_id, weekly_fields, "Изменение цены · цвет", [
        ("Снижение", "green"), ("Рост", "red"), ("Без изменения", "light-gray"),
    ])
    weekly_rows = rows_for(sync, weekly_table_id)
    set_color_values(sync, weekly_table_id, weekly_rows, "Изменение цены · цвет", lambda row: (
        "Снижение" if isinstance(row.get("Δ, %"), (int, float)) and row["Δ, %"] < 0 else
        "Рост" if isinstance(row.get("Δ, %"), (int, float)) and row["Δ, %"] > 0 else "Без изменения"
    ))
    weekly_by_name = {**weekly_fields, "Изменение цены · цвет": {"id": weekly_color}}
    ensure_view(sync, weekly_table_id, "Цены по неделям", [
        ("Лицензия", 270, False), ("Маркировка", 90, False), ("Продавец", 140, False),
        ("SKU", 125, False), ("Неделя с", 110, False), ("Неделя по", 110, False),
        ("Цена, RUB", 110, False), ("Δ, %", 80, False), ("Изменение цены · цвет", 140, False),
        ("Источник", 120, True), ("Ключ", 120, True),
    ], filter_field=weekly_by_name["Маркировка"]["id"], filter_type="equal", filter_value="ДЕМО",
       group_field=weekly_by_name["Продавец"]["id"], sort_field=weekly_by_name["Неделя с"]["id"], sort_order="ASC")
    ensure_view(sync, weekly_table_id, "Снижения цен", [
        ("Лицензия", 270, False), ("Продавец", 140, False), ("SKU", 125, False),
        ("Неделя с", 110, False), ("Цена, RUB", 110, False), ("Δ, %", 80, False),
        ("Изменение цены · цвет", 140, False), ("Маркировка", 90, False),
    ], filter_field=weekly_by_name["Δ, %"]["id"], filter_type="lower_than", filter_value="0",
       group_field=weekly_by_name["Продавец"]["id"], sort_field=weekly_by_name["Δ, %"]["id"], sort_order="ASC")

    backlog_id, backlog_fields = find_table(sync, db_id, BACKLOG_TABLE)
    pm_color = ensure_select(sync, backlog_id, backlog_fields, "Статус PM · цвет", [
        ("В бэклоге", "blue"), ("Поддержана", "green"), ("На рассмотрении", "orange"),
        ("Ожидает замера", "yellow"), ("Отклонена", "red"),
    ])
    redmine_color = ensure_select(sync, backlog_id, backlog_fields, "Статус Redmine · цвет", [
        ("Открыта", "blue"), ("Закрыта", "green"), ("Не найдена", "light-gray"),
    ])
    backlog_rows = rows_for(sync, backlog_id)
    set_color_values(sync, backlog_id, backlog_rows, "Статус PM · цвет", lambda row: {
        "in_backlog": "В бэклоге", "supported": "Поддержана", "proposed": "На рассмотрении",
        "awaiting_measurement": "Ожидает замера", "rejected": "Отклонена",
    }.get(row.get("Статус PM"), "На рассмотрении"))
    set_color_values(sync, backlog_id, backlog_rows, "Статус Redmine · цвет", lambda row: {
        "open": "Открыта", "closed": "Закрыта", "не найдено": "Не найдена",
    }.get(row.get("Статус Redmine"), "Не найдена"))
    bf = {**backlog_fields, "Статус PM · цвет": {"id": pm_color}, "Статус Redmine · цвет": {"id": redmine_color}}
    for name, filt, value in [("Задачи бэклога", None, ""), ("Открытые Redmine", bf["Статус Redmine · цвет"]["id"], "Открыта"), ("ДЕМО", bf["ДЕМО"]["id"], "true")]:
        ensure_view(sync, backlog_id, name, [
            ("Задача", 270, False), ("ДЕМО", 70, False), ("Статус PM · цвет", 130, False),
            ("Статус Redmine · цвет", 140, False), ("Redmine ID", 90, False),
            ("Цена задержки, ₽/нед", 130, False), ("Сигнал", 190, False),
            ("Уверенность", 120, False), ("Ссылка Redmine", 120, True), ("ID гипотезы", 120, True),
        ], filter_field=filt, filter_type="single_select_equal" if name == "Открытые Redmine" else ("boolean" if name == "ДЕМО" else None), filter_value=value,
           group_field=bf["Статус PM · цвет"]["id"], sort_field=bf["Статус Redmine · цвет"]["id"], sort_order="ASC")

    signals_id, signals_fields = find_table(sync, db_id, SIGNALS_TABLE)
    signal_color = ensure_select(sync, signals_id, signals_fields, "Статус · цвет", [
        ("В бэклоге", "blue"), ("Предложена", "orange"), ("Ждёт измерения", "yellow"),
        ("Подтверждена", "green"), ("Отклонена", "red"),
    ])
    signal_rows = rows_for(sync, signals_id)
    set_color_values(sync, signals_id, signal_rows, "Статус · цвет", lambda row: row.get("Статус", "Предложена"))
    sf = {**signals_fields, "Статус · цвет": {"id": signal_color}}
    ensure_view(sync, signals_id, "Сигналы и гипотезы", [
        ("Гипотеза", 330, False), ("Товар", 190, False), ("Статус · цвет", 140, False),
        ("Уверенность, %", 100, False), ("Уровень доказанности", 150, False),
        ("Стоимость задержки, RUB/нед.", 150, False), ("Сигнал", 190, False),
        ("Маркировка", 90, False), ("Источник PM", 220, False),
        ("Задача Redmine", 120, True), ("Карточка PM", 120, True), ("Ключ строки", 120, True),
    ], group_field=sf["Статус · цвет"]["id"], sort_field=sf["Стоимость задержки, RUB/нед."]["id"], sort_order="DESC")
    ensure_view(sync, signals_id, "Требуют подтверждения", [
        ("Гипотеза", 330, False), ("Товар", 190, False), ("Статус · цвет", 140, False),
        ("Уверенность, %", 100, False), ("Уровень доказанности", 150, False),
        ("Стоимость задержки, RUB/нед.", 150, False), ("Сигнал", 190, False), ("Маркировка", 90, False),
    ], filter_field=sf["Статус · цвет"]["id"], filter_type="single_select_equal", filter_value="Предложена",
       group_field=sf["Статус · цвет"]["id"], sort_field=sf["Уверенность, %"]["id"], sort_order="ASC")
    ensure_view(sync, signals_id, "Есть задача Redmine", [
        ("Гипотеза", 330, False), ("Товар", 190, False), ("Статус · цвет", 140, False),
        ("Уверенность, %", 100, False), ("Задача Redmine", 140, False), ("Маркировка", 90, False),
        ("Источник PM", 220, False),
    ], filter_field=sf["Задача Redmine"]["id"], filter_type="not_empty", filter_value="",
       group_field=sf["Статус · цвет"]["id"], sort_field=sf["Стоимость задержки, RUB/нед."]["id"], sort_order="DESC")

    return {"weekly": weekly_table_id, "backlog": backlog_id, "signals": signals_id}


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
    visuals = configure_visuals(sync, db_id, table_id, {name: {"id": value} for key, name, _ in FIELDS for value in [ids[key]]})
    count, updated, readback = publish(sync, table_id, ids, new_rows)
    print(json.dumps({"database": db_id, "table": table_id, "visual_tables": visuals, "rows": count, "readback": readback, "updated": updated, "created": count - updated}, ensure_ascii=False))


if __name__ == "__main__":
    main()
