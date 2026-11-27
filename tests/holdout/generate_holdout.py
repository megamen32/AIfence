#!/usr/bin/env python3
"""Independent holdout generator for the Metrika blind watchdog.

Written without reading the watchdog/scanner code or the earlier blind30
generator. It knows only the input file format (metrika.json, releases.json)
and a plain-language description of each fault type:

  S0   no fault (with distractors: seasonality, a shared market dip, one-week promo spikes)
  S6   bot traffic: desktop visits grow, bounce grows, visit time drops, orders flat
  S7   product key migration: a family's series ends and continues under a new key
  S12  A/B experiment on part of the traffic, small effect, no real fault
  S13  late order delivery: tracked orders drop in the last weeks while checkouts stay flat
  S14  after a release or campaign visits grow while checkouts (and orders) fall

Effect sizes, strength buckets and weeks were fixed before the first watchdog
run and are not changed after seeing results.

Usage:
  python3 generate_holdout.py OUT_DIR [--seed 20261127]

Writes OUT_DIR/run-holdout/inputs/case-NN/{metrika.json,releases.json}
and OUT_DIR/truth/case-NN.json (labels are kept outside the inputs tree).
"""
import argparse
import datetime as dt
import hashlib
import json
import math
import os
import random

CATALOG = {
    "kaspersky_small_office_security": ("Kaspersky Small Office Security", "KL4141RCEFR", "Softline",
        "https://store.softline.ru/kaspersky/kl4141rcefr-248971/", 9800),
    "astra_linux_special_edition": ("Astra Linux Special Edition", "OS2304ELB81BOX000WS01-SO36", "SoftMagazin",
        "https://www.softmagazin.ru/pao-gruppa-astra/os2304elb81box000ws01-so36/", 21500),
    "microsoft_365_business_premium": ("Microsoft 365 Business Premium", "CFQ7TTC0LCHC-0002-P1YY", "SoftMagazin",
        "https://www.softmagazin.ru/microsoft/cfq7ttc0lchc-0002-p1yy/", 26900),
    "red_hat_enterprise_linux_server_premium": ("Red Hat Enterprise Linux Server, Premium", "RH00003", "Softmap",
        "https://softmap.ru/redhat/red-hat-enterprise-linux-server/element-red-hat-enterprise-linux-server-premium-physical-or-virtual-nodes-1-year/", 168000),
    "kaspersky_endpoint_security": ("Kaspersky Endpoint Security для бизнеса Расширенный Плюс", "KL4210RAKDE", "SoftMagazin",
        "https://www.softmagazin.ru/laboratoriya-kasperskogo/kl4210rakde/", 4300),
    "dr_web_desktop_security_suite": ("Dr.Web Desktop Security Suite", "LBW-BC-12M-100-A3", "SoftMagazin",
        "https://www.softmagazin.ru/dr-web/lbw-bc-12m-100-a3/", 2100),
    "myoffice_private_cloud": ("МойОфис Частное облако", "X2-CLD-NB-3NUNL-A", "Softmap",
        "https://softmap.ru/myoffice/moyofis/element-moyofis-chastnoe-oblako-licenziya-korporativnaya-na-polzovatelya-dlya-kommercheskih-zakazchikov-bez-ogranicheniya-sroka-deystviya-s-pravom-na-poluchenie-obnovleniy-v-techenie-treh-let/", 7600),
}

# New keys used when a family migrates (S7). Realistic renames of the same product.
MIGRATION = {
    "kaspersky_small_office_security": ("kaspersky_small_office_security_8", "Kaspersky Small Office Security 8"),
    "astra_linux_special_edition": ("astra_linux_se_1_8", "Astra Linux Special Edition 1.8"),
    "microsoft_365_business_premium": ("m365_business_premium", "Microsoft 365 Business Premium (NCE)"),
    "red_hat_enterprise_linux_server_premium": ("rhel_server_premium", "Red Hat Enterprise Linux Server Premium"),
    "kaspersky_endpoint_security": ("kes_business_advanced_plus", "Kaspersky Endpoint Security для бизнеса – Расширенный Плюс"),
    "dr_web_desktop_security_suite": ("drweb_desktop_security_suite", "Dr.Web Desktop Security Suite (Комплексная защита)"),
    "myoffice_private_cloud": ("myoffice_private_cloud_3", "МойОфис Частное облако 3"),
}

DEVICES = ["desktop", "mobile"]
RELEASE_DECOYS = [
    ("Изменение карточки продукта", "card_views"), ("Обновление интеграции цен", "revenue"),
    ("Исправление отображения лицензий", "card_views"), ("Обновление интерфейса каталога", "card_views"),
    ("Техническое обновление checkout", "checkouts"), ("Обновление правил скидок для партнёров", "revenue"),
    ("Правка текстов на странице оплаты", "orders"), ("Обновление фильтров каталога", "card_views"),
]

