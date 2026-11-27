#!/usr/bin/env python3
"""Score cause_signal candidates of offline blind scans against vault truth (no model).

Scans: BLIND30_DIR=<dir with run-*> BLIND30_OUT=<out> go test -run TestBlind30Offline . (apps/metrika-analyst).
Usage: blind30_detector_table.py CASES SCANS [-v]
hit = family, exact onset week and cause_type; abstain_ok = no cause when a metric the cause needs
was dropped from the snapshot; S0/S12 and every non-planted family must get no cause."""
import json, sys, glob, datetime as dt
from collections import Counter
root, scans = sys.argv[1], sys.argv[2]; verbose = "-v" in sys.argv
NEED = {"S6": {"bounce_rate", "avg_visit_duration"}, "S13": {"checkouts"}, "S14": {"checkouts"}}
res = Counter(); rows = []; other = 0; nfam = 0
for t in sorted(glob.glob(f"{root}/run-*/vault/case-*/truth.json")):
    run, case = t.split("/")[-4], t.split("/")[-2]
    T = json.load(open(t)); ch = T["choice"]; dev = T.get("dev", {})
    sc = json.load(open(f"{scans}/{run}/{case}.scan.json"))
    cs = [c for c in sc["candidates"] if c["kind"] == "cause_signal"]
    start = str(dt.date(2026, 5, 11) + dt.timedelta(weeks=ch["week"]))
    s = ch["scenario"]; fam = ch["family"]
    mine = [c for c in cs if c["product_key"] == fam]
    others = [c for c in cs if c["product_key"] != fam]
    nfam += 7 if s in ("S0", "S12") else 6
    other += len(others) if s not in ("S0", "S12") else 0
    dropped = set(dev.get("dropped", []))
    if s in ("S0", "S12"):
        v = "false_alarm" if cs else "ok"; other += len(cs)
    else:
        c = mine[0] if mine else None
        can = not (NEED.get(s, set()) & dropped)
        if c is None: v = "miss" if can else "abstain_ok"
        elif c["cause_type"] != ch["cause_type"]: v = "wrong_cause"
        elif c["period"]["from"] != start: v = "wrong_week"
        else: v = "hit"
    res[(s, v)] += 1
    rows.append((run[-6:], case, s, ch.get("severity"), ch.get("traffic_lift", ch.get("bot_share", "")), dev.get("overlap", ""), ",".join(dropped), start,
                 [(c["product_key"][:10], c["cause_type"][:10], c["period"]["from"], c.get("cause_strength")) for c in cs], v))
if verbose:
    for r in rows: print(*r)
pos = [r for r in rows if r[2] not in ("S0", "S12")]
print(sorted(res.items()))
print(f"positives: hit {sum(r[-1]=='hit' for r in pos)}/{len(pos)} (abstain_ok {sum(r[-1]=='abstain_ok' for r in pos)}, wrong_week {sum(r[-1]=='wrong_week' for r in pos)}, wrong_cause {sum(r[-1]=='wrong_cause' for r in pos)}, miss {sum(r[-1]=='miss' for r in pos)}); false cause on clean families {other}/{nfam}")
