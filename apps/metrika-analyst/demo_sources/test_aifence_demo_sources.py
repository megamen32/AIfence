"""Deterministic checks for the AIfence Metrika demo sources.

Run from apps/metrika-analyst:
    python3 -m unittest discover -s demo_sources -p 'test_*.py' -v

The test rebuilds the sources from the private workbook (AIFENCE_WORKBOOK
overrides the default path) and checks the agent-visible artifacts.  Expected
values live only in this file and are never written to generated data.
"""
from __future__ import annotations

import datetime as dt
import json
import os
import sqlite3
import unittest
from pathlib import Path

import aifence_demo_sources as src

WORKBOOK = Path(os.environ.get("AIFENCE_WORKBOOK") or
                src.ROOT.parents[2] / "data" / "softline_dataset_v17.xlsx")
HOLDOUT_SHEET = "Эталонный_кейс"
KES = "kaspersky_endpoint_security"
ASTRA = "astra_linux_special_edition"
TOLERANCE = 0.01
SCENARIO_IDS = {"kes_regression", "tracking_gap", "mobile_regression", "recommendation_ctr", "false_anomaly"}

# (from, to, expected week count, {product_key: expected mean orders_index})
WINDOWS = [
    ("2026-06-29", "2026-09-21", 13, {KES: 165.85, ASTRA: 98.54}),
    ("2026-10-05", "2026-11-16", 7, {KES: 111.57, ASTRA: 96.00}),
]

# Test-only: answer hints for sandbox scenarios.  They must not appear in
# scenario metadata or sandbox files that the agent reads.
SCENARIO_HINTS = [
    '"expected_signal"', '"risk"', "KES orders -32.5%", "Astra -3.3%",
    "mobile -21.4%", "desktop +1.8%", "CTR 8.1% -> 3.2%", "partial day, status delayed",
    "изменения конфигуратора", "R-09", "causal_inference", "unknown_is_not_zero",
    "segment_confounding", "denominator_drift", "data_lag",
]


def agent_visible_texts() -> dict[str, str]:
    texts = {name: (src.DATA / name).read_text(encoding="utf-8")
             for name in ("original-evidence.json", "sandbox-evidence.json", "source-manifest.json")}
    con = sqlite3.connect(src.DATA / "aifence_sources.sqlite3")
    try:
        tables = [r[0] for r in con.execute("select name from sqlite_master where type='table'")]
        for table in tables:
            cur = con.execute(f"select * from {table}")
            cols = [c[0] for c in cur.description]
            rows = [json.dumps(dict(zip(cols, r)), ensure_ascii=False, default=str) for r in cur.fetchall()]
            texts[f"sqlite:{table}"] = "\n".join([",".join(cols)] + rows)
    finally:
        con.close()
    return texts


class DemoSourcesTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not WORKBOOK.exists():
            raise unittest.SkipTest(f"workbook not found: {WORKBOOK}")
        cls.build = src.build_db(WORKBOOK)
        cls.texts = agent_visible_texts()
        cls.original = json.loads((src.DATA / "original-evidence.json").read_text(encoding="utf-8"))
        cls.sandbox = json.loads((src.DATA / "sandbox-evidence.json").read_text(encoding="utf-8"))
        cls.manifest = json.loads((src.DATA / "source-manifest.json").read_text(encoding="utf-8"))
        cls.sales = [o for o in cls.original["observations"]
                     if o["source_ref"]["locator"].startswith("Продажи!row ")]

    def test_answer_key_sheet_is_not_agent_input(self):
        for name, text in self.texts.items():
            self.assertNotIn(HOLDOUT_SHEET, text, name)
        self.assertNotIn(HOLDOUT_SHEET, self.build["sheet_counts"])
        for o in self.original["observations"]:
            self.assertNotIn(HOLDOUT_SHEET, o["source_ref"]["locator"])

    def test_sales_observations_shape(self):
        self.assertEqual(len(self.sales), 58)  # 29 weeks x 2 product families
        self.assertEqual({o["product_key"] for o in self.sales}, {KES, ASTRA})
        for o in self.sales:
            with self.subTest(locator=o["source_ref"]["locator"]):
                self.assertEqual(o["metric_key"], "orders_index")
                self.assertEqual(o["unit"], "index")
                self.assertEqual(o["segment"], "self_service")
                start = dt.date.fromisoformat(o["period"]["from"])
                self.assertEqual(o["period"]["to"], (start + dt.timedelta(days=6)).isoformat())
                self.assertIn("not a count of orders or revenue", " ".join(o["caveats"]))

    def test_window_means_match_reference(self):
        for start, end, weeks, means in WINDOWS:
            for product, expected in means.items():
                values = [o["value"] for o in self.sales
                          if o["product_key"] == product and o["status"] == "observed"
                          and start <= o["period"]["from"] <= end]  # window is defined by week start dates
                with self.subTest(window=start, product=product):
                    self.assertEqual(len(values), weeks)
                    self.assertAlmostEqual(sum(values) / len(values), expected, delta=TOLERANCE)

    def test_scenario_metadata_has_no_answer_hints(self):
        self.assertEqual(set(self.manifest["scenarios"]), SCENARIO_IDS)
        for sid, meta in self.manifest["scenarios"].items():
            self.assertEqual(set(meta), {"title", "description"}, sid)
        for name in ("source-manifest.json", "sandbox-evidence.json", "sqlite:scenarios"):
            for hint in SCENARIO_HINTS:
                self.assertNotIn(hint, self.texts[name], f"{name} contains {hint!r}")

    def test_sandbox_scenarios_kept(self):
        ids = {o["details"]["scenario_id"] for o in self.sandbox["observations"]}
        self.assertEqual(ids, SCENARIO_IDS)


if __name__ == "__main__":
    unittest.main()
