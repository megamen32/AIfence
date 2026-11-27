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

Канонический масштаб сигнала хранится в `details.magnitude.relative_change_pct`.
Его основа `capped_four_week_relative_change` и единица `percent`. Для каждого
из двух полных окон берётся среднее четырёх недель, причём вклад каждой
недели ограничен сверху 1,4 медианы **своего** окна. Формула масштаба —
`100 × (current.capped_mean / baseline.capped_mean − 1)`. Полные периоды и
средние входят в `magnitude.baseline` и `magnitude.current`. Collect отдаёт
тот же объект как `first_alert_magnitude` со своим `first_alert_signal_id`.

Относительное изменение контроля рассчитывается тем же оконным методом.
С поправкой на контроль используется
`100 × ((target_current / target_baseline) / (control_current / control_baseline) − 1)`.
Порог решения отдельно указан в `details.detection_thresholds`. Последние
две сырые недели и их процент доступны как диагностика в
`details.raw_week_comparison`. Поле `details.relative_change_pct` сохранено
как алиас канонического значения для старых потребителей. Старые снимки без
`magnitude` сохраняют исходный контракт. Для обновления используется повторный
расчёт проверенного снимка с тем же signal ID.

Регрессия обнаружена при разборе ответов QA: порог попал в текст как масштаб
снижения. Локальная проверка
`GOMAXPROCS=2 GOFLAGS=-p=1 go test -run TestWatchdogMagnitudeSameAcrossDetectReplayAndCollect -count=1 .`
сравнивает три входа инструмента, оба окна, поправку на контроль и сырые недели
на одной синтетической серии.

`query_metrics` и `compare_periods` считают средние исторического
`orders_index` по окнам с понедельника по воскресенье. Для индекса окна
могут иметь разную длину. По умолчанию наличие неполной недели оставляет
среднее неизвестным. В `compare_periods` опция `include_incomplete: true`
показывает опубликованное значение как `estimated`, включая зависимые изменения.

### Встроенный синтетический прогон

```javascript
await platform.inspectApp({app:"metrika-analyst",section:"tools"})
await apps.metrika_analyst.tools.replay_signals({
  product_key:"kaspersky_small_office_security", control_product_key:"dr_web_desktop_security_suite",
  metric_key:"orders_index", from:"2023-02-13", to:"2023-03-05", as_of:"2023-03-03",
  source_path:"fixtures/synthetic-watch.json",
  context_path:"fixtures/synthetic-watch-context.json"
})
```

Ожидаемые значения `false → true → null`. Цель — Kaspersky Small Office
Security (Softline, `KL4141RCEFR`), контроль — Dr.Web Desktop Security Suite
(SoftMagazin, `LBW-BC-12M-100-A3`). Наблюдения обоих рядов синтетические. Для контекстных вызовов
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

## Нативный ежедневный сторож

Airlock регистрирует `watchdog_run@v1` и cron `watchdog_daily` на 06:00 UTC.
Cron с `watchdog_canary_enabled=true` использует только встроенную синтетику
и тот же Go-детектор. По умолчанию результат cron — `disabled`.

Для проверки на M1 после выкладки и штатного входа SDK в локальный Airlock:

```sh
python3 -B data-tools/watch_job_canary.py --fire
```

Команда из корня репозитория вызывает штатный `POST /api/v1/agents/{id}/schedules/watchdog_daily/fire`,
печатает ID задания и читает его через `GET /api/v1/jobs/{id}`. Ожидаются
`succeeded` и решения `false, true, null`. После прерванного ожидания повторите
чтение с `--job-id UUID`. В интерфейсе Airlock этому соответствует
**Расписания → watchdog_daily → Запустить сейчас**.

