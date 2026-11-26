#!/usr/bin/env python3
"""Generate blind, deterministic AIfence product-analytics datasets.

The generator creates one internal truth and derives three artifacts from it:
Yandex-Metrika-like evidence, a customer-shaped ``Продажи`` workbook, and
release/backlog context.  Scenario answers live outside the agent input tree.
Only the standard library is used, including a tiny XLSX writer.
"""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import math
import random
import statistics
import time
import urllib.error
import urllib.parse
import urllib.request
import zipfile
from pathlib import Path
from xml.etree import ElementTree as ET

PRODUCTS = {
    "KES": {"label": "Kaspersky Endpoint Security", "visits": 1000, "view_rate": .72, "cart_rate": .21, "checkout_rate": .56, "order_rate": .76, "aov": 14800},
    "Astra": {"label": "Astra Linux Special Edition", "visits": 920, "view_rate": .70, "cart_rate": .20, "checkout_rate": .58, "order_rate": .76, "aov": 13200},
    "Office": {"label": "Microsoft 365", "visits": 1280, "view_rate": .73, "cart_rate": .22, "checkout_rate": .60, "order_rate": .78, "aov": 17100},
    "VMware": {"label": "VMware vSphere", "visits": 720, "view_rate": .68, "cart_rate": .18, "checkout_rate": .55, "order_rate": .74, "aov": 22500},
    "DrWeb": {"label": "Dr.Web Enterprise", "visits": 610, "view_rate": .69, "cart_rate": .19, "checkout_rate": .57, "order_rate": .75, "aov": 8900},
}
DEVICES = {"desktop": .76, "mobile": .24}
METRICS = ("visits", "card_views", "cart_additions", "checkouts", "orders", "revenue")
SCENARIOS = ("S0", "S1", "S2", "S3", "S4")
START = dt.date(2026, 1, 5)
MP_ENDPOINT = "https://mc.yandex.ru/collect"
DEFAULT_COUNTER = "114698578"


def stable_id(*values: object, size: int = 16) -> str:
    raw = json.dumps(values, ensure_ascii=False, sort_keys=True).encode()
    return hashlib.sha256(raw).hexdigest()[:size]


def week_start(i: int) -> dt.date:
    return START + dt.timedelta(days=7 * i)


def parse_week(value: str) -> int:
    return int(value.replace("W", "").replace("w", ""))


def product_key(family: str) -> str:
    return family.lower()


def round_value(metric: str, value: float) -> int | float:
    if metric == "revenue":
        return round(max(value, 0.0), 2)
    return max(0, int(round(value)))


def pick_scenario(seed: int, scenario: str) -> dict:
    rng = random.Random(f"{seed}:{scenario}:choice")
    family = rng.choice(list(PRODUCTS))
    week = rng.randint(8, 20)
    severity = round(rng.uniform(.18, .38), 3)
    return {"scenario": scenario, "family": family, "week": week, "severity": severity,
            "cause_type": {"S0": "none", "S1": "tracking_gap", "S2": "mobile_regression",
                           "S3": "price_competition", "S4": "release_regression"}[scenario]}


