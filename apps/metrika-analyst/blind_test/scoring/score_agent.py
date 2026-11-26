#!/usr/bin/env python3
"""Score an agent card against a hidden blind-test answer.

The scorer deliberately lives outside the generator/input tree.  It accepts
JSON or Markdown cards and emits a compact JSON plus a CSV table suitable for
the hackathon video.
"""
from __future__ import annotations

import argparse
import csv
import json
import re
import sys
from pathlib import Path
from typing import Any


FAMILY_ALIASES = {
    "kes": {"kes", "kaspersky", "kaspersky_endpoint_security", "kaspersky_configurator", "конфигуратор_kaspersky", "kaspersky_endpoint_security"},
    "astra": {"astra", "astra_linux", "astra_control", "astra_configurator", "конфигуратор_astra", "конфигуратор_астра"},
}
def family_norm(value: Any) -> str:
    key = norm(value)
    for canonical, aliases in FAMILY_ALIASES.items():
        if key == canonical or key in {norm(x) for x in aliases}:
            return canonical
    return key

TYPE_ALIASES = {
    "tracking_gap": {"tracking_gap", "учет", "сбой учета", "недоучет", "measurement_gap", "instrumentation"},
    "mobile_regression": {"mobile_regression", "mobile", "мобильный регресс", "регресс мобильного"},
    "price_competition": {"price_competition", "price", "цена", "конкурент", "конкурентная цена"},
    "release_regression": {"release_regression", "release", "релиз", "регресс после релиза"},
    "traffic_quality": {"traffic_quality", "bot_traffic", "bots", "боты", "ботовый трафик", "некачественный трафик"},
    "taxonomy_migration": {"taxonomy_migration", "catalog_rename", "product_rename", "переименование", "миграция справочника"},
    "late_event_delivery": {"late_event_delivery", "late_attribution", "delayed_attribution", "задержка событий", "запаздывающие события"},
    "masked_multi_cause_regression": {"masked_multi_cause_regression", "multi_cause", "masked_regression", "замаскированный регресс", "две причины"},
    "experiment_allocation_shift": {"experiment_allocation_shift", "experiment_contamination", "ab_allocation", "a_b", "алокация эксперимента", "эксперимент"},
    "clean": {"clean", "none", "no_issue", "нет проблемы", "чистый набор", "проблема не найдена"},
}

OUTCOME_ALIASES = {
    "no_alert": {"no_alert", "no alert", "silent", "silence", "correct_silence", "no_issue", "clean", "нет тревоги", "без тревоги", "молчание"},
    "insufficient_data": {"insufficient_data", "insufficient", "inconclusive", "unavailable", "incomparable", "not_enough_data", "недостаточно данных", "данных недостаточно", "нельзя сделать вывод", "неопределенно"},
}



def norm(value: Any) -> str:
    text = str(value or "").strip().casefold().replace("ё", "е")
    return re.sub(r"[^\w]+", "_", text, flags=re.UNICODE).strip("_")


def type_norm(value: Any) -> str:
    key = norm(value)
    for canonical, aliases in TYPE_ALIASES.items():
        if key == canonical or key in {norm(x) for x in aliases}:
            return canonical
    return key


def outcome_norm(value: Any) -> str:
    key = norm(value)
    for canonical, aliases in OUTCOME_ALIASES.items():
        if key == canonical or key in {norm(x) for x in aliases}:
            return canonical
    return key


def _outcome_values(data: dict[str, Any]) -> list[str]:
    values: list[Any] = []
    for key in ("outcome", "expected_outcome", "expected_response", "decision", "disposition", "response", "status"):
        raw = data.get(key)
        if raw not in (None, ""):
            values.extend(raw if isinstance(raw, list) else [raw])
    for key in ("accepted_outcomes", "expected_outcomes", "accepted_responses"):
        raw = data.get(key)
        if raw not in (None, ""):
            values.extend(raw if isinstance(raw, list) else [raw])
    if "alert" in data and isinstance(data.get("alert"), bool):
        values.append("no_alert" if data["alert"] is False else "alert")
    result = []
    for value in values:
        normalized = outcome_norm(value)
        if normalized and normalized not in result:
            result.append(normalized)
    return result


def read_any(path: Path) -> Any:
    if path.suffix.lower() == ".json":
        return json.loads(path.read_text(encoding="utf-8"))
    return path.read_text(encoding="utf-8")


def _first(data: dict[str, Any], *keys: str) -> Any:
    for key in keys:
        if data.get(key) not in (None, ""):
            return data[key]
    return None


