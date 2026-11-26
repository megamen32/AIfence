#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""Build a deterministic competitor matrix and an agent-vs-reference report.

The workbook is consumed through the checked-in .study JSON representation.  The
agent snapshot is deliberately a separate input: missing live observations stay
missing and never become zero.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import math
import os
import re
import tempfile
from datetime import date as date_type, datetime, timedelta, timezone
from pathlib import Path
from typing import Any, Dict, Iterable, List, Optional, Tuple


DEFAULT_COMPETITORS = ("allsoft.ru", "syssoft.ru", "softmagazin.ru")
BLOCK_WEIGHTS = {
    "Поиск": 10,
    "Цена": 25,
    "Покупка/счёт": 25,
    "Личный кабинет": 20,
    "Сервис/преимущества": 10,
    "UX сайта": 10,
}
BLOCK_SHEETS = {
    "Цена": ("03_Цена", 4),
    "Покупка/счёт": ("04_Покупка_Счет", 4),
    "Личный кабинет": ("06_Личный_Кабинет", 4),
    "Сервис/преимущества": ("07_Сервис_Преимущества", 4),
    "UX сайта": ("08_UX_Сайт", 3),
    "Поиск": ("02_Поиск_Семантика", 4),
}
MANUAL_ONLY = {"Личный кабинет", "Сервис/преимущества", "Поиск"}


def norm(value: Any) -> str:
    return re.sub(r"\s+", " ", str(value or "").strip()).casefold()


def domain(value: Any) -> str:
    text = norm(value).replace("https://", "").replace("http://", "")
    text = text.split("/", 1)[0].split(":", 1)[0]
    if text.startswith("www."):
        text = text[4:]
    aliases = {
        "softmagazin": "softmagazin.ru",
        "softmagazin.ru": "softmagazin.ru",
        "allsoft": "allsoft.ru",
        "allsoft.ru": "allsoft.ru",
        "syssoft": "syssoft.ru",
        "syssoft.ru": "syssoft.ru",
    }
    return aliases.get(text, text)


def numeric(value: Any) -> Optional[float]:
    if isinstance(value, bool) or value is None:
        return None
    if isinstance(value, (int, float)):
        return float(value) if math.isfinite(float(value)) else None
    text = str(value).replace("\xa0", " ").strip()
    if not text:
        return None
    text = re.sub(r"[^\d,.\-]", "", text)
    if not text:
        return None
    if "," in text and "." in text:
        text = text.replace(".", "").replace(",", ".")
    else:
        text = text.replace(",", ".")
    try:
        result = float(text)
    except ValueError:
        return None
    return result if math.isfinite(result) else None


def truth_score(value: Any) -> Optional[float]:
    text = norm(value)
    if not text or text in {"н/д", "н.д.", "—", "-", "не собрано"}:
        return None
    if text in {"да", "есть", "получен", "видно", "доступно", "true", "1"}:
        return 5.0
    if text in {"частично", "иногда", "ограниченно"}:
        return 3.0
    if text in {"нет", "отсутствует", "не получен", "false", "0"}:
        return 1.0
    if text.startswith("трудно") or text.startswith("ошиб"):
        return 2.0
    return None


def observed_date(value: Any) -> str:
    number = numeric(value)
    if number is not None and 20000 <= number <= 80000:
        return (date_type(1899, 12, 30) + timedelta(days=int(number))).isoformat()
    text = str(value or "").strip()
    if re.match(r"^\d{4}-\d{2}-\d{2}", text):
        return text[:10]
    return ""

def price_score(index: Optional[float]) -> Optional[float]:
    if index is None or index <= 0:
        return None
    if index <= 0.90:
        return 5.0
    if index <= 1.00:
        return 4.0
    if index <= 1.10:
        return 3.0
    if index <= 1.25:
        return 2.0
    return 1.0


def step_score(minutes: Optional[float]) -> Optional[float]:
    if minutes is None or minutes < 0:
        return None
    if minutes <= 3:
        return 5.0
    if minutes <= 6:
        return 4.0
    if minutes <= 10:
        return 3.0
    if minutes <= 20:
        return 2.0
    return 1.0


def load_sheets(path: Path) -> Dict[str, Dict[int, Dict[str, Any]]]:
    raw = json.loads(path.read_text(encoding="utf-8"))
    if isinstance(raw, dict) and "sheets" in raw:
        raw = raw["sheets"]
    result: Dict[str, Dict[int, Dict[str, Any]]] = {}
    for item in raw:
        if isinstance(item, dict):
            name, rows = item.get("name"), item.get("rows", [])
        else:
            name, rows = item[0], item[1]
        if not isinstance(name, str):
            continue
        result[name] = {int(row[0]): dict(row[1]) for row in rows if isinstance(row, list) and len(row) == 2}
    return result


