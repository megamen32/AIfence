# Модели, лицензии и предыдущие наработки

## Модели и среда

| Компонент | Использование | Лицензия и подтверждение |
| --- | --- | --- |
| Gemma 4 | В операторском руководстве зафиксирован профиль выполнения `gemma4:aifence-64k` на базе `gemma4:latest` и отдельный профиль сборки `gemma4:latest`. У измеренного образа указаны 8B, Q4_K_M и префикс дайджеста `c6eb396dbd59`. | Для весов Gemma 4 E4B исходная карточка Google указывает Apache-2.0. Точный upstream-артефакт установленной квантовки сверяется по манифесту перед сдачей. В тесте модельного маршрута из сети стенда обработано 60 614 входных токенов и восстановлены 3 из 3 контрольных меток. Полный KES-прогон через Airlock отслеживается отдельно. [Карточка Google](https://huggingface.co/google/gemma-4-E4B-it) · [QA маршрута](model-acceptance/QA-64K-20261126.md) · [Профиль стенда](../qa/stand.md). |
| Ollama | Среда выполнения Gemma 4 в зафиксированном модельном тесте. | Код Ollama — MIT. Точную версию рантайма для стенда указывает оператор при фиксации релиза. [LICENSE Ollama](https://github.com/ollama/ollama/blob/main/LICENSE). |
| Sol 6.2 и Terra 6.1 | Для разработки по сведениям владельца. | Перед заполнением формы подтвердить провайдера, точный ID моделей и условия использования. Название библиотеки `airlockrun/sol` учитывается отдельно. |
| Airlock Enterprise | Предыдущая наработка. Приватная платформа v0.8.15, в проекте закреплён релиз. | Лицензия и право конкурсного показа определяются условиями Enterprise. Для конкурсной версии остаётся пин, исходный код и образы идут через разрешённый операторский маршрут. [Пин платформы](../../platform/airlock.lock.json). |
| AIfence | Три приложения, контракты, синтетические данные и документация проекта. | Корневой файл LICENSE пока отсутствует. Авторство, состав работ до конкурса и лицензию исходников подтверждает владелец перед передачей. Упоминание traz в плане не подкреплено фактической зависимостью зафиксированных приложений. |

## Основные библиотеки

| Библиотека | Версия | Лицензия |
| --- | --- | --- |
| `airlockrun/agentsdk`, `airlockrun/goai`, `airlockrun/sol` (транзитивная) | v0.11.0, v0.3.4, v0.1.23 | Apache-2.0, Oleg Karpov |
| `a-h/templ`, `lib/pq`, `temoto/robotstxt` | v0.3.1020, v1.12.3, v1.1.2 | MIT |
| `PuerkitoBio/goquery`, `gofrs/flock`, `google/uuid` | v1.14.0, v0.13.1, v1.6.0 | BSD-3-Clause |
| `modelcontextprotocol/go-sdk` (транзитивная) | v1.8.0 | Apache-2.0 и сохранившиеся MIT-вклады, документация вне спецификаций CC BY 4.0 |
| Tailwind CSS, daisyUI, htmx, Lucide | Tailwind v4.1.0 и daisyUI v5.5.23 по Dockerfile. htmx и Lucide предоставляет SDK. | Tailwind CSS и daisyUI — MIT, htmx — 0BSD, Lucide — ISC и MIT для иконок из Feather |
| `yandex-metrica-mcp`, `supergateway` (шлюз Metrika Analyst) | 1.0.1, 4.3.0 | MIT по метаданным [BoxLab](https://registry.npmjs.org/yandex-metrica-mcp/1.0.1) и [Supergateway](https://registry.npmjs.org/supergateway/4.3.0) |
| Playwright (проверка пути покупки) | Фиксированного Python-пина в приложении нет | Apache-2.0. Для включения браузерного бинарника сохранить уведомления соответствующей сборки. [LICENSE](https://github.com/microsoft/playwright-python/blob/main/LICENSE). |

Версии Go сверяются по `go.mod` каждого приложения, лицензии — по LICENSE именно этих версий. Для поставляемых бинарников сохранить оригинальные LICENSE, copyright и NOTICE по итоговой сборке. Готовые уведомления [Market Watch](../../apps/market-watch/THIRD_PARTY_NOTICES.generated.md) и [Product Manager](../../apps/product-manager/THIRD_PARTY_NOTICES.generated.md) сверить с финальным артефактом. Для поставки шлюза использовать [npm lock](../../apps/metrika-analyst/package-lock.json); таблица покрывает основные компоненты.