Приватный запуск включается по явному согласию владельца на странице
`/watchdog`. Он работает пользовательскими `JobHandle.EnqueueAt` заданиями.
Airlock сохраняет реального инициатора дочерней доставки. Каждый запуск
сверяет пользователя и текущий job ID с его записью согласия, проверяет
последнюю закрытую календарную неделю и ставит следующее задание на 06:00 UTC.
Остановка отзывает согласие в БД и отменяет ближайшее задание. Для смены ключей
цели и контроля остановите расписание и включите его снова. Свежие снимки
загружаются той же парой файлов. Задание сохраняет только решения и ID
следующей доставки. При ограничении доступа Airlock или отключении закрытого
режима для возобновления требуется новый запуск владельцем.

Настройка закрытой интеграции и путь загрузки — в [data-tools](../../data-tools/README.md).

### Первое обнаружение

Миграция `00003_watchdog_first_seen.sql` фиксирует подтверждённую тревогу один
раз по владельцу, набору и `signal_id`. `detected_at` — UTC RFC 3339,
`detected_at_source: agent_observed`. Повторные вызовы возвращают сохранённое
время. Выбранный снимок имеет общий ключ для native job, закрытого replay и
blind tools. Обычные инструменты используют хеш содержимого выбранного ряда.
Cron-canary хранит время отдельно от пользовательских записей.

Native job записывает время в БД и сохраняет прежний контракт
`watchdog_run@v1`. Поля доступны в кандидатах discover/scan, на верхнем уровне
collect и в `details` соответствующей строки `watchdog.alert`. Масштаб
сигнала берите из `magnitude.relative_change_pct` в строке или кандидате и
`first_alert_magnitude.relative_change_pct` на верхнем уровне collect.
`as_of` и `period` описывают данные. Старые job receipts сохраняют прежний
формат: они содержат итог последнего запуска без идентификатора снимка и
без доказательства первой регистрации. Для них время остаётся неизвестным.
Метка `first_registered` предназначена для исторической записи с проверенной
датой первой регистрации.

## Слепой анализ выбранного набора

В закрытой установке задайте `watchdog_closed_origin`, примените миграцию
`00002_blind_selection.sql` и откройте `/watchdog` под тем же пользователем,
который будет вести чат. Загрузите числовой `evidence-v1` с источником
`metrika` или `sales` и, если есть, каталог контекста. Строки должны
содержать фактические даты, измерения, ссылки на источники и явный
`details.synthetic`. После загрузки нажмите «Разрешить анализ этого снимка
в чате». Для импортированных данных требуется также одобренный закрытый
модельный маршрут и `watchdog_blind_model_route=approved`. Для передачи их
кейса из чата в Product Manager применяется его отдельное разрешение
`private_model_route=approved`.

- `discover_watchdog_series({})` возвращает `dataset_id`, `origin` выбранного набора
  (`synthetic_fixture` или `imported_historical`), дату среза,
  число измерений и страницу их метаданных. `scan_status` и `findings`
  содержат итог полного скана и до четырёх выводов, готовых к обсуждению в чате.
- `scan_watchdog_series({})` возвращает те же `dataset_id` и `origin`, `status`, покрытие,
  полное число кандидатов и страницу кандидатов с `candidate_id` и ссылками.
  Оба инструмента принимают `offset` и `limit` (до 64), а следующую страницу
  указывает `next_offset`. Сканы охватывают все даты, по которым есть
  недельные строки. На синтетическом наборе из 1 960 строк и 70 измерений
  первая страница discovery заняла около 6 КиБ, скан выполнялся за 29–35 мс
  на Mac M1. Вычислительный бюджет ограничивает число проходов по окнам,
  контролям и контексту. При превышении бюджета скан выдаёт ошибку. Контроль выбирается среди
  сопоставимых продуктов по покрытию, стабильности и форме **базового** окна.
  Текущие значения в подборе контроля не участвуют. Сканер использует
  закрытые интервалы понедельник–воскресенье и те же пороги сторожа.
  Повторный сигнал с тем же контролем в соседних окнах имеет приоритет перед
  одиночным. `confirmed_windows` считает соседние перекрывающиеся окна.
  Последнее из них сопровождается `last_confirmed_week` и `last_confirmation_refs`.
