#!/usr/bin/env python3
"""Build the deterministic DEMO evidence payload for the owner stand 42080.

The script emits normal aifence.v1 batches for Product Manager import and the
Metrika watchdog uploader. It never contacts a stand and never contains a
credential. Upload/import is performed through the app UI or Airlock tools.
"""
from __future__ import annotations

import argparse
import hashlib
import json
from datetime import date, timedelta
from pathlib import Path

AS_OF = "2026-11-27T18:00:00Z"
BASELINE_FROM = "2026-09-07"
BASELINE_TO = "2026-10-04"
RELEASED_ON = "2026-10-05"
CURRENT_FROM = "2026-10-05"
CURRENT_TO = "2026-11-01"
KES = "kaspersky_endpoint_security"
ASTRA = "astra_linux_special_edition"
DRWEB = "dr_web_desktop_security_suite"
KSOS = "kaspersky_small_office_security"


def ident(*parts: object) -> str:
    raw = "|".join(map(str, parts)).encode()
    return "demo-r09-" + hashlib.sha256(raw).hexdigest()[:20]


def ref(source_id: str, locator: str) -> dict:
    return {"source_id": source_id, "locator": locator, "revision": "demo-r09-v1"}


def obs(source: str, product: str, metric: str, value, unit: str, start: str, end: str,
        locator: str, *, kind: str = "metric", status: str = "observed", segment: str = "all",
        details: dict | None = None, source_id: str | None = None, caveats: list[str] | None = None) -> dict:
    return {
        "id": ident(source, product, metric, start, end, locator),
        "kind": kind,
        "product_key": product,
        "metric_key": metric,
        "value": value,
        "unit": unit,
        "period": {"from": start, "to": end},
        "segment": segment,
        "status": status,
        "source_ref": ref(source_id or source, locator),
        "details": {"synthetic": True, "origin": "synthetic_fixture", "demo_label": "ДЕМО", **(details or {})},
        "caveats": caveats or ["ДЕМО: синтетический ряд для показа Product Manager."],
    }


def batch(source: str, rows: list[dict]) -> dict:
    return {"schema_version": "aifence.v1", "source": source, "as_of": AS_OF,
            "status": "ok", "observations": rows, "warnings": ["ДЕМО: синтетические данные."]}


