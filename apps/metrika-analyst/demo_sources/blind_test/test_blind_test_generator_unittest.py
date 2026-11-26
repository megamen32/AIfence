import json
import re
import tempfile
import unittest
import zipfile
from pathlib import Path

import blind_test_generator as g


class BlindGeneratorTests(unittest.TestCase):
    def test_determinism_and_hidden_answers(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            a = g.write_dataset(root / "a", 20261126, "S2", 28)
            b = g.write_dataset(root / "b", 20261126, "S2", 28)
            for name in ("metrika.json", "releases.json", "backlog.json", "manifest.json", "Продажи.xlsx"):
                self.assertEqual((Path(a["input"]) / name).read_bytes(), (Path(b["input"]) / name).read_bytes())
            self.assertEqual((Path(a["answers"]) / "S2.json").read_bytes(), (Path(b["answers"]) / "S2.json").read_bytes())
            for p in Path(a["input"]).glob("*.json"):
                text = p.read_text()
                self.assertTrue(re.search(r"\bS(?:0|1|2|3|4|6|7|12|13|14)\b", text) is None)

    def test_minimum_family_count_and_independent_catalog_profiles(self):
        fields = ("visits", "view_rate", "cart_rate", "checkout_rate", "order_rate", "aov")
        profiles = {tuple(product[field] for field in fields) for product in g.PRODUCTS.values()}
        self.assertGreaterEqual(len(g.PRODUCTS), 5)
        self.assertEqual(len(profiles), len(g.PRODUCTS))
        self.assertEqual({family: product["sku"] for family, product in g.PRODUCTS.items()}, {
            "kaspersky_small_office_security": "KL4141RCEFR",
            "dr_web_desktop_security_suite": "LBW-BC-12M-100-A3",
            "astra_linux_special_edition": "OS2304ELB81BOX000WS01-SO36",
            "microsoft_365_family": "6GQ-00084",
            "vmware_vsphere_starter_kit": "PRGN-0121-105",
            "kaspersky_endpoint_security": "KL4210RAKDE",
            "myoffice_private_cloud": "X2-CLD-NB-3NUNL-A",
        })
        self.assertEqual(len({product["catalog_url"] for product in g.PRODUCTS.values()}), len(g.PRODUCTS))
        self.assertEqual(g.PRODUCTS["astra_linux_special_edition"]["catalog_url"],
                         "https://www.softmagazin.ru/pao-gruppa-astra/os2304elb81box000ws01-so36/")

    def test_catalog_identity_and_frozen_output(self):
        with tempfile.TemporaryDirectory() as tmp:
            output = Path(tmp)
            result = g.write_dataset(output, 20261126, "S7", 28)
            folder = Path(result["input"])
            snapshot = {p.name: p.read_bytes() for p in folder.iterdir()}
            evidence = snapshot["metrika.json"]
            allowed_keys = set(g.PRODUCTS) | {p["sku"].lower() for p in g.PRODUCTS.values()}
            for row in json.loads(evidence)["observations"]:
                details = row["details"]
                family = details.get("canonical_product_key", row["product_key"])
                self.assertIn(row["product_key"], allowed_keys)
                self.assertTrue(details["synthetic"])
                self.assertEqual(details["sku"], g.PRODUCTS[family]["sku"])
                self.assertEqual(details["product_label"], g.PRODUCTS[family]["label"])
                self.assertEqual(details["catalog_url"], g.PRODUCTS[family]["catalog_url"])
                if row["product_key"] != family:
                    self.assertEqual(row["product_key"], g.PRODUCTS[family]["sku"].lower())
            for name in ("releases.json", "backlog.json"):
                for row in json.loads((folder / name).read_text()):
                    self.assertTrue(row["synthetic"])
                    self.assertEqual(row["sku"], g.PRODUCTS[row["family"]]["sku"])
                    self.assertEqual(row["catalog_url"], g.PRODUCTS[row["family"]]["catalog_url"])
            with self.assertRaises(FileExistsError):
                g.write_dataset(output, 20261126, "S7", 28)
            self.assertEqual({p.name: p.read_bytes() for p in folder.iterdir()}, snapshot)

    def test_loss_range_and_shape(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = g.write_dataset(Path(tmp), 20261126, "S1", 28)
            truth = json.loads(Path(result["truth"]).read_text())
            self.assertEqual(len(truth["truth"]), 28 * len(g.PRODUCTS) * len(g.DEVICES))
            self.assertGreaterEqual(min(truth["losses"]), .10)
            self.assertLessEqual(max(truth["losses"]), .25)

    def test_s1_gap_and_xlsx(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = g.write_dataset(Path(tmp), 20261126, "S1", 28)
            evidence = json.loads((Path(result["input"]) / "metrika.json").read_text())
            self.assertTrue(any(o["status"] == "missing" and o["metric_key"] == "cart_additions" for o in evidence["observations"]))
            with zipfile.ZipFile(Path(result["input"]) / "Продажи.xlsx") as workbook:
                self.assertIn("xl/workbook.xml", workbook.namelist())

    def test_s0_silence(self):
        answer = {"scenario": "S0", "family": None, "week": None, "cause_type": "none"}
        self.assertTrue(g.score_card(answer, None)["correct_cause"])

    def test_selected_scenarios_have_distinct_signatures(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            for scenario in ("S6", "S7", "S12", "S13", "S14"):
                result = g.write_dataset(root / scenario, 20261127, scenario, 28)
                truth = json.loads(Path(result["truth"]).read_text())
                evidence = json.loads((Path(result["input"]) / "metrika.json").read_text())
                canonical_families = {
                    row["details"].get("canonical_product_key", row["product_key"])
                    for row in evidence["observations"]
                }
                self.assertEqual(canonical_families, set(g.PRODUCTS))
                self.assertEqual(truth["choice"]["scenario"], scenario)
                self.assertEqual({row["family"] for row in truth["truth"]}, set(g.PRODUCTS))
                self.assertEqual(len(truth["truth"]), 28 * len(g.PRODUCTS) * len(g.DEVICES))
                for field in ("family", "sku", "catalog_url"):
                    self.assertTrue(all(row.get(field) for row in truth["truth"]))
                if scenario == "S6":
                    self.assertTrue(any(o["details"].get("traffic_quality") == "low" for o in evidence["observations"]))
                elif scenario == "S7":
                    self.assertTrue(any(o["product_key"] == o["details"]["sku"].lower() and o["details"].get("canonical_product_key") in g.PRODUCTS for o in evidence["observations"]))
                elif scenario == "S12":
                    answer = json.loads((Path(result["answers"]) / "S12.json").read_text())
                    self.assertEqual(answer["accepted_outcomes"], ["no_alert", "insufficient_data"])
                    self.assertEqual(g.score_card(answer, {"outcome": "no_alert"})["status"], "correct_no_alert")
                    self.assertEqual(g.score_card(answer, {"status": "insufficient_data"})["status"], "correct_insufficient_data")
                elif scenario == "S13":
                    self.assertTrue(any(o["status"] == "missing" and o["details"].get("availability_status") == "late" and o["details"].get("available_on") for o in evidence["observations"]))
                else:
                    self.assertTrue(any(o["details"].get("additional_tracking_loss_pct") == 18 for o in evidence["observations"]))


if __name__ == "__main__":
    unittest.main()
