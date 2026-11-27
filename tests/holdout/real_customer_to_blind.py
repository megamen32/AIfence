#!/usr/bin/env python3
"""Convert the customer's workbook (Датасет_ИИ-помощник_PM_v17.xlsx) into the blind-scan
input format (metrika.json + releases.json), with no labels and no tuning.

Daily Метрики_данные rows -> weekly (Monday) values per product/segment/metric:
counts are summed, ratios are averaged. Only desktop/mobile segments are kept.
Продажи.orders_index -> metric "orders" (segment "all") for the two families.
Naming map (the only interpretation step): cfg_family_views -> visits (family page visits).
The sheet Эталонный_кейс is never read.

Usage: real_customer_to_blind.py WORKBOOK OUT_DIR
"""
import datetime as dt
import hashlib
import json
import os
import sys

import openpyxl

PRODUCT = {"Конфигуратор Kaspersky": "kaspersky_endpoint_security", "Конфигуратор Астра": "astra_linux_special_edition",
           "ЛК": "personal_account", "Kaspersky Endpoint Security": "kaspersky_endpoint_security",
           "Astra Linux Special Edition": "astra_linux_special_edition"}
RENAME = {"cfg_family_views": "visits"}


def monday(d):
    d = dt.date.fromisoformat(str(d)[:10])
    return d - dt.timedelta(days=d.weekday())


def fam_of_release(text):
    t = text.lower()
    if "kaspersky" in t:
        return "kaspersky_endpoint_security"
    if "astra" in t:
        return "astra_linux_special_edition"
    if "лк" in t.split() or " лк" in t or "лк:" in t or "ЛК" in text:
        return "personal_account"
    return ""


def main(wbp, out):
    wb = openpyxl.load_workbook(wbp, read_only=True, data_only=True)
    rows = list(wb["Метрики_данные"].iter_rows(values_only=True))
    hdr = rows[0]
    acc = {}
    for r in rows[1:]:
        r = dict(zip(hdr, r))
        if r["segment"] not in ("desktop", "mobile") or r["value"] is None:
            continue
        key = (PRODUCT[r["product"]], RENAME.get(r["metric_id"], r["metric_id"]), r["segment"], monday(r["date"]))
        a = acc.setdefault(key, {"vals": [], "unit": r["unit"]})
        a["vals"].append(float(r["value"]))
    obs = []

    def add(pk, mk, seg, wk, val, unit, ref):
        loc = f"week={wk};family={pk};device={seg};metric={mk}"
        obs.append({"kind": "metric", "product_key": pk, "metric_key": mk, "value": val, "unit": unit,
                    "period": {"from": wk.isoformat(), "to": (wk + dt.timedelta(days=6)).isoformat()},
                    "segment": seg, "source_ref": {"source_id": "customer-workbook", "locator": loc, "revision": ref},
                    "status": "observed", "caveats": [], "details": {"device": seg, "origin": "customer_workbook"},
                    "id": "obs-" + hashlib.sha1(loc.encode()).hexdigest()[:16]})

    for (pk, mk, seg, wk), a in sorted(acc.items()):
        unit = a["unit"] or ""
        ratio = any(s in mk for s in ("rate", "share", "ctr"))
        val = sum(a["vals"]) / len(a["vals"]) if ratio else sum(a["vals"])
        add(pk, mk, seg, wk, round(val, 4), "ratio" if ratio else "count", "Метрики_данные")
    srows = list(wb["Продажи"].iter_rows(values_only=True))
    sh = srows[0]
    for r in srows[1:]:
        r = dict(zip(sh, r))
        if not r["week_start"]:
            continue
        add(PRODUCT[r["product_family"]], "orders", "all", monday(r["week_start"]), r["orders_index"], "count", "Продажи")
    rels = []
    for r in list(wb["Релизы"].iter_rows(values_only=True))[1:]:
        if not r[0]:
            continue
        rels.append({"release_id": r[0], "release_date": str(r[1])[:10], "release_type": r[2], "description": r[3],
                     "related_task_ids": r[4], "affected_metrics": r[5], "status": r[6], "family": fam_of_release(r[3] or "")})
    as_of = max(o["period"]["to"] for o in obs) + "T23:59:59Z"
    d = os.path.join(out, "run-real", "inputs", "case-01")
    os.makedirs(d, exist_ok=True)
    json.dump({"schema_version": "aifence.v1", "source": "metrika", "as_of": as_of, "status": "ok",
               "observations": obs, "warnings": []}, open(os.path.join(d, "metrika.json"), "w"), ensure_ascii=False)
    json.dump(rels, open(os.path.join(d, "releases.json"), "w"), ensure_ascii=False, indent=1)
    print(len(obs), "observations,", len(rels), "releases ->", d)


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
