# Metrika Analyst

Airlock SDK v0.11.0. Готовый MCP → Supergateway → штатный MCP-ресурс Airlock → аналитические инструменты + инструкции. Нет собственного клиента API Метрики, базы метрик или второго сервиса импорта.

## Выбор MCP

BoxLab `yandex-metrica-mcp@1.0.1` (исследованный исходник 67ea99ef) + `supergateway@4.3.0`. Точный npm lock включён. Выбор по функциональности: цели, e-commerce, сравнения периодов, временные ряды и детализация. DevCrabs имеет закрытый каталог метрик без e-commerce; expremiental не реализует специализированные сравнения. Транспорт stdio не является причиной отказываться от более функционального MCP.

Живое сравнение вызывает `run_comparison` BoxLab и нормализует результат в контракт evidence-v1. При работе с историческими агрегатами из файлов Airlock выполняются детерминированные расчёты. Нет подмены live синтетическими данными.

## Подключение

1. В отдельной закрытой сети каждого заказчика запустить готовые пакеты командой ниже. Задать `YANDEX_METRIKA_TOKEN` (только metrika:read) и отдельный `SUPERGATEWAY_API_KEY` из защищённого окружения. Не публиковать порт наружу и не передавать секреты в чат. `YANDEX_METRIKA_ENABLE_WRITE=false`.
2. SDK объявляет штатный MCP-ресурс `yandex_metrika`, `http://metrika-mcp:8000/mcp`, с token-auth. В credentials Airlock указать ключ шлюза, не токен Яндекса. MCP-прокси должен видеть DNS-имя metrika-mcp в закрытой сети.
3. Указать `counter_id`, `ecommerce_confirmed=true` только после проверки полноты инструментации. Для файлов явно выбрать `evidence_path` или `source_path` (evidence-v1 JSON).
4. `check_metrika` → `compare_periods`. Live-метрики магазина: `product_key=store`, сегмент `all` или `filter:<проверенный фильтр API>` (предварительно уточнить значения через отчёты MCP). Денежные показатели RUB для MVP; SKU и цели требуют явной конфигурации или отдельного прямого запроса MCP.

```sh
# AIFENCE_MCP_NETWORK — существующая закрытая сеть прокси Airlock.
# YANDEX_METRIKA_TOKEN и SUPERGATEWAY_API_KEY — из защищённого окружения.
docker compose -f metrika.compose.yml up -d --build
go test ./...
AIRLOCK_AGENT_MODE=manifest go run .
go tool air build
```

Шлюз публикует только get_metadata, describe_counter, run_report, run_comparison, run_timeseries, run_drilldown. Выгрузка сырых событий, административные операции и записи не входят в MVP. Внутри приложения: query_metrics, compare_periods, check_metrika; возврат evidence-v1. Равные непересекающиеся окна; абсолютные, относительные и п.п. изменения; null при отсутствии данных/нулевой базе; семплирование помечается. UI / выводит таблицу со ссылками на факты. Интерпретация — чат.

## AIfence demo sources

The standalone demo_sources/ layer keeps the Softline workbook immutable and
separates synthetic problem scenarios. It does not change Airlock core.

Build it on the Mac M1:

    python3 demo_sources/aifence_demo_sources.py build       --workbook /Users/user/AIfence/data/softline_dataset_v17.xlsx

Set the app's demo_data_root to
/Users/user/AIfence/apps/metrika-analyst/demo_sources/data and choose either
original-evidence.json or sandbox-evidence.json as source_path. The existing MCP
tools then use the same evidence-v1 contract. See demo_sources/README.md for
source provenance and scenario IDs.

One physical Yandex counter (114698578) is enough for recent synthetic
replay/smoke events. Historical workbook aggregates stay in the imported
source because Measurement Protocol cannot rewrite past history.

## Инструкции агента

INSTRUCTIONS.md встроен в Go-бинарник и зарегистрирован штатным `agent.AddInstruction`. Это не отдельный runtime-механизм SKILL.md: приложение получает методологию в системном промпте. Поддержка developer skills относится к среде разработки.

## Verification on Mac M1 (2026-11-26)