def answer_fields(raw: Any, scenario: str) -> dict[str, Any]:
    if isinstance(raw, dict):
        data = raw
        nested = data.get("answer") if isinstance(data.get("answer"), dict) else {}
        data = {**data, **nested}
        family = _first(data, "family", "product_family", "product", "target_family")
        week = _first(data, "week", "week_start", "failure_week", "release_week")
        cause = _first(data, "cause_type", "reason_type", "type", "root_cause")
        scenario_value = _first(data, "scenario", "scenario_id", "id") or scenario
        signals = data.get("signals") or data.get("expected_signals") or []
        if isinstance(signals, dict):
            signals = [signals]
        normalized_cause = type_norm(cause)
        accepted = _outcome_values(data)
        if normalized_cause == "clean" and "no_alert" not in accepted:
            accepted.append("no_alert")
        return {"scenario": str(scenario_value), "family": str(family or ""), "week": str(week or ""),
                "cause_type": normalized_cause, "signals": signals,
                "accepted_outcomes": sorted(set(accepted))}
    text = str(raw)
    def find(*patterns: str) -> str:
        for pattern in patterns:
            match = re.search(pattern, text, flags=re.IGNORECASE | re.MULTILINE)
            if match:
                return match.group(1).strip().strip(" *")
        return ""
    cause_value = type_norm(find(r"(?:cause_type|reason|причин[аы])\s*[:=]\s*([^\n,;]+)"))
    accepted = []
    response_value = find(r"(?:outcome|decision|response|решение|ответ)\s*[:=]\s*([^\n,;]+)")
    if response_value:
        accepted.append(outcome_norm(response_value))
    if cause_value == "clean":
        accepted.append("no_alert")
    return {"scenario": scenario,
            "family": find(r"(?:family|product_family|семейств[оа])\s*[:=]\s*([^\n,;]+)"),
            "week": find(r"(?:week|week_start|недел[яи])\s*[:=]\s*([0-9]{4}-[0-9]{2}-[0-9]{2})"),
            "cause_type": cause_value, "signals": [],
            "accepted_outcomes": sorted(set(x for x in accepted if x))}


def card_fields(raw: Any) -> dict[str, Any]:
    if isinstance(raw, dict):
        signals = raw.get("signals") or raw.get("findings") or raw.get("claims") or []
        if isinstance(signals, dict):
            signals = [signals]
        family = _first(raw, "family", "product_family", "product", "target_family")
        week = _first(raw, "week", "week_start", "failure_week", "release_week")
        cause = _first(raw, "cause_type", "reason_type", "type", "root_cause")
        outcomes = _outcome_values(raw)
        return {"family": str(family or ""), "week": str(week or ""),
                "cause_type": type_norm(cause), "signals": signals,
                "found": raw.get("found"), "outcome": outcomes[0] if outcomes else "",
                "text": json.dumps(raw, ensure_ascii=False)}
    text = str(raw)
    family_match = re.search(r"(?:family|product_family|семейств[оа])\s*[:=]\s*([^\n,;]+)", text, re.I)
    week_match = re.search(r"(?:week|week_start|недел[яи])\s*[:=]\s*([0-9]{4}-[0-9]{2}-[0-9]{2})", text, re.I)
    cause_match = re.search(r"(?:cause_type|reason|причин[аы])\s*[:=]\s*([^\n,;]+)", text, re.I)
    outcome_match = re.search(r"(?:outcome|decision|response|решение|ответ|status)\s*[:=]\s*([^\n,;]+)", text, re.I)
    return {"family": family_match.group(1).strip() if family_match else "",
            "week": week_match.group(1) if week_match else "",
            "cause_type": type_norm(cause_match.group(1) if cause_match else ""),
            "signals": [], "found": None,
            "outcome": outcome_norm(outcome_match.group(1) if outcome_match else ""),
            "text": text}


def signal_matches(card: dict[str, Any], answer: dict[str, Any]) -> bool:
    cf, af = family_norm(card["family"]), family_norm(answer["family"])
    cw, aw = norm(card["week"]), norm(answer["week"])
    if card.get("found") is False:
        return False
    text = norm(card.get("text", ""))
    family_ok = bool(af and (af in cf or cf in af or af in text))
    week_ok = bool(aw and (aw == cw or aw in text))
    if not af and not aw:
        return False
    return family_ok and week_ok


def false_alarm(card: dict[str, Any], answer: dict[str, Any]) -> bool:
    cf, af = family_norm(card["family"]), family_norm(answer["family"])
    cw, aw = norm(card["week"]), norm(answer["week"])
    if not cf and not cw:
        return False
    family_wrong = bool(cf and af and af not in cf and cf not in af)
    week_wrong = bool(cw and aw and cw != aw)
    return family_wrong or week_wrong


