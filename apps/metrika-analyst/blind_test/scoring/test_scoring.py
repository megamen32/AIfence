import importlib.util
import json
import tempfile
import unittest
from pathlib import Path

BASE = Path(__file__).resolve().parent

def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, BASE / filename)
    module = importlib.util.module_from_spec(spec); spec.loader.exec_module(module); return module

class BlindScoreTests(unittest.TestCase):
    def test_score_and_false_alarm(self):
        scorer = load("scorer", "score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); answers=root/"answers"; cards=root/"cards"; answers.mkdir(); cards.mkdir()
            (answers/"S4.json").write_text(json.dumps({"scenario":"S4","family":"KES","week":"2026-10-05","cause_type":"release_regression"}))
            (cards/"S4.json").write_text(json.dumps({"family":"KES","week":"2026-10-05","cause_type":"release_regression"}))
            self.assertEqual(scorer.score_one(answers/"S4.json",cards/"S4.json")["status"],"correct")
            (cards/"S4.json").write_text(json.dumps({"family":"Astra","week":"2026-10-06","cause_type":"release_regression"}))
            self.assertTrue(scorer.score_one(answers/"S4.json",cards/"S4.json")["false_alarm"])
    def test_clean_silence(self):
        scorer=load("scorer","score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); a=root/"S0.json"; c=root/"S0-card.json"
            a.write_text(json.dumps({"scenario":"S0","cause_type":"clean"})); c.write_text(json.dumps({"cause_type":"clean"}))
            self.assertEqual(scorer.score_one(a,c)["status"],"correct_silence")
    def test_clean_false_alarm(self):
        scorer = load("scorer", "score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); a=root/"S0.json"; c=root/"S0-card.json"
            a.write_text(json.dumps({"scenario":"S0","cause_type":"clean"})); c.write_text(json.dumps({"family":"KES","week":"2026-10-05","cause_type":"clean"}))
            self.assertEqual(scorer.score_one(a,c)["status"],"false_alarm")

    def test_nested_generator_layout(self):
        scorer = load("scorer", "score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); answers=root/"answers"/"set-abc"; cards=root/"cards"/"set-abc"
            answers.mkdir(parents=True); cards.mkdir(parents=True)
            (answers/"S1.json").write_text(json.dumps({"scenario":"S1","family":"KES","week":"2026-10-05","cause_type":"tracking_gap"}))
            (cards/"card.json").write_text(json.dumps({"found":True,"family":"KES","week":"2026-10-05","cause_type":"tracking_gap"}))
            out=root/"score.json"
            import subprocess, sys
            proc=subprocess.run([sys.executable,str(Path(__file__).with_name("score_agent.py")),"--answers",str(root/"answers"),"--cards",str(root/"cards"),"--out",str(out)],capture_output=True,text=True)
            self.assertEqual(proc.returncode,0); self.assertIn('"correct": 1',out.read_text())

    def test_sales_xlsx(self):
        exporter=load("exporter","export_sales.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); truth=root/"truth.json"; out=root/"Продажи.xlsx"
            truth.write_text(json.dumps({"weeks":[{"week_start":"2026-01-05","product_family":"KES","orders":100,"revenue_rub":1000},{"week_start":"2026-01-12","product_family":"KES","orders":80,"revenue_rub":900}]}))
            exporter.write_xlsx(exporter.aggregate(exporter.rows_from_truth(truth)),out)
            self.assertEqual(out.read_bytes()[:2],b"PK")

if __name__=="__main__":
    unittest.main()