def workbook_revision(path: Path) -> str:
    return "sha256:" + hashlib.sha256(path.read_bytes()).hexdigest()


def product_matches(row: Dict[str, Any], target: str) -> bool:
    if not target:
        return True
    text = norm(" ".join(str(row.get(k, "")) for k in ("A", "B", "C", "E", "F", "G", "H")))
    wanted = [token for token in re.findall(r"[a-zа-я0-9]+", norm(target)) if len(token) >= 3]
    if not wanted:
        return True
    aliases = {"kaspersky endpoint security": ("kaspersky", "endpoint"), "kes": ("kaspersky", "endpoint")}
    tokens = aliases.get(norm(target), tuple(wanted))
    return all(token in text for token in tokens)


def cell(
    competitor: str,
    product: str,
    block: str,
    criterion: str,
    value: Any,
    score: Optional[float],
    source: str,
    status: str,
    sheet: str,
    row: int,
    column: str,
    *,
    date: str = "",
    locator: str = "",
    reason: str = "",
) -> Dict[str, Any]:
    return {
        "cell_id": f"{source}/{block}/{competitor}/{product}/{criterion}",
        "competitor": competitor,
        "product": product,
        "block": block,
        "criterion": criterion,
        "value": value,
        "score": score,
        "source": source,
        "status": status,
        "date": date,
        "source_ref": {
            "sheet": sheet,
            "row": row,
            "column": column,
            "locator": locator or f"{sheet}!{column}{row}",
        },
        **({"reason": reason} if reason else {}),
    }


def add_reference_cells(sheets: Dict[str, Dict[int, Dict[str, Any]]], target: str, competitors: set[str]) -> List[Dict[str, Any]]:
    out: List[Dict[str, Any]] = []
    for block, (sheet, header_row) in BLOCK_SHEETS.items():
        rows = sheets.get(sheet, {})
        headers = rows.get(header_row, {})
        for row_number, row in sorted(rows.items()):
            if row_number <= header_row:
                continue
            # Price is SKU-specific. Purchase, UX, search and manual capability
            # rows are site-level evidence in the reference workbook and do not
            # repeat the product name; retain them with their recorded product.
            if block == "Цена" and not product_matches(row, target):
                continue
            competitor = domain(row.get("A"))
            if competitor not in competitors:
                continue
            product = target if block in {"Личный кабинет", "Сервис/преимущества", "UX сайта", "Поиск"} else str(row.get("B") or row.get("C") or target).strip()
            status = "manual_only" if block in MANUAL_ONLY else "observed"
            source = "manual_reference"
            if block == "Цена":
                if norm(row.get("E")) not in {"да", "yes", "true", "1"}:
                    continue
                competitor_price = numeric(row.get("I"))
                store_price = numeric(row.get("J"))
                index = numeric(row.get("K"))
                if index is None and competitor_price is not None and store_price:
                    index = competitor_price / store_price
                if index is None:
                    continue
                item = cell(competitor, product, block, "price_index", round(index, 6), price_score(index), source, status, sheet, row_number, "K",
                            date=observed_date(row.get("N")), locator=f"{sheet}!A{row_number}:N{row_number}")
                item["raw_competitor_price"] = competitor_price
                item["store_price"] = store_price
                out.append(item)
                continue
            if block == "Покупка/счёт":
                for criterion, column, scorer in (
                    ("registration_required", "D", truth_score),
                    ("online_invoice", "E", truth_score),
                    ("invoice_minutes", "F", lambda x: step_score(numeric(x))),
                    ("checkout_minutes", "G", lambda x: step_score(numeric(x))),
                    ("delivery_term_visible", "I", truth_score),
                ):
                    if column not in row:
                        continue
                    value = row.get(column)
                    score = scorer(value)
                    if score is not None:
                        out.append(cell(competitor, product, block, criterion, value, score, source, status, sheet, row_number, column,
                                        date=observed_date(row.get("C")), locator=f"{sheet}!{column}{row_number}"))
                continue
            if block == "UX сайта":
                for column, criterion in (
                    ("B", "site_search"), ("C", "filters"), ("D", "navigation"),
                    ("E", "product_card"), ("F", "licensing_clarity"), ("G", "compare_variants"),
                    ("H", "price_visible"), ("I", "availability"), ("J", "action_clarity"),
                    ("K", "checkout_steps"), ("L", "mobile"), ("M", "load_speed"),
                ):
                    if column in row:
                        score = truth_score(row.get(column))
                        if score is not None:
                            out.append(cell(competitor, product, block, criterion, row.get(column), score, source, status, sheet, row_number, column,
                                            locator=f"{sheet}!{column}{row_number}"))
                explicit = numeric(row.get("O"))
                if explicit is not None:
                    out.append(cell(competitor, product, block, "reference_score", explicit, explicit, source, status, sheet, row_number, "O",
                                    locator=f"{sheet}!O{row_number}"))
                continue
            if block == "Личный кабинет":
                for column, header in headers.items():
                    if column < "B" or column > "N" or column not in row:
                        continue
                    score = truth_score(row.get(column))
                    if score is not None:
                        out.append(cell(competitor, product, block, norm(header).replace(" ", "_"), row.get(column), score, source, status, sheet, row_number, column,
                                        locator=f"{sheet}!{column}{row_number}"))
                continue
            if block == "Сервис/преимущества":
                for column, header in headers.items():
                    if column < "B" or column > "G" or column not in row:
                        continue
                    score = truth_score(row.get(column))
                    if score is not None:
                        out.append(cell(competitor, product, block, norm(header).replace(" ", "_"), row.get(column), score, source, status, sheet, row_number, column,
                                        locator=f"{sheet}!{column}{row_number}"))
                explicit = numeric(row.get("I"))
                if explicit is not None:
                    out.append(cell(competitor, product, block, "reference_score", explicit, explicit, source, status, sheet, row_number, "I",
                                    locator=f"{sheet}!I{row_number}"))
                continue
            if block == "Поиск":
                explicit = numeric(row.get("N"))
                if explicit is not None:
                    out.append(cell(competitor, product, block, "search_score", explicit, explicit, source, status, sheet, row_number, "N",
                                    locator=f"{sheet}!N{row_number}"))
    return out


