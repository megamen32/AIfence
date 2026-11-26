"""Offline import regression checks using synthetic worksheet cells."""
import json
import unittest
from urllib.parse import parse_qs

import aifence_demo_sources as src
import aifence_seed_metrika as seed
from blind_test.blind_test_generator import PRODUCTS, catalog_reference


class DemoSourcesTest(unittest.TestCase):
    def test_sales_row_locator_and_incomplete_window(self):
        rows = src.xlsx_rows({
            1: dict(A="week_start", B="product_family", C="orders_index", G="comment"),
            4: dict(A="2034-01-02", B="Kaspersky Endpoint Security", C=71, G="Неполная неделя"),
            8: dict(A="2034-01-09", B="Astra Linux Special Edition", G="нет данных"),
        })
        target, control = src.sales_index_observations({"Продажи": rows})
        self.assertEqual(target["source_ref"]["locator"], "Продажи!row 4")
        self.assertEqual(target["period"], {"from": "2034-01-02", "to": "2034-01-08"})
        self.assertTrue(target["details"]["partial_week"])
        self.assertEqual(control["source_ref"]["locator"], "Продажи!row 8")
        self.assertEqual(control["status"], "missing")
        self.assertIsNone(control["value"])

    def test_holdout_references_never_become_rows(self):
        rows = src.xlsx_rows({2: dict(A="Эталонный_кейс"), 4: dict(B="synthetic")})
        self.assertEqual(rows, [[], [], [], ["", "synthetic"]])

    def test_sandbox_has_explicit_synthetic_provenance(self):
        observations = src.sandbox_observations()
        self.assertEqual({o["details"]["scenario_id"] for o in observations}, set(src.SCENARIOS))
        self.assertTrue(all(o["details"]["synthetic"] for o in observations))
        self.assertGreaterEqual(len({o["product_key"] for o in observations}), 5)
        for o in observations:
            for key, value in catalog_reference(o["product_key"]).items():
                self.assertEqual(o["details"][key], value)
        self.assertTrue(all(set(v) == {"title", "description"} for v in src.SCENARIOS.values()))

    def test_seed_pageviews_keep_catalog_identity(self):
        _, batches, _ = seed.make_data("catalog", 1800000000)
        families = set()
        for batch in batches:
            for line in batch:
                params = parse_qs(line).get("params")
                if not params:
                    continue
                event = json.loads(params[0])["aifence_demo"]
                family = event["product"]
                self.assertEqual(event["synthetic"], "yes")
                if family != "lk":
                    families.add(family)
                    for key, value in catalog_reference(family).items():
                        self.assertEqual(event[key], value)
        self.assertEqual(families, set(PRODUCTS))