# Case plan: (fault, strength). Strength is a business-size bucket, not a detector threshold.
PLAN = (
    [("S6", s) for s in ("strong", "strong", "medium", "medium", "weak", "weak")]
    + [("S7", s) for s in ("clean", "relabel", "sku_change", "overlap", "level_shift")]
    + [("S13", s) for s in ("strong", "strong", "medium", "medium", "weak")]
    + [("S14", s) for s in ("strong", "strong", "medium", "medium", "weak", "weak")]
    + [("S0", "none")] * 5
    + [("S12", "none")] * 3
)

S6_BOT_SHARE = {"strong": 1.0, "medium": 0.45, "weak": 0.18}        # extra desktop visits as share of baseline
S13_ORDER_LOSS = {"strong": 0.55, "medium": 0.30, "weak": 0.12}    # share of orders not yet delivered, last week
S14_EFFECT = {"strong": (0.60, 0.35), "medium": (0.30, 0.20), "weak": (0.15, 0.10)}  # (visits up, checkout down)


def poisson(rng, lam):
    if lam <= 0:
        return 0
    if lam < 30:
        l, k, p = math.exp(-lam), 0, 1.0
        while True:
            p *= rng.random()
            if p <= l:
                return k
            k += 1
    return max(0, int(round(rng.gauss(lam, math.sqrt(lam)))))


def binom(rng, n, p):
    p = min(max(p, 0.0), 1.0)
    if n <= 0:
        return 0
    mean, var = n * p, n * p * (1 - p)
    if n < 40:
        return sum(1 for _ in range(n) if rng.random() < p)
    return min(n, max(0, int(round(rng.gauss(mean, math.sqrt(max(var, 1e-9)))))))


