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

## Инструкции агента

INSTRUCTIONS.md встроен в Go-бинарник и зарегистрирован штатным `agent.AddInstruction`. Это не отдельный runtime-механизм SKILL.md: приложение получает методологию в системном промпте. Поддержка developer skills относится к среде разработки.

## Verification on Mac M1 (2026-11-26)

- go test -v ./...: seven tests passed; go vet passed; Go offline manifest generated.
- Actual npm packages installed; Supergateway streamable HTTP initialized, requests without gateway key rejected, six-tool allowlist verified. Real BoxLab metadata, report and comparison calls against a local synthetic API passed (-20% comparison).
- Reproduce: `npm ci --omit=dev && python3 smoke_boxlab.py`. This test uses only a localhost synthetic API and never accesses a customer's account.
- Live Yandex token/counter not configured. Local Airlock HTTP at :42080 was unavailable. No end-to-end deployment or real customer data acceptance claimed.
