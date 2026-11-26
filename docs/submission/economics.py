#!/usr/bin/env python3
"""Calculate an explicitly sourced operating-cost and opportunity scenario."""
import argparse
import json
from decimal import Decimal, InvalidOperation
from pathlib import Path


def calculate(data):
    def number(key):
        value = data.get(key)
        if value is None or value == "":
            return None
        try:
            n = Decimal(str(value))
        except InvalidOperation as exc:
            raise ValueError(f"{key}: expected a number") from exc
        if not n.is_finite() or n < 0:
            raise ValueError(f"{key}: expected a finite nonnegative number")
        return n

    keys = ("input_tokens", "output_tokens", "input_rub_per_million", "output_rub_per_million",
            "compute_rub_per_hour", "run_seconds", "other_rub_per_run", "runs_per_week",
            "baseline_orders_per_week", "observed_orders_per_week", "contribution_rub_per_order",
            "weeks_earlier", "recoverable_fraction")
    v = {k: number(k) for k in keys}
    if v["recoverable_fraction"] is not None and v["recoverable_fraction"] > 1:
        raise ValueError("recoverable_fraction must be between 0 and 1")
    def calc(names, fn):
        return fn(*[v[n] for n in names]) if all(v[n] is not None for n in names) else None
    model = calc(keys[:4], lambda i, o, pi, po: (i*pi + o*po)/Decimal(1000000))
    compute = calc(keys[4:6], lambda rate, seconds: rate*seconds/Decimal(3600))
    other = v["other_rub_per_run"]
    run = model + compute + other if None not in (model, compute, other) else None
    weekly = run * v["runs_per_week"] if run is not None and v["runs_per_week"] is not None else None
    gap = calc(keys[8:10], lambda baseline, observed: max(Decimal(0), baseline-observed))
    opportunity = (gap * v["contribution_rub_per_order"] * v["weeks_earlier"] * v["recoverable_fraction"]
                   if gap is not None and all(v[k] is not None for k in keys[10:13]) else None)
    return {k: (str(value) if value is not None else None) for k, value in {
        "model_rub_per_run": model, "compute_rub_per_run": compute,
        "total_rub_per_run": run, "total_rub_per_week": weekly,
        "order_gap_per_week": gap, "scenario_opportunity_rub": opportunity}.items()}


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("input", type=Path, help="private JSON file; see economics.md")
    args = parser.parse_args()
    try:
        result = calculate(json.loads(args.input.read_text(encoding="utf-8")))
    except (OSError, ValueError, TypeError, AttributeError) as exc:
        parser.error(str(exc))
    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
