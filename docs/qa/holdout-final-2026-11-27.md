# Финальная запечатанная выборка: сторож v2 (19d7423) против v1 (ce760c2)

Прогон один, без модели, сторож не правился. Выборка: 30 новых кейсов, seed открыт владельцем только после заморозки v2. Генератор `tests/holdout/generate_holdout.py` @ `8add1c5`, `--jitter 0.3`.

Хэши совпали с запечатанными до прогона:
- truth `a254a461076875f53aca6904c16049e69daa9712aaddda11584b9edc4969601e`
- inputs `9bc23fa1c8d4cb4c1228d99ea68517e7281e123781d8d0733c7733c50f945ffa`

## Итог

| | v1 (ce760c2) | v2 (19d7423) |
|---|---|---|
| Попадания (семейство + тип + неделя ±1) | 7/22 | **11/22** |
| из них точно в неделю | 5 | 10 |
| Неверный тип/неделя на нужном семействе | 2 | 0 |
| Ложные `cause_signal` (152 чистых слота, S0/S12 8 кейсов) | 1 | **0** |

По типам (v1 → v2):
- **S7 смена ключа:** 4/5 → 4/5. v2 ловит перекрытие ключей, но теряет миграцию со сдвигом уровня −10…−13%.
- **S13 поздняя доставка:** 2/5 → 4/5. Слабый кейс (−3% по заказам после шума) не ловят обе версии.
- **S6 бот-трафик:** 1/6 → 3/6, по одному на каждую силу. Пропущены сильный с визитами +155% и отказами +120%, средний с +75%/+44% и слабый.
- **S14 кампания + падение checkout:** 0/6 → 0/6. Не ловятся даже сильные кейсы: визиты +66…+73%, checkout −33%. Сторож видит в них только `controlled_decline`.

По силе эффекта (S6+S13+S14, v2): сильный 3/6, средний 3/6, слабый 1/5.

## Где ломается v2

1. **S14 не работает совсем.** Во всех 6 кейсах отказы растут на 9–16%. Кампанийный трафик так себя и ведёт, но, судя по описанию правила в PR #4, оно требует ровных отказов. Это предположение: код v2 я не читал.
2. **S6 нестабилен.** Самый сильный кейс выборки (case-23) пропущен. Попадания и промахи не упорядочены по силе эффекта, значит решение держится на порогах около границы.
3. **S7 со сдвигом уровня** после миграции (−10%) пропущен.
4. **Ложных тревог нет.** Ложная тревога v1 (поздняя доставка на последней неделе у шумного семейства) в v2 ушла.

## Сравнение с первой отложенной выборкой

На первой выборке v1 набрал 9/22, на этой 7/22. Это та же версия на других кейсах, так что разброс ±2 кейса между выборками считай шумом оценки. Рост v2 до 11/22 и 0 ложных тревог выходит за этот шум по S13 и S6, а по S14 улучшения нет.

## Как воспроизвести

```
git show 8add1c5:tests/holdout/generate_holdout.py > gen.py
python3 gen.py OUT --seed 672862278566 --jitter 0.3
# в копии apps/metrika-analyst @19d7423 без agentsdk.WithReadOnly (нет в v0.11.0), BLIND30_Z не задан:
BLIND30_DIR=OUT BLIND30_OUT=SCANS go test -run TestBlind30Offline -count=1 .
python3 tests/holdout/score_holdout.py OUT SCANS
```

## Таблица по кейсам, v2

