# Видео-сториборд AIfence · 3:00

Формат: один непрерывный проход в браузере, ширина 1440 px. Кадры — реальные screenshots из архива записи владельца и текущей репетиции на локальном `42080`. Последовательность показа: вход → Metrika ДЕМО → Market Watch и Baserow → карточка Product Manager → delivery → Redmine → Dion.

| Время | Что на экране | Реплика voice-over | Screenshot |
|---|---|---|---|
| 00:00–00:12 | Вход в Airlock и каталог приложений | «Открываем рабочее пространство владельца и выбираем источник аналитики.» | [08-login.png](video-one-take-27-11/08-login.png) |
| 00:12–00:35 | Metrika Analyst ДЕМО: Kaspersky Small Office Security, 100 → 75, −25% и предупреждение о синтетическом replay | «В демо-срезе видно падение индекса заказов: сто против семидесяти пяти, минус двадцать пять процентов.» | [01-metrika-demo.png](video-one-take-27-11/01-metrika-demo.png) |
| 00:35–00:50 | Wordstat с поисковой динамикой | «Спрос даёт контекст для сигнала, но сам по себе ещё не доказывает причину.» | [02-wordstat.png](scenes-27-11/02-wordstat.png) |
| 00:50–01:05 | Google Trends с временным графиком | «Второй источник помогает сверить изменение интереса по времени.» | [03-google-trends.png](scenes-27-11/03-google-trends.png) |
| 01:05–01:20 | Market Watch: 87 633 предложения, 6 магазинов, цены и время наблюдения | «Проверяем рынок по конкретным предложениям, продавцам, условиям и времени наблюдения.» | [06-market-watch.png](video-one-take-27-11/06-market-watch.png) |
| 01:20–01:30 | Baserow: Market Watch Database в workspace Airlock | «Исходный каталог остаётся доступен в рабочей таблице.» | [07-baserow.png](video-one-take-27-11/07-baserow.png) |
| 01:25–01:40 | Web Scraper с извлечённой страницей и значениями | «Недостающие значения можно проверить по исходной странице и сохранить её как доказательство.» | [05-web-scraper.png](scenes-27-11/05-web-scraper.png) |
| 01:40–02:10 | Product Manager: график target/control, named cause и evidence | «Карточка связывает сигнал с проверяемой причиной: видны цель, контроль и точки доказательств.» | [06-product-manager-card.png](scenes-27-11/06-product-manager-card.png) |
| 02:10–02:28 | Та же карточка: before/after и decision | «До и после превращают наблюдение в решение: команда видит, что именно менять и как проверить результат.» | [06-product-manager-card.png](scenes-27-11/06-product-manager-card.png) |
| 02:28–02:45 | Redmine #8 с русским readable template, signal ID и before/after | «Задача содержит флаг помощника, ID сигнала, цифры до и после, evidence и ограничения демо.» | [04-redmine-issue.png](video-one-take-27-11/04-redmine-issue.png) |
| 02:45–03:00 | Delivery/readback и Dion | «На текущей локальной базе delivery показывает отправленную дневную сводку старого watchdog-сигнала. Новую сводку по signal ID нужно записывать отдельным подтверждённым кадром.» | [05-dion-message.png](video-one-take-27-11/05-dion-message.png) |

## Статус кадров

- `01-metrika-analyst.png` и `04-market-watch.png` — реальные кадры локального Airlock владельца.
- `02-wordstat.png`, `03-google-trends.png`, `05-web-scraper.png`, `06-product-manager-card.png`, `07-delivery.png`, `08-redmine-issue.png` — реальные архивные кадры владельца, сохранённые до текущего read-only прогона.
- Отдельного кадра с сообщением или receipt Dion в доступном архиве нет; `07-delivery.png` используется как последний доступный кадр delivery/readback и явно не выдаётся за подтверждённый Dion receipt.
- Текущая репетиция на `42080` показала, что Metrika chat prompt уходит в длинный `run_js` цикл, `/delivery` после одного клика сохраняет только старую #7, Redmine #8 открывается с новым шаблоном и signal ID, а Dion показывает `2026-11-27 · отправлено` для старого сигнала. Recovery для этих различий записан в [one-take script](video-one-take-27-11.md).
- Стенд `42082` не изменялся.
