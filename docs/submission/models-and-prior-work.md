# Модели, API, лицензии и предыдущие наработки

Сверка выполнена 27 ноября 2026 года по `apps/*/go.mod`,
`apps/*/package.json`, npm lock-файлам, Dockerfile и сгенерированным
`THIRD_PARTY_NOTICES.generated.md`. Таблица описывает состав продукта; закрытые
Airlock-исходники и ключи в репозиторий не входят.

## Модель и платформенная среда

| Компонент | Где используется | Лицензия и статус для подачи |
| --- | --- | --- |
| **Terra fast** | Заявленная продуктовая модель исполнения Product Manager в конкурсной версии; actual execution ID и route read-back приведены ниже | В слотах Airlock виден только route alias. Точный checkpoint и лицензия пока не установлены, поэтому Terra не объявляется допущенной open-weight моделью до чтения этой пары из конфигурации. |
| Airlock Enterprise v0.8.15 | Аутентификация, чат, слоты моделей, storage, jobs, native app bindings и каталог файлов | Проприетарная Enterprise-лицензия и условия владельца платформы; это предыдущая наработка и закрытая зависимость, её исходники не публикуются. Версия зафиксирована в [`platform/airlock.lock.json`](../../platform/airlock.lock.json). |
| AIfence | Три приложения, контракты, workflow, синтетические fixtures и документация | Собственный код команды/владельца, заявляемый как собственная предыдущая наработка. В корне репозитория нет `LICENSE`; публичную лицензию или условия передачи конкурсной версии должен выбрать владелец. |

Старые QA-файлы в `docs/submission/model-acceptance/` содержат отдельные
исторические проверки Gemma/Ollama. Они не являются заявлением о текущей модели
продукта и не входят в список моделей конкурсного продукта. Скрипт
`scripts/stand/configure.py` с Gemma — отдельная legacy-настройка стенда, не
доказательство состава конкурсного runtime.

## Фактический read-back слота демо-стенда

27 ноября 2026 года конфигурация локального Mac Airlock была прочитана из
таблиц `system_settings`, `providers` и `provider_models` в read-only режиме:

| Поле | Значение |
| --- | --- |
| Effective execution provider | `omniroute` (`openai-compatible`) |
| Effective execution model ID | `combo/airlock-personal-exec-terra61-safe` |
| Catalog display name | `Terra 6.1, MiniMax failover, personal chat` |
| Model catalog capabilities | tool call: `true`; reasoning: `false`; context: 128 000; output: 8 192 |
| Effective build model ID | `combo/airlock-personal-build-sol62-safe` |

Это actual route read-back, а не значение из исторического QA-файла. В имени
выбранного safe-маршрута явно указан MiniMax failover; поэтому в заявке Terra
fast следует называть primary execution model, а fallback — отдельным свойством
маршрута. Чтобы выполнить правило open weights, нужно отдельно подтвердить
checkpoint Terra и checkpoint fallback с их лицензиями. В этой конфигурации эти
артефакты не раскрыты и не должны быть придуманы.

## Проверенные open-weight лицензии

| Модель или семейство | Что проверено | Лицензия и ссылка |
| --- | --- | --- |
| **Qwen3** | Официальный репозиторий Qwen3 прямо указывает Apache 2.0 для open-weight моделей. Карточки Qwen3-8B и Qwen3-32B также содержат `apache-2.0`. | Apache License 2.0. [Официальный репозиторий Qwen3](https://github.com/QwenLM/Qwen3) · [Qwen3-8B](https://huggingface.co/Qwen/Qwen3-8B) · [Qwen3-32B](https://huggingface.co/Qwen/Qwen3-32B) |
| **Gemma** | Исторические QA-файлы проверяли Gemma. Google распространяет веса по отдельным Gemma Terms of Use с обязательным notice и ограничениями использования. | Это открытые веса с custom terms, а не Apache 2.0. [Gemma Terms of Use](https://ai.google.dev/gemma/terms) |

Qwen3 является проверенным open-weight кандидатом для конкурсной конфигурации.
Он не подменяет фактический read-back Terra fast. В текущем слоте Airlock не
раскрыты имена и лицензии underlying checkpoint Terra и MiniMax fallback. До
их сверки в заявку можно включать только проверенную лицензионную информацию,
а не предположение о соответствии route alias.

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

Версии взяты из `go.mod` приложений. Notices Market Watch дополнены блоками
BSD-3-Clause для goquery и MIT для robotstxt. Остальные notices остаются
сгенерированными pinned SDK-скриптом.

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
