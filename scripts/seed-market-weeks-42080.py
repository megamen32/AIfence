#!/usr/bin/env python3
"""Seed two labelled KES price weeks on the personal Market Watch stand.

The workbook remains outside the repository. The script reads its parsed study
JSON, writes a private per-store JSONL feed, and uses the normal importcatalog
command. Every visible offer name carries the ДЕМО label and its URL contains
the workbook sheet/cell locator used as source_ref.locator by Market Watch.
"""

from __future__ import annotations

import argparse
import json
import os
import subprocess
from datetime import datetime, timedelta
from pathlib import Path


DEFAULT_STUDY = Path.home() / "AIfence/материалы/Материалы заказчика/.study/Store_Конкурентное_исследование.json"
DEFAULT_RUNTIME = Path.home() / "AIfence/.runtime/market-weeks-42080"
DEFAULT_CHECKPOINT = Path.home() / "AIfence/.runtime/market-weeks-42080.uploaded"
IMPORTER = Path.home() / "AIfence/.runtime/bin/importcatalog"
AIR = Path.home() / "AIfence/.runtime/bin/market-air"
APP_DIR = Path.home() / "AIfence/apps/market-watch"

BASELINE_WEEK = "2026-11-09"
CURRENT_WEEK = "2026-11-16"
SKU = "KL4863RAKFS"

# Small, deterministic moves make the seed repeatable. The -32.7% KES value
# belongs to add_to_cart in the story, so the price channel moves only -3.0%.
MOVES = (-0.030, 0.018, -0.021, 0.009, -0.032, 0.014, -0.012, 0.026)


def excel_date(value: str) -> str:
    return (datetime(1899, 12, 30) + timedelta(days=float(value))).strftime("%Y-%m-%d")


def load_rows(path: Path) -> list[tuple[int, dict[str, str]]]:
    sheets = dict(json.loads(path.read_text(encoding="utf-8")))
    rows = dict(sheets["03_Цена"])
    selected = []
    for row_number, row in rows.items():
        if "Kaspersky Endpoint Security" not in row.get("B", ""):
            continue
        if row.get("E") != "Да" or not row.get("I") or not row.get("C"):
            continue
        selected.append((int(row_number), row))
    if not selected:
        raise SystemExit("No populated Kaspersky Endpoint Security rows found in 03_Цена")
    return selected


def offer(row_number: int, row: dict[str, str], week: str, move: float) -> dict[str, object]:
    base_minor = round(float(row["I"]) * 100)
    price_minor = base_minor if week == BASELINE_WEEK else round(base_minor * (1 + move))
    locator = f"03_Цена!I{row_number}"
    # softline is the configured source accepted by the ordinary import route.
    # The original workbook seller remains visible in the labelled name.
    url = f"https://store.softline.ru/Store_Конкурентное_исследование.xlsx/{locator}/"
    return {
        "source": "softline",
        "sku": SKU,
        "name": f"ДЕМО — {row['B']} — {row['A']}",
        "price_minor": price_minor,
        "currency": "RUB",
        "quantity": 1,
        "max_quantity": 1,
        "price_basis": "per_unit",
        "url": url,
        "captured_at": f"{week}T12:00:00Z",
    }


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--study", type=Path, default=DEFAULT_STUDY)
    parser.add_argument("--runtime", type=Path, default=DEFAULT_RUNTIME)
    parser.add_argument("--checkpoint", type=Path, default=DEFAULT_CHECKPOINT)
    parser.add_argument("--url", default=os.environ.get("AIRLOCK_URL", "http://localhost:42080"))
    args = parser.parse_args()

    rows = load_rows(args.study)
    args.runtime.mkdir(parents=True, exist_ok=True)
    feed = args.runtime / "softline/offers.jsonl"
    feed.parent.mkdir(parents=True, exist_ok=True)
    with feed.open("w", encoding="utf-8") as output:
        for index, (row_number, row) in enumerate(rows):
            for week in (BASELINE_WEEK, CURRENT_WEEK):
                output.write(json.dumps(offer(row_number, row, week, MOVES[index]), ensure_ascii=False) + "\n")

    command = [
        str(IMPORTER), "-root", str(args.runtime), "-air", str(AIR),
        "-app-dir", str(APP_DIR), "-url", args.url, "-agent", "market-watch",
        "-checkpoint", str(args.checkpoint),
    ]
    subprocess.run(command, check=True)
    print(f"Seeded {len(rows) * 2} labelled observations for {SKU} on {args.url}")
    print(f"Weeks: {BASELINE_WEEK} and {CURRENT_WEEK}")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
