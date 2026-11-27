#!/usr/bin/env python3
"""Generate blind, deterministic AIfence product-analytics datasets.

The generator creates one internal truth and derives three artifacts from it:
Yandex-Metrika-like evidence, a customer-shaped ``Продажи`` workbook, and
release/backlog context.  Scenario answers live outside the agent input tree.
Only the standard library is used, including a tiny XLSX writer.
Planted changes land in the last weeks of the extract and, by default, on a
Kaspersky family, because the blind director question is about Kaspersky.
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

# Catalog identities and list prices are real Market Watch offers captured on
# 2026-11-26 (.runtime/market-catalog/verified-app-offers.csv).  Funnel volumes,
# conversion and weekly movements below are synthetic.
PRICE_CAPTURED = "2026-11-26"
PRODUCTS = {
    "kaspersky_small_office_security": {
        "label": "Kaspersky Small Office Security", "sku": "KL4141RCEFR", "catalog_seller": "Softline",
        "catalog_url": "https://store.softline.ru/kaspersky/kl4141rcefr-248971/",
        # Softline 4 718.70; Softmap 4 719 (old 5 243); SoftMagazin 5 243.
        "price": 4718.70, "competitor_price": 4719.00, "licences_per_order": 1.4,
        "visits": 16000, "mobile_share": .31, "view_rate": .52, "cart_rate": .085, "checkout_rate": .58, "order_rate": .74,
    },
    "astra_linux_special_edition": {
        "label": "Astra Linux Special Edition", "sku": "OS2304ELB81BOX000WS01-SO36", "catalog_seller": "SoftMagazin",
        "catalog_url": "https://www.softmagazin.ru/pao-gruppa-astra/os2304elb81box000ws01-so36/",
        "price": 56900.00, "competitor_price": None, "licences_per_order": 3.5,
        "visits": 7000, "mobile_share": .18, "view_rate": .53, "cart_rate": .065, "checkout_rate": .57, "order_rate": .72,
    },
    "microsoft_365_business_premium": {
        "label": "Microsoft 365 Business Premium", "sku": "CFQ7TTC0LCHC-0002-P1YY", "catalog_seller": "SoftMagazin",
        "catalog_url": "https://www.softmagazin.ru/microsoft/cfq7ttc0lchc-0002-p1yy/",
        "price": 40089.00, "competitor_price": None, "licences_per_order": 6.0,
        "visits": 9000, "mobile_share": .22, "view_rate": .50, "cart_rate": .060, "checkout_rate": .60, "order_rate": .72,
    },
    "red_hat_enterprise_linux_server_premium": {
        "label": "Red Hat Enterprise Linux Server, Premium", "sku": "RH00003", "catalog_seller": "Softmap",
        "catalog_url": "https://softmap.ru/redhat/red-hat-enterprise-linux-server/element-red-hat-enterprise-linux-server-premium-physical-or-virtual-nodes-1-year/",
        "price": 193708.00, "competitor_price": None, "licences_per_order": 1.6,
        "visits": 7200, "mobile_share": .20, "view_rate": .48, "cart_rate": .058, "checkout_rate": .55, "order_rate": .70,
    },
    "kaspersky_endpoint_security": {
        "label": "Kaspersky Endpoint Security для бизнеса Расширенный Плюс", "sku": "KL4210RAKDE", "catalog_seller": "SoftMagazin",
        "catalog_url": "https://www.softmagazin.ru/laboratoriya-kasperskogo/kl4210rakde/",
        # Price per licence in the 10–14 band; a typical order buys ~12.
        "price": 4445.00, "competitor_price": None, "licences_per_order": 12.0,
        "visits": 11000, "mobile_share": .24, "view_rate": .55, "cart_rate": .070, "checkout_rate": .56, "order_rate": .70,
    },
    "dr_web_desktop_security_suite": {
        "label": "Dr.Web Desktop Security Suite", "sku": "LBW-BC-12M-100-A3", "catalog_seller": "SoftMagazin",
        "catalog_url": "https://www.softmagazin.ru/dr-web/lbw-bc-12m-100-a3/",
        # One SKU = 100 PCs for 12 months.
        "price": 151535.00, "competitor_price": None, "licences_per_order": 1.0,
        "visits": 8000, "mobile_share": .22, "view_rate": .51, "cart_rate": .062, "checkout_rate": .55, "order_rate": .71,
    },
    "myoffice_private_cloud": {
        "label": "МойОфис Частное облако", "sku": "X2-CLD-NB-3NUNL-A", "catalog_seller": "Softmap",
        "catalog_url": "https://softmap.ru/myoffice/moyofis/element-moyofis-chastnoe-oblako-licenziya-korporativnaya-na-polzovatelya-dlya-kommercheskih-zakazchikov-bez-ogranicheniya-sroka-deystviya-s-pravom-na-poluchenie-obnovleniy-v-techenie-treh-let/",
        "price": 12060.00, "competitor_price": None, "licences_per_order": 30.0,
        "visits": 6200, "mobile_share": .19, "view_rate": .50, "cart_rate": .061, "checkout_rate": .56, "order_rate": .71,
    },
}
KASPERSKY_FAMILIES = ("kaspersky_small_office_security", "kaspersky_endpoint_security")
DEVICES = {"desktop": .76, "mobile": .24}
METRICS = ("visits", "card_views", "cart_additions", "checkouts", "orders", "revenue")
QUALITY_METRICS = ("bounce_rate", "avg_visit_duration")
SCENARIOS = ("S0", "S1", "S2", "S3", "S4", "S6", "S7", "S12", "S13", "S14")
# 28 weeks end on the last complete week before the price capture.
START = dt.date(2026, 5, 11)
MP_ENDPOINT = "https://mc.yandex.ru/collect"
DEFAULT_COUNTER = "114698578"
# Release wording that looks specific.  Each dataset gets several of these on
# unrelated families and weeks, so the planted release is never the only one.
SPECIFIC_RELEASES = (
    "Обновление каталога и правила обхода роботов", "Миграция названий и ключей каталога",
    "Запуск checkout-эксперимента", "Техническое обслуживание платёжной интеграции",
    "Запуск кампании в каталоге", "Новый блок рекомендаций на карточке",
    "Перенос корзины на новый сервис", "Обновление правил скидок для партнёров",
)
SCENARIO_RELEASE = {"S6": SPECIFIC_RELEASES[0], "S7": SPECIFIC_RELEASES[1], "S12": SPECIFIC_RELEASES[2],
                    "S13": SPECIFIC_RELEASES[3], "S14": SPECIFIC_RELEASES[4]}
GENERIC_RELEASES = ("Обновление интерфейса каталога", "Изменение карточки продукта", "Техническое обновление checkout",
                    "Обновление интеграции цен", "Исправление отображения лицензий")


def stable_id(*values: object, size: int = 16) -> str:
    raw = json.dumps(values, ensure_ascii=False, sort_keys=True).encode()
    return hashlib.sha256(raw).hexdigest()[:size]


def week_start(i: int) -> dt.date:
    return START + dt.timedelta(days=7 * i)


def parse_week(value: str) -> int:
    return int(value.replace("W", "").replace("w", ""))


def week_index(value: object) -> int | None:
    """Accept a generator week number or an ISO week-start date."""
    if value is None or value == "":
        return None
    if isinstance(value, int):
        return value
    text = str(value).strip()
    if text.isdigit():
        return int(text)
    try:
        return (dt.date.fromisoformat(text[:10]) - START).days // 7
    except ValueError:
        return None


def product_key(family: str) -> str:
    return family.lower()


def catalog_reference(family: str) -> dict:
    product = PRODUCTS[family]
    return {"product_label": product["label"], "sku": product["sku"],
            "catalog_seller": product["catalog_seller"], "catalog_url": product["catalog_url"]}


def round_value(metric: str, value: float) -> int | float:
    if metric == "revenue":
        return round(max(value, 0.0), 2)
    return max(0, int(round(value)))


def seasonality(day: dt.date) -> float:
    """Calendar shape shared by all families: summer lull, quarter end, autumn budgets."""
    doy = day.timetuple().tm_yday
    summer = -.06 * math.exp(-((doy - 212) / 40) ** 2)
    quarter_end = .03 * math.exp(-((doy - 268) / 6) ** 2)
    budgets = .03 * min(1.0, max(0.0, (doy - 285) / 30))
    return 1.0 + summer + quarter_end + budgets


def clip_gauss(rng: random.Random, sd: float, width: float = 2.5) -> float:
    return max(1 - width * sd, min(1 + width * sd, rng.gauss(1.0, sd)))


def pick_scenario(seed: int, scenario: str, weeks: int = 28, family_pool: str = "kaspersky") -> dict:
    rng = random.Random(f"{seed}:{scenario}:choice")
    pool = list(KASPERSKY_FAMILIES) if family_pool == "kaspersky" else list(PRODUCTS)
    family = rng.choice(pool)
    # The director asks about a recent drop, so the change lands in the last
    # 4–6 weeks of the extract.  Late events by definition affect the tail.
    week = weeks - 2 if scenario == "S13" else rng.randint(weeks - 6, weeks - 4)
    severity = round(rng.uniform(.25, .38), 3)
    cause_types = {
        "S0": "none", "S1": "tracking_gap", "S2": "mobile_regression",
        "S3": "price_competition", "S4": "release_regression",
        "S6": "traffic_quality", "S7": "taxonomy_migration",
        "S12": "honest_refusal", "S13": "late_event_delivery",
        "S14": "masked_multi_cause_regression"}
    if scenario not in cause_types:
        raise ValueError(f"unknown scenario: {scenario}")
    choice = {"scenario": scenario, "family": family, "week": week,
              "severity": severity, "cause_type": cause_types[scenario]}
    if scenario == "S6":
        choice["bot_share"] = round(.45 + severity * .5, 3)
    if scenario == "S12":
        choice.update({"expected_decisions": ["no_alert", "insufficient_data"],
                       "decision_reason": "insufficient_comparable_evidence"})
    if scenario == "S13":
        choice["late_retention"] = [.55, .18]
    if scenario == "S14":
        choice["secondary_causes"] = ["traffic_mix_shift", "checkout_regression", "tracking_loss"]
        choice["traffic_lift"] = .5
    return choice


def truth_rows(seed: int, scenario: str, weeks: int = 28, family_pool: str = "kaspersky") -> tuple[list[dict], dict]:
    if weeks < 26:
        raise ValueError("weeks must be >= 26")
    choice = pick_scenario(seed, scenario, weeks, family_pool)
    rng = random.Random(f"{seed}:{scenario}:truth")
    rows: list[dict] = []
    hit = lambda family, w: family == choice["family"] and w >= choice["week"]
    for w in range(weeks):
        season = seasonality(week_start(w) + dt.timedelta(days=3))
        for family, base in PRODUCTS.items():
            family_noise = clip_gauss(rng, .02)
            for device in DEVICES:
                share = base["mobile_share"] if device == "mobile" else 1 - base["mobile_share"]
                visits = base["visits"] * share * season * family_noise * clip_gauss(rng, .02)
                views = visits * base["view_rate"] * clip_gauss(rng, .01)
                cart = views * base["cart_rate"] * clip_gauss(rng, .012)
                checkouts = cart * base["checkout_rate"] * clip_gauss(rng, .012)
                unit_price = base["price"]
                competitor = base["competitor_price"] or base["price"] * (1 + (int(stable_id(family), 16) % 5) / 100)
                competitor_price = competitor * clip_gauss(rng, .004)
                if scenario == "S2" and hit(family, w) and device == "mobile":
                    checkouts *= 1 - choice["severity"]
                if scenario == "S3" and hit(family, w):
                    unit_price *= 1 + choice["severity"]
                    checkouts *= 1 - choice["severity"] * .9
                if scenario == "S4" and hit(family, w):
                    cart *= 1 - choice["severity"]; checkouts *= 1 - choice["severity"]
                if scenario == "S14" and hit(family, w):
                    lift = 1 + choice["traffic_lift"]
                    visits *= lift; views *= lift; cart *= lift * .97
                    checkouts *= 1 - choice["severity"]
                orders = checkouts * base["order_rate"] * clip_gauss(rng, .01)
                revenue = orders * base["licences_per_order"] * unit_price * clip_gauss(rng, .02)
                human_visits = visits; bot_visits = 0.0
                if scenario == "S6" and hit(family, w) and device == "desktop":
                    bot_visits = human_visits / (1 - choice["bot_share"]) - human_visits
                    visits = human_visits + bot_visits
                    views += bot_visits * .22
                row = {"week": w, "week_start": week_start(w).isoformat(), "family": family, "device": device,
                       "visits": round_value("visits", visits), "card_views": round_value("card_views", views),
                       "cart_additions": round_value("cart_additions", cart), "checkouts": round_value("checkouts", checkouts),
                       "orders": round_value("orders", orders), "revenue": round_value("revenue", revenue),
                       "unit_price": round_value("revenue", unit_price), "competitor_price": round_value("revenue", competitor_price),
                       "human_visits": round_value("visits", human_visits), "bot_visits": round_value("visits", bot_visits)}
                row.update(synthetic=True, **catalog_reference(family))
                rows.append(row)
    return rows, choice


def series_loss(seed: int, scenario: str, family: str, device: str, metric: str) -> tuple[float, random.Random]:
    """One stable undercount per series; Metrika loses roughly the same share every week."""
    rng = random.Random(f"{seed}:{scenario}:loss:{family}:{device}:{metric}")
    if metric in ("visits", "card_views", "cart_additions", "checkouts"):
        return rng.uniform(.12, .21), rng
    if metric == "orders":
        return rng.uniform(.06, .14), rng
    return rng.uniform(.03, .08), rng


def evidence_rows(truth: list[dict], seed: int, scenario: str, choice: dict) -> tuple[dict, list[float]]:
    losses: list[float] = []
    weeks = len({r["week"] for r in truth})
    series: dict[tuple, tuple[float, random.Random]] = {}
    quality_rng = random.Random(f"{seed}:{scenario}:quality")
    observations: list[dict] = []
    hit = lambda row: row["family"] == choice["family"] and row["week"] >= choice["week"]
    for row in truth:
        product_observed = row["family"]
        if scenario == "S7" and hit(row):
            product_observed = PRODUCTS[row["family"]]["sku"].lower()
        period_from = row["week_start"]
        period_to = (dt.date.fromisoformat(period_from) + dt.timedelta(days=6)).isoformat()
        details = {"device": row["device"], "synthetic": True, "origin": "synthetic_fixture", **catalog_reference(row["family"])}

        def emit(metric: str, value: float | None, unit: str, status: str, caveats: list[str]) -> None:
            source = {"source_id": "blind-metrika",
                      "locator": f"week={row['week']};family={product_observed};device={row['device']};metric={metric}",
                      "revision": "metrika-weekly-v3"}
            observation = {"kind": "metric", "product_key": product_key(product_observed), "metric_key": metric,
                           "value": value, "unit": unit, "period": {"from": period_from, "to": period_to},
                           "segment": row["device"], "source_ref": source, "status": status,
                           "caveats": caveats, "details": dict(details)}
            observation["id"] = "obs-" + stable_id(observation)
            observations.append(observation)

        for metric in METRICS:
            key = (row["family"], row["device"], metric)
            if key not in series:
                series[key] = series_loss(seed, scenario, *key)
            base_loss, rng = series[key]
            loss = min(.25 if metric in ("visits", "card_views", "cart_additions", "checkouts") else .2,
                       max(.10 if metric in ("visits", "card_views", "cart_additions", "checkouts") else .02,
                           base_loss + rng.gauss(0, .006)))
            factor = 1 - loss
            status = "observed"
            caveats = ["Недельный агрегат Метрики; часть событий не фиксируется (блокировщики, отказ от cookies)."]
            value: float | None = float(row[metric]) * factor
            if scenario == "S14" and hit(row) and metric == "cart_additions":
                # Cart events are additionally under-counted after the campaign
                # launch; total loss stays inside the 10–25% band.
                factor = max(.75, factor * .86); value = float(row[metric]) * factor
            if scenario == "S13" and hit(row) and metric in ("orders", "revenue"):
                # Purchase confirmations reach Metrika days later; the tail of
                # the extract is short.  The sales workbook keeps full orders.
                factor *= choice["late_retention"][min(row["week"] - choice["week"], 1)]
                value = float(row[metric]) * factor
            if scenario == "S1" and hit(row) and metric == "cart_additions":
                value = None; status = "missing"; caveats = ["Сбор события неполный за период; пропуск не равен нулю."]
            if scenario == "S12" and hit(row) and metric in ("checkouts", "orders", "revenue"):
                value = None; status = "incomparable"
                caveats = ["Шаги оформления изменены для части посетителей; сравнение с прошлыми неделями некорректно."]
            if value is not None:
                if metric in ("visits", "card_views", "cart_additions", "checkouts"):
                    losses.append(1 - factor)
                value = round_value(metric, value)
            emit(metric, value, "RUB" if metric == "revenue" else "count", status, caveats)
        # Visit quality: bots bounce and leave within seconds.
        human, bots = float(row["human_visits"]), float(row["bot_visits"])
        bounce_h = .27 * clip_gauss(quality_rng, .03); duration_h = 168 * clip_gauss(quality_rng, .04)
        total = max(human + bots, 1.0)
        bounce = (human * bounce_h + bots * .87) / total
        duration = (human * duration_h + bots * 4.0) / total
        caveat = ["Недельный агрегат Метрики по визитам."]
        emit("bounce_rate", round(bounce, 3), "ratio", "observed", caveat)
        emit("avg_visit_duration", round(duration, 1), "seconds", "observed", caveat)
    partial = scenario in ("S1", "S12")
    warnings = ["Недельный агрегат: неделя × семейство × устройство.",
                "Поведенческие события Метрики теряют 10–25% относительно фактических."]
    return {"schema_version": "aifence.v1", "source": "metrika",
            "as_of": (week_start(weeks - 1) + dt.timedelta(days=6)).isoformat() + "T23:59:59Z",
            "status": "partial" if partial else "ok", "observations": observations, "warnings": warnings}, losses


def releases_and_backlog(seed: int, scenario: str, choice: dict, weeks: int) -> tuple[list[dict], list[dict]]:
    rng = random.Random(f"{seed}:{scenario}:context")
    release_rows = []
    for i in range(weeks):
        release_rows.append({"release_id": f"R-{i+1:02d}", "release_date": week_start(i).isoformat(),
                             "release_type": rng.choice(["minor", "major", "technical"]),
                             "description": rng.choice(GENERIC_RELEASES), "related_task_ids": f"T-{i+1:02d}",
                             "affected_metrics": rng.choice(["card_views", "cart_additions", "checkouts", "orders", "revenue"]),
                             "status": "выпущен", "family": rng.choice(list(PRODUCTS))})
    target = release_rows[choice["week"]]
    target["family"] = choice["family"]
    if scenario in SCENARIO_RELEASE:
        target["description"] = SCENARIO_RELEASE[scenario]
    # Decoys: specific-looking releases on other weeks, some on the same family.
    used = {SCENARIO_RELEASE.get(scenario)}
    others = [i for i in range(2, weeks) if i != choice["week"]]
    for i in rng.sample(others, 4):
        description = rng.choice([d for d in SPECIFIC_RELEASES if d not in used])
        used.add(description)
        release_rows[i]["description"] = description
        release_rows[i]["family"] = rng.choice(list(PRODUCTS))
    tasks = []
    for i in range(max(weeks - 5, 22)):
        family = rng.choice(list(PRODUCTS))
        tasks.append({"task_id": f"T-{i+1:02d}", "title": rng.choice(["Улучшить путь выбора лицензии", "Проверить события электронной торговли", "Сверить цены в каталоге", "Проверить мобильный checkout", "Уточнить карточку семейства"]),
                      "type": rng.choice(["Фича", "Улучшение", "Технический долг"]), "status": rng.choice(["Done", "In Progress", "Backlog"]),
                      "priority": rng.choice(["Low", "Medium", "High"]), "created_date": week_start(max(0, i - 3)).isoformat(),
                      "closed_date": week_start(i).isoformat() if i % 3 == 0 else "", "release_id": f"R-{i+1:02d}",
                      "description_short": "Проверить полноту и качество продуктового пути", "linked_metric_ids": rng.choice(["card_views", "cart_additions", "checkouts", "orders"]), "family": family})
    for fact in release_rows + tasks:
        fact.update(synthetic=True, **catalog_reference(fact["family"]))
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


def write_dataset(output: Path, seed: int, scenario: str, weeks: int, family_pool: str = "kaspersky") -> dict:
    truth, choice = truth_rows(seed, scenario, weeks, family_pool)
    evidence, losses = evidence_rows(truth, seed, scenario, choice)
    releases, backlog = releases_and_backlog(seed, scenario, choice, weeks)
    dataset_id = "set-" + stable_id(evidence, releases, backlog)
    root = output / dataset_id
    root.mkdir(parents=True)
    input_dir = root / "input"; answer_dir = root / "answers"
    input_dir.mkdir(); answer_dir.mkdir()
    # Truth stays outside the input tree. It is needed for scoring and loss QA.
    truth_payload = {"synthetic": True, "seed": seed, "scenario": scenario, "choice": choice, "truth": truth, "losses": losses}
    (root / "truth.json").write_text(json.dumps(truth_payload, ensure_ascii=False, indent=2) + "\n")
    located = scenario not in ("S0", "S12")
    # Severity is the effect actually applied to the data, not a free number.
    effect = {"S0": 0, "S6": choice.get("bot_share"), "S7": None, "S12": None,
              "S13": round(1 - choice.get("late_retention", [1])[0], 3)}.get(scenario, choice["severity"])
    answer_payload = {"scenario": scenario,
                      "family": choice["family"] if located else None,
                      "week": week_start(choice["week"]).isoformat() if located else None,
                      "week_index": choice["week"] if located else None,
                      "cause_type": choice["cause_type"],
                      "severity": effect}
    if scenario == "S12":
        # Keep the organizer answer explicit: either an honest no-alert or
        # an insufficient-data refusal is correct for this deliberately
        # incomparable experiment slice.
        answer_payload.update({"accepted_outcomes": choice["expected_decisions"],
                               "expected_decisions": choice["expected_decisions"],
                               "decision_reason": choice["decision_reason"]})
    if answer_payload["family"] is not None:
        answer_payload.update(catalog_reference(answer_payload["family"]))
    answer_payload["synthetic"] = True
    if scenario == "S14":
        answer_payload["secondary_causes"] = choice["secondary_causes"]
        answer_payload["traffic_lift"] = choice["traffic_lift"]
    if scenario == "S12":
        answer_payload["question_family"] = choice["family"]
        answer_payload["incomparable_from"] = week_start(choice["week"]).isoformat()
    (answer_dir / f"{scenario}.json").write_text(json.dumps(answer_payload, ensure_ascii=False, indent=2) + "\n")
    (input_dir / "metrika.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n")
    (input_dir / "releases.json").write_text(json.dumps(releases, ensure_ascii=False, indent=2) + "\n")
    (input_dir / "backlog.json").write_text(json.dumps(backlog, ensure_ascii=False, indent=2) + "\n")
    sales = []
    base_orders = {f: statistics.mean([r["orders"] for r in truth if r["family"] == f and r["week"] < 4]) for f in PRODUCTS}
    rolling: dict[tuple[str, str], list[float]] = {}
    for r in truth:
        key = (r["family"], r["device"]); rolling.setdefault(key, []).append(r["orders"])
        recent = rolling[key][-4:]
        comment = ""
        if scenario == "S12" and r["family"] == choice["family"] and r["week"] >= choice["week"]:
            comment = "Часть заказов оформлена по новой схеме; сверка с 1С за период не закрыта"
        sales.append({"week_start": r["week_start"], "product_family": PRODUCTS[r["family"]]["label"], "product_key": r["family"], **catalog_reference(r["family"]), "synthetic": True, "price_origin": f"market_watch_{PRICE_CAPTURED}", "competitor_price_origin": f"market_watch_{PRICE_CAPTURED}" if PRODUCTS[r["family"]]["competitor_price"] else "synthetic", "orders_index": round(r["orders"] / max(base_orders[r["family"]], 1) * 100, 1), "orders_index_4w": round(statistics.mean(recent) / max(base_orders[r["family"]], 1) * 100, 1), "base_period": f"{START.isoformat()} + 4 weeks = 100", "channel": "Самостоятельные заказы клиентов", "comment": comment, "device": r["device"], "visits": r["visits"], "card_views": r["card_views"], "cart_additions": r["cart_additions"], "checkouts": r["checkouts"], "orders": r["orders"], "revenue_rub": r["revenue"], "our_price_rub": r["unit_price"], "competitor_price_rub": r["competitor_price"]})
    write_xlsx(input_dir / "Продажи.xlsx", {"Продажи": sales, "Релизы": releases, "Бэклог_задачи": backlog})
    # No scenario labels or answer paths in the agent-visible manifest.
    (input_dir / "manifest.json").write_text(json.dumps({"schema_version": "blind-input.v1", "synthetic": True, "dataset_id": dataset_id, "weeks": weeks, "families": list(PRODUCTS), "catalog": {family: catalog_reference(family) for family in PRODUCTS}, "devices": list(DEVICES), "files": ["metrika.json", "Продажи.xlsx", "releases.json", "backlog.json"]}, ensure_ascii=False, indent=2) + "\n")
    return {"dataset_id": dataset_id, "input": str(input_dir), "answers": str(answer_dir), "truth": str(root / "truth.json"), "scenario": scenario, "loss_min": min(losses) if losses else None, "loss_max": max(losses) if losses else None}


def score_card(answer: dict, card: dict | None) -> dict:
    if not card:
        silent = answer["cause_type"] == "none"
        return {"scenario": answer["scenario"], "found": False, "missed": not silent, "false_alarm": False, "correct_cause": silent, "status": "silent (no card)" if silent else "missing card"}
    accepted = set(answer.get("accepted_outcomes") or answer.get("expected_decisions") or [])
    if answer.get("cause_type") == "honest_refusal" or accepted:
        value = ""
        for key in ("outcome", "decision", "response", "status"):
            if card.get(key) not in (None, ""):
                value = str(card[key]).strip().casefold().replace(" ", "_")
                break
        if card.get("alert") is False:
            value = "no_alert"
        if value in accepted:
            return {"scenario": answer["scenario"], "found": False, "missed": False, "false_alarm": False, "correct_cause": True, "status": "correct_" + value}
        if value in ("alert", "found", "true") or card.get("found") is True:
            return {"scenario": answer["scenario"], "found": False, "missed": False, "false_alarm": True, "correct_cause": False, "status": "false_alarm"}
        return {"scenario": answer["scenario"], "found": False, "missed": True, "false_alarm": False, "correct_cause": False, "status": "missing decision"}
    family_ok = answer["family"] is None or card.get("family") == answer["family"]
    week_ok = answer["week"] is None or week_index(card.get("week")) == week_index(answer["week"])
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
    catalog_by_key = {}
    for obs in observations:
        details = obs.get("details", {})
        if details.get("synthetic") is not True:
            raise ValueError("MP plan requires synthetic observations")
        key = obs["product_key"]
        family = details.get("canonical_product_key", key)
        catalog_by_key[key] = catalog_reference(family)
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
                      "dr": "https://www.example.com/", "dt": catalog_by_key[family]["product_label"],
                      "et": ts, "params": json.dumps({"aifence_blind": "yes", "family": family,
                      "device": device, "week": latest, "synthetic": True, **catalog_by_key[family]},
                      ensure_ascii=False, separators=(",", ":"))}
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
            "synthetic": True, "catalog": catalog_by_key,
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
    g = sub.add_parser("generate"); g.add_argument("--seed", type=int, required=True); g.add_argument("--scenario", choices=[*SCENARIOS, "all"], default="all"); g.add_argument("--weeks", type=int, default=28); g.add_argument("--output", type=Path, required=True); g.add_argument("--family-pool", choices=["kaspersky", "all"], default="kaspersky")
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
        results = [write_dataset(a.output, a.seed, x, a.weeks, a.family_pool) for x in scenarios]
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