def score_one(answer_path: Path, card_path: Path) -> dict[str, Any]:
    scenario = answer_path.stem
    answer = answer_fields(read_any(answer_path), scenario)
    card = card_fields(read_any(card_path))
    clean_expected = answer["cause_type"] == "clean"
    accepted_outcomes = set(answer.get("accepted_outcomes") or [])
    refusal_expected = (not clean_expected) and bool(accepted_outcomes) and (
        accepted_outcomes <= {"no_alert", "insufficient_data"}
        or answer["cause_type"] in {"no_alert", "insufficient_data"}
        or scenario.endswith("/S12")
        or scenario == "S12"
    )
    response_ok = bool(card.get("outcome")) and card.get("outcome") in accepted_outcomes
    has_signal = signal_matches(card, answer)
    has_false_alarm = false_alarm(card, answer)
    cause_correct = card["cause_type"] == answer["cause_type"] if answer["cause_type"] else False
    if refusal_expected:
        if response_ok:
            status = "correct_no_alert" if card["outcome"] == "no_alert" else "correct_insufficient_data"
            found, missed, false, cause = False, False, False, True
        else:
            status = "false_alarm" if has_signal or has_false_alarm or card.get("cause_type") or card.get("family") or card.get("week") else "missed"
            found, missed, false, cause = False, status == "missed", status == "false_alarm", False
    elif clean_expected:
        status = "false_alarm" if has_false_alarm or card["cause_type"] not in ("", "clean") or card["family"] or card["week"] else "correct_silence"
        found, missed, false, cause = False, False, status == "false_alarm", status == "correct_silence"
    else:
        status = "correct" if has_signal and cause_correct else ("found_wrong_cause" if has_signal else "missed")
        found, missed, false, cause = has_signal, not has_signal, has_false_alarm, cause_correct
    return {"scenario": scenario, "expected_family": answer["family"], "expected_week": answer["week"],
            "expected_cause_type": answer["cause_type"], "expected_outcomes": sorted(accepted_outcomes),
            "outcome": card.get("outcome", ""), "found": found, "missed": missed,
            "false_alarm": false, "cause_type_correct": cause, "outcome_correct": response_ok,
            "status": status, "answer_file": str(answer_path), "card_file": str(card_path)}


def ensure_isolated(input_root: Path | None, answers_root: Path) -> None:
    if input_root is None:
        return
    root = input_root.resolve()
    answers = answers_root.resolve()
    if answers == root or root in answers.parents:
        raise SystemExit("hidden answers must be outside the agent input catalog")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--answers", type=Path, required=True, help="directory with hidden <scenario>.json answers")
    parser.add_argument("--cards", type=Path, required=True, help="directory with agent cards, same scenario names")
    parser.add_argument("--input-root", type=Path, help="agent-readable input catalog; used for leakage check")
    parser.add_argument("--out", type=Path, default=Path("score.json"))
    args = parser.parse_args()
    ensure_isolated(args.input_root, args.answers)
    rows = []
    # In the generator layout the answer directory sits beside input/ and
    # truth.json. Never recurse into those agent-visible or QA files.
    answer_paths = sorted(p for p in args.answers.rglob("*.json")
                          if (p.parent == args.answers and args.answers.name == "answers")
                          or p.parent.name == "answers"
                          or p.parent.parent.name == "answers")
    for answer_path in answer_paths:
        # Generator layout is set-<hash>/answers/S#.json with one card at
        # set-<hash>/card.json. Keep the flat layout compatible as well.
        set_root = answer_path.parent.parent if answer_path.parent.name == "answers" else answer_path.parent
        candidates = [args.cards / answer_path.name, args.cards / set_root.name / "card.json", args.cards / "card.json"]
        card_path = next((candidate for candidate in candidates if candidate.exists()), candidates[0])
        scenario_name = f"{set_root.name}/{answer_path.stem}" if answer_path.parent.name == "answers" else answer_path.stem
        if not card_path.exists():
            answer = answer_fields(read_any(answer_path), scenario_name)
            clean = answer["cause_type"] == "clean"
            rows.append({"scenario": scenario_name,
                         "status": "correct_silence" if clean else "missed",
                         "found": False, "missed": not clean, "false_alarm": False,
                         "cause_type_correct": clean, "outcome_correct": False,
                         "expected_family": answer["family"], "expected_week": answer["week"],
                         "expected_cause_type": answer["cause_type"],
                         "expected_outcomes": answer.get("accepted_outcomes", []),
                         "outcome": "", "answer_file": str(answer_path), "card_file": str(card_path)})
        else:
            row = score_one(answer_path, card_path)
            row["scenario"] = scenario_name
            rows.append(row)
    args.out.parent.mkdir(parents=True, exist_ok=True)
    payload = {"schema_version": "aifence.blind-score.v1", "rows": rows,
               "summary": {"scenarios": len(rows), "correct": sum(r["status"] in ("correct", "correct_silence") for r in rows),
                           "missed": sum(r["status"] == "missed" for r in rows),
                           "false_alarm": sum(r["false_alarm"] for r in rows),
                           "cause_type_correct": sum(r["cause_type_correct"] for r in rows)}}
    args.out.write_text(json.dumps(payload, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    csv_path = args.out.with_suffix(".csv")
    fields = ["scenario", "status", "found", "missed", "false_alarm", "cause_type_correct", "outcome_correct", "outcome", "expected_outcomes", "expected_family", "expected_week", "expected_cause_type"]
    with csv_path.open("w", newline="", encoding="utf-8") as fh:
        writer = csv.DictWriter(fh, fieldnames=fields); writer.writeheader(); writer.writerows({k: row.get(k, "") for k in fields} for row in rows)
    print(json.dumps(payload["summary"], ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
