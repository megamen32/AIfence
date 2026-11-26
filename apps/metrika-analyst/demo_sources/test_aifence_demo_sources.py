"""Offline import regression checks using synthetic worksheet cells."""
import unittest

import aifence_demo_sources as src


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
        self.assertTrue(all(set(v) == {"title", "description"} for v in src.SCENARIOS.values()))