| case | planted | strength | family | onset | realized in data (8 wk before → after, both devices) | cause_signal on family | verdict | note | false alarms | false-alarm detail |
|---|---|---|---|---|---|---|---|---|---|---|
| case-01 | S13 | medium | microsoft_365_business_premium | 2026-11-02 | vis(d) -1% bnc(d) -2% chk -2% ord -22% | late_event_delivery@2026-11-09 | hit (+1 нед.) |  | 0 |  |
| case-02 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-03 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-04 | S6 | weak | microsoft_365_business_premium | 2026-09-14 | vis(d) +28% bnc(d) +30% chk +19% ord +16% | — | miss | только controlled_decline | 0 |  |
| case-05 | S12 | none | kaspersky_endpoint_security | 2026-10-19 |  | — | clean |  | 0 |  |
| case-06 | S13 | weak | kaspersky_small_office_security | 2026-11-09 | vis(d) +10% bnc(d) +4% chk +6% ord -3% | — | miss | ничего по семейству | 0 |  |
| case-07 | S14 | strong | myoffice_private_cloud | 2026-08-24 | vis(d) +66% bnc(d) +13% chk -33% ord -34% | — | miss | только controlled_decline | 0 |  |
| case-08 | S6 | medium | myoffice_private_cloud | 2026-07-27 | vis(d) +75% bnc(d) +44% chk +11% ord +17% | — | miss | только controlled_decline | 0 |  |
| case-09 | S13 | strong | myoffice_private_cloud | 2026-11-09 | vis(d) -0% bnc(d) +3% chk +1% ord -33% | late_event_delivery@2026-11-09 | hit (exact) |  | 0 |  |
| case-10 | S7 | overlap | red_hat_enterprise_linux_server_premium | 2026-10-19 | vis(d) +14% bnc(d) +41% chk +29% ord +26% | taxonomy_migration@2026-10-19 | hit (exact) |  | 0 |  |
| case-11 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-12 | S14 | strong | kaspersky_small_office_security | 2026-08-17 | vis(d) +73% bnc(d) +13% chk -33% ord -34% | — | miss | только controlled_decline | 0 |  |
| case-13 | S14 | medium | red_hat_enterprise_linux_server_premium | 2026-08-03 | vis(d) +25% bnc(d) +9% chk -22% ord -19% | — | miss | только controlled_decline | 0 |  |
| case-14 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-15 | S7 | level_shift | microsoft_365_business_premium | 2026-09-21 | vis(d) -10% bnc(d) -3% chk -13% ord -13% | — | miss | ничего по семейству | 0 |  |
| case-16 | S14 | weak | astra_linux_special_edition | 2026-09-28 | vis(d) +17% bnc(d) +16% chk -7% ord -6% | — | miss | только temporary_spike | 0 |  |
| case-17 | S6 | strong | astra_linux_special_edition | 2026-09-21 | vis(d) +136% bnc(d) +120% chk +13% ord +15% | traffic_quality@2026-09-21 | hit (exact) |  | 0 |  |
| case-18 | S12 | none | kaspersky_small_office_security | 2026-09-14 |  | — | clean |  | 0 |  |
| case-19 | S6 | medium | kaspersky_endpoint_security | 2026-10-19 | vis(d) +56% bnc(d) +47% chk +3% ord +5% | traffic_quality@2026-10-19 | hit (exact) |  | 0 |  |
| case-20 | S13 | strong | microsoft_365_business_premium | 2026-11-09 | vis(d) -0% bnc(d) -3% chk -4% ord -33% | late_event_delivery@2026-11-09 | hit (exact) |  | 0 |  |
| case-21 | S7 | sku_change | red_hat_enterprise_linux_server_premium | 2026-08-17 | vis(d) +2% bnc(d) -1% chk -3% ord -4% | taxonomy_migration@2026-08-17 | hit (exact) |  | 0 |  |
| case-22 | S13 | medium | astra_linux_special_edition | 2026-11-02 | vis(d) +8% bnc(d) +3% chk +5% ord -18% | late_event_delivery@2026-11-02 | hit (exact) |  | 0 |  |
| case-23 | S6 | strong | microsoft_365_business_premium | 2026-08-17 | vis(d) +155% bnc(d) +120% chk +9% ord +9% | — | miss | только controlled_decline | 0 |  |
| case-24 | S14 | weak | red_hat_enterprise_linux_server_premium | 2026-09-28 | vis(d) +17% bnc(d) +14% chk -3% ord -4% | — | miss | ничего по семейству | 0 |  |
| case-25 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-26 | S6 | weak | myoffice_private_cloud | 2026-08-31 | vis(d) +35% bnc(d) +22% chk +12% ord +10% | traffic_quality@2026-08-31 | hit (exact) |  | 0 |  |
| case-27 | S7 | relabel | red_hat_enterprise_linux_server_premium | 2026-10-19 | vis(d) -3% bnc(d) -2% chk -8% ord -9% | taxonomy_migration@2026-10-19 | hit (exact) |  | 0 |  |
| case-28 | S14 | medium | microsoft_365_business_premium | 2026-08-31 | vis(d) +37% bnc(d) +13% chk -20% ord -19% | — | miss | только temporary_spike | 0 |  |
| case-29 | S7 | clean | kaspersky_small_office_security | 2026-08-10 | vis(d) +17% bnc(d) -3% chk +16% ord +14% | taxonomy_migration@2026-08-10 | hit (exact) |  | 0 |  |
| case-30 | S12 | none | dr_web_desktop_security_suite | 2026-07-06 |  | — | clean |  | 0 |  |

