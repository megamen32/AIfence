# Сторож Метрики на отложенной выборке (holdout), 27.11.2026

Проверка без подгонки: новый независимый генератор, сторож заморожен на `main` `ce760c2` (PR #4 влит), один прогон, без модели.

## Итог

- **Попаданий 9 из 22** кейсов со сбоем (семейство + тип причины + неделя ±1). Точно в неделю: 6 из 22. На прежних 30 кейсах было 20/20, то есть та цифра во многом отражает подгонку под свой генератор.
- **Ложных тревог: 1** `cause_signal` на 152 семейства без сбоя (S0 и S12: 8 из 8 чистые). Ложная — «поздняя доставка заказов» на последней неделе у малообъёмного семейства (шум).
- По типам: смена ключа S7 **4/5**, поздняя доставка S13 **4/5**, кампания/релиз S14 **1/6**, бот-трафик S6 **0/6**.
- По силе эффекта (S6, S13, S14): сильный 3/6, средний 2/6, слабый 0/5.
- На реальном Excel заказчика сторож выдаёт **1 причину, и она неправдоподобна** (см. ниже). Падение заказов KES после конфигуратора он видит только как `controlled_decline` без причины.

## Где ломается

1. **S6 бот-трафик, 0/6.** В сильных кейсах визиты десктопа +132…+139%, отказы +86…+109%, время визита −47%, но заказы +18…+27%: генератор кладёт осеннюю сезонность и дрейф, и правило «заказы в пределах ±15%» перестаёт выполняться. В средних кейсах время визита падает на 27–30%, на границе правила «−30%». Требовать все условия сразу слишком жёстко для реального шума.
2. **S14 кампания + падение checkout, 1/6.** После шума checkout в средних кейсах падает на 11–13%, порог −15% их отсекает. В одном сильном кейсе (case-25) тип угадан, но неделя на 3 недели позже.
3. **Сдвиг недели начала.** case-25 (+3 нед.) и case-30 (+4 нед., тип верный) — точка разреза выбирается позже реального начала.
4. **S7 с перекрытием.** Когда старый ключ ещё 2 недели отдаёт хвост данных (обычная ситуация при миграции), смена ключа не распознаётся.
5. **S13 слабый (1 неделя, −12%)** не отличим от шума. Это честный предел, не ошибка.
6. **Причина без данных.** На реальных данных `late_event_delivery` срабатывает, хотя checkout в данных нет вовсе: условие «checkout ровный» выполняется пустым множеством.

## Реальные данные заказчика (Датасет_ИИ-помощник_PM_v17.xlsx)

Адаптер `tests/holdout/real_customer_to_blind.py`: дневные строки `Метрики_данные` сведены в недели, `Продажи.orders_index` стал `orders`, а `cfg_family_views` переименован в `visits`. Лист `Эталонный_кейс` не читается. В данных нет checkout, отказов и времени визита, поэтому правилам причин почти нечего сравнивать.

Сторож выдал 33 кандидата: 15 `uncontrolled_decline`, 11 `temporary_spike`, 6 `controlled_decline` и 1 `cause_signal`.

- `cause_signal` **Astra, late_event_delivery, с 02.11**: заказы −38% (113 → 70). Неправдоподобно: индекс заказов Astra скачет 42…189 неделя к неделе, это не события Метрики, а продажи, и checkout в данных нет. Частично верно одно: в листе есть пометка, что последняя неделя неполная (данные по 19.11).
- Главный сюжет заказчика, **падение заказов KES после релиза конфигуратора R-09 (01.10)**, сторож видит как `controlled_decline` KES orders −31% (окно с 28.09, контроль Astra). Окно совпадает с релизом, но причины и ссылки на R-09 нет.
- Остальное — шум по долям конфигуратора и ЛК (`cfg_back_reset_rate`, `lk_home_views` и др.) без контроля. Для директора это 30+ строк, из которых полезна одна.

## Метод и независимость

- Генератор `tests/holdout/generate_holdout.py` написан по описанию типов сбоев. Код сторожа/сканера и прежний генератор не читались. Формат входа взят из одного готового кейса (`metrika.json`, `releases.json`).
- **Оговорка:** я видел описание PR #4 с таблицей правил (пороги ±15%, +40% и т.д.). Силы эффектов (S6 доля ботов 1.0/0.45/0.18, S13 потеря заказов 0.55/0.30/0.12, S14 визиты/checkout 0.6/0.35, 0.3/0.2, 0.15/0.1) заданы как бизнес-размеры до прогона. После первого прогона сторожа генератор не менялся. До прогона были две правки корректности генератора: S14 задаёт падение checkout в абсолютных числах, дрейф уменьшен до ±0.5%/нед.
- 30 кейсов: S6×6, S7×5, S13×5, S14×6, S0×5, S12×3. 5–7 реальных товаров каталога, 24–32 недели, сезонность B2B, майские праздники, общий рыночный провал, промо-всплески, AR(1)-шум, пуассоновские счётчики, ~0.8 ложных релиза в неделю. Порядок кейсов перемешан, разметка лежит отдельно (`truth/`).
- Прогон: `TestBlind30Offline` из `main`. Чтобы собрать тест, в копии модуля убран `agentsdk.WithReadOnly` (его нет в v0.11.0) и удалены два теста, которые не компилируются. Логика сторожа не тронута.
- Правила оценки (`score_holdout.py`) зафиксированы до прогона: попадание — `cause_signal` на нужном семействе с ожидаемым типом и неделей ±1. Ложная тревога — любой `cause_signal` на семействе без сбоя.

Воспроизвести:
```
python3 tests/holdout/generate_holdout.py /tmp/holdout          # seed 20261127
# в копии apps/metrika-analyst без WithReadOnly:
BLIND30_DIR=/tmp/holdout BLIND30_OUT=/tmp/holdout-out go test -run TestBlind30Offline -count=1 .
python3 tests/holdout/score_holdout.py /tmp/holdout /tmp/holdout-out
```

## Таблица по кейсам

| case | planted | strength | family | onset | realized in data (8 wk before → after, both devices) | cause_signal on family | verdict | note | false alarms | false-alarm detail |
|---|---|---|---|---|---|---|---|---|---|---|
| case-01 | S14 | weak | kaspersky_endpoint_security | 2026-08-24 | vis(d) +17% bnc(d) +11% chk -9% ord -11% | — | miss | только controlled_decline | 1 | red_hat_enterprise_linux_server_premium:late_event_delivery@2026-11-16 |
| case-02 | S12 | none | kaspersky_small_office_security | 2026-08-24 |  | — | clean |  | 0 |  |
| case-03 | S12 | none | astra_linux_special_edition | 2026-09-21 |  | — | clean |  | 0 |  |
| case-04 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-05 | S7 | level_shift | kaspersky_small_office_security | 2026-06-29 | vis(d) -7% bnc(d) -2% chk -3% ord -3% | taxonomy_migration@2026-06-29 | hit (exact) |  | 0 |  |
| case-06 | S7 | clean | kaspersky_endpoint_security | 2026-09-07 | vis(d) +18% bnc(d) -5% chk +10% ord +11% | taxonomy_migration@2026-09-07 | hit (exact) |  | 0 |  |
| case-07 | S14 | strong | microsoft_365_business_premium | 2026-10-26 | vis(d) +63% bnc(d) +15% chk -36% ord -37% | masked_multi_cause_regression@2026-10-26 | hit (exact) |  | 0 |  |
| case-08 | S13 | medium | kaspersky_endpoint_security | 2026-11-09 | vis(d) -3% bnc(d) +6% chk +0% ord -23% | late_event_delivery@2026-11-16 | hit (+1 нед.) |  | 0 |  |
| case-09 | S14 | medium | kaspersky_small_office_security | 2026-07-27 | vis(d) +46% bnc(d) +11% chk -11% ord -12% | — | miss | только controlled_decline | 0 |  |
| case-10 | S14 | weak | myoffice_private_cloud | 2026-08-17 | vis(d) +33% bnc(d) +19% chk +8% ord +8% | — | miss | ничего по семейству | 0 |  |
| case-11 | S13 | weak | kaspersky_small_office_security | 2026-11-09 | vis(d) +3% bnc(d) +0% chk +20% ord +7% | — | miss | только controlled_decline | 0 |  |
| case-12 | S6 | medium | astra_linux_special_edition | 2026-10-26 | vis(d) +22% bnc(d) +38% chk -11% ord -9% | — | miss | только controlled_decline | 0 |  |
| case-13 | S13 | strong | astra_linux_special_edition | 2026-11-02 | vis(d) -3% bnc(d) +8% chk +4% ord -36% | late_event_delivery@2026-11-09 | hit (+1 нед.) |  | 0 |  |
| case-14 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-15 | S6 | strong | red_hat_enterprise_linux_server_premium | 2026-07-20 | vis(d) +132% bnc(d) +86% chk +25% ord +27% | — | miss | только controlled_decline | 0 |  |
| case-16 | S13 | medium | dr_web_desktop_security_suite | 2026-11-09 | vis(d) -12% bnc(d) +9% chk -17% ord -38% | late_event_delivery@2026-11-16 | hit (+1 нед.) |  | 0 |  |
| case-17 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-18 | S6 | medium | kaspersky_endpoint_security | 2026-10-12 | vis(d) +46% bnc(d) +51% chk +5% ord +5% | — | miss | только controlled_decline | 0 |  |
| case-19 | S12 | none | microsoft_365_business_premium | 2026-09-28 |  | — | clean |  | 0 |  |
| case-20 | S7 | relabel | kaspersky_small_office_security | 2026-08-24 | vis(d) +7% bnc(d) +1% chk +3% ord +3% | taxonomy_migration@2026-08-24 | hit (exact) |  | 0 |  |
| case-21 | S13 | strong | myoffice_private_cloud | 2026-11-09 | vis(d) -10% bnc(d) -3% chk -0% ord -45% | late_event_delivery@2026-11-09 | hit (exact) |  | 0 |  |
| case-22 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-23 | S6 | weak | astra_linux_special_edition | 2026-07-20 | vis(d) +33% bnc(d) +36% chk +12% ord +14% | — | miss | только temporary_spike | 0 |  |
| case-24 | S6 | weak | microsoft_365_business_premium | 2026-07-27 | vis(d) +30% bnc(d) +40% chk +13% ord +14% | — | miss | только temporary_spike | 0 |  |
| case-25 | S14 | strong | red_hat_enterprise_linux_server_premium | 2026-09-07 | vis(d) +66% bnc(d) +9% chk -33% ord -33% | masked_multi_cause_regression@2026-09-28 | wrong | masked_multi_cause_regression @ 2026-09-28 | 0 |  |
| case-26 | S7 | overlap | red_hat_enterprise_linux_server_premium | 2026-07-20 | vis(d) +7% bnc(d) +16% chk +9% ord +7% | — | miss | только controlled_decline | 0 |  |
| case-27 | S0 | none | — | — |  | — | clean |  | 0 |  |
| case-28 | S7 | sku_change | kaspersky_small_office_security | 2026-06-22 | vis(d) +14% bnc(d) -1% chk +6% ord +7% | taxonomy_migration@2026-06-22 | hit (exact) |  | 0 |  |
| case-29 | S14 | medium | red_hat_enterprise_linux_server_premium | 2026-08-03 | vis(d) +46% bnc(d) +13% chk -13% ord -15% | — | miss | только controlled_decline | 0 |  |
| case-30 | S6 | strong | dr_web_desktop_security_suite | 2026-09-07 | vis(d) +139% bnc(d) +109% chk +15% ord +18% | traffic_quality@2026-10-05 | wrong | traffic_quality @ 2026-10-05 | 0 |  |

| fault | strength | hits / cases |
|---|---|---|
| S13 | medium | 2/2 |
| S13 | strong | 2/2 |
| S13 | weak | 0/1 |
| S14 | medium | 0/2 |
| S14 | strong | 1/2 |
| S14 | weak | 0/2 |
| S6 | medium | 0/2 |
| S6 | strong | 0/2 |
| S6 | weak | 0/2 |
| S7 | clean | 1/1 |
| S7 | level_shift | 1/1 |
| S7 | overlap | 0/1 |
| S7 | relabel | 1/1 |
| S7 | sku_change | 1/1 |

TOTAL hits 9/22; false-alarm cause_signals 1
