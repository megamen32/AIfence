import json
import re
import statistics
import tempfile
import unittest
import zipfile
from pathlib import Path

import blind_test_generator as g

SKU_ALIAS = {p["sku"].lower(): family for family, p in g.PRODUCTS.items()}
LEAK_FIELDS = ("bot_share_estimate", "traffic_quality", "traffic_source", "canonical_product_key",
               "availability_status", "available_on", "alias_from", "alias_to", "traffic_campaign",
               "experiment_id", "allocation_status", "our_price_rub", "competitor_price_rub")


def four_week_drops(observations, metric, by_device):
    series = {}
    for o in observations:
        if o["metric_key"] != metric or o["value"] is None:
            continue
        week = int(o["source_ref"]["locator"].split(";")[0].split("=")[1])
        key = (SKU_ALIAS.get(o["product_key"], o["product_key"]), o["segment"] if by_device else "all")
        series.setdefault(key, {}).setdefault(week, 0)
        series[key][week] += o["value"]
    out = []
    for key, values in series.items():
        for end in range(7, max(values) + 1):
            a = [values.get(w) for w in range(end - 7, end - 3)]
            b = [values.get(w) for w in range(end - 3, end + 1)]
            if None in a or None in b or not sum(a):
                continue
            out.append((sum(b) / sum(a) - 1, key, end))
    return out


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
        fields = ("visits", "view_rate", "cart_rate", "checkout_rate", "order_rate", "price")
        profiles = {tuple(product[field] for field in fields) for product in g.PRODUCTS.values()}
        self.assertGreaterEqual(len(g.PRODUCTS), 5)
        self.assertEqual(len(profiles), len(g.PRODUCTS))
        self.assertEqual({family: product["sku"] for family, product in g.PRODUCTS.items()}, {
            "kaspersky_small_office_security": "KL4141RCEFR",
            "dr_web_desktop_security_suite": "LBW-BC-12M-100-A3",
            "astra_linux_special_edition": "OS2304ELB81BOX000WS01-SO36",
            "microsoft_365_business_premium": "CFQ7TTC0LCHC-0002-P1YY",
            "red_hat_enterprise_linux_server_premium": "RH00003",
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
                family = SKU_ALIAS.get(row["product_key"], row["product_key"])
                self.assertNotIn("canonical_product_key", details)
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
                canonical_families = {SKU_ALIAS.get(row["product_key"], row["product_key"])
                                      for row in evidence["observations"]}
                self.assertEqual(canonical_families, set(g.PRODUCTS))
                self.assertEqual(truth["choice"]["scenario"], scenario)
                self.assertEqual({row["family"] for row in truth["truth"]}, set(g.PRODUCTS))
                self.assertEqual(len(truth["truth"]), 28 * len(g.PRODUCTS) * len(g.DEVICES))
                for field in ("family", "sku", "catalog_url"):
                    self.assertTrue(all(row.get(field) for row in truth["truth"]))
                family = truth["choice"]["family"]; event = truth["choice"]["week"]
                if scenario == "S6":
                    bounce = {o["source_ref"]["locator"].split(";")[0]: o["value"] for o in evidence["observations"]
                              if o["product_key"] == family and o["segment"] == "desktop" and o["metric_key"] == "bounce_rate"}
                    self.assertGreater(bounce[f"week={event}"], 2 * bounce[f"week={event - 1}"])
                elif scenario == "S7":
                    alias = g.PRODUCTS[family]["sku"].lower()
                    weeks = {int(o["source_ref"]["locator"].split(";")[0][5:]) for o in evidence["observations"] if o["product_key"] == alias}
                    self.assertEqual(min(weeks), event)
                    self.assertFalse(any(o["product_key"] == family and o["period"]["from"] >= g.week_start(event).isoformat()
                                         for o in evidence["observations"]))
                elif scenario == "S12":
                    answer = json.loads((Path(result["answers"]) / "S12.json").read_text())
                    self.assertEqual(answer["accepted_outcomes"], ["no_alert", "insufficient_data"])
                    self.assertEqual(g.score_card(answer, {"outcome": "no_alert"})["status"], "correct_no_alert")
                    self.assertEqual(g.score_card(answer, {"status": "insufficient_data"})["status"], "correct_insufficient_data")
                elif scenario == "S13":
                    last = g.week_start(27).isoformat()
                    seen = sum(o["value"] for o in evidence["observations"] if o["product_key"] == family
                               and o["metric_key"] == "orders" and o["period"]["from"] == last)
                    actual = sum(r["orders"] for r in truth["truth"] if r["family"] == family and r["week"] == 27)
                    self.assertLess(seen, .3 * actual)
                    self.assertEqual(evidence["status"], "ok")
                else:
                    start = g.week_start(truth["choice"]["week"]).isoformat()
                    actual = {(r["week_start"], r["device"]): r["cart_additions"]
                              for r in truth["truth"] if r["family"] == family}
                    retention = {False: [], True: []}
                    for o in evidence["observations"]:
                        if o["product_key"] == family and o["metric_key"] == "cart_additions":
                            date = o["period"]["from"]
                            retention[date >= start].append(o["value"] / actual[date, o["segment"]])
                    self.assertLess(statistics.mean(retention[True]), statistics.mean(retention[False]))

    def test_s0_noise_floor(self):
        # A clean dataset must not show the drops the detector alerts on.
        with tempfile.TemporaryDirectory() as tmp:
            for seed in range(10):
                result = g.write_dataset(Path(tmp) / str(seed), 9100 + seed, "S0", 28)
                obs = json.loads((Path(result["input"]) / "metrika.json").read_text())["observations"]
                self.assertGreaterEqual(min(four_week_drops(obs, "orders", False))[0], -.12, seed)
                self.assertGreaterEqual(min(four_week_drops(obs, "orders", True))[0], -.15, seed)

    def test_planted_change_is_recent_and_on_the_question_family(self):
        with tempfile.TemporaryDirectory() as tmp:
            for seed in range(6):
                for scenario in ("S6", "S7", "S12", "S13", "S14"):
                    choice = g.pick_scenario(seed, scenario, 28)
                    self.assertIn(choice["family"], g.KASPERSKY_FAMILIES)
                    self.assertGreaterEqual(choice["week"], 28 - 6)
                result = g.write_dataset(Path(tmp) / str(seed), 9200 + seed, "S14", 28)
                truth = json.loads(Path(result["truth"]).read_text())["choice"]
                obs = json.loads((Path(result["input"]) / "metrika.json").read_text())["observations"]
                drops = [d for d in four_week_drops(obs, "orders", False) if d[2] == 27]
                worst = min(drops)
                self.assertEqual(worst[1][0], truth["family"])
                noise = min(d for d in drops if d[1][0] != truth["family"])
                self.assertLess(worst[0], 2 * noise[0])
            self.assertIn(g.pick_scenario(1, "S6", 28, "all")["family"], g.PRODUCTS)

    def test_no_answer_fields_and_decoy_releases(self):
        with tempfile.TemporaryDirectory() as tmp:
            for scenario in ("S0", "S6", "S7", "S12", "S13", "S14"):
                result = g.write_dataset(Path(tmp) / scenario, 9300, scenario, 28)
                folder = Path(result["input"])
                for name in ("metrika.json", "releases.json", "backlog.json"):
                    text = (folder / name).read_text()
                    for field in LEAK_FIELDS:
                        self.assertNotIn(f'"{field}"', text, (scenario, name))
                    self.assertNotIn("Synthetic measurement", text)
                releases = json.loads((folder / "releases.json").read_text())
                self.assertGreaterEqual(sum(r["description"] in g.SPECIFIC_RELEASES for r in releases), 4)
                # Every observation keeps machine-readable provenance for the platform.
                obs = json.loads((folder / "metrika.json").read_text())["observations"]
                self.assertTrue(all(o["details"]["synthetic"] and o["details"]["origin"] == "synthetic_fixture" for o in obs))

    def test_sales_prices_are_catalog_prices(self):
        with tempfile.TemporaryDirectory() as tmp:
            result = g.write_dataset(Path(tmp), 9400, "S0", 28)
            truth = json.loads(Path(result["truth"]).read_text())["truth"]
            for row in truth:
                self.assertEqual(row["unit_price"], g.PRODUCTS[row["family"]]["price"])
            self.assertEqual(g.PRODUCTS["kaspersky_small_office_security"]["price"], 4718.70)

    def test_agent_inputs_exclude_seeds_and_scenario_verdicts(self):
        forbidden = (
            "seed:", "seed_hash", "context_flag", "checkout_regression_pct",
            "additional_tracking_loss_pct", "accepted decision", "secondary_causes",
            "real checkout regression",
        )
        seed = 20261127
        with tempfile.TemporaryDirectory() as tmp:
            for scenario in ("S6", "S7", "S12", "S13", "S14"):
                with self.subTest(scenario=scenario):
                    result = g.write_dataset(Path(tmp), seed, scenario, 28)
                    folder = Path(result["input"])
                    docs = [path.read_text() for path in folder.glob("*.json")]
                    with zipfile.ZipFile(folder / "Продажи.xlsx") as workbook:
                        docs.extend(workbook.read(name).decode() for name in workbook.namelist()
                                    if name.startswith("xl/worksheets/"))
                    for text in docs:
                        for token in (*forbidden, str(seed)):
                            self.assertNotIn(token, text)



if __name__ == "__main__":
    unittest.main()
