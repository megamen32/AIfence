# AIfence: источники демо для Яндекс Метрики

`aifence_demo_sources.py` добавляет к приложению `metrika-analyst` два явно
разделённых источника, которые выбираются теми же `evidence-v1` инструментами
MCP (`query_metrics`, `compare_periods`, `check_metrika`):

| Режим | Источник | Для чего |
| --- | --- | --- |
| `original` | `softline-workbook-imported` | Неизменяемая выгрузка исходного Softline workbook. Строки `Метрики_данные` и недельный индекс заказов из `Продажи` превращаются в evidence-v1 с `sheet/row` provenance. Лист с эталонным кейсом организаторов исключён из всех артефактов и не является входом агента. |
| `sandbox` | `aifence-sandbox-synthetic` | Изолированные синтетические сценарии. Они помечены `synthetic=true` и никогда не смешиваются с original. |

## Сборка

Исходный workbook хранится только в приватном каталоге проекта:

```sh
cd /Users/user/AIfence/apps/metrika-analyst
python3 demo_sources/aifence_demo_sources.py build \
  --workbook /Users/user/AIfence/data/softline_dataset_v17.xlsx
```

Результат появляется в `demo_sources/data/` (каталог не коммитится):

- `original-evidence.json` — наблюдения из `Метрики_данные` и недельный индекс заказов из `Продажи` (58 наблюдений: 29 недель × 2 семейства, locator `Продажи!row N`);
- `sandbox-evidence.json` — пять синтетических сценариев;
- `aifence_sources.sqlite3` — каталог источников, raw-строки всех листов кроме исключённого, наблюдения и сценарии;
- `source-manifest.json` — карта источников, provenance и сценариев.

Для быстрой проверки:

```sh
python3 demo_sources/aifence_demo_sources.py list-sources
python3 demo_sources/aifence_demo_sources.py query --source original
python3 demo_sources/aifence_demo_sources.py query --source sandbox --scenario kes_regression
python3 demo_sources/aifence_demo_sources.py replay-plan
```

## Тесты

```sh
cd /Users/user/AIfence/apps/metrika-analyst
python3 -m unittest discover -s demo_sources -p 'test_*.py' -v
```

Тест пересобирает источники из workbook (путь можно переопределить переменной
`AIFENCE_WORKBOOK`) и проверяет, что лист эталонного кейса не попадает ни в один
артефакт, что средние недельного индекса по окнам совпадают с эталонными значениями
в пределах 0.01, и что служебные ожидания сценариев не попадают в файлы, которые
читает агент.

## Как сравнивать периоды

Сравнивайте окна из нескольких недель, а не одну неделю с одной. Недельный индекс
заказов шумный: отдельные недели могут отличаться в несколько раз, поэтому выводы
строятся по средним за несколько недель и по контрольному семейству. Например,
для Kaspersky Endpoint Security сравниваются 13 недель с 29.06 по 21.09.2026 и
7 недель с 05.10 по 16.11.2026, а Astra Linux Special Edition используется как
контроль на тех же окнах.

Индекс заказов не равен количеству заказов или выручке, и в данных только
самостоятельные заказы клиентов. Комментарии листа `Продажи` сохраняются как caveat.

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

Идентификаторы: `kes_regression`, `tracking_gap`, `mobile_regression`,
`recommendation_ctr`, `false_anomaly`. Описания намеренно нейтральны и не содержат
ожидаемых выводов; агент должен проверить данные сам.
