#!/usr/bin/env python3
"""Score watchdog scan outputs against holdout truth.

Rules (fixed before the first run):
  expected cause_type: S6 traffic_quality, S7 taxonomy_migration,
                       S13 late_event_delivery, S14 masked_multi_cause_regression
  hit        : a cause_signal on the planted family (S7: old or new key) with the
               expected cause_type and period start within +-1 week of the onset
  exact      : same, onset week exact
  wrong_type : a cause_signal on the planted family but another cause_type or week off by >1
  miss       : no cause_signal on the planted family
  false alarm: any cause_signal on a family that has no planted fault
               (all families in S0/S12; non-target families in S6/S7/S13/S14)
  decline_near: any candidate (any kind) on the planted family within +-3 weeks (for context)

Usage: score_holdout.py HOLDOUT_DIR SCAN_DIR > table.md
"""
import datetime as dt
import glob
import json
import os
import sys

EXPECT = {"S6": "traffic_quality", "S7": "taxonomy_migration", "S13": "late_event_delivery",
          "S14": "masked_multi_cause_regression"}


def weeks_between(a, b):
    return (dt.date.fromisoformat(a) - dt.date.fromisoformat(b)).days // 7


def realized_effect(hold, t):
    """What the planted fault actually looks like after noise, computed from the inputs only."""
    m = json.load(open(os.path.join(hold, "run-holdout", "inputs", t["case"], "metrika.json")))
    keys = {t["family"], t.get("new_key")} - {None}
    agg = {}
    for o in m["observations"]:
        if o["product_key"] in keys:
            agg.setdefault((o["metric_key"], o["segment"]), {}).setdefault(o["period"]["from"], 0)
            agg[(o["metric_key"], o["segment"])][o["period"]["from"]] += o["value"]
    on = t["onset_week"]
    out = []
    for metric, dev, label in (("visits", "desktop", "vis(d)"), ("bounce_rate", "desktop", "bnc(d)"),
                               ("checkouts", None, "chk"), ("orders", None, "ord")):
        series = {}
        for (mk, sg), s in agg.items():
            if mk == metric and (dev is None or sg == dev):
                for w, v in s.items():
                    series[w] = series.get(w, 0) + v
        before = [v for w, v in sorted(series.items()) if w < on][-8:]
        after = [v for w, v in sorted(series.items()) if w >= on]
        if before and after and sum(before):
            out.append(f"{label} {(sum(after) / len(after)) / (sum(before) / len(before)) * 100 - 100:+.0f}%")
    return " ".join(out)


def main(hold, scans):
    rows, totals = [], {}
    fa_total = 0
    for tf in sorted(glob.glob(os.path.join(hold, "truth", "*.json"))):
        t = json.load(open(tf))
        scan = json.load(open(os.path.join(scans, "run-holdout", t["case"] + ".scan.json")))
        cands = scan.get("candidates") or []
        causes = [c for c in cands if c.get("kind") == "cause_signal"]
        fam_keys = {t["family"], t.get("new_key")} - {None}
        on_target = [c for c in causes if c["product_key"] in fam_keys]
        off_target = [c for c in causes if c["product_key"] not in fam_keys]
        fa = len(off_target)
        fa_total += fa
        verdict, detail = "—", ""
        if t["fault"] in EXPECT:
            exp = EXPECT[t["fault"]]
            best = None
            for c in on_target:
                dw = weeks_between(c["period"]["from"], t["onset_week"])
                ok = c.get("cause_type") == exp and abs(dw) <= 1
                if ok and (best is None or abs(dw) < abs(best[1])):
                    best = (c, dw)
            if best:
                verdict = "hit (exact)" if best[1] == 0 else f"hit ({best[1]:+d} нед.)"
            elif on_target:
                c = on_target[0]
                verdict = "wrong"
                detail = f"{c.get('cause_type')} @ {c['period']['from']}"
            else:
                near = [c for c in cands if c["product_key"] in fam_keys
                        and abs(weeks_between(c["period"]["from"], t["onset_week"])) <= 3]
                verdict = "miss"
                detail = f"только {near[0]['kind']}" if near else "ничего по семейству"
            key = (t["fault"], t["strength"])
            totals.setdefault(key, [0, 0])
            totals[key][1] += 1
            if verdict.startswith("hit"):
                totals[key][0] += 1
        else:
            verdict = "clean" if fa == 0 else "false alarm"
        fa_detail = ", ".join(f"{c['product_key']}:{c.get('cause_type')}@{c['period']['from']}" for c in off_target)
        realized = realized_effect(hold, t) if t["fault"] in EXPECT else ""
        rows.append((t["case"], t["fault"], t["strength"], t["family"] or "—", t["onset_week"] or "—", realized,
                     ", ".join(f"{c.get('cause_type')}@{c['period']['from']}" for c in on_target) or "—",
                     verdict, detail, fa, fa_detail))
    print("| case | planted | strength | family | onset | realized in data (8 wk before → after, both devices) | cause_signal on family | verdict | note | false alarms | false-alarm detail |")
    print("|---|---|---|---|---|---|---|---|---|---|---|")
    for r in rows:
        print("| " + " | ".join(str(x) for x in r) + " |")
    print()
    print("| fault | strength | hits / cases |")
    print("|---|---|---|")
    for (f, s), (h, n) in sorted(totals.items()):
        print(f"| {f} | {s} | {h}/{n} |")
    h = sum(v[0] for v in totals.values()); n = sum(v[1] for v in totals.values())
    print(f"\nTOTAL hits {h}/{n}; false-alarm cause_signals {fa_total}")


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
