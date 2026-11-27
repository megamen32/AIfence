import json
import re
import zipfile
from pathlib import Path

import blind_test_generator as g


def test_same_seed_is_byte_deterministic(tmp_path):
    a = g.write_dataset(tmp_path / "a", 20261126, "S2", 28)
    b = g.write_dataset(tmp_path / "b", 20261126, "S2", 28)
    for name in ("metrika.json", "releases.json", "backlog.json", "manifest.json", "Продажи.xlsx"):
        assert (Path(a["input"]) / name).read_bytes() == (Path(b["input"]) / name).read_bytes()
    assert (Path(a["answers"]) / "S2.json").read_bytes() == (Path(b["answers"]) / "S2.json").read_bytes()


def test_different_seed_changes_truth(tmp_path):
    a = g.write_dataset(tmp_path / "a", 1, "S4", 28)
    b = g.write_dataset(tmp_path / "b", 2, "S4", 28)
    assert Path(a["truth"]).read_bytes() != Path(b["truth"]).read_bytes()


def test_input_does_not_contain_answers_and_has_26_weeks(tmp_path):
    result = g.write_dataset(tmp_path, 20261126, "S1", 28)
    input_dir = Path(result["input"])
    truth = json.loads(Path(result["truth"]).read_text())
    assert len(truth["truth"]) == 28 * len(g.PRODUCTS) * len(g.DEVICES)
    for p in input_dir.glob("*.json"):
        text = p.read_text()
        assert re.search(r"\bS(?:0|1|2|3|4|6|7|12|13|14)\b", text) is None
    assert (Path(result["answers"]) / "S1.json").exists()


def test_measurement_loss_is_10_to_25_percent(tmp_path):
    result = g.write_dataset(tmp_path, 20261126, "S0", 28)
    losses = json.loads(Path(result["truth"]).read_text())["losses"]
    assert losses and min(losses) >= .10 and max(losses) <= .25


def test_s1_has_missing_cart_after_failure_week(tmp_path):
    result = g.write_dataset(tmp_path, 20261126, "S1", 28)
    truth = json.loads(Path(result["truth"]).read_text())
    answer = json.loads((Path(result["answers"]) / "S1.json").read_text())
    evidence = json.loads((Path(result["input"]) / "metrika.json").read_text())
    missing = [o for o in evidence["observations"] if o["metric_key"] == "cart_additions" and o["status"] == "missing"]
    assert missing
    assert answer["family"] == truth["choice"]["family"]
    assert answer["week_index"] == truth["choice"]["week"]
    assert answer["week"] == g.week_start(truth["choice"]["week"]).isoformat()


def test_xlsx_is_real_zip(tmp_path):
    result = g.write_dataset(tmp_path, 20261126, "S0", 28)
    with zipfile.ZipFile(Path(result["input"]) / "Продажи.xlsx") as workbook:
        assert "xl/workbook.xml" in workbook.namelist()
        assert "xl/worksheets/sheet1.xml" in workbook.namelist()


def test_s0_silence_scores_as_correct():
    answer = {"scenario": "S0", "family": None, "week": None, "cause_type": "none"}
    assert g.score_card(answer, None)["correct_cause"]