- `collect_selected_watchdog_case({dataset_id,candidate_id})` собирает
  кейс `controlled_decline`. При необходимости добавьте `keywords` для
  поиска интервью. Данные передаются в импорт PM внутри одного вызова.
  Для `quiet` или `incomplete` достаточно результата скана.

`measurement_issue` указывает на запись реестра разрывов или неделю,
помеченную источником как несопоставимая. `campaign_effect` означает
временный всплеск, совпавший с документированной кампанией. Без такой
записи возвращается `temporary_spike`. Одиночная или несопоставимая серия
может дать `uncontrolled_decline` при статусе `incomplete`. Дневные
ряды видны в discovery, недельный скан их не агрегирует.

Одна активная версия в `ScopeUser` на пользователя. Следующая загрузка
снимает выбор. Отзыв закрывает слепые инструменты для снимка. Старые ID не
подходят к другому набору. Для шести живых прогонов под одним пользователем
завершайте чат до переключения набора, затем запускайте новый чат. Для
параллельных прогонов используйте отдельных пользователей.

## Две недели внешнего спроса

`compare_wordstat` возвращает число запросов, `compare_google_trends` — недельный индекс 0–100. Вход обоих инструментов содержит точные `product_key`, `query`, `geo` и `monday` (UTC-понедельник сразу после двух сравниваемых недель). Периоды включают даты с понедельника по воскресенье. `ALL` означает все регионы. Для Wordstat `RU` соответствует региону 225, также принимается код региона Яндекса.

- Wordstat без `source_path` выполняет один запрос к официальному API через пользовательское подключение `wordstat` и необязательный `wordstat_folder_id`. API отдаёт историю по 7 октября 2026 года. Более поздняя неделя, отсутствие подключения или строки за неделю дают `unavailable`.
- Wordstat с `source_path` и Google Trends читают один явно выбранный `aifence.v1` JSON с `source: research` из области текущего пользователя в Airlock (`evidence/user-<id>/…`). Укажите `fixtures/external-demand.json`, чтобы явно выбрать встроенный синтетический ряд Kaspersky Endpoint Security за недели 14–20 и 21–27 сентября 2026 года. Пустой путь для Trends даёт `unavailable`.
- Каждое наблюдение сохраняет `period`, `source_ref` с локатором исходной строки и `details` с `query`, `geo`, `origin`, `synthetic` и `normalization`. Маркеры происхождения — `live_api`, `authorized_import` и `synthetic_fixture`. В импорте используйте только локаторы и URL без учётных данных.
- Для Wordstat задайте `normalization: weekly_count_all_devices`. Для Google Trends возьмите обе недели из одной выгрузки с общей шкалой, установите `normalization: google_trends_0_100:<export-id>` и `source_ref.revision: <export-id>` для обеих строк. Не объединяйте отдельно нормированные выгрузки и не записывайте подавленное или отсутствующее значение как 0. Индекс описывает относительный поисковый интерес.
- `ok` содержит ровно две сопоставимые строки, от ранней к поздней. При неполных данных `unavailable` содержит пустой массив и причину. Некорректный ответ, дубликаты и ошибки чтения API имеют явную ошибку. Наблюдаемый ноль остаётся числом.

Для `sourcewatch` сохранён вызов `compare_demand({phrase, monday})` с `source: wordstat`, `product_key: <phrase>`, источником `yandex-wordstat:search-api` и пустым массивом для `unavailable`. Новые результаты с `source: research` совместимы с импортом evidence-v1 в Product Manager. Существующие `read_wordstat` и `read_google_trends` работают со снимками Product Manager.

Локальная проверка связки и происхождения данных в `apps/metrika-analyst`:

```sh
GOMAXPROCS=2 GOFLAGS=-p=1 go test -run '^TestExternalDemand' -count=1 .
```
