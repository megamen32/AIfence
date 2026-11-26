# AIfence: источники демо для Яндекс Метрики

`aifence_demo_sources.py` добавляет к приложению `metrika-analyst` два явно
разделённых источника, которые выбираются теми же `evidence-v1` инструментами
MCP (`query_metrics`, `compare_periods`, `check_metrika`):

| Режим | Источник | Для чего |
| --- | --- | --- |
| `original` | `softline-workbook-imported` | Неизменяемая выгрузка исходного Softline workbook. В SQLite сохраняются все листы и строки с `sheet/row` provenance; `Метрики_данные` превращаются в evidence-v1. |
| `sandbox` | `aifence-sandbox-synthetic` | Изолированные проблемные сценарии для показа аналитики. Они помечены `synthetic=true` и никогда не смешиваются с original. |

## Сборка

Исходный workbook хранится только в приватном каталоге проекта:

```sh
cd /Users/user/AIfence/apps/metrika-analyst
python3 demo_sources/aifence_demo_sources.py build \
  --workbook /Users/user/AIfence/data/softline_dataset_v17.xlsx
```

Результат появляется в `demo_sources/data/`:

- `original-evidence.json` — все 3 698 строк `Метрики_данные` плюс контрольный срез `Эталонный_кейс`;
- `sandbox-evidence.json` — пять сценариев;
- `aifence_sources.sqlite3` — каталог источников, raw-строки всех девяти листов и наблюдения;
- `source-manifest.json` — карта источников, provenance и сценариев.

Для быстрой проверки:

```sh
python3 demo_sources/aifence_demo_sources.py list-sources
python3 demo_sources/aifence_demo_sources.py query --source sandbox --scenario kes_regression
python3 demo_sources/aifence_demo_sources.py replay-plan
```

## Подключение к существующему MCP-приложению

Приложение принимает явно выбранный `evidence_path`. Для локального демо
установите `demo_data_root` в каталог `demo_sources/data` и передайте:

```text
source_path=original-evidence.json
```

или

```text
source_path=sandbox-evidence.json
```

`historical()` не делает live fallback, проверяет схему `aifence.v1`, требует
точное окно и возвращает `missing`, если значения нет. Поэтому отсутствие
`add_to_cart` у Kaspersky остаётся пропуском с caveat T-08, а не превращается в
ноль.

## Яндекс Метрика и счётчики

В демо используется один физический счётчик `114698578` для свежих synthetic
replay/smoke-событий. Исторические агрегаты нельзя корректно переписать в
прошлое Measurement Protocol, поэтому original остаётся отдельным
`imported_historical` источником. Это два логических режима одного MCP, а не
обязательные два счётчика.

`replay-plan` намеренно не содержит токена. Если понадобится отправить
свежий synthetic replay, используйте локальный `aifence_seed_metrika.py` с
интерактивным вводом токена; токен не записывается в репозиторий или файлы.

## Сценарии

- `kes_regression` — устойчивое падение KES при почти стабильной Astra;
- `tracking_gap` — пропущенная корзина KES, unknown ≠ zero;
- `mobile_regression` — сегментный провал checkout;
- `recommendation_ctr` — рост показов при падении CTR;
- `false_anomaly` — неполный последний день, вывод откладывается.

