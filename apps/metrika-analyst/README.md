# Metrika Analyst

Airlock SDK v0.11.0. Готовый read-only MCP → штатная интеграция Airlock → аналитические инструменты + SKILL.md. Нет собственного API-клиента, базы метрик или синхронизации. Выгрузки — файлы Airlock в формате evidence-v1, не второй сервис.

## Выбор MCP

DevCrabs: 9bd1c25caf130b79ee5af60284c95674bd7bb18b, MIT, stdio и локальный OAuth/OS keychain. Для переносимого хакатонного стенда выбран expremiental: 4e708b38327ff4056e1e634cd3ea862b214edd1a, MIT, нативный HTTP, произвольные метрики, сырые totals и metadata, accuracy=full. Не нужен персональный GPTAdmin или дополнительный шлюз. Исходники MCP не копируются в этот репозиторий.

## Подключение

1. Запустить готовый MCP в закрытой сети Airlock. У каждого заказчика отдельный экземпляр/ресурс с отдельным разрешённым токеном metrika:read. Не публиковать HTTP порт и не делить общий токен между клиентами. Токен передавать через защищённое окружение, никогда через чат.
2. SDK объявляет ресурс yandex_metrika, http://metrika-mcp:8000/mcp. Использовать этот DNS alias в частной сети MCP-прокси или совместимый привязанный ресурс. Подключение ещё должно быть проверено на живом Airlock.
3. В Env приложения указать counter_id. ecommerce_confirmed=true только после проверки инструментации и непрерывности сбора за оба периода. При необходимости указать evidence_path или source_path в инструменте: это явно выбранный исторический evidence-v1 JSON, без автоматической подмены live.
4. check_metrika → compare_periods. Live: product_key=store, сегменты all/mobile/desktop, денежные метрики в RUB. Идентификация SKU требует явного маппинга.

```sh
# AIFENCE_MCP_NETWORK — существующая закрытая сеть прокси Airlock.
# YANDEX_METRIKA_TOKEN — из защищённого окружения.
docker compose -f metrika.compose.yml up -d --build
go test ./...
AIRLOCK_AGENT_MODE=manifest go run .
go tool air build
```

Инструменты: query_metrics, compare_periods, check_metrika. Равные непересекающиеся окна; абсолютные, относительные и п.п. изменения; null при отсутствии данных/нулевой базе; неполные данные помечаются. UI / выводит таблицу со ссылками на факты; чат интерпретирует. Реальные выгрузки должен сопоставить с evidence-v1 владелец трека данных; синтетические тесты не подтверждают реальное подключение.

## Verification on Mac M1 (2026-11-26)

- go test -v ./...: six tests passed (deltas including zero/missing, report parser and sampling, historical exact windows, counter check, agent definition, typed live query with a synthetic MCP response).
- go vet ./...: passed. Go binary built and offline Airlock manifest generated.
- Official air build initiated; result pending due to toolchain download / inaccessible job receipt. Local Airlock http://localhost:42080 unavailable. No deployment marker, no live Yandex token or counter, no claim of end-to-end acceptance.
