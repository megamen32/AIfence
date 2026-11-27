#!/usr/bin/env python3
"""Project Product Manager backlog records into the owner's Baserow database."""

from __future__ import annotations

import argparse
import datetime as dt
import html
import json
import os
import re
import subprocess
import sys
from pathlib import Path


ROOT = Path(__file__).resolve().parents[1]
RUNTIME_ROOT = Path(os.environ.get("AIFENCE_RUNTIME_ROOT", ROOT / ".runtime"))
SDK = Path(os.environ.get("AIFENCE_AIRLOCK", RUNTIME_ROOT / "bin" / "market-air"))
ORIGIN = os.environ.get("AIFENCE_ORIGIN", "http://localhost:42080")
DATABASE = "Market Watch"
TABLE = "Задачи бэклога · Redmine"


def airlock(*args: str, agent: str) -> dict:
    command = [str(SDK), *args, "--agent", agent, "--remote", "default", "--target", "production", "--url", ORIGIN]
    completed = subprocess.run(command, cwd=ROOT, check=True, capture_output=True, text=True)
    return json.loads(completed.stdout)


def result_output(document: dict) -> object:
    return json.loads(document["data"]["result"]["output"])


def redmine_rows() -> list[dict]:
    document = airlock("request", "GET", "/delivery", "--json", agent="product-manager")
    body = document["data"]["body"]
    rows = []
    pattern = re.compile(
        r'<span class="(?P<status>ok|warn)">#(?P<id>\d+)</span>\s*'
        r'(?P<title>.*?)</span>.*?href="(?P<url>[^"]+/issues/(?P=id))"',
        re.S,
    )
    for match in pattern.finditer(body):
        title = html.unescape(re.sub(r"<[^>]+>", "", match.group("title"))).strip()
        rows.append({
            "redmine_id": int(match.group("id")),
            "redmine_status": "open" if match.group("status") == "ok" else "needs_attention",
            "redmine_title": title,
            "redmine_url": html.unescape(match.group("url")),
        })
    return rows


def safe_title(title: str, seeded: bool) -> str:
    if not seeded:
        return title
    return "ДЕМО · задача Product Manager"


def backlog_rows() -> list[dict]:
    records = result_output(airlock("tools", "invoke", "list_hypotheses", "--json", "--input", "{}", agent="product-manager"))["records"]
    redmine = redmine_rows()
    by_title = {row["redmine_title"]: row for row in redmine}
    output = []
    now = dt.datetime.now(dt.timezone.utc).isoformat(timespec="seconds").replace("+00:00", "Z")
    for record in records:
        hypothesis = record.get("hypothesis", {})
        task = record.get("task") or {}
        title = task.get("title") or hypothesis.get("title") or "Задача Product Manager"
        seeded = any(token in json.dumps(record, ensure_ascii=False) for token in ("synthetic", "fixture", "demo_", "qa/"))
        issue = by_title.get(title)
        if issue is None:
            issue = next((row for row in redmine if title in row["redmine_title"]), None)
        output.append({
            "Задача": safe_title(title, seeded),
            "ДЕМО": seeded,
            "Статус PM": hypothesis.get("status", "unknown"),
            "Статус Redmine": issue["redmine_status"] if issue else "не найдено",
            "Redmine ID": issue["redmine_id"] if issue else None,
            "Ссылка Redmine": issue["redmine_url"] if issue else "",
            "Уверенность": hypothesis.get("evidence_level", "не указана"),
            "Цена задержки, ₽/нед": (record.get("planning") or {}).get("weekly_potential_rub"),
            "Сигнал": (hypothesis.get("signal_ids") or [""])[0],
            "ID гипотезы": hypothesis.get("id", ""),
            "Обновлено UTC": now[:10],
        })
    return output


def api(method: str, path: str, body: object | None = None) -> object:
    payload = json.dumps(body, ensure_ascii=False, separators=(",", ":")) if body is not None else None
    document = airlock("request", method, "/api/" + path, "--json", *( ["--header", "Content-Type: application/json", "--data", payload] if payload else [] ), agent="baserow")
    status = document["data"]["statusCode"]
    if status >= 300:
        raise RuntimeError(f"Baserow API returned HTTP {status}")
    return json.loads(document["data"]["body"])


def one(rows: list[dict], name: str) -> dict | None:
    matches = [row for row in rows if row.get("name") == name]
    if len(matches) > 1:
        raise RuntimeError(f"duplicate Baserow object: {name}")
    return matches[0] if matches else None


def sync() -> dict:
    databases = [row for row in api("GET", "applications/") if row.get("type") == "database"]
    database = one(databases, DATABASE)
    if not database:
        raise RuntimeError(f"Baserow database not found: {DATABASE}")
    table_path = f"database/tables/database/{database['id']}/"
    table = one(api("GET", table_path), TABLE)
    if not table:
        table = api("POST", table_path, {"name": TABLE, "data": [["Задача"]], "first_row_header": True})
    table_id = table["id"]
    fields_path = f"database/fields/table/{table_id}/"
    definitions = {
        "ДЕМО": "boolean",
        "Статус PM": "text",
        "Статус Redmine": "text",
        "Redmine ID": "number",
        "Ссылка Redmine": "url",
        "Уверенность": "text",
        "Цена задержки, ₽/нед": "number",
        "Сигнал": "text",
        "ID гипотезы": "text",
        "Обновлено UTC": "date",
    }
    fields = {row["name"]: row for row in api("GET", fields_path)}
    for name, kind in definitions.items():
        if name not in fields:
            fields[name] = api("POST", fields_path, {"name": name, "type": kind})
        if fields[name]["type"] != kind:
            raise RuntimeError(f"Baserow field type changed: {name}")
    rows = backlog_rows()
    row_path = f"database/rows/table/{table_id}/"
    existing = api("GET", row_path + "?user_field_names=true&size=200")["results"]
    for value in rows:
        matches = [row for row in existing if row.get("ID гипотезы") == value["ID гипотезы"]]
        if len(matches) > 1:
            raise RuntimeError(f"duplicate backlog row: {value['ID гипотезы']}")
        if matches:
            api("PATCH", row_path + str(matches[0]["id"]) + "/?user_field_names=true", value)
        else:
            api("POST", row_path + "?user_field_names=true", value)
    after = api("GET", row_path + "?user_field_names=true&size=200")["results"]
    for value in rows:
        match = next(row for row in after if row.get("ID гипотезы") == value["ID гипотезы"])
        for key in ("Задача", "Статус PM", "Статус Redmine", "ID гипотезы"):
            if match.get(key) != value[key]:
                raise RuntimeError(f"Baserow read-back mismatch: {key}")
    views_path = f"database/views/table/{table_id}/"
    views = api("GET", views_path)
    view_ids = {}
    for name in ("Задачи бэклога", "Открытые Redmine", "ДЕМО"):
        view = one(views, name)
        if not view:
            view = api("POST", views_path, {"name": name, "type": "grid"})
        view_ids[name] = view["id"]
    return {"database": database["id"], "table": table_id, "views": view_ids, "rows": len(rows), "fields": {name: field["id"] for name, field in fields.items()}}


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--json", action="store_true")
    parser.parse_args()
    print(json.dumps(sync(), ensure_ascii=False, sort_keys=True))
