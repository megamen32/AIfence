#!/usr/bin/env python3
"""Calculate measured Terra fast blind30 run cost in USD."""
import argparse
import json
from decimal import Decimal, InvalidOperation
from pathlib import Path


def _number(data, key):
    value = data.get(key)
    if value is None or value == "":
        raise ValueError(f"{key}: value is required")
    try:
        number = Decimal(str(value))
    except InvalidOperation as exc:
        raise ValueError(f"{key}: expected a number") from exc
    if not number.is_finite() or number < 0:
        raise ValueError(f"{key}: expected a finite nonnegative number")
    return number


def calculate(data):
    """Return the live cost formula: price × cases × model calls."""
    price = _number(data, "price_usd_per_case")
    cases = _number(data, "cases_per_period")
    calls = _number(data, "model_call_count")
    if cases == 0 or calls == 0:
        raise ValueError("cases_per_period and model_call_count must be positive")
    total = price * cases * calls
    result = {
        "price_usd_per_case": price,
        "cases_per_period": cases,
        "model_call_count": calls,
        "cost_usd_per_period": total,
    }
    for key in ("correct_cause", "false_alarms", "median_seconds", "errors"):
        if key in data and data[key] not in (None, ""):
            result[key] = _number(data, key)
    return {key: str(value) for key, value in result.items()}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=Path)
    args = parser.parse_args()
    try:
        data = json.loads(args.input.read_text(encoding="utf-8"))
        result = calculate(data)
    except (OSError, ValueError, TypeError, json.JSONDecodeError) as exc:
        parser.error(str(exc))
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
