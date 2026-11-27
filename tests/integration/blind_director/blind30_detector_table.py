#!/usr/bin/env python3
"""Score offline blind scans (no model) against vault truth.

Generate scans: BLIND30_DIR=<dir with run-*> BLIND30_OUT=<out> go test -run TestBlind30Offline .
(in apps/metrika-analyst). Then: blind30_detector_table.py <dir with run-*> <out> [topN] [week_tolerance_days]
S0/S12: any controlled or cause alert in top-N is a false alarm. Positives: hit = family, exact
start week and cause_type match."""
import json, sys, glob, os, datetime as dt
root, scans = sys.argv[1], sys.argv[2]
top = int(sys.argv[3]) if len(sys.argv) > 3 else 3
TOL = int(sys.argv[4]) if len(sys.argv) > 4 else 0
ALERT = {"controlled_decline", "uncontrolled_decline", "cause"}
rows = []
for tpath in sorted(glob.glob(f"{root}/run-*/vault/case-*/truth.json")):
    run, case = tpath.split("/")[-4], tpath.split("/")[-2]
    ch = json.load(open(tpath))["choice"]
    sc = json.load(open(f"{scans}/{run}/{case}.scan.json"))
    start = dt.date(2026, 5, 11) + dt.timedelta(weeks=ch["week"])
    cands = sc.get("candidates") or []
    alerts = [c for c in cands if c.get("kind") in ALERT or c.get("cause_type")]
    topc = alerts[:top]
    fam_ok = lambda c: c.get("product_key") in (ch["family"],) or c.get("family") == ch["family"]
    def wk_ok(c):
        f = dt.date.fromisoformat(c["period"]["from"])
        return abs((f - start).days) <= TOL
    s = ch["scenario"]
    if s in ("S0", "S12"):
        verdict = "ok" if not topc else "false_alarm"
        det = ""
    else:
        m = [c for c in topc if fam_ok(c) and wk_ok(c)]
        ct = [c for c in m if c.get("cause_type") == ch["cause_type"]]
        verdict = "hit" if ct else ("family+week" if m else ("miss" if not topc else "wrong"))
        det = ""
    d0 = topc[0] if topc else {}
    rows.append((run[4:10], case, s, ch["family"].replace("kaspersky_", "k_"), str(start), ch["cause_type"],
                 d0.get("cause_type") or d0.get("kind", "-"), (d0.get("product_key") or "-").replace("kaspersky_", "k_"),
                 (d0.get("period") or {}).get("from", "-"), len(alerts), verdict))
w = [6, 7, 4, 32, 10, 30, 30, 32, 10, 3, 12]
hdr = ("run", "case", "scn", "planted family", "week", "planted cause", "detected #1", "det family", "det week", "n", "verdict")
print(" ".join(h.ljust(x) for h, x in zip(hdr, w)))
for r in rows:
    print(" ".join(str(v).ljust(x) for v, x in zip(r, w)))
from collections import Counter
by = Counter((r[2], r[-1]) for r in rows)
print(); print(sorted(by.items()))
pos = [r for r in rows if r[2] not in ("S0", "S12")]
print(f"hits {sum(r[-1]=='hit' for r in pos)}/{len(pos)}; family+week (any type) {sum(r[-1] in ('hit','family+week') for r in pos)}/{len(pos)}; S0/S12 false alarms {sum(r[-1]=='false_alarm' for r in rows)}/{len(rows)-len(pos)}")