- go test -v ./...: seven tests passed; go vet passed; Go offline manifest generated.
- Actual npm packages installed; Supergateway streamable HTTP initialized, requests without gateway key rejected, six-tool allowlist verified. Real BoxLab metadata, report and comparison calls against a local synthetic API passed (-20% comparison).
- Reproduce: `npm ci --omit=dev && python3 smoke_boxlab.py`. This test uses only a localhost synthetic API and never accesses a customer's account.
- Live Yandex token/counter not configured. Local Airlock HTTP at :42080 was unavailable. No end-to-end deployment or real customer data acceptance claimed.

## Сторож и исторические свидетельства

| SDK tool | Вход | Результат |
|---|---|---|
| `detect_signal`, `replay_signals` | `product_key`, `control_product_key`, `metric_key`, `from`, `to`; опционально `as_of`, `segment`, `source_path`, `context_path`, `scenario_id`, `drop_pct`, `control_max_pct` | Evidence-v1 `source=sales`, `watchdog.alert`. Одна неделя для detect, ряд для replay. |
| `releases_in_window` | `from`, `to`; опционально `product_key`, `source_path`, `limit` | Релизы и связанные метрики и задачи. |
| `backlog_for_metrics` | `metric_keys`; опционально пара `from`, `to`, а также `product_key`, `source_path`, `limit` | Задачи, у которых указана хотя бы одна из метрик, и ссылки на определения. |
| `data_breaks_in_window` | `from`, `to`; опционально `metric_keys`, `product_key`, `source_path`, `limit` | Разрывы учёта, исключаемые и неполные периоды. |
| `interview_search` | `keywords`; опционально `product_key`, `source_path`, `limit` | Короткие цитаты с координатами ячеек. Поиск буквальный, без учёта регистра. |

Даты включительные. Для каждой недели сравниваются два соседних окна по
четыре недели у цели и контроля. В каждом окне верхний вклад недели ограничен
1,4 медианы окна. Тревога требует падения цели и падения с поправкой на контроль
минимум на `drop_pct` (20% по умолчанию). Контроль должен оставаться в пределах
`control_max_pct` (20% по умолчанию). Пропуск, дубль, разрыв учёта, открытая
или помеченная неполной неделя дают `null` с причиной. Соседние недели могут
обе вернуть `true`. Идентификатор решения стабилен для недели и серии.

`query_metrics` и `compare_periods` считают средние исторического
`orders_index` по окнам с понедельника по воскресенье. Для индекса окна
могут иметь разную длину. По умолчанию наличие неполной недели оставляет
среднее неизвестным. В `compare_periods` опция `include_incomplete: true`
показывает опубликованное значение как `estimated`, включая зависимые изменения.

### Встроенный синтетический прогон

```javascript
await platform.inspectApp({app:"metrika-analyst",section:"tools"})
await apps.metrika_analyst.tools.replay_signals({
  product_key:"demo_target", control_product_key:"demo_control",
  metric_key:"orders_index", from:"2023-02-13", to:"2023-03-05", as_of:"2023-03-03",
  source_path:"fixtures/synthetic-watch.json",
  context_path:"fixtures/synthetic-watch-context.json"
})
```

Ожидаемые значения `false → true → null`. Для контекстных вызовов
выберите `source_path:"fixtures/synthetic-watch-context.json"`.
Для повторного замера цели и контроля используйте
`fixtures/synthetic-after-fix.json`, база 2023-01-30–2023-02-26,
окно после исправления 2023-02-27–2023-03-26.

Приватные входы готовит [импорт](../../data-tools/README.md). Для штатного
хранилища Airlock укажите авторизованные пути в `evidence_path` и
`watch_context_path`. Каталог `evidence` имеет область текущего пользователя.
`demo_data_root` включает отдельный режим общей папки в закрытой установке,
где все допущенные пользователи приложения имеют доступ к загруженным данным.
Приватная папка исключена из source bundle SDK. В публичной конфигурации
используйте встроенные фикстуры и оставьте приватные источники ненастроенными.

Сборка выполняется одним процессом сборки SDK.
`GOMAXPROCS=2 GOFLAGS=-p=1 go tool air build` проверяет исходный пакет,
манифест, replay, контроль, разрывы и повторный замер. Для приватной проверки
укажите `AIFENCE_WATCH_CASE` на каталог со снимками и локальным оракулом.