def daily_rows(product: str, start: str, end: str, total: int, label: str) -> list[dict]:
    first, last = date.fromisoformat(start), date.fromisoformat(end)
    days = (last - first).days + 1
    values = [total // days] * days
    for i in range(total % days):
        values[i] += 1
    return [obs("metrika", product, "add_to_cart", value, "count", (first + timedelta(days=i)).isoformat(),
                (first + timedelta(days=i)).isoformat(), f"ДЕМО:{label}/day/{i+1:02d}",
                details={"release_id": "R-09", "event_name": "add_to_cart", "series": label})
            for i, value in enumerate(values)]


def make_sales() -> list[dict]:
    baseline_mean, current_mean = 110.0, 74.0
    control_baseline, control_current = 100.0, 97.4
    change = (current_mean / baseline_mean - 1.0) * 100.0
    control_change = (control_current / control_baseline - 1.0) * 100.0
    adjusted = ((current_mean / baseline_mean) / (control_current / control_baseline) - 1.0) * 100.0
    magnitude = {
        "basis": "capped_four_week_relative_change", "unit": "percent", "relative_change_pct": change,
        "baseline": {"period": {"from": BASELINE_FROM, "to": BASELINE_TO}, "capped_mean": baseline_mean},
        "current": {"period": {"from": CURRENT_FROM, "to": CURRENT_TO}, "capped_mean": current_mean},
        "value_unit": "index", "spike_cap_median_multiple": 1.4,
    }
    details = {
        "control_product_key": ASTRA, "metric_key": "add_to_cart", "origin": "synthetic_fixture",
        "reason": "target_drop_with_stable_control", "detected_at": "2026-10-26T06:00:00Z",
        "first_detected_at": "2026-10-26T06:00:00Z", "baseline_period": {"from": BASELINE_FROM, "to": BASELINE_TO},
        "current_period": {"from": CURRENT_FROM, "to": CURRENT_TO}, "window_weeks": 4,
        "baseline_mean": baseline_mean, "current_mean": current_mean,
        "control_baseline_mean": control_baseline, "control_current_mean": control_current,
        "relative_change_pct": change, "control_change_pct": control_change,
        "control_adjusted_change_pct": adjusted,
        "magnitude": magnitude,
        "detection_thresholds": {"minimum_target_and_control_adjusted_drop_pct": 20, "max_absolute_control_change_pct": 20},
        "release_id": "R-09", "release_date": RELEASED_ON,
        "source_refs": [{"id": "demo-r09-kes-baseline", "source_ref": ref("metrika-demo", "ДЕМО:R-09/add_to_cart/KES/baseline")},
                        {"id": "demo-r09-kes-current", "source_ref": ref("metrika-demo", "ДЕМО:R-09/add_to_cart/KES/current")}],
    }
    row = obs("sales", KES, "watchdog.alert", True, "boolean", "2026-10-26", "2026-11-01",
              "ДЕМО:R-09/watchdog/2026-11-01", kind="metric", details=details, source_id="aifence-watchdog")
    def weekly_contrast(product: str, baseline: float, current: float, change: float, label: str) -> dict:
        return obs(
            "sales", product, "family_orders_idx.relative_change_pct", round(change, 3), "percent",
            CURRENT_FROM, CURRENT_TO, f"ДЕМО:R-09/orders/{label}/full_weeks", details={
                "variant": "full_weeks",
                "baseline_mean": baseline,
                "current_mean": current,
                "baseline_weeks": 4,
                "current_weeks": 4,
                "baseline_from": BASELINE_FROM,
                "baseline_to": BASELINE_TO,
                "current_from": CURRENT_FROM,
                "current_to": CURRENT_TO,
                "release_id": "R-09",
                "control_product_key": ASTRA,
            })

    target = weekly_contrast(KES, baseline_mean, current_mean, change, "KES")
    control = weekly_contrast(ASTRA, control_baseline, control_current, control_change, "Astra")
    row["details"]["evidence_ids"] = [target["id"], control["id"]]
    row["details"]["source_refs"] = [
        {"id": target["id"], "source_ref": target["source_ref"]},
        {"id": control["id"], "source_ref": control["source_ref"]},
    ]
    return [row, target, control]


def make_research() -> list[dict]:
    rows: list[dict] = []
    products = [(KES, "Kaspersky Endpoint Security", [12800, 12700, 12900, 12600, 12750, 12850, 12650, 12750]),
                (ASTRA, "Astra Linux Special Edition", [7600, 7550, 7620, 7580, 7540, 7610, 7570, 7590]),
                (DRWEB, "Dr.Web Desktop Security Suite", [6200, 6210, 6190, 6220, 6200, 6180, 6210, 6200]),
                (KSOS, "Kaspersky Small Office Security", [8400, 8380, 8420, 8390, 8410, 8370, 8400, 8390])]
    starts = [date(2026, 9, 7) + timedelta(days=7 * i) for i in range(8)]
    for product, label, values in products:
        for i, value in enumerate(values):
            start = starts[i].isoformat(); end = (starts[i] + timedelta(days=6)).isoformat()
            rows.append(obs("research", product, "search_queries", value, "count", start, end,
                             f"ДЕМО:Wordstat/{label}/{start}", source_id="wordstat",
                             details={"query": label, "geo": "ALL", "normalization": "weekly_count_all_devices",
                                      "product_label": label, "source_name": "Wordstat", "release_id": "R-09"}))
    trends = [(KES, "Kaspersky Endpoint Security", [52, 51, 50, 52, 51, 50, 52, 51]),
              (ASTRA, "Astra Linux Special Edition", [49, 50, 48, 49, 50, 49, 48, 50]),
              (DRWEB, "Dr.Web Desktop Security Suite", [41, 42, 41, 40, 42, 41, 40, 41]),
              (KSOS, "Kaspersky Small Office Security", [46, 45, 47, 46, 45, 46, 47, 46])]
    for product, label, values in trends:
        for i, value in enumerate(values):
            start = starts[i].isoformat(); end = (starts[i] + timedelta(days=6)).isoformat()
            rows.append(obs("research", product, "search_interest", value, "index", start, end,
                             f"ДЕМО:Google Trends/{label}/{start}", source_id="google_trends",
                             details={"query": label, "geo": "RU", "property": "web", "normalization": "weekly_index_0_100",
                                      "product_label": label, "source_name": "Google Trends", "release_id": "R-09"}))
    interviews = [
        (KES, "Покупатель просит сразу показать состав лицензии и ожидает добавить KES в корзину с карточки.", "ДЕМО:Интервью/KES/01"),
        (KES, "После выбора редакции посетитель возвращается к цене и не видит понятного подтверждения шага корзины.", "ДЕМО:Интервью/KES/02"),
        (ASTRA, "Контрольная карточка Astra Linux проходит тот же путь без заметного изменения интереса.", "ДЕМО:Интервью/Astra/01"),
    ]
    for product, text, locator in interviews:
        rows.append(obs("research", product, "interview_excerpt", text, "text", BASELINE_FROM, CURRENT_TO, locator,
                         source_id="interviews-demo", details={"period_basis": "ДЕМО: срез интервью вокруг релиза R-09",
                                                                 "scope": "Путь покупки", "source_level": "qualitative"}))
    return rows


def make_release() -> list[dict]:
    return [obs("release", KES, "release_log", "ДЕМО · R-09 · обновление конфигуратора Kaspersky Endpoint Security",
                 "text", RELEASED_ON, RELEASED_ON, "ДЕМО:Релизы/R-09", source_id="release-demo",
                 details={"release_id": "R-09", "release_type": "product", "status": "released",
                          "affected_metrics": ["add_to_cart"], "release_name": "R-09"})]


def make_browser() -> list[dict]:
    return [
        obs("browser", KES, "purchase_path", "path_broken", "verdict", "2026-10-12", "2026-10-12",
            "ДЕМО:R-09/path-check/desktop", kind="path_check", segment="desktop",
            details={"stage": "add_to_cart", "reason": "metrika_add_not_observed", "release_id": "R-09"}, source_id="path-check-demo"),
        obs("browser", KES, "purchase_path", "ok", "verdict", "2026-10-12", "2026-10-12",
            "ДЕМО:R-09/path-check/mobile", kind="path_check", segment="mobile",
            details={"stage": "cart_item", "reason": "", "release_id": "R-09"}, source_id="path-check-demo"),
    ]


def make_metrika() -> list[dict]:
    rows = []
    rows += daily_rows(KES, BASELINE_FROM, BASELINE_TO, 1100, "KES-baseline")
    rows += daily_rows(KES, CURRENT_FROM, CURRENT_TO, 740, "KES-current")
    rows += daily_rows(ASTRA, BASELINE_FROM, BASELINE_TO, 1000, "Astra-baseline")
    rows += daily_rows(ASTRA, CURRENT_FROM, CURRENT_TO, 974, "Astra-current")
    rows += [
        obs("metrika", KES, "add_to_cart", 1100, "count", BASELINE_FROM, BASELINE_TO, "ДЕМО:R-09/add_to_cart/KES/baseline",
            details={"release_id": "R-09", "event_name": "add_to_cart", "aggregate": True}),
        obs("metrika", KES, "add_to_cart", 740, "count", CURRENT_FROM, CURRENT_TO, "ДЕМО:R-09/add_to_cart/KES/current",
            details={"release_id": "R-09", "event_name": "add_to_cart", "aggregate": True}),
        obs("metrika", ASTRA, "add_to_cart", 1000, "count", BASELINE_FROM, BASELINE_TO, "ДЕМО:R-09/add_to_cart/Astra/baseline",
            details={"release_id": "R-09", "event_name": "add_to_cart", "aggregate": True}),
        obs("metrika", ASTRA, "add_to_cart", 974, "count", CURRENT_FROM, CURRENT_TO, "ДЕМО:R-09/add_to_cart/Astra/current",
            details={"release_id": "R-09", "event_name": "add_to_cart", "aggregate": True}),
        obs("metrika", KES, "cfg_reach_rate.observed_rows", 400, "count", CURRENT_FROM, CURRENT_TO, "ДЕМО:R-09/funnel/configurator",
            details={"release_id": "R-09", "stage": "configuration"}),
        obs("metrika", KES, "cfg_add_to_cart_rate", 0.12, "ratio", CURRENT_FROM, CURRENT_TO, "ДЕМО:R-09/funnel/add_to_cart",
            details={"release_id": "R-09", "stage": "add_to_cart"}),
    ]
    return rows


def build() -> dict:
    return {"schema_version": "aifence.case.v1", "as_of": AS_OF,
            "warnings": ["ДЕМО: R-09 synthetic owner-stand seed."],
            "batches": [batch("sales", make_sales()), batch("metrika", make_metrika()),
                        batch("research", make_research()), batch("release", make_release()),
                        batch("browser", make_browser())]}


def main() -> int:
    ap = argparse.ArgumentParser()
    ap.add_argument("--out", type=Path, default=Path(".runtime/sol-sessions/sol-seed"))
    args = ap.parse_args()
    args.out.mkdir(parents=True, exist_ok=True)
    document = build()
    (args.out / "pm-demo-r09.json").write_text(json.dumps(document, ensure_ascii=False, indent=2) + "\n")
    series = batch("metrika", make_metrika())
    context = {"schema_version": "aifence.watch-context.v1", "synthetic": True, "batches": {
        "release": batch("release", make_release()),
        "research": batch("research", make_research()),
        "metrika": batch("metrika", []),
        "backlog": batch("backlog", []),
    }}
    (args.out / "metrika-series-demo-r09.json").write_text(json.dumps(series, ensure_ascii=False, indent=2) + "\n")
    (args.out / "metrika-context-demo-r09.json").write_text(json.dumps(context, ensure_ascii=False, indent=2) + "\n")
    print(f"Wrote DEMO payloads to {args.out}")
    print(f"PM observations: {sum(len(b['observations']) for b in document['batches'])}")
    print("Wordstat KES: 12800 -> 12750 (-0.3906%)")
    print("Wordstat Astra Linux: 7600 -> 7590 (-0.1316%)")
    print("Google Trends KES: 52 -> 51 (-1.9231%)")
    print("Google Trends Astra Linux: 49 -> 50 (+2.0408%)")
    print("KES add_to_cart: 1100 -> 740 (-32.7273%)")
    print("Astra Linux add_to_cart: 1000 -> 974 (-2.6%)")
    print("Release: R-09 on 2026-10-05")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