def truth_rows(seed: int, scenario: str, weeks: int = 28) -> tuple[list[dict], dict]:
    if weeks < 26:
        raise ValueError("weeks must be >= 26")
    choice = pick_scenario(seed, scenario)
    rng = random.Random(f"{seed}:{scenario}:truth")
    rows: list[dict] = []
    for w in range(weeks):
        seasonal = 1.0 + .075 * math.sin((2 * math.pi * (w + 1)) / 13) + .035 * math.cos((2 * math.pi * w) / 7)
        year_end = 1.0 + (.08 if w in (24, 25) else 0.0)
        for family, base in PRODUCTS.items():
            for device, share in DEVICES.items():
                noise = max(.84, min(1.18, rng.gauss(1.0, .055)))
                visits = base["visits"] * share * seasonal * year_end * noise
                effect = 1.0
                if scenario == "S2" and family == choice["family"] and w >= choice["week"] and device == "mobile":
                    effect = 1 - choice["severity"]
                elif scenario == "S3" and family == choice["family"] and w >= choice["week"]:
                    # The price signal changes conversion, not traffic.
                    effect = 1.0
                elif scenario == "S4" and family == choice["family"] and w >= choice["week"]:
                    effect = 1 - choice["severity"]
                visits *= effect
                unit_price = base["aov"] * max(.94, min(1.06, rng.gauss(1, .018)))
                competitor_price = base["aov"] * .96 * max(.96, min(1.04, rng.gauss(1, .012)))
                if scenario == "S3" and family == choice["family"] and w >= choice["week"]:
                    unit_price *= 1 + choice["severity"]
                views = visits * base["view_rate"] * max(.88, min(1.12, rng.gauss(1, .035)))
                cart = views * base["cart_rate"] * max(.88, min(1.12, rng.gauss(1, .04)))
                checkouts = cart * base["checkout_rate"] * max(.88, min(1.12, rng.gauss(1, .045)))
                if scenario == "S3" and family == choice["family"] and w >= choice["week"]:
                    checkouts *= 1 - choice["severity"] * .9
                orders = checkouts * base["order_rate"] * max(.88, min(1.12, rng.gauss(1, .04)))
                revenue = orders * unit_price * max(.93, min(1.07, rng.gauss(1, .025)))
                rows.append({"week": w, "week_start": week_start(w).isoformat(), "family": family, "device": device,
                             "visits": round_value("visits", visits), "card_views": round_value("card_views", views),
                             "cart_additions": round_value("cart_additions", cart), "checkouts": round_value("checkouts", checkouts),
                             "orders": round_value("orders", orders), "revenue": round_value("revenue", revenue),
                             "unit_price": round_value("revenue", unit_price), "competitor_price": round_value("revenue", competitor_price)})
    return rows, choice


def loss_factor(rng: random.Random, metric: str) -> float:
    # Behaviour events intentionally lose 10–25% in the Metrika view.
    if metric in ("visits", "card_views", "cart_additions", "checkouts"):
        return rng.uniform(.75, .90)
    if metric == "orders":
        return rng.uniform(.82, .95)
    return rng.uniform(.90, .99)


def evidence_rows(truth: list[dict], seed: int, scenario: str, choice: dict) -> tuple[dict, list[float]]:
    rng = random.Random(f"{seed}:{scenario}:metrika")
    observations: list[dict] = []
    losses: list[float] = []
    for row in truth:
        for metric in METRICS:
            period_from = row["week_start"]
            period_to = (dt.date.fromisoformat(period_from) + dt.timedelta(days=6)).isoformat()
            source = {"source_id": "blind-metrika", "locator": f"week={row['week']};family={row['family']};device={row['device']}", "revision": f"seed:{seed}"}
            status = "observed"
            caveats: list[str] = ["Synthetic measurement with event undercount; aggregate only."]
            value: float | None = float(row[metric]) * loss_factor(rng, metric)
            if scenario == "S1" and row["family"] == choice["family"] and row["week"] >= choice["week"] and metric == "cart_additions":
                value = None
                status = "missing"
                caveats = ["Event collection is incomplete for this period; absence is not zero."]
            if value is not None:
                factor = value / max(float(row[metric]), 1.0)
                if metric in ("visits", "card_views", "cart_additions", "checkouts"):
                    losses.append(1 - factor)
                value = round_value(metric, value)
            item = {"id": "obs-" + stable_id(seed, scenario, row["week"], row["family"], row["device"], metric),
                    "kind": "metric", "product_key": product_key(row["family"]), "metric_key": metric,
                    "value": value, "unit": "RUB" if metric == "revenue" else "count",
                    "period": {"from": period_from, "to": period_to}, "segment": row["device"],
                    "source_ref": source, "status": status, "caveats": caveats,
                    "details": {"device": row["device"], "synthetic": True}}
            observations.append(item)
    return {"schema_version": "aifence.v1", "source": "metrika", "as_of": (week_start(len(set(r['week'] for r in truth)) - 1) + dt.timedelta(days=6)).isoformat() + "T23:59:59Z", "status": "partial" if scenario == "S1" else "ok", "observations": observations,
            "warnings": ["Blind synthetic dataset; aggregate week × family × device only.", "Metrika behaviour events include 10–25% measurement loss."]}, losses


