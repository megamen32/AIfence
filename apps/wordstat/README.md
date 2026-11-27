# Wordstat for AIfence

Самостоятельное приложение на Airlock SDK 0.11.0 и локальное MCP-подключение к готовому Wordstat-серверу. Приложение получает поисковый спрос, помесячную динамику и географию. Общую продуктовую гипотезу и итог исследования собирает менеджер продукта.

## Состав

| Часть | Назначение |
| --- | --- |
| main.go, demand/, handlers/, views/ | Приложение Airlock с UI и тремя типизированными инструментами |
| scripts/wordstat-mcp | Постоянный stdio-процесс для локального MCP-клиента |
| mcp/remote.json | Готовый Supergateway: HTTPS Streamable HTTP → stdio |
| mcp/package-lock.json | Зафиксированные зависимости провайдера и моста |
| mcp/verify.mjs | Проверка того же stdio-процесса официальным MCP-клиентом |
| mcp/Dockerfile | Образ готового провайдера для отдельного размещения |
| validation/ | Результаты сборки и реального запроса без секретов |

На Mac запускается мост к существующему серверу. Yandex Cloud API key и folder ID остаются на сервере. Для моста используется service bearer в игнорируемой папке `.runtime`.

## Ограничения данных

По [официальной документации Wordstat](https://aistudio.yandex.ru/ru/docs/search-api/concepts/wordstat), проверенной 27.11.2026, статистика через API после **07.10.2026** недоступна, хотя методы продолжают отвечать. Последний полный месяц для сравнения в приложении: **2026-09**.

Точное фактическое окно `top_requests` и `regions` API не сообщает. Его нельзя называть последними 30 днями относительно даты запроса. Проверка initialize и tools/list подтвердила: исходный MCP 2.5.0 по-прежнему использует описание «последние 30 дней». Это описание не уточняет окно после остановки обновления. Для выводов учитывайте ограничение официальной документации.

Локальный stdio-мост передаёт исходные инструменты готового провайдера. Проверка периода и добавление названий регионов реализованы в доменном сервисе приложения Airlock.

Частотность фразы не равна числу покупателей. Частотности пересекающихся фраз и вложенных территорий нельзя складывать. Отсутствующее значение не заменяется нулём; настоящий ноль сохраняется. Без регионального фильтра запрос относится ко всем регионам, а не автоматически к России.

## Приложение Airlock

| Инструмент | Результат |
| --- | --- |
| top_queries | Частотность и связанные формулировки с фактическими фильтрами |
| demand_dynamics | Помесячная динамика для явных from_month и to_month |
| regional_demand | Частотности, region_id, region_name, region_name_status и region_lookup_status |

UI и инструменты используют один demand.Service. Проверка пользователя выполняется до обращения к источнику. MCP зарегистрирован с MCPAuthToken и BindingShared; токен добавляется через штатный ресурс Airlock.

Публичный адрес задаётся в `mcp-endpoint.txt` и включается в бинарный файл при сборке. Для другого провайдера измените файл и пересоберите приложение. newAgent не читает runtime-файлы, переменные окружения или сеть.

Успешный ответ сохраняет исходный JSON в data, включая строковые числовые значения. rows предназначены для представления. Ошибка авторизации, недоступный период и сбой источника имеют отдельные состояния. Период после сентября 2026 не подменяется более ранним месяцем.

### Названия регионов

После успешного regions сервис сам вызывает list_regions через тот же ресурс. Дерево id/label/children разбирается рекурсивно. Успешный справочник кешируется на время жизни сервиса; параллельные обращения разделяют один запрос. Обе операции используют общий предел 55 секунд. Неудачный справочник не кешируется.

Если имя отсутствует или один код связан с разными названиями, строка остаётся с кодом и статусом unknown либо ambiguous. Недоступность справочника сохраняет статистику и возвращает region_lookup_status=unavailable; частичное сопоставление возвращает partial. Модель завершает ответ с этими данными и не ищет названия через веб или отдельные MCP.

Формат справочника: [официальная REST-схема](https://aistudio.yandex.ru/en/docs/search-api/api-ref/Wordstat/getRegionsTree).

## Локальное подключение

Требуются Node.js 20+ и Python 3. На проверенном Mac M1 Node расположен в /opt/homebrew/bin/node. Секрет не передаётся в argv и не входит в конфигурацию клиента.

Из корня приложения:

```sh
npm --prefix mcp ci --omit=dev --ignore-scripts --no-audit --no-fund
scripts/configure-connection --url https://airlock.bezrabotnyi.com/wordstat-mcp/mcp < /path/to/protected-service-token
```

Токен также можно передать стандартным вводом из защищённого хранилища или SSH-процесса. Скрипт создаёт .runtime/service-token и .runtime/connection.json с правами 0600, каталог с правами 0700.

Запись MCP-клиента содержит только абсолютный путь:

```json
{
  "command": "/absolute/path/to/apps/wordstat/scripts/wordstat-mcp",
  "args": []
}
```

Launcher проверяет HTTPS-адрес и права файла, затем запускает Supergateway 4.3.0. Bearer попадает в конфигурацию через окружение дочернего процесса. stdout занят протоколом MCP; локальный сетевой порт не открывается. Регистрация в конкретном клиенте выполняется отдельно владельцем клиента.

Реальная проверка:

```sh
node mcp/verify.mjs
```

Официальный MCP JS SDK 1.32.2 подключается к тому же launcher, выполняет initialize, tools/list и один top_requests для фразы «касперский для бизнеса». Это запрос с обычным тарифом провайдера. Фильтры регионов и устройств не подставляются. Receipt сохраняется в .runtime/mac-mcp-receipt.json без секретов.

Исходные инструменты: top_requests, dynamics, regions, list_regions, raw_request. Для общего вопроса о спросе достаточно начать с частотности нужной формулировки. Динамика и география нужны, когда отвечают на конкретный вопрос.

## Сборка

Go 1.26.11 и SDK 0.11.0 зафиксированы в go.mod. На проверенном Mac Go расположен в /usr/local/go/bin/go.

```sh
go tool air toolchain install
go tool air build
go test -p=1 -count=1 -race ./demand
```

air build запускает генерацию шаблонов и CSS, тесты, сборку и формирование манифеста. .airlock, .runtime, node_modules и генерируемые файлы исключены из Git. Для обновления существующего приложения сначала получите последнюю ревизию штатным CLI без force и сохраните локальные изменения. Привязка и секретный ресурс не входят в перенос исходников.

## Готовые зависимости

- [askads/mcp-yandex-wordstat](https://github.com/askads/mcp-yandex-wordstat), 2.5.0, commit 5d7395ed352679ad639e872fd7fe232e2c1c61c9, MIT.
- [supercorp-ai/supergateway](https://github.com/supercorp-ai/supergateway), 4.3.0, commit 0afe993496636bb30dc1eb6810dc4d18f0f66f42, MIT.
- Node image зафиксирован digest в mcp/Dockerfile.

Dockerfile служит для отдельного размещения провайдера. Он требует runtime-секретов: Yandex key/folder ID в штатном secret storage и файл /run/secrets/wordstat-service-token для --apiKeyFile. Внешний доступ должен использовать HTTPS и bearer. При обычном подключении Mac образ не строится и второй провайдер не запускается.

Исходники не содержат ядро Airlock, закрытые материалы организаторов, данные покупателей и учётные данные.

## Verified Mac registration

On 2026-11-27 the owner registered one ShellMCP child named wordstat using the project launcher. The registered consumer returned totalCount as the string 842 for the unchanged phrase, with no region or device filter. See validation/mac-registration-2026-11-27.json and the full nonsecret MCP receipt. Runtime connection files are intentionally outside Git and must be provisioned again if this checkout is moved.
