# QA сцен демо · 27 ноября 2026

Проверка выполнена в отдельном headless Chromium через Playwright на локальном Airlock `http://localhost:42080`. Размер viewport — 420×900 CSS px, device scale factor — 1. Отдельно проверены `color_scheme=light` и `color_scheme=dark`. Учётные данные в отчёт не попали.

## Снимки

- [Сцена 1 · Metrika Analyst](scenes-27-11/01-data-collection.png)
- [Сцена 2 · Market Watch · поиск Kaspersky Small Office Security](scenes-27-11/02-competitor-prices.png)
- [Сцена 3 · Product Manager](scenes-27-11/03-product-manager-card.png)
- [Сцена 4 · Redmine и Dion](scenes-27-11/04-delivery.png)

Снимки сделаны из живых страниц стенда до следующей выкладки исходников этой ветки. Сцены 3 и 4 сохранены как честные empty states, поскольку карточка и доставка в текущем владельце ещё не появились.

## Сцена 1 · сбор данных

| Проверка | Результат |
| --- | --- |
| Metrika Analyst | PASS. На экране видны Kaspersky Small Office Security, 100 заказов за 5–11 октября, 150 в предыдущем окне и −33,3 %. На экране есть SVG-график и карточка источника. |
| 420 px | PASS. `body.clientWidth = 420`, `body.scrollWidth = 420`, 3 SVG. |
| Светлая тема | PASS по живому DOM. |
| Тёмная тема | PASS по живому DOM и снимку. |
| Wordstat, Google Trends | Чтение объявлено в Product Manager, отдельного визуального маршрута у этого владельца нет. |
| Web Scraper | BLOCKED. Владелец стенда не имеет доступного приложения Web Scraper, прямой маршрут вернул access denied. Это доступ/интеграция, не дефект CSS. |

В живом снимке Metrika исходно показывала технические имена сохранённого набора в source card. В `apps/metrika-analyst/index.html` добавлена UI-нормализация этих подписей до «Яндекс Метрика · недельное чтение» и «Сохранённое наблюдение Метрики». Её результат требует следующей выкладки.

## Сцена 2 · цены конкурентов

| Проверка | Результат |
| --- | --- |
| Market Watch | PASS. Поиск `Kaspersky Small Office Security` вернул предложения с названиями, SKU, продавцами, ценами и временем наблюдения. |
| Числа | PASS. В видимых строках есть KL4135RCEFR за 2 512 ₽ в SoftMagazin, KL4135RCEFS за 3 588 ₽ в SoftMagazin, KL4141RCEFR за 4 718,70 ₽ в Softline, KL4141RCEFS за 6 739,20 ₽ в Softline. |
| График/визуализация | PASS для каталога. На overview есть SVG-бары предложений по магазинам и анимированные карточки. Сравнение цен открывается отдельным экраном. |
| 420 px и тёмная тема | PASS. `body.clientWidth = 420`, `body.scrollWidth = 420`, 34 SVG. Навигация прокручивается внутри своей строки. |

## Сцена 3 · карточка Product Manager

**BLOCKED на текущем владельце.** `/?mode=demo&tab=overview` открылся с последним снимком, но без тревог и без карточки гипотезы. Вкладки «Гипотеза», «Путь покупки» и «После релиза» сообщают, что гипотез пока нет. Поэтому named cause, before/after numbers и decision в живом браузере не подтверждены.

Это блокер данных и интеграции source-watch. Исправлять его стилями нельзя.

## Сцена 4 · Redmine и Dion

**BLOCKED на текущем владельце.** `/delivery` открылся и показывает честное состояние «Сигналов с проверенными цифрами пока нет», время следующей сверки и кнопки ручной проверки. Redmine issue ID и Dion receipt отсутствуют, потому что входного verified signal нет.

## Общий слой интерфейса

В этой ветке добавлен build-time слой `apps/shared/ui/design-system.css` с едиными токенами шрифта, ширины контента, отступов, радиусов, границ, тени и motion. Он подключён к CSS Product Manager, Market Watch, Wordstat и Web Scraper. Metrika и inline Web Scraper используют те же имена переменных. Runtime-проверка после native build ещё не выполнена.

## Команды и ограничения

- `python3 scripts/lint-placeholder-names.py apps/metrika-analyst apps/product-manager apps/market-watch apps/wordstat apps/web-scraper` — PASS, `No placeholder product names found.`
- `git diff --check` — PASS.
- `go test ./...` в Metrika Analyst — BLOCKED базовой несовместимостью patched agentsdk, включая `undefined: agentsdk.WithReadOnly`.
- `go test ./...` в Product Manager, Market Watch и Wordstat — BLOCKED отсутствующими сгенерированными `views/static/app.css` и SQLC-артефактами в checkout. В Product Manager также видны отсутствующие `db.New` и query-типы.
- `go test ./...` в Web Scraper — PASS.

Изменения UI не объявляются выкаченными на стенд до прохождения native build и live screenshot после выкладки.

## Набор кадров для видео, 1440 px

Для storyboard подготовлен отдельный набор из восьми кадров. Каждый файл имеет ширину 1440 px и сохранён в `scenes-27-11/`:

- [01 · Metrika Analyst](scenes-27-11/01-metrika-analyst.png)
- [02 · Wordstat](scenes-27-11/02-wordstat.png)
- [03 · Google Trends](scenes-27-11/03-google-trends.png)
- [04 · Market Watch](scenes-27-11/04-market-watch.png)
- [05 · Web Scraper](scenes-27-11/05-web-scraper.png)
- [06 · Product Manager card](scenes-27-11/06-product-manager-card.png)
- [07 · Delivery](scenes-27-11/07-delivery.png)
- [08 · Redmine issue](scenes-27-11/08-redmine-issue.png)

Кадры 02, 03, 05, 06, 07 и 08 взяты из исторического набора владельца `/Users/user/AIfence/.runtime/film-owner-20261127/`; они не являются новым подтверждением доступности соответствующих маршрутов на 42080.