def releases_and_backlog(seed: int, scenario: str, choice: dict, weeks: int) -> tuple[list[dict], list[dict]]:
    rng = random.Random(f"{seed}:{scenario}:context")
    release_rows = []
    for i in range(max(weeks - 3, 24)):
        w = i % weeks
        family = rng.choice(list(PRODUCTS))
        release_rows.append({"release_id": f"R-{i+1:02d}", "release_date": week_start(w).isoformat(), "release_type": rng.choice(["minor", "major", "technical"]),
                             "description": rng.choice(["Обновление интерфейса каталога", "Изменение карточки продукта", "Техническое обновление checkout", "Обновление интеграции цен", "Исправление отображения лицензий"]),
                             "related_task_ids": f"T-{i+1:02d}", "affected_metrics": rng.choice(["cfg_family_views", "cart_additions", "checkouts", "orders", "revenue"]), "status": "выпущен", "family": family,
                             "our_price_rub": round(PRODUCTS[family]["aov"] * rng.uniform(.95, 1.05)), "competitor_price_rub": round(PRODUCTS[family]["aov"] * .96 * rng.uniform(.96, 1.04))})
    # A release is placed at the hidden week, but descriptions remain generic.
    target = release_rows[choice["week"] % len(release_rows)]
    target["release_date"] = week_start(choice["week"]).isoformat()
    target["family"] = choice["family"]
    if scenario == "S3":
        target["our_price_rub"] = round(PRODUCTS[choice["family"]]["aov"] * (1 + choice["severity"]))
        target["competitor_price_rub"] = round(PRODUCTS[choice["family"]]["aov"] * .94)
    tasks = []
    for i in range(max(weeks - 5, 22)):
        family = rng.choice(list(PRODUCTS))
        tasks.append({"task_id": f"T-{i+1:02d}", "title": rng.choice(["Улучшить путь выбора лицензии", "Проверить события электронной торговли", "Сверить цены в каталоге", "Проверить мобильный checkout", "Уточнить карточку семейства"]),
                      "type": rng.choice(["Фича", "Улучшение", "Технический долг"]), "status": rng.choice(["Done", "In Progress", "Backlog"]),
                      "priority": rng.choice(["Low", "Medium", "High"]), "created_date": week_start(max(0, i - 3)).isoformat(),
                      "closed_date": week_start(i).isoformat() if i % 3 == 0 else "", "release_id": f"R-{i+1:02d}",
                      "description_short": "Проверить полноту и качество продуктового пути", "linked_metric_ids": rng.choice(["cfg_family_views", "cart_additions", "checkouts", "orders"]), "family": family})
    return release_rows, tasks


def xml_cell(ref: str, value: object) -> ET.Element:
    cell = ET.Element("c", {"r": ref})
    if isinstance(value, (int, float)) and not isinstance(value, bool):
        v = ET.SubElement(cell, "v")
        v.text = str(value)
    else:
        cell.set("t", "inlineStr")
        inline = ET.SubElement(cell, "is")
        t = ET.SubElement(inline, "t")
        t.text = "" if value is None else str(value)
    return cell


