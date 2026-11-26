# Offline data preparation
Owner: research-data (shared main checkout). See docs/tracks/research-data.md. Outputs for actual dataset belong under ignored data/ in main project; only code/synthetic fixture in Git.

## Импорт для сторожа

```sh
python3 -B data-tools/watch_import.py \
  --workbook "$WORKBOOK" --as-of "$CUTOFF" \
  --breaks "$BREAK_REGISTRY" --interviews "$INTERVIEW_SELECTION" \
  --out apps/metrika-analyst/demo_sources/data/track-b
```

- `--workbook` принимает оригинал XLSX. Читаются продажи, метрики, справочник,
  релизы и бэклог. Лист ответов и ссылки на него исключены.
- `--breaks` принимает приватный JSON-массив. Запись содержит `sheet`, `row`,
  `column` для ссылки на первичный источник, даты `from`, `to`, `metric_keys`,
  `effect` (`exclude`, `boundary` или `missing`) и `reason`. Допустимы
  `product_key`, `segment` и дата, когда разрыв стал известен, `known_from`.
  Пометки источника о неполной неделе продаж добавляются автоматически.
- `--interviews` принимает приватный JSON-массив. Каждая запись задаёт `file`,
  `sheets` (соответствие листа массиву букв столбцов с ответами) и опциональный
  `first_response_row`. Пути относительны к файлу настроек или абсолютны.
  Размечайте только разрешённые ответы. Импорт пропускает ячейки с контактными
  маркерами. При отсутствии даты интервью используется дата среза с явной пометкой.
- `watch-series.json` содержит числовые наблюдения. `watch-context.json` содержит
  релизы, задачи, разрывы и выбранные ответы. Привязка использует имя файла, SHA-256
  и координаты исходных строк или ячеек. Оба файла создаются в игнорируемом
  каталоге `data/` или `apps/metrika-analyst/demo_sources/data/`.

## Закрытая проверка на локальном Airlock

1. Интегратор выкладывает Metrika Analyst со штатной миграцией и задаёт
   `watchdog_closed_origin=http://metrika-analyst.localhost:42080` только в
   закрытой локальной установке. На публичном стенде оставьте настройку пустой.
2. Войдите в Airlock как пользователь, которому разрешено работать с данными.
   Откройте нативное приложение в отдельной вкладке и перейдите на
   `http://metrika-analyst.localhost:42080/watchdog`.
3. В форме «Загрузить снимок» выберите подготовленные
   `apps/metrika-analyst/demo_sources/data/track-b/watch-series.json` и
   `watch-context.json` из того же каталога. Ожидается HTTP 201
   и `{"status":"stored"}`. Передача идёт напрямую из браузера в локальный
   Airlock. Файлы фиксируются парой в нативном хранилище `ScopeUser` с доступом
   только для кода приложения. Gzip-хранение исключает извлечение текста
   индексатором. Загрузка, выполнение и результаты проходят вне чата.
4. Для одного исторического прогона заполните ключи цели и контроля,
   `orders_index` и диапазон из локального оракула. Нажмите «Проверить историю».
   Ответ содержит даты, решения и коды причин. Для ежедневного запуска нужна
   отдельная отметка согласия. Первый запуск возвращает `job_id`, который
   можно проверить через `python3 -B data-tools/watch_job_canary.py --job-id UUID`.
   У успешной ежедневной доставки ожидается `next_job_id`.
5. Нажмите «Остановить ежедневный запуск» после закрытой проверки, если
   постоянное согласие клиента пока отсутствует.

### Генерация синтетических серий

Для нового набора выберите отдельный каталог вне Git. Генератор использует
реальные идентификаторы позиций Market Watch, а значения метрик и цен помечены
как синтетические. Импорт v17 запускается командой из предыдущего раздела.

```sh
python3 -B apps/metrika-analyst/demo_sources/blind_test/blind_test_generator.py generate \
  --seed 20261127 --scenario all --weeks 28 --output .runtime/blind-catalog-v1
```

В Airlock передаётся только `set-*/input/`. Таблицы `truth.json` и `answers/`
остаются у организатора.
