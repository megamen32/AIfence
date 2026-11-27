#!/usr/bin/env python3
"""Build or verify a private five-case pack. Launch with python3 -B.

Supply the author's JSON on stdin with --build. Keep the plan and vault out of
the tester's context until the raw responses have been frozen.
"""
import argparse
from collections import Counter
from datetime import date, timedelta
import hashlib
import json
from pathlib import Path
import sys
import tempfile

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "blind_director"))
from prompt_guard import PRICE_ACCEPTANCE, require_prompt

QUESTION = PRICE_ACCEPTANCE
CASES = tuple(f"case-{i:02d}" for i in range(7, 12))
SERIES = ("sales", "market", "wordstat", "google_trends")
PRODUCTS = (
    ("kaspersky_small_office_security", "Kaspersky Small Office Security", "KL4141RCEFR"),
    ("dr_web_desktop_security_suite", "Dr.Web Desktop Security Suite", "LBW-BC-12M-100-A3"),
)
OFFERS = (
    (0, "softline", "https://store.softline.ru/kaspersky/kl4141rcefr-248971/"),
    (0, "softmagazin", "https://www.softmagazin.ru/laboratoriya-kasperskogo/kl4141rcefr/"),
    (0, "softmap", "https://softmap.ru/kaspersky/kaspersky-small-office-security/element-kaspersky-small-office-security-for-desktops-and-mobiles-russian-edition-5-mobile-device-5-desktop-5-user-1-year-renewal-license-pack/"),
    (1, "softmagazin", "https://www.softmagazin.ru/dr-web/lbw-bc-12m-100-a3/"),
)
START = date(2026, 9, 7)
WINDOWS = [(START + timedelta(days=7*i), START + timedelta(days=7*i+6)) for i in range(8)]
QUOTE_WEEKS = (0, 4, 7)
AS_OF = "2026-11-02T00:00:00Z"


def encode(value):
    return (json.dumps(value, ensure_ascii=False, sort_keys=True, indent=2) + "\n").encode()


def make_snapshot(series):
    def row(channel, product, kind, metric, value, unit, period, locator, **details):
        family, label, sku = PRODUCTS[product]
        url = details.pop("url", None)
        ref = {"source_id": channel, "locator": locator}
        if url:
            ref["url"] = url
        return dict(id=f"{channel}/{locator}", kind=kind, metric_key=metric, value=value, unit=unit,
                    product_key=sku if kind == "offer" else family,
                    period={"from": str(period[0]), "to": str(period[1])},
                    source_ref=ref, status="observed",
                    details=dict(details, synthetic=True, origin="synthetic_fixture",
                                 product_label=label, sku=sku))

    batches = {}
    for channel, metric, unit in (
        ("sales", "self_service_order_index", "index"),
        ("wordstat", "search_queries", "count"),
        ("google_trends", "search_interest", "index"),
    ):
        obs = []
        target, control = series[channel]
        for product, values in enumerate((target, control)):
            if len(values) != len(WINDOWS) or any(type(v) is not int or v < 0 for v in values):
                raise ValueError("Invalid weekly series")
            if channel == "google_trends" and max(values) != 100:
                raise ValueError("Trends index must peak at 100 in its per-query extraction")
            label = PRODUCTS[product][1]
            for period, value in zip(WINDOWS, values):
                details = dict(complete=True, normalization="mean_of_first_four_weeks", base=100,
                               population="paid_self_service_orders", quality="comparable")
                if channel != "sales":
                    details = dict(geo="RU", query=label, series=label,
                                   normalization="raw_weekly_count" if channel == "wordstat" else "per_query_window_peak",
                                   base="queries" if channel == "wordstat" else 100)
                obs.append(row(channel, product, "metric", metric, value, unit, period,
                               f"{PRODUCTS[product][0]}/{period[0]}", **details))
        batches[channel] = obs

    quotes = []
    if len(series["market"]) != len(QUOTE_WEEKS):
        raise ValueError("Wrong number of quote captures")
    for week, amounts in zip(QUOTE_WEEKS, series["market"]):
        if len(amounts) != len(OFFERS) or any(type(v) is not int or v <= 0 for v in amounts):
            raise ValueError("Invalid quote amounts")
        day = WINDOWS[week][1]
        for (product, seller, url), amount in zip(OFFERS, amounts):
            sku = PRODUCTS[product][2]
            quotes.append(row(seller, product, "offer", "displayed_price", amount, "RUB",
                              (day, day), f"{sku}/{day}", url=url, name=PRODUCTS[product][1],
                              seller=seller, captured_at=f"{day}T12:00:00Z",
                              quantity=1, max_quantity=1, price_basis="per_unit",
                              market_comparison={"group": f"{sku}/{day}/one", "comparable": True})
                          | {"currency": "RUB", "tax_basis": "included"})
    batches["market"] = quotes
    return dict(schema_version="aifence.case.v1", as_of=AS_OF, warnings=[], batches=[
        dict(schema_version="aifence.v1", source=c if c in ("sales", "market") else "research",
             as_of=AS_OF, status="ok", observations=batches[c], warnings=[]) for c in SERIES
    ])


