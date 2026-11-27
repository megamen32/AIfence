# Видео-сториборд AIfence · 3:00

Формат: headless Chromium/Playwright, ширина 1440 px. Кадры — реальные screenshots из архива записи владельца от 27.11.2026. Последовательность показа: данные → аналитика → цены конкурентов → карточка Product Manager с причиной → решение → Redmine → Dion.

| Время | Что на экране | Реплика voice-over | Screenshot |
|---|---|---|---|
| 00:00–00:15 | Карта источников: Metrika Analyst и сохранённые чтения | «Сторож начинает с данных и показывает источник каждой цифры.» | [01-metrika-analyst.png](scenes-27-11/01-metrika-analyst.png) |
| 00:15–00:35 | Metrika Analyst: Kaspersky Small Office Security, 100 заказов против 150, −33,3 %, SVG-график | «В аналитике видно падение: сто заказов против ста пятидесяти — минус тридцать три целых три.» | [01-metrika-analyst.png](scenes-27-11/01-metrika-analyst.png) |
| 00:35–00:50 | Wordstat с поисковой динамикой | «Спрос даёт контекст для сигнала, но сам по себе ещё не доказывает причину.» | [02-wordstat.png](scenes-27-11/02-wordstat.png) |
| 00:50–01:05 | Google Trends с временным графиком | «Второй источник помогает сверить изменение интереса по времени.» | [03-google-trends.png](scenes-27-11/03-google-trends.png) |
| 01:05–01:25 | Market Watch: SKU, продавцы, цены и время наблюдения | «Теперь сравниваем цены конкурентов по конкретным предложениям и моменту наблюдения.» | [04-market-watch.png](scenes-27-11/04-market-watch.png) |
| 01:25–01:40 | Web Scraper с извлечённой страницей и значениями | «Недостающие значения можно проверить по исходной странице и сохранить её как доказательство.» | [05-web-scraper.png](scenes-27-11/05-web-scraper.png) |
| 01:40–02:10 | Product Manager: график target/control, named cause и evidence | «Карточка связывает сигнал с проверяемой причиной: видны цель, контроль и точки доказательств.» | [06-product-manager-card.png](scenes-27-11/06-product-manager-card.png) |
| 02:10–02:28 | Та же карточка: before/after и decision | «До и после превращают наблюдение в решение: команда видит, что именно менять и как проверить результат.» | [06-product-manager-card.png](scenes-27-11/06-product-manager-card.png) |
| 02:28–02:45 | Redmine task readback с причиной, числами и следующим шагом | «Подтверждённое решение собирается в задачу Redmine с причиной, цифрами и ссылками на источники.» | [08-redmine-issue.png](scenes-27-11/08-redmine-issue.png) |
| 02:45–03:00 | Delivery/readback карточка передачи | «Дальше задача должна уйти в Dion. В доступном текущем владельце receipt Dion не подтверждён, поэтому этот кадр показывает честное состояние доставки.» | [07-delivery.png](scenes-27-11/07-delivery.png) |

## Статус кадров

- `01-metrika-analyst.png` и `04-market-watch.png` — реальные кадры локального Airlock владельца.
- `02-wordstat.png`, `03-google-trends.png`, `05-web-scraper.png`, `06-product-manager-card.png`, `07-delivery.png`, `08-redmine-issue.png` — реальные архивные кадры владельца, сохранённые до текущего read-only прогона.
- Отдельного кадра с сообщением или receipt Dion в доступном архиве нет; `07-delivery.png` используется как последний доступный кадр delivery/readback и явно не выдаётся за подтверждённый Dion receipt.
- На текущем владельце `42080` Wordstat и Google Trends возвращают `agent not found`, Web Scraper и Redmine — `access denied`, а Product Manager не имеет verified signal. Стенд `42082` не изменялся.
