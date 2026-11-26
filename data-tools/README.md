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
