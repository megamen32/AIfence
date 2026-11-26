# Проверка пути KES

Из корня репозитория запустите утилиту Python с Playwright 1.60.0.
Нужен установленный Google Chrome для `--channel chrome` или Chromium,
установленный через `python3 -m playwright install chromium`, для запуска
без этого флага.

```sh
umask 077
run_dir=$(mktemp -d /tmp/aifence-path-check.XXXXXXXX)
GOMAXPROCS=2 GOFLAGS=-p=1 python3 apps/path-check/check_purchase_path.py \
  --channel chrome > "$run_dir/evidence.json" 2> "$run_dir/artifacts.txt"
jq -Rs '{document: .}' "$run_dir/evidence.json" > "$run_dir/pm-input.json"
```

JSON из `pm-input.json` — аргументы существующего инструмента Product
Manager `import_evidence`. Также можно загрузить сам `evidence.json`
через форму PM `/import`. Для вызова из Python функция
`check_purchase_path(channel=None)` возвращает пару
`(batch, каталог скриншотов)`. Два профиля выполняются последовательно
в одном браузере с отдельными анонимными контекстами.

Stdout содержит один batch `aifence.v1` с `source=browser` и
`observation.kind=path_check`. `segment` различает `desktop` и `mobile`
(эмуляция Pixel 7). Код завершения 0 означает, что batch сформирован.
Проверяйте `status`, `value` и `details` для каждого профиля.

| `value` | Наблюдение |
| --- | --- |
| `ok` | Строка KES с продлением видна в корзине и браузер отправил ecommerce `add` в Метрику. |
| `path_broken` | POST добавления в корзину получил HTTP 5xx. |
| `metrika_add_not_observed` | Строка KES видна в корзине, Метрика отвечала на watch-запросы, за окно наблюдения ecommerce `add` не обнаружен. |
| `null` | Итог неизвестен. `details` сохраняет отдельно шаг, исход корзины и наблюдение Метрики. |

Утилита распознаёт `__ym.ecommerce[].add.products` в параметре
`site-info` запроса к счётчику. Окно наблюдения закрывается через 3 секунды
после ответа корзины. Наблюдение запроса подтверждает отправку из браузера.
Обработка события в отчётах Метрики и прошлые периоды этим тестом
не проверяются. Сценарий заканчивается на корзине без входа и оформления.

В SDK-образе Debian обёртка передаёт `--chromium-path /usr/bin/chromium`
и `--artifact-dir "$run_dir"`. Передавайте уникальный приватный каталог.
Снимки `desktop.png` и `mobile.png` сохраняются прямо в него. Без
`--artifact-dir` утилита создаёт отдельный каталог в `/tmp`. Путь печатается
в stderr, а `details.screenshot` содержит только имя сохранённого файла.
`--chromium-path` и `--channel chrome` взаимоисключающие. JSON для PM не
содержит снимков, логов трафика или сырых параметров запросов.

Smoke 26.11.2026. Команда выше с `--channel chrome` вернула `ok` на
desktop и Pixel 7. В обоих профилях строка KES появилась в корзине,
а в сетевом запросе Метрики найден ecommerce `add`. Batch прошёл
проверку `contracts/evidence-v1.schema.json`. В первом desktop-прогоне
строка корзины не появилась за 5 секунд и результат остался `missing`
при наблюдаемой отправке события. Повтор с ожиданием строки до 10 секунд
подтвердил корзину.
