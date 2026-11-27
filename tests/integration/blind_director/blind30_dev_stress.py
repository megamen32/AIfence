#!/usr/bin/env python3
"""Stress dev set on top of the v3 generator (NOT the holdout).
Randomises effect size, traffic lift, bot share, onset week; adds family-specific
demand trends, S7 key overlap and dropped metrics. Layout: OUT/run-dev/{inputs,vault}/case-NN.
Usage: blind30_dev_stress.py OUT N [BASE_SEED]. Development data only; never a holdout."""
import sys, json, random, shutil, math, hashlib, importlib.util, datetime as dt
from pathlib import Path
spec = importlib.util.spec_from_file_location("g", str(Path(__file__).resolve().parents[3] / "apps/metrika-analyst/demo_sources/blind_test/blind_test_generator.py"))
g = importlib.util.module_from_spec(spec); spec.loader.exec_module(g)
orig = g.pick_scenario
def pick(seed, scenario, weeks=28, family_pool="kaspersky"):
    c = orig(seed, scenario, weeks, family_pool)
    r = random.Random(f"{seed}:dev")
    c["severity"] = round(r.uniform(.12, .38), 3)
    if scenario not in ("S13",):
        c["week"] = r.randint(weeks - 10, weeks - 3)
    else:
        c["week"] = weeks - r.choice([2, 3])
    if scenario == "S6": c["bot_share"] = round(r.uniform(.15, .6), 3)
    if scenario == "S14": c["traffic_lift"] = round(r.uniform(.15, .6), 3)
    return c
g.pick_scenario = pick
out = Path(sys.argv[1]); n = int(sys.argv[2]); base = int(sys.argv[3]) if len(sys.argv) > 3 else 1000
run = out / "run-dev"; shutil.rmtree(run, ignore_errors=True)
work = out / "_work"; shutil.rmtree(work, ignore_errors=True)
scen = ["S0", "S6", "S7", "S12", "S13", "S14"]
for i in range(n):
    seed = base + i; sc = scen[i % len(scen)]
    res = g.write_dataset(work / str(i), seed, sc, 28)
    case = f"case-{i+1:02d}"; dst = run / "inputs" / case
    shutil.copytree(res["input"], dst)
    v = run / "vault" / case; v.mkdir(parents=True)
    truth = json.loads(Path(res["truth"]).read_text()); ch = truth["choice"]
    r = random.Random(f"{seed}:post")
    m = json.loads((dst / "metrika.json").read_text())
    obs = m["observations"]
    wk = lambda o: (dt.date.fromisoformat(o["period"]["from"]) - g.START).days // 7
    # family-specific demand trend over the last 10 weeks (all families, incl. target)
    trend = {f: r.uniform(-.15, .35) if r.random() < .6 else 0 for f in g.PRODUCTS}
    for o in obs:
        fam = o["details"].get("product_label"); f = next(k for k, p in g.PRODUCTS.items() if p["label"] == fam)
        w = wk(o)
        if o["metric_key"] in ("visits", "card_views", "cart_additions", "checkouts", "orders", "revenue") and o["value"] is not None:
            ramp = min(1, max(0, (w - 17) / 10))
            o["value"] = round(o["value"] * (1 + trend[f] * ramp), 2 if o["metric_key"] == "revenue" else None) if o["metric_key"] == "revenue" else int(round(o["value"] * (1 + trend[f] * ramp)))
    meta = {"trend": trend}
    if sc == "S7":
        ov = r.choice([0, 1, 2, 3]); meta["overlap"] = ov
        new = g.PRODUCTS[ch["family"]]["sku"].lower(); extra = []
        for o in obs:
            if o["product_key"] == new and ch["week"] <= wk(o) < ch["week"] + ov:
                a = (wk(o) - ch["week"] + 1) / (ov + 1)
                old = json.loads(json.dumps(o)); old["product_key"] = ch["family"]
                old["source_ref"]["locator"] = old["source_ref"]["locator"].replace(f"family={new}", f"family={ch['family']}")
                if o["metric_key"] not in ("bounce_rate", "avg_visit_duration"):
                    tot = o["value"]; o["value"] = type(tot)(round(tot * a)); old["value"] = type(tot)(round(tot * (1 - a)))
                old["id"] = "obs-" + hashlib.sha256(json.dumps(old, sort_keys=True).encode()).hexdigest()[:16]
                extra.append(old)
        obs.extend(extra)
    drop = r.random()
    if drop < .15: meta["dropped"] = ["bounce_rate", "avg_visit_duration"]
    elif drop < .25: meta["dropped"] = ["checkouts"]
    else: meta["dropped"] = []
    m["observations"] = [o for o in obs if o["metric_key"] not in meta["dropped"]]
    (dst / "metrika.json").write_text(json.dumps(m, ensure_ascii=False))
    truth["dev"] = meta
    (v / "truth.json").write_text(json.dumps(truth, ensure_ascii=False))
shutil.rmtree(work, ignore_errors=True)
print(run)
