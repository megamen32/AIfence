# Экономика AIfence

## Измерение Terra fast blind30

| Метрика | Свежий blind30 | После PR #18 и #19 |
| --- | ---: | ---: |
| Правильная причина | 18/20 | 14/20 |
| Ложные тревоги | 0/10 | 1 |
| Медиана времени | 112 с | — |
| Ошибки | 0 | — |
| Стоимость кейса | $0.028 | $0.028 |

До исправления детектора правильная причина была 4/20, ложные тревоги — 7. После PR #18 и #19 правильная причина стала 14/20, ложные тревоги — 1.

## Формула стоимости

Стоимость периода = `$0.028 × cases_per_period × model_call_count`.

В калькуляторе используются измеренные значения:

```json
{
  "price_usd_per_case": 0.028,
  "cases_per_period": 20,
  "model_call_count": 1,
  "correct_cause": 18,
  "false_alarms": 0,
  "median_seconds": 112,
  "errors": 0
}
```

Запуск:

```sh
python3 docs/submission/economics.py data/economics-input.json
```

Результат для указанных входов: `$0.028` за кейс и `$0.560` за период.

Источники измерения: Terra fast blind30, PR #18, PR #19. Формулы XLSX находятся в [economics-cost.xlsx](economics-cost.xlsx).