def load_agent_cells(path: Optional[Path], target: str, competitors: set[str]) -> List[Dict[str, Any]]:
    if not path or not path.exists():
        return []
    raw = json.loads(path.read_text(encoding="utf-8"))
    if isinstance(raw, dict) and isinstance(raw.get("cells"), list):
        return [dict(item) for item in raw["cells"] if isinstance(item, dict)]
    batches = raw.get("batches", []) if isinstance(raw, dict) else []
    cells: List[Dict[str, Any]] = []
    for batch in batches:
        if batch.get("source") != "market":
            continue
        for observation in batch.get("observations", []):
            if observation.get("status") != "observed":
                continue
            details = observation.get("details") or {}
            product = str(observation.get("product_key") or details.get("product_label") or target)
            seller = domain(details.get("seller") or observation.get("source_ref", {}).get("source_id"))
            if seller not in competitors or not product_matches({"A": seller, "B": product, "C": details.get("name")}, target):
                continue
            metric = observation.get("metric_key")
            if metric not in {"displayed_price", "historical_competitor_price"}:
                continue
            date = (observation.get("period") or {}).get("to", "")
            cells.append(cell(seller, product, "Цена", "price_index", numeric(observation.get("value")), None, "agent", "observed",
                              "market", 0, "", date=date, locator=(observation.get("source_ref") or {}).get("url", "")))
            cells[-1]["value_type"] = "price"
            cells[-1]["source_ref"]["observation_id"] = observation.get("id", "")
    return cells


def comparable(a: Any, b: Any, criterion: str) -> Tuple[bool, str]:
    if a is None or b is None:
        return False, "missing_value"
    if criterion in {"price_index", "price"} or criterion.endswith("minutes"):
        aa, bb = numeric(a), numeric(b)
        if aa is None or bb is None:
            return norm(a) == norm(b), "same_text" if norm(a) == norm(b) else "different_text"
        if math.isclose(aa, bb, rel_tol=0.0, abs_tol=0.01):
            return True, "same_value"
        return False, "value_changed_since_reference"
    if norm(a) == norm(b):
        return True, "same_value"
    sa, sb = truth_score(a), truth_score(b)
    if sa is not None and sb is not None and math.isclose(sa, sb, abs_tol=0.0):
        return True, "same_score"
    return False, "value_changed_since_reference"


def weighted_scores(cells: Iterable[Dict[str, Any]]) -> List[Dict[str, Any]]:
    groups: Dict[str, Dict[str, List[float]]] = {}
    for item in cells:
        if item.get("source") != "manual_reference" or item.get("score") is None:
            continue
        groups.setdefault(item["competitor"], {}).setdefault(item["block"], []).append(float(item["score"]))
    result = []
    for competitor in sorted(groups):
        block_scores = {block: round(sum(values) / len(values), 3) for block, values in groups[competitor].items() if values}
        denominator = sum(BLOCK_WEIGHTS.get(block, 0) for block in block_scores)
        score = sum(block_scores[block] * BLOCK_WEIGHTS.get(block, 0) for block in block_scores) / denominator if denominator else None
        result.append({
            "competitor": competitor,
            "score": round(score, 3) if score is not None else None,
            "blocks": block_scores,
            "missing_blocks": sorted(set(BLOCK_WEIGHTS) - set(block_scores)),
        })
    return result


