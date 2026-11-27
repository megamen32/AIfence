# Модели, API, лицензии и предыдущие наработки

Сверка выполнена 27 ноября 2026 года по `apps/*/go.mod`,
`apps/*/package.json`, npm lock-файлам, Dockerfile и сгенерированным
`THIRD_PARTY_NOTICES.generated.md`. Таблица описывает состав продукта; закрытые
Airlock-исходники и ключи в репозиторий не входят.

## Модель и платформенная среда

| Компонент | Где используется | Лицензия и статус для подачи |
| --- | --- | --- |
| **Terra fast** | Единственная модель исполнения Product Manager в конкурсной версии; вызывается через настроенный слот Airlock | Точный checkpoint/model ID, карточка весов и текст лицензии должны быть зафиксированы в финальном релизе. Допуск конкурса: open weights, локальный запуск и лицензия, разрешающая использование; неподтверждённый артефакт нельзя объявлять лицензированным. |
| Airlock Enterprise v0.8.15 | Аутентификация, чат, слоты моделей, storage, jobs, native app bindings и каталог файлов | Проприетарная Enterprise-лицензия и условия владельца платформы; это предыдущая наработка и закрытая зависимость, её исходники не публикуются. Версия зафиксирована в [`platform/airlock.lock.json`](../../platform/airlock.lock.json). |
| AIfence | Три приложения, контракты, workflow, синтетические fixtures и документация | Собственный код команды/владельца, заявляемый как собственная предыдущая наработка. В корне репозитория нет `LICENSE`; публичную лицензию или условия передачи конкурсной версии должен выбрать владелец. |

Старые QA-файлы в `docs/submission/model-acceptance/` содержат отдельные
исторические проверки Gemma/Ollama. Они не являются заявлением о текущей модели
продукта: в подачу выносится только Terra fast после финального read-back.

## API и внешние сервисы

| API или сервис | Назначение | Лицензия/условия |
| --- | --- | --- |
| Airlock native SDK и app bindings | Запуск приложений, storage, jobs, каталог файлов и вызовы источников от имени пользователя | `agentsdk`/Airlock Enterprise: Apache-2.0 для опубликованного SDK; приватная платформа и её условия описаны отдельно выше. |
| Yandex Metrika Reports API | Авторизованные отчёты, сравнение периодов и проверка счётчика | Условия API Yandex; токен не хранится в Git. Локальный мост `yandex-metrica-mcp` — MIT. |
| Yandex Wordstat | Динамика спроса по ключам через MCP-мост | Условия API Yandex; `mcp-yandex-wordstat` — MIT. |
| Google Trends через HasData MCP | Дополнительная динамика спроса; bridge использует Streamable HTTP | Условия HasData и Google Trends; удалённый сервис не является поставляемой библиотекой. Локальный `mcp-remote` — MIT. |
| Публичные страницы и структурированные данные магазинов | Market Watch читает разрешённые публичные карточки, цены и условия, с датой и URL | Условия сайтов, robots и rate limits; обход ограничений и авторизация за пределами разрешённых доступов не используются. |
| Redmine MCP 2.23.0 | Идемпотентное создание задачи с флагом «сгенерирована ИИ-помощником» и ID сигнала | `redmine-mcp-server` — MIT; Redmine и его данные подчиняются условиям отдельного стенда. |
| UserIO/Dion MCP | Подготовка и отправка утренней сводки в один разрешённый чат | Внешний сервис и пользовательская учётка; условия сервиса, токен и идентификатор чата не входят в Git. |

## Go-библиотеки

| Модуль | Версия | Лицензия |
| --- | --- | --- |
| `github.com/airlockrun/agentsdk` | v0.11.0 | Apache-2.0, copyright Oleg Karpov; уведомление в `THIRD_PARTY_NOTICES.generated.md` |
| `github.com/airlockrun/goai` | v0.3.4 | Apache-2.0; notice также фиксирует происхождение Go-порта Vercel AI SDK |
| `github.com/airlockrun/sol` | v0.1.23, транзитивный/build profile | Apache-2.0 |
| `github.com/a-h/templ` | v0.3.1020 | MIT |
| `github.com/PuerkitoBio/goquery` | v1.14.0 | BSD-3-Clause |
| `github.com/temoto/robotstxt` | v1.1.2 | MIT |
| `github.com/gofrs/flock` | v0.13.1 | BSD-3-Clause |
| `github.com/lib/pq` | v1.12.3 | MIT |
| `github.com/google/uuid` | v1.6.0 | BSD-3-Clause |
| `github.com/google/jsonschema-go` | v0.4.3 | MIT |
| `github.com/modelcontextprotocol/go-sdk` | v1.8.0, транзитивный | Apache-2.0 и сохранившиеся MIT-вклады; документация вне спецификаций CC BY-4.0 |

Версии взяты из `go.mod` приложений. `PuerkitoBio/goquery` и
`temoto/robotstxt` реально импортируются Market Watch, но в текущем
сгенерированном notice Market Watch их блоков нет; перед публикацией нужно
перегенерировать уведомления и проверить итоговый бинарник.

## Node-мосты и UI

| Пакет | Версия | Лицензия |
| --- | --- | --- |
| `mcp-remote` | 0.1.43 | MIT; pinned upstream описан в `apps/google-trends/UPSTREAM.md` |
| `yandex-metrica-mcp` | 1.0.1 | MIT по `apps/metrika-analyst/package-lock.json` |
| `mcp-yandex-wordstat` | 2.5.0 | MIT по `apps/wordstat/mcp/package-lock.json` |
| `supergateway` | 4.3.0 | MIT |
| `@modelcontextprotocol/*` | версии из npm lock | Смешанный Apache-2.0/MIT состав; брать лицензии из lock-файла конкретного моста |
| Tailwind CSS / daisyUI | 4.1.0 / 5.5.23 | MIT |
| htmx / Lucide | 4.0.0 / 1.34.0 | htmx — 0BSD; Lucide — ISC, отдельные Feather-иконки — MIT |
| Playwright | 1.55.0 в `apps/product-manager/setup.sh` | Apache-2.0; Chromium и уведомления бинарной сборки проверяются отдельно |

Сгенерированные уведомления приложений находятся в:

- [`apps/market-watch/THIRD_PARTY_NOTICES.generated.md`](../../apps/market-watch/THIRD_PARTY_NOTICES.generated.md);
- [`apps/product-manager/THIRD_PARTY_NOTICES.generated.md`](../../apps/product-manager/THIRD_PARTY_NOTICES.generated.md);
- [`apps/google-trends/THIRD_PARTY_NOTICES.generated.md`](../../apps/google-trends/THIRD_PARTY_NOTICES.generated.md);
- [`apps/wordstat/THIRD_PARTY_NOTICES.generated.md`](../../apps/wordstat/THIRD_PARTY_NOTICES.generated.md).

Финальный пакет должен сохранить эти уведомления, LICENSE/copyright всех
поставляемых компонентов и отдельно подтвердить лицензию именно того
checkpoint Terra fast, который попадёт на стенд.