def sha(data):
    return hashlib.sha256(data).hexdigest()


def checksums(files):
    return "".join(f"{sha(files[name])}  {name}\n" for name in sorted(files)).encode()


def pack(plan):
    require_prompt(QUESTION, purpose="price_acceptance")
    if set(plan) != set(CASES):
        raise ValueError("Expected five neutral case IDs")
    inputs, vault = {}, {}
    projections = {}
    for case in CASES:
        spec = plan[case]
        doc = make_snapshot(spec["series"])
        evidence = dict(zip(SERIES, (b["observations"] for b in doc["batches"])))
        oracle = spec["oracle"]
        required = set(oracle["required_sources"])
        ids = [r["id"] for rows in evidence.values() for r in rows]
        if len(ids) != len(set(ids)):
            raise ValueError("Repeated observation ID")
        if not 2 <= len(required) <= 4 or not required <= evidence.keys():
            raise ValueError("An oracle must require multiple available sources")
        oracle = dict(oracle, evidence_ids={k: [r["id"] for r in evidence[k]] for k in sorted(required)})
        snapshot = encode(doc)
        for hidden in (oracle["supported_conclusion"], oracle["allowed_alternative"], oracle["verdict"]):
            if hidden.casefold() in snapshot.decode().casefold() or hidden.casefold() in QUESTION.casefold():
                raise ValueError("A hidden conclusion leaked into the input")
        if case.encode() in snapshot or b"vault" in snapshot:
            raise ValueError("Input contains a case label or vault reference")
        inputs[f"inputs/{case}/snapshot.json"] = snapshot
        inputs[f"inputs/{case}/question.txt"] = (QUESTION + "\n").encode()
        vault[f"vault/{case}.json"] = encode(oracle)
        projections[case] = {k: sha(encode(rows)) for k, rows in evidence.items()}

    if len({plan[c]["oracle"]["verdict"] for c in CASES}) != len(CASES):
        raise ValueError("Expected distinct combined verdicts")
    for channel in SERIES:
        counts = Counter(p[channel] for p in projections.values())
        if min(counts.values()) < 2:
            raise ValueError("A single source identifies a verdict")
    if len({tuple(p.values()) for p in projections.values()}) != len(CASES):
        raise ValueError("Combined evidence does not distinguish the cases")
    vault["vault/author-plan.json"] = encode(plan)
    return inputs | vault | {"inputs.sha256": checksums(inputs), "vault.sha256": checksums(vault)}


def verify_or_write(root, files):
    if root.exists() or root.is_symlink():
        existing = {p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file()}
        if (existing != files.keys() or any(p.is_symlink() or p.stat().st_mode & 0o077
                                            for p in [root, *root.rglob("*")])
                or any((root / name).read_bytes() != data for name, data in files.items())):
            raise ValueError("Existing private pack differs; refusing to overwrite")
        return
    with tempfile.TemporaryDirectory(dir=root.parent, prefix=".multisource-") as tmp:
        stage = Path(tmp) / "pack"
        stage.mkdir(mode=0o700)
        for folder in ("inputs", "vault"):
            (stage / folder).mkdir(mode=0o700)
        for name, data in files.items():
            p = stage / name
            p.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            p.touch(mode=0o600)
            p.write_bytes(data)
        if root.exists() or root.is_symlink():
            raise ValueError("Pack appeared while staging")
        stage.rename(root)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("output", type=Path)
    parser.add_argument("--build", action="store_true", help="Read the private author plan from stdin")
    args = parser.parse_args()
    if args.build:
        plan = json.load(sys.stdin)
    else:
        plan = json.loads((args.output / "vault/author-plan.json").read_bytes())
    files = pack(plan)
    verify_or_write(args.output, files)
    rows = sum(len(b["observations"]) for name, data in files.items() if name.startswith("inputs/") and name.endswith(".json")
               for b in json.loads(data)["batches"])
    print(f"{len(CASES)} cases, {rows} observations, {len(CASES)*len(SERIES)} single-source ambiguity checks")


if __name__ == "__main__":
    main()
