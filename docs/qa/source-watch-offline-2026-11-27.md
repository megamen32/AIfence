# Sourcewatch blind replay, 27 November 2026

This is an offline Go replay, separate from the [11 hosted chats](blind-hosted-2026-11-27.md). It invokes the actual `sourcewatch.Check`, not a second implementation. The source was the staged owner worktree index: `service.go` Git blob `3d599a7504a7c535de45c601a277e55dbbadf92a`, `demand.go` blob `4edd9aeb6b2f78ab9fe20c16faac8194da21974a`; the owner worktree had an unresolved merge, so this is not an assertion that this code was released or present in b09ef84. The temporary staging directory was removed after the run.

The callback supplied the source batch directly from each input file. It did not receive the oracle and did not fabricate missing source readings. The second binding was configured as Market Watch, with `visits`, a 20% threshold, and the Monday following the input `as_of`; the launch callback was an in-memory stub. These choices describe an applicability check of the current store-level contract, not a per-product diagnosis. All results were frozen before reading scenario answers.

| Inputs / expected scenario | Count | Observed `Check` result |
|---|---:|---|
| S0 / no anomaly | 17 | Error: market source absent in input |
| S1 / tracking gap | 16 | Error: market source absent in input |
| S2 / mobile regression | 16 | Error: market source absent in input |
| S3 / price competition | 15 | Error: market source absent in input |
| S4 / release regression | 15 | Error: market source absent in input |
| S6 / traffic quality | 1 | Error: market source absent in input |
| S7 / taxonomy migration | 1 | Error: market source absent in input |
| S12 / insufficient data | 1 | Error: market source absent in input |
| S13 / late event delivery | 1 | Error: market source absent in input |
| S14 / masked multi-cause regression | 1 | Error: market source absent in input |
| Five multisource cases / case-specific market and demand expectations | 5 | Error: Metrika source absent in input |
| **Total** | **89** | **84 missing market; 5 missing Metrika; 0 simulated launches** |

The replay covered 78 historical inputs under the canonical Mac runtime, six current blind-director inputs, and five multisource snapshots. None supplied the complete two-binding input needed by this run, and the historical family-level observations cannot be silently relabeled as a live store comparison. Therefore the detector's hit rate is **0/0, undefined**, and false-alarm rate is **0/0, undefined**. These are not zero-percent performance measurements. A meaningful replay requires a documented, source-preserving producer adapter or datasets with the actual store-level Metrika comparison and a second source. Some expected multi-cause or product-level outcomes are also outside this check's store-threshold contract.

The one bounded test command used `GOMAXPROCS=2 GOFLAGS=-p=1` with a Go overlay from `tests/integration/sourcewatch_blind_test.go`: it passed and wrote 89 create-only raw results to the private runtime. Passing means the replay executed; it does not mean the detector found the hidden scenarios. Runtime records, dataset hashes and errors are in `.runtime/blind-director/eleven-independent/offline-frozen.json` and are not included in Git.

## Повтор на опубликованной версии `0f01bb0`

Правило всех соседних полных недель зафиксировано до запуска в [отдельном документе](source-watch-blind-rule.md), commit `4687383`; код replay — commit `2485bb5`. Источники и хеши зафиксированы до результатов. Go overlay вызвал фактический `Check` из `0f01bb0f34148b008ad9f9058aad95ac82105748` без изменения приложения; `GOMAXPROCS=2 GOFLAGS=-p=1`, один процесс. В выходном файле заморожены **69 958 окон 84 входов**. Дополнительный источник Google Trends — независимый постоянный синтетический контроль; он не является фактическим спросом. Оставшиеся пять multisource-наборов не содержат исходной Метрики и исключены из этого replay.

| Проверка порогового сигнала | Числитель / знаменатель | Что именно измерено |
|---|---:|---|
| Совпадение с размеченной неделей и семейством | 51/65 | Хотя бы один сигнал по любой из поддержанных метрик и сегментов в аннотированном окне; **не** распознавание семантической причины |
| S0: хотя бы один сигнал за всю историю | 17/17 | Строгое покейсное правило «любой сигнал» по всем доступным семействам, сегментам и неделям; много возможностей на один кейс, это не частота ложных тревог на отдельное окно |

S12 исключён из сопоставления по неделе: в эталоне нет конкретных семейства и недели. Один S7 исключён из знаменателя положительных кейсов, поскольку не нашлось сопоставимого окна аннотированного семейства. Разные типы сигнала и причинность этим тестом не определяются; `submitted` означает только запуск заглушки. Наличие 84 входов само по себе не доказывает выполнение требования КТ3 о 30 самостоятельных сценариях: среди входов есть повторы сценарных классов, а независимость набора следует проверить отдельно. Подробные покейсные исходы и сигналы вне окна сохранены в приватном `sourcewatch-measured-score.json`, неизменяемые окна — в `adjacent-replay/windows-frozen.jsonl`, хеши — в `adjacent-replay/freeze.json`.
