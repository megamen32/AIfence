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

    def test_missing_card_clean_is_silence(self):
        scorer = load("scorer", "score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root = Path(td); answers = root / "answers"; cards = root / "cards"
            answers.mkdir(); cards.mkdir()
            (answers / "S0.json").write_text(json.dumps({"scenario": "S0", "cause_type": "none"}))
            out = root / "score.json"
            import subprocess, sys
            proc = subprocess.run([sys.executable, str(Path(__file__).with_name("score_agent.py")),
                                   "--answers", str(answers), "--cards", str(cards), "--out", str(out)],
                                  capture_output=True, text=True)
            self.assertEqual(proc.returncode, 0)
            payload = json.loads(out.read_text())
            self.assertEqual(payload["rows"][0]["status"], "correct_silence")
            self.assertEqual(payload["summary"]["correct"], 1)

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

    def test_new_cause_aliases(self):
        scorer = load("scorer", "score_agent.py")
        self.assertEqual(scorer.type_norm("bot traffic"), "traffic_quality")
        self.assertEqual(scorer.type_norm("переименование"), "taxonomy_migration")
        self.assertEqual(scorer.type_norm("late attribution"), "late_event_delivery")
        self.assertEqual(scorer.type_norm("masked regression"), "masked_multi_cause_regression")
        self.assertEqual(scorer.type_norm("experiment contamination"), "experiment_allocation_shift")

    def test_s12_accepts_no_alert_and_insufficient_data(self):
        scorer = load("scorer", "score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td)
            answer=root/"S12.json"
            answer.write_text(json.dumps({
                "scenario":"S12", "family":None, "week":None,
                "cause_type":"honest_refusal",
                "accepted_outcomes":["no_alert", "insufficient_data"]
            }))
            no_alert=root/"no-alert.json"
            no_alert.write_text(json.dumps({"outcome":"no_alert"}))
            insufficient=root/"insufficient.json"
            insufficient.write_text(json.dumps({"status":"insufficient_data"}))
            row1=scorer.score_one(answer,no_alert)
            row2=scorer.score_one(answer,insufficient)
            self.assertEqual(row1["status"], "correct_no_alert")
            self.assertTrue(row1["outcome_correct"])
            self.assertEqual(row2["status"], "correct_insufficient_data")
            self.assertTrue(row2["outcome_correct"])

    def test_s12_alert_is_false_alarm(self):
        scorer = load("scorer", "score_agent.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td)
            answer=root/"S12.json"
            answer.write_text(json.dumps({
                "scenario":"S12", "cause_type":"honest_refusal",
                "accepted_outcomes":["no_alert", "insufficient_data"]
            }))
            card=root/"card.json"
            card.write_text(json.dumps({"found":True, "family":"KES", "week":"2026-10-05", "cause_type":"traffic_quality"}))
            row=scorer.score_one(answer,card)
            self.assertEqual(row["status"], "false_alarm")
            self.assertTrue(row["false_alarm"])

    def test_sales_xlsx(self):
        exporter=load("exporter","export_sales.py")
        with tempfile.TemporaryDirectory() as td:
            root=Path(td); truth=root/"truth.json"; out=root/"Продажи.xlsx"
            truth.write_text(json.dumps({"weeks":[{"week_start":"2026-01-05","product_family":"KES","orders":100,"revenue_rub":1000},{"week_start":"2026-01-12","product_family":"KES","orders":80,"revenue_rub":900}]}))
            exporter.write_xlsx(exporter.aggregate(exporter.rows_from_truth(truth)),out)
            self.assertEqual(out.read_bytes()[:2],b"PK")

if __name__=="__main__":
    unittest.main()