def build_report(reference_path: Path, agent_path: Optional[Path], target: str, competitors: Iterable[str]) -> Dict[str, Any]:
    wanted = {domain(c) for c in competitors}
    reference_cells = add_reference_cells(load_sheets(reference_path), target, wanted)
    agent_cells = load_agent_cells(agent_path, target, wanted)
    lookup: Dict[Tuple[str, str, str], Dict[str, Any]] = {}
    for item in agent_cells:
        lookup[(domain(item["competitor"]), norm(item["product"]), item["criterion"])] = item
    comparisons = []
    for ref in reference_cells:
        key = (domain(ref["competitor"]), norm(ref["product"]), ref["criterion"])
        observed = lookup.get(key)
        if observed is None and ref["criterion"] == "price_index":
            observed = next((x for x in agent_cells if domain(x["competitor"]) == domain(ref["competitor"]) and x["criterion"] == "price_index"), None)
        if observed is None:
            comparisons.append({"cell_id": ref["cell_id"], "status": "missing", "reason": "agent_observation_missing", "reference": ref})
            continue
        if ref["criterion"] == "price_index" and observed.get("value_type") == "price":
            ok, reason = comparable(ref.get("raw_competitor_price"), observed.get("value"), "price")
        else:
            ok, reason = comparable(ref.get("value"), observed.get("value"), ref["criterion"])
        comparisons.append({"cell_id": ref["cell_id"], "status": "match" if ok else "mismatch", "reason": reason,
                            "reference": ref, "agent": observed})
    matches = sum(item["status"] == "match" for item in comparisons)
    mismatches = sum(item["status"] == "mismatch" for item in comparisons)
    missing = sum(item["status"] == "missing" for item in comparisons)
    compared = matches + mismatches
    return {
        "schema_version": "aifence.competitive.v1",
        "source_policy": "local_project_only",
        "generated_at": datetime.now(timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z"),
        "target_product": target,
        "competitors": sorted(wanted),
        "weights": BLOCK_WEIGHTS,
        "reference": {"path": str(reference_path), "revision": workbook_revision(reference_path), "cell_count": len(reference_cells)},
        "agent_snapshot": {"path": str(agent_path) if agent_path else "", "status": "loaded" if agent_cells else "missing", "cell_count": len(agent_cells)},
        "reference_cells": reference_cells,
        "agent_cells": agent_cells,
        "comparisons": comparisons,
        "summary": {
            "reference_cells": len(reference_cells),
            "compared_cells": compared,
            "matches": matches,
            "mismatches": mismatches,
            "missing": missing,
            "match_rate_percent": round(100.0 * matches / compared, 2) if compared else None,
        },
        "weighted_scores": weighted_scores(reference_cells),
        "limitations": [
            "Снимок агента отсутствует или содержит только ценовые наблюдения; пропуски не считаются нулевыми.",
            "Личный кабинет и сервис требуют учётки/человека и остаются manual_only.",
            "Оценки цены и UX кодируются по строкам эталонной рубрики; vision и Playwright пока не запускаются на чужих доменах.",
        ],
    }


def atomic_write(path: Path, data: Dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, tmp = tempfile.mkstemp(prefix=path.name + ".", dir=str(path.parent))
    try:
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            json.dump(data, handle, ensure_ascii=False, indent=2, sort_keys=True)
            handle.write("\n")
            handle.flush()
            os.fsync(handle.fileno())
        os.replace(tmp, path)
    finally:
        if os.path.exists(tmp):
            os.unlink(tmp)


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--reference", required=True, type=Path)
    parser.add_argument("--agent", type=Path)
    parser.add_argument("--output", required=True, type=Path)
    parser.add_argument("--product", default="Kaspersky Endpoint Security")
    parser.add_argument("--competitor", action="append", dest="competitors")
    args = parser.parse_args()
    report = build_report(args.reference, args.agent, args.product, args.competitors or DEFAULT_COMPETITORS)
    atomic_write(args.output, report)
    print(json.dumps({
        "output": str(args.output),
        "product": args.product,
        "reference_cells": report["summary"]["reference_cells"],
        "agent_cells": report["agent_snapshot"]["cell_count"],
        "matches": report["summary"]["matches"],
        "mismatches": report["summary"]["mismatches"],
        "missing": report["summary"]["missing"],
        "match_rate_percent": report["summary"]["match_rate_percent"],
    }, ensure_ascii=False))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
