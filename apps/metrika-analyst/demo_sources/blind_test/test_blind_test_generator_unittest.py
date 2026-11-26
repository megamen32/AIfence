import json
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
                self.assertTrue(all(f"S{i}" not in text for i in range(5)))

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


if __name__ == "__main__":
    unittest.main()
