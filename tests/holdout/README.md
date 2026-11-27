# Holdout для сторожа Метрики

Независимый генератор слепых кейсов и оценка. Отчёт: `docs/qa/holdout-watchdog-2026-11-27.md`.

- `generate_holdout.py OUT` — 30 кейсов в `OUT/run-holdout/inputs/case-NN`, разметка в `OUT/truth` (не класть рядом с входами).
- `score_holdout.py OUT SCANS` — таблица попаданий по выходу `TestBlind30Offline`.
- `real_customer_to_blind.py WORKBOOK OUT` — данные заказчика в тот же формат (без листа Эталонный_кейс).

Правило: генератор не правится под результаты сторожа. Новая версия — новый seed и новый отчёт.