def make_case(idx, fault, strength, rng, out, jitter=0.0):
    # --jitter: each case draws its own effect size within +-jitter of the bucket value
    jit = (lambda x: x * (1 + rng.uniform(-jitter, jitter))) if jitter else (lambda x: x)
    n_weeks = rng.choice([24, 26, 28, 30, 32])
    last_monday = dt.date(2026, 11, 16)
    start = last_monday - dt.timedelta(weeks=n_weeks - 1)
    weeks = [start + dt.timedelta(weeks=i) for i in range(n_weeks)]
    fams = list(CATALOG)
    rng.shuffle(fams)
    fams = fams[: rng.choice([5, 6, 7])]
    target = fams[0] if fault not in ("S0",) else None

    # onset week: leave >= 8 clean weeks before; S13 is about the tail.
    if fault == "S13":
        k_tail = {"strong": rng.choice([2, 3]), "medium": rng.choice([2, 3]), "weak": rng.choice([1, 2])}[strength]
        onset = n_weeks - k_tail
    elif fault in ("S6", "S14", "S12"):
        onset = rng.randint(10, n_weeks - 4)
    elif fault == "S7":
        onset = rng.randint(10, n_weeks - 5)
    else:
        onset = None

    # Shared market factor: B2B seasonality (May holidays dip, summer slump, autumn growth) + one shared dip.
    shared_dip_week = rng.randint(6, n_weeks - 3)
    shared_dip = rng.uniform(0.80, 0.92) if rng.random() < 0.6 else 1.0
    market = []
    for i, w in enumerate(weeks):
        doy = w.timetuple().tm_yday
        s = 1.0 + 0.10 * math.sin(2 * math.pi * (doy - 200) / 365.0)
        if w.month == 5 and w.day <= 12:
            s *= 0.82
        if i == shared_dip_week:
            s *= shared_dip
        market.append(s)

    obs, releases, truth_notes = [], [], {}
    release_id = [0]

    def add_release(day, fam, desc, affected, rtype):
        release_id[0] += 1
        label, sku, seller, url, _ = CATALOG.get(fam, (None,) * 5)
        releases.append({
            "release_id": f"R-{release_id[0]:02d}", "release_date": day.isoformat(), "release_type": rtype,
            "description": desc, "related_task_ids": f"T-{release_id[0]:02d}", "affected_metrics": affected,
            "status": "выпущен", "family": fam, "synthetic": True, "product_label": label, "sku": sku,
            "catalog_seller": seller, "catalog_url": url,
        })

    # Decoy releases: ~0.8 per week, random families, no effect.
    for i, w in enumerate(weeks):
        for _ in range(poisson(rng, 0.8)):
            desc, aff = rng.choice(RELEASE_DECOYS)
            add_release(w + dt.timedelta(days=rng.randint(0, 4)), rng.choice(fams), desc, aff,
                        rng.choice(["minor", "minor", "technical", "major"]))

    if fault == "S14":
        kind = rng.choice(["campaign", "release"])
        if kind == "campaign":
            add_release(weeks[onset], target, "Запуск кампании в каталоге", "visits", "marketing")
        else:
            add_release(weeks[onset], target, rng.choice(["Перенос корзины на новый сервис", "Новая форма оформления заказа"]),
                        "checkouts", "major")
    if fault == "S12":
        add_release(weeks[onset], target, "Запуск checkout-эксперимента (A/B 50/50)", "checkouts", "experiment")
    bot_ramp = rng.choice([1, 2])
    eff_bot = jit(S6_BOT_SHARE.get(strength, 0))
    eff_s13 = min(0.9, jit(S13_ORDER_LOSS.get(strength, 0)))
    eff_s14 = tuple(jit(x) for x in S14_EFFECT.get(strength, (0, 0)))
    if fault == "S7":
        add_release(weeks[onset], target, "Миграция названий и ключей каталога", "card_views", "major")

    for fam in fams:
        label, sku, seller, url, price = CATALOG[fam]
        base_visits = rng.uniform(1800, 14000) * (0.4 if price > 100000 else 1.0)
        trend = rng.uniform(-0.004, 0.005)             # weekly drift
        promo_week = rng.randint(4, n_weeks - 2) if rng.random() < 0.35 else None
        for dev in DEVICES:
            dev_share = 1.0 if dev == "desktop" else rng.uniform(0.30, 0.65)
            r_card = rng.uniform(0.40, 0.60) * (0.9 if dev == "mobile" else 1.0)
            r_cart = rng.uniform(0.05, 0.11) * (0.8 if dev == "mobile" else 1.0)
            r_chk = rng.uniform(0.50, 0.65)
            r_ord = rng.uniform(0.75, 0.90)
            bounce0 = rng.uniform(0.24, 0.40) + (0.06 if dev == "mobile" else 0.0)
            dur0 = rng.uniform(140, 260) * (0.8 if dev == "mobile" else 1.0)
            ar = 0.0
            for i, w in enumerate(weeks):
                ar = 0.55 * ar + rng.gauss(0, 0.06)
                lam = base_visits * dev_share * market[i] * (1 + trend) ** i * math.exp(ar)
                if promo_week is not None and i == promo_week:
                    lam *= rng.uniform(1.3, 1.7)
                human = poisson(rng, lam)
                bots = 0
                visits_mult, chk_mult, ord_late = 1.0, 1.0, 0.0
                is_t = fam == target and onset is not None and i >= onset

                if fault == "S6" and is_t and dev == "desktop":
                    ramp = min(1.0, (i - onset + 1) / bot_ramp)
                    bots = poisson(rng, lam * eff_bot * ramp)
                if fault == "S14" and is_t:
                    v_up, c_down = eff_s14
                    visits_mult = 1 + v_up
                    # absolute checkouts fall by c_down despite the extra visits
                    chk_mult = (1 - c_down) / ((1 + v_up) * 0.85)
                if fault == "S13" and is_t:
                    pos = i - onset                    # 0 .. k-1; the latest week is the most incomplete
                    k = n_weeks - onset
                    ord_late = eff_s13 * (pos + 1) / k
                if fault == "S12" and is_t:
                    chk_mult = 1 + 0.5 * rng.uniform(-0.06, 0.04)  # half the traffic, small variant effect

                if visits_mult != 1.0:
                    human = poisson(rng, lam * visits_mult)
                cards_h = binom(rng, human, r_card)
                cards = cards_h + binom(rng, bots, 0.25)
                carts = binom(rng, cards_h, r_cart * (0.85 if visits_mult > 1 else 1.0))
                chks = binom(rng, carts, r_chk * chk_mult)
                orders_true = binom(rng, chks, r_ord)
                orders = binom(rng, orders_true, 1 - ord_late) if ord_late else orders_true
                revenue = round(orders * price * rng.uniform(0.92, 1.08), 2)
                visits = human + bots
                b_h = min(0.95, max(0.05, bounce0 + rng.gauss(0, 0.015) + (0.04 if visits_mult > 1 else 0)))
                bounce = (human * b_h + bots * rng.uniform(0.85, 0.97)) / max(visits, 1)
                d_h = max(20, dur0 * math.exp(rng.gauss(0, 0.05)))
                dur = (human * d_h + bots * rng.uniform(4, 14)) / max(visits, 1)

                key, lab, sk = fam, label, sku
                if fault == "S7" and fam == target:
                    new_key, new_label = MIGRATION[fam]
                    switched = i >= onset
                    if strength == "overlap" and onset <= i < onset + 2:
                        # both keys report for two weeks: old gets a small tail
                        emit_overlap = True
                    else:
                        emit_overlap = False
                    if switched:
                        key = new_key
                        lab = label if strength in ("clean", "sku_change") else new_label
                        sk = sku if strength != "sku_change" else sku + "-N"
                        if strength == "level_shift":
                            pass  # level change applied below
                    if switched and strength == "level_shift":
                        f = 0.8
                        visits, cards, carts, chks, orders = (int(x * f) for x in (visits, cards, carts, chks, orders))
                        revenue = round(revenue * f, 2)
                    if emit_overlap:
                        tail = 0.08
                        emit(obs, weeks[i], i, fam, label, sku, seller, url, dev,
                             int(visits * tail), int(cards * tail), int(carts * tail), int(chks * tail),
                             int(orders * tail), round(revenue * tail, 2), bounce, dur)
                        visits, cards, carts, chks, orders = (int(x * (1 - tail)) for x in (visits, cards, carts, chks, orders))
                        revenue = round(revenue * (1 - tail), 2)
                emit(obs, weeks[i], i, key, lab, sk, seller, url, dev, visits, cards, carts, chks, orders, revenue, bounce, dur)

    releases.sort(key=lambda r: r["release_date"])
    for n, r in enumerate(releases, 1):
        r["release_id"] = f"R-{n:02d}"
        r["related_task_ids"] = f"T-{n:02d}"

    as_of = (weeks[-1] + dt.timedelta(days=6)).isoformat() + "T23:59:59Z"
    metrika = {
        "schema_version": "aifence.v1", "source": "metrika", "as_of": as_of, "status": "ok",
        "observations": obs,
        "warnings": ["Недельный агрегат: неделя × семейство × устройство.",
                     "Поведенческие события Метрики теряют 10–25% относительно фактических."],
    }
    cid = f"case-{idx:02d}"
    d = os.path.join(out, "run-holdout", "inputs", cid)
    os.makedirs(d, exist_ok=True)
    with open(os.path.join(d, "metrika.json"), "w") as f:
        json.dump(metrika, f, ensure_ascii=False)
    with open(os.path.join(d, "releases.json"), "w") as f:
        json.dump(releases, f, ensure_ascii=False, indent=1)
    truth = {
        "case": cid, "fault": fault, "strength": strength, "family": target,
        "onset_week_index": onset, "onset_week": weeks[onset].isoformat() if onset is not None else None,
        "weeks": n_weeks, "families": fams,
        "new_key": MIGRATION[target][0] if fault == "S7" else None,
        "shared_dip_week": weeks[shared_dip_week].isoformat() if shared_dip < 1 else None,
    }
    os.makedirs(os.path.join(out, "truth"), exist_ok=True)
    with open(os.path.join(out, "truth", cid + ".json"), "w") as f:
        json.dump(truth, f, ensure_ascii=False, indent=1)