def write_xlsx(path: Path, sheets: dict[str, list[dict]]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    ns = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
    rel_ns = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
    ET.register_namespace("", ns); ET.register_namespace("r", rel_ns)
    files: dict[str, bytes] = {}
    names = list(sheets)
    workbook = ET.Element(f"{{{ns}}}workbook")
    shs = ET.SubElement(workbook, f"{{{ns}}}sheets")
    for i, name in enumerate(names, 1):
        ET.SubElement(shs, f"{{{ns}}}sheet", {"name": name, "sheetId": str(i), f"{{{rel_ns}}}id": f"rId{i}"})
        root = ET.Element(f"{{{ns}}}worksheet"); data = ET.SubElement(root, f"{{{ns}}}sheetData")
        rows = sheets[name]
        headers = list(rows[0]) if rows else []
        for rn, values in enumerate([headers] + [[r.get(h, "") for h in headers] for r in rows], 1):
            row = ET.SubElement(data, f"{{{ns}}}row", {"r": str(rn)})
            for col, value in enumerate(values):
                letters = ""; n = col + 1
                while n: n, rem = divmod(n - 1, 26); letters = chr(65 + rem) + letters
                row.append(xml_cell(f"{letters}{rn}", value))
        files[f"xl/worksheets/sheet{i}.xml"] = ET.tostring(root, encoding="utf-8", xml_declaration=True)
    files["xl/workbook.xml"] = ET.tostring(workbook, encoding="utf-8", xml_declaration=True)
    rels = ET.Element("Relationships", {"xmlns": "http://schemas.openxmlformats.org/package/2006/relationships"})
    for i in range(1, len(names) + 1): ET.SubElement(rels, "Relationship", {"Id": f"rId{i}", "Type": "http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet", "Target": f"worksheets/sheet{i}.xml"})
    files["xl/_rels/workbook.xml.rels"] = ET.tostring(rels, encoding="utf-8", xml_declaration=True)
    root_rels = ET.Element("Relationships", {"xmlns": "http://schemas.openxmlformats.org/package/2006/relationships"})
    ET.SubElement(root_rels, "Relationship", {"Id": "rId1", "Type": "http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument", "Target": "xl/workbook.xml"})
    files["_rels/.rels"] = ET.tostring(root_rels, encoding="utf-8", xml_declaration=True)
    types = ET.Element("Types", {"xmlns": "http://schemas.openxmlformats.org/package/2006/content-types"})
    ET.SubElement(types, "Default", {"Extension": "rels", "ContentType": "application/vnd.openxmlformats-package.relationships+xml"})
    ET.SubElement(types, "Default", {"Extension": "xml", "ContentType": "application/xml"})
    ET.SubElement(types, "Override", {"PartName": "/xl/workbook.xml", "ContentType": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"})
    for i in range(1, len(names) + 1): ET.SubElement(types, "Override", {"PartName": f"/xl/worksheets/sheet{i}.xml", "ContentType": "application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"})
    files["[Content_Types].xml"] = ET.tostring(types, encoding="utf-8", xml_declaration=True)
    with zipfile.ZipFile(path, "w", zipfile.ZIP_DEFLATED) as z:
        # ZipFile defaults to current wall-clock timestamps, which would make
        # identical seeds produce different workbook bytes.
        for name, data in files.items():
            info = zipfile.ZipInfo(name, date_time=(1980, 1, 1, 0, 0, 0))
            info.compress_type = zipfile.ZIP_DEFLATED
            z.writestr(info, data)


def write_dataset(output: Path, seed: int, scenario: str, weeks: int) -> dict:
    truth, choice = truth_rows(seed, scenario, weeks)
    evidence, losses = evidence_rows(truth, seed, scenario, choice)
    releases, backlog = releases_and_backlog(seed, scenario, choice, weeks)
    dataset_id = "set-" + stable_id(seed, scenario)
    root = output / dataset_id
    input_dir = root / "input"; answer_dir = root / "answers"
    input_dir.mkdir(parents=True, exist_ok=True); answer_dir.mkdir(parents=True, exist_ok=True)
    # Truth stays outside the input tree. It is needed for scoring and loss QA.
    truth_payload = {"seed": seed, "scenario": scenario, "choice": choice, "truth": truth, "losses": losses}
    (root / "truth.json").write_text(json.dumps(truth_payload, ensure_ascii=False, indent=2) + "\n")
    (answer_dir / f"{scenario}.json").write_text(json.dumps({"scenario": scenario, "family": choice["family"] if scenario != "S0" else None, "week": choice["week"] if scenario != "S0" else None, "cause_type": choice["cause_type"], "severity": choice["severity"] if scenario != "S0" else 0}, ensure_ascii=False, indent=2) + "\n")
    (input_dir / "metrika.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n")
    (input_dir / "releases.json").write_text(json.dumps(releases, ensure_ascii=False, indent=2) + "\n")
    (input_dir / "backlog.json").write_text(json.dumps(backlog, ensure_ascii=False, indent=2) + "\n")
    sales = []
    base_orders = {f: statistics.mean([r["orders"] for r in truth if r["family"] == f and r["week"] < 4]) for f in PRODUCTS}
    rolling: dict[tuple[str, str], list[float]] = {}
    for r in truth:
        key = (r["family"], r["device"]); rolling.setdefault(key, []).append(r["orders"])
        recent = rolling[key][-4:]
        sales.append({"week_start": r["week_start"], "product_family": PRODUCTS[r["family"]]["label"], "orders_index": round(r["orders"] / max(base_orders[r["family"]], 1) * 100, 1), "orders_index_4w": round(statistics.mean(recent) / max(base_orders[r["family"]], 1) * 100, 1), "base_period": f"{START.isoformat()} + 4 weeks = 100", "channel": "Самостоятельные заказы клиентов", "comment": "", "device": r["device"], "visits": r["visits"], "card_views": r["card_views"], "cart_additions": r["cart_additions"], "checkouts": r["checkouts"], "orders": r["orders"], "revenue_rub": r["revenue"], "our_price_rub": r["unit_price"], "competitor_price_rub": r["competitor_price"]})
    write_xlsx(input_dir / "Продажи.xlsx", {"Продажи": sales, "Релизы": releases, "Бэклог_задачи": backlog})
    # No scenario labels or answer paths in the agent-visible manifest.
    (input_dir / "manifest.json").write_text(json.dumps({"schema_version": "blind-input.v1", "dataset_id": dataset_id, "seed_hash": stable_id(seed), "weeks": weeks, "families": list(PRODUCTS), "devices": list(DEVICES), "files": ["metrika.json", "Продажи.xlsx", "releases.json", "backlog.json"]}, ensure_ascii=False, indent=2) + "\n")
    return {"dataset_id": dataset_id, "input": str(input_dir), "answers": str(answer_dir), "truth": str(root / "truth.json"), "scenario": scenario, "loss_min": min(losses) if losses else None, "loss_max": max(losses) if losses else None}


def score_card(answer: dict, card: dict | None) -> dict:
    if not card:
        silent = answer["cause_type"] == "none"
        return {"scenario": answer["scenario"], "found": False, "missed": not silent, "false_alarm": False, "correct_cause": silent, "status": "silent (no card)" if silent else "missing card"}
    family_ok = answer["family"] is None or card.get("family") == answer["family"]
    week_ok = answer["week"] is None or card.get("week") == answer["week"]
    found = bool(card.get("found", True)) and family_ok and week_ok
    false_alarm = answer["cause_type"] == "none" and bool(card.get("found", False))
    cause_ok = (not false_alarm) if answer["cause_type"] == "none" else (found and card.get("cause_type") == answer["cause_type"])
    return {"scenario": answer["scenario"], "found": found, "missed": not found, "false_alarm": false_alarm, "correct_cause": cause_ok, "status": "ok" if found or (answer["cause_type"] == "none" and not false_alarm) else "wrong"}


def score(root: Path, cards: Path) -> list[dict]:
    out = []
    for dataset in sorted(root.glob("set-*/")):
        answer_files = list((dataset / "answers").glob("S*.json"))
        if not answer_files: continue
        answer = json.loads(answer_files[0].read_text())
        card_path = cards / dataset.name / "card.json"
        card = json.loads(card_path.read_text()) if card_path.exists() else None
        out.append(score_card(answer, card))
    return out


def build_mp_plan(metrika_path: Path, output_path: Path, seed: int = 0,
                  counter: str = DEFAULT_COUNTER, max_visits: int = 900) -> dict:
    """Build a token-free Measurement Protocol plan from the latest evidence week.

    Measurement Protocol accepts recent hits only.  The generated blind weeks are
    historical, so the plan deliberately re-times a proportional sample into the
    last eight hours; it never pretends to backfill the weekly timestamps.
    """
    evidence = json.loads(metrika_path.read_text(encoding="utf-8"))
    observations = evidence.get("observations", [])
    if not observations:
        raise ValueError("metrika evidence has no observations")
    latest = max(str(o.get("period", {}).get("from", "")) for o in observations)
    grouped: dict[tuple[str, str], dict[str, float]] = {}
    for obs in observations:
        period = obs.get("period", {})
        if period.get("from") != latest or obs.get("status") == "missing":
            continue
        key = (str(obs.get("product_key", "unknown")), str(obs.get("segment", "all")))
        value = obs.get("value")
        if value is None:
            continue
        grouped.setdefault(key, {})[str(obs.get("metric_key"))] = float(value)
    total = sum(max(0.0, v.get("visits", 0.0)) for v in grouped.values())
    if total <= 0:
        raise ValueError("latest evidence week has no visits")
    scale = min(1.0, float(max_visits) / total)
    rng = random.Random(f"mp:{seed}:{latest}")
    now = int(time.time())
    hits: list[str] = []
    visit_no = 0
    event_counts = {"pageview": 0, "card_view": 0, "cart_add": 0, "checkout": 0, "purchase": 0}
    for (family, device), metrics in sorted(grouped.items()):
        visits = max(0, int(round(metrics.get("visits", 0.0) * scale)))
        if visits == 0:
            continue
        card_rate = min(1.0, metrics.get("card_views", 0.0) / max(metrics.get("visits", 1.0), 1.0))
        cart_rate = min(1.0, metrics.get("cart_additions", 0.0) / max(metrics.get("card_views", 1.0), 1.0)) if metrics.get("cart_additions") is not None else 0.0
        checkout_rate = min(1.0, metrics.get("checkouts", 0.0) / max(metrics.get("cart_additions", 1.0), 1.0)) if metrics.get("cart_additions") is not None else 0.0
        order_rate = min(1.0, metrics.get("orders", 0.0) / max(metrics.get("checkouts", 1.0), 1.0))
        avg_revenue = metrics.get("revenue", 0.0) / max(metrics.get("orders", 1.0), 1.0)
        for i in range(visits):
            visit_no += 1
            ts = now - 8 * 3600 + int((visit_no / max(total * scale, 1.0)) * (8 * 3600 - 120))
            cid = str(10**17 + int(hashlib.sha256(f"blind:{seed}:{latest}:{family}:{device}:{i}".encode()).hexdigest()[:15], 16) % (9*10**17))
            path = "/catalog/" + urllib.parse.quote(family, safe="")
            common = {"cid": cid, "dl": "https://aifence-demo.example.com" + path,
                      "dr": "https://www.example.com/", "dt": "AIfence blind synthetic",
                      "et": ts, "params": json.dumps({"aifence_blind": "yes", "family": family,
                      "device": device, "week": latest}, ensure_ascii=False, separators=(",", ":"))}
            hits.append(urllib.parse.urlencode({"t": "pageview", **common}))
            event_counts["pageview"] += 1
            if rng.random() < card_rate:
                hits.append(urllib.parse.urlencode({"t": "event", "ea": "card_view", **common, "et": ts + 2}))
                event_counts["card_view"] += 1
            if rng.random() < cart_rate:
                hits.append(urllib.parse.urlencode({"t": "event", "ea": "cart_add", **common, "et": ts + 5}))
                event_counts["cart_add"] += 1
                if rng.random() < checkout_rate:
                    hits.append(urllib.parse.urlencode({"t": "event", "ea": "checkout", **common, "et": ts + 8}))
                    event_counts["checkout"] += 1
                    if rng.random() < order_rate:
                        hits.append(urllib.parse.urlencode({"t": "event", "ea": "purchase", "tr": round(avg_revenue, 2), "cu": "RUB", **common, "et": ts + 12}))
                        event_counts["purchase"] += 1
    output_path.parent.mkdir(parents=True, exist_ok=True)
    plan = {"schema_version": "aifence.metrika-mp-plan.v1", "counter": str(counter),
            "latest_evidence_week": latest, "generated_at": dt.datetime.now(dt.timezone.utc).isoformat(),
            "historical_warning": "Re-timed proportional sample; Measurement Protocol does not backfill these historical weeks.",
            "hit_count": len(hits), "event_counts": event_counts, "hits": hits}
    output_path.write_text(json.dumps(plan, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    return plan


def send_mp_plan(plan_path: Path, token: str, counter: str | None = None) -> list[tuple[int, str]]:
    """Send an explicit plan; token is never written to disk or output."""
    if not token:
        raise ValueError("Measurement Protocol token is required")
    plan = json.loads(plan_path.read_text(encoding="utf-8"))
    target_counter = str(counter or plan.get("counter") or DEFAULT_COUNTER)
    hits = list(plan.get("hits") or [])
    results: list[tuple[int, str]] = []
    batch: list[str] = []
    size = 0
    for line in hits:
        n = len(line.encode("utf-8")) + 1
        if batch and size + n > 240_000:
            results.append(_send_mp_batch(batch, target_counter, token))
            batch, size = [], 0
        batch.append(line); size += n
    if batch:
        results.append(_send_mp_batch(batch, target_counter, token))
    return results


def _send_mp_batch(lines: list[str], counter: str, token: str) -> tuple[int, str]:
    body = "\n".join(lines).encode("utf-8")
    request = urllib.request.Request(
        MP_ENDPOINT + "?" + urllib.parse.urlencode({"tid": counter, "ms": token}),
        data=body, headers={"Content-Type": "text/plain; charset=UTF-8"}, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=30) as response:
            text = response.read(500).decode("utf-8", "replace")
            return response.status, text
    except urllib.error.HTTPError as exc:
        return exc.code, exc.read(500).decode("utf-8", "replace")
    except (OSError, urllib.error.URLError) as exc:
        return 0, f"delivery uncertain: {type(exc).__name__}"



def main() -> int:
    p = argparse.ArgumentParser()
    sub = p.add_subparsers(dest="cmd", required=True)
    g = sub.add_parser("generate"); g.add_argument("--seed", type=int, required=True); g.add_argument("--scenario", choices=[*SCENARIOS, "all"], default="all"); g.add_argument("--weeks", type=int, default=28); g.add_argument("--output", type=Path, required=True)
    s = sub.add_parser("score"); s.add_argument("--root", type=Path, required=True); s.add_argument("--cards", type=Path, required=True); s.add_argument("--output", type=Path)
    m = sub.add_parser("mp-plan", help="make a token-free recent Measurement Protocol sample from metrika.json")
    m.add_argument("--input", type=Path, required=True); m.add_argument("--output", type=Path, required=True)
    m.add_argument("--seed", type=int, default=0); m.add_argument("--counter", default=DEFAULT_COUNTER); m.add_argument("--max-visits", type=int, default=900)
    ms = sub.add_parser("mp-send", help="send an already reviewed MP plan; token is read from an environment variable")
    ms.add_argument("--plan", type=Path, required=True); ms.add_argument("--counter", default=None); ms.add_argument("--token-env", default="YANDEX_METRIKA_TOKEN")
    ms.add_argument("--yes", action="store_true", help="skip the counter confirmation prompt")
    a = p.parse_args()
    if a.cmd == "generate":
        scenarios = SCENARIOS if a.scenario == "all" else (a.scenario,)
        results = [write_dataset(a.output, a.seed, x, a.weeks) for x in scenarios]
        print(json.dumps(results, ensure_ascii=False, indent=2)); return 0
    if a.cmd == "mp-plan":
        plan = build_mp_plan(a.input, a.output, a.seed, a.counter, a.max_visits)
        print(json.dumps({k: plan[k] for k in ("counter", "latest_evidence_week", "hit_count", "event_counts", "historical_warning")}, ensure_ascii=False, indent=2))
        return 0
    if a.cmd == "mp-send":
        counter = a.counter or json.loads(a.plan.read_text(encoding="utf-8")).get("counter", DEFAULT_COUNTER)
        if not a.yes and input(f"Type {counter} to confirm the isolated test counter: ").strip() != str(counter):
            print("Cancelled."); return 1
        import os
        token = os.environ.get(a.token_env, "").strip()
        if not token:
            raise SystemExit(f"Set {a.token_env} in the environment; it is never stored by this command.")
        results = send_mp_plan(a.plan, token, counter)
        print(json.dumps({"counter": str(counter), "batches": [{"status": status, "body": body} for status, body in results]}, ensure_ascii=False, indent=2))
        return 0 if results and all(status == 200 for status, _ in results) else 2
    rows = score(a.root, a.cards)
    if a.output: a.output.write_text(json.dumps(rows, ensure_ascii=False, indent=2) + "\n")
    print(json.dumps(rows, ensure_ascii=False, indent=2)); return 0


if __name__ == "__main__":
    raise SystemExit(main())