| fault | strength | hits / cases |
|---|---|---|
| S13 | medium | 2/2 |
| S13 | strong | 2/2 |
| S13 | weak | 0/1 |
| S14 | medium | 0/2 |
| S14 | strong | 0/2 |
| S14 | weak | 0/2 |
| S6 | medium | 1/2 |
| S6 | strong | 1/2 |
| S6 | weak | 1/2 |
| S7 | clean | 1/1 |
| S7 | level_shift | 0/1 |
| S7 | overlap | 1/1 |
| S7 | relabel | 1/1 |
| S7 | sku_change | 1/1 |

TOTAL hits 11/22; false-alarm cause_signals 0

## Таблица по кейсам, v1

| case | planted | strength | family | onset | realized in data (8 wk before → after, both devices) | cause_signal on family | verdict | note | false alarms | false-alarm detail |
|---|---|---|---|---|---|---|---|---|---|---|
| case-01 | S13 | medium | microsoft_365_business_premium | 2026-11-02 | vis(d) -1% bnc(d) -2% chk -2% ord -22% | late_event_delivery@2026-11-16 | wrong | late_event_delivery @ 2026-11-16 | 0 |  |
| case-02 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-03 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-04 | S6 | weak | microsoft_365_business_premium | 2026-09-14 | vis(d) +28% bnc(d) +30% chk +19% ord +16% | — | miss | только controlled_decline | 0 |  |
| case-05 | S12 | none | kaspersky_endpoint_security | 2026-10-19 |  | — | clean |  | 0 |  |
| case-06 | S13 | weak | kaspersky_small_office_security | 2026-11-09 | vis(d) +10% bnc(d) +4% chk +6% ord -3% | — | miss | ничего по семейству | 0 |  |
| case-07 | S14 | strong | myoffice_private_cloud | 2026-08-24 | vis(d) +66% bnc(d) +13% chk -33% ord -34% | — | miss | только controlled_decline | 0 |  |
| case-08 | S6 | medium | myoffice_private_cloud | 2026-07-27 | vis(d) +75% bnc(d) +44% chk +11% ord +17% | — | miss | только controlled_decline | 0 |  |
| case-09 | S13 | strong | myoffice_private_cloud | 2026-11-09 | vis(d) -0% bnc(d) +3% chk +1% ord -33% | late_event_delivery@2026-11-16 | hit (+1 нед.) |  | 0 |  |
| case-10 | S7 | overlap | red_hat_enterprise_linux_server_premium | 2026-10-19 | vis(d) +14% bnc(d) +41% chk +29% ord +26% | — | miss | только controlled_decline | 0 |  |
| case-11 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-12 | S14 | strong | kaspersky_small_office_security | 2026-08-17 | vis(d) +73% bnc(d) +13% chk -33% ord -34% | — | miss | только controlled_decline | 0 |  |
| case-13 | S14 | medium | red_hat_enterprise_linux_server_premium | 2026-08-03 | vis(d) +25% bnc(d) +9% chk -22% ord -19% | — | miss | только controlled_decline | 0 |  |
| case-14 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-15 | S7 | level_shift | microsoft_365_business_premium | 2026-09-21 | vis(d) -10% bnc(d) -3% chk -13% ord -13% | taxonomy_migration@2026-09-21 | hit (exact) |  | 0 |  |
| case-16 | S14 | weak | astra_linux_special_edition | 2026-09-28 | vis(d) +17% bnc(d) +16% chk -7% ord -6% | — | miss | только temporary_spike | 0 |  |
| case-17 | S6 | strong | astra_linux_special_edition | 2026-09-21 | vis(d) +136% bnc(d) +120% chk +13% ord +15% | — | miss | только controlled_decline | 0 |  |
| case-18 | S12 | none | kaspersky_small_office_security | 2026-09-14 |  | — | clean |  | 0 |  |
| case-19 | S6 | medium | kaspersky_endpoint_security | 2026-10-19 | vis(d) +56% bnc(d) +47% chk +3% ord +5% | traffic_quality@2026-10-19 | hit (exact) |  | 0 |  |
| case-20 | S13 | strong | microsoft_365_business_premium | 2026-11-09 | vis(d) -0% bnc(d) -3% chk -4% ord -33% | late_event_delivery@2026-11-09 | hit (exact) |  | 0 |  |
| case-21 | S7 | sku_change | red_hat_enterprise_linux_server_premium | 2026-08-17 | vis(d) +2% bnc(d) -1% chk -3% ord -4% | taxonomy_migration@2026-08-17 | hit (exact) |  | 0 |  |
| case-22 | S13 | medium | astra_linux_special_edition | 2026-11-02 | vis(d) +8% bnc(d) +3% chk +5% ord -18% | late_event_delivery@2026-11-16 | wrong | late_event_delivery @ 2026-11-16 | 0 |  |
| case-23 | S6 | strong | microsoft_365_business_premium | 2026-08-17 | vis(d) +155% bnc(d) +120% chk +9% ord +9% | — | miss | только controlled_decline | 0 |  |
| case-24 | S14 | weak | red_hat_enterprise_linux_server_premium | 2026-09-28 | vis(d) +17% bnc(d) +14% chk -3% ord -4% | — | miss | ничего по семейству | 0 |  |
| case-25 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-26 | S6 | weak | myoffice_private_cloud | 2026-08-31 | vis(d) +35% bnc(d) +22% chk +12% ord +10% | — | miss | ничего по семейству | 0 |  |
| case-27 | S7 | relabel | red_hat_enterprise_linux_server_premium | 2026-10-19 | vis(d) -3% bnc(d) -2% chk -8% ord -9% | taxonomy_migration@2026-10-19 | hit (exact) |  | 0 |  |
| case-28 | S14 | medium | microsoft_365_business_premium | 2026-08-31 | vis(d) +37% bnc(d) +13% chk -20% ord -19% | — | miss | только temporary_spike | 0 |  |
| case-29 | S7 | clean | kaspersky_small_office_security | 2026-08-10 | vis(d) +17% bnc(d) -3% chk +16% ord +14% | taxonomy_migration@2026-08-10 | hit (exact) |  | 0 |  |
| case-30 | S12 | none | dr_web_desktop_security_suite | 2026-07-06 |  | — | false alarm |  | 1 | kaspersky_endpoint_security:late_event_delivery@2026-11-16 |

| fault | strength | hits / cases |
|---|---|---|
| S13 | medium | 0/2 |
| S13 | strong | 2/2 |
| S13 | weak | 0/1 |
| S14 | medium | 0/2 |
| S14 | strong | 0/2 |
| S14 | weak | 0/2 |
| S6 | medium | 1/2 |
| S6 | strong | 0/2 |
| S6 | weak | 0/2 |
| S7 | clean | 1/1 |
| S7 | level_shift | 1/1 |
| S7 | overlap | 0/1 |
| S7 | relabel | 1/1 |
| S7 | sku_change | 1/1 |

TOTAL hits 7/22; false-alarm cause_signals 1