def emit(obs, week, i, key, label, sku, seller, url, dev, visits, cards, carts, chks, orders, revenue, bounce, dur):
    vals = [("visits", visits, "count"), ("card_views", cards, "count"), ("cart_additions", carts, "count"),
            ("checkouts", chks, "count"), ("orders", orders, "count"), ("revenue", revenue, "RUB"),
            ("bounce_rate", round(bounce, 4), "ratio"), ("avg_visit_duration", round(dur, 1), "seconds")]
    for m, v, u in vals:
        loc = f"week={i};family={key};device={dev};metric={m}"
        obs.append({
            "kind": "metric", "product_key": key, "metric_key": m, "value": v, "unit": u,
            "period": {"from": week.isoformat(), "to": (week + dt.timedelta(days=6)).isoformat()},
            "segment": dev,
            "source_ref": {"source_id": "blind-metrika", "locator": loc, "revision": "metrika-weekly-holdout"},
            "status": "observed",
            "caveats": ["Недельный агрегат Метрики; часть событий не фиксируется (блокировщики, отказ от cookies)."],
            "details": {"device": dev, "synthetic": True, "origin": "synthetic_fixture", "product_label": label,
                        "sku": sku, "catalog_seller": seller, "catalog_url": url},
            "id": "obs-" + hashlib.sha1(f"{key}|{loc}|{week}".encode()).hexdigest()[:16],
        })


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("out")
    ap.add_argument("--seed", type=int, default=20261127)
    ap.add_argument("--jitter", type=float, default=0.0, help="per-case effect-size spread, e.g. 0.3")
    a = ap.parse_args()
    rng = random.Random(a.seed)
    plan = list(PLAN)
    rng.shuffle(plan)  # case number does not reveal the fault type
    for i, (fault, strength) in enumerate(plan, 1):
        make_case(i, fault, strength, random.Random(rng.random()), a.out, a.jitter)
    print(f"wrote {len(plan)} cases to {a.out}")


if __name__ == "__main__":
    main()
