# One-take AIfence · 27.11.2026

Цель записи — показать путь от демо-сравнения в Metrika Analyst до карточки Product Manager, сверки `/delivery`, Redmine issue и сообщения Dion. Целевой хронометраж — 01:55, запас до лимита 03:00 оставлен для одной recovery-паузы.

Стенд — локальный Airlock `http://localhost:42080`, приложения из `ca4beffc458b11abcd001d1bb29a01ecc27bcb87`, часовой пояс UTC+5. `42082` в записи не используется.

| Timecode | Шаг и URL | Что сделать | Что должно быть видно | Ожидание | Recovery |
|---|---|---|---|---:|---|
| 00:00–00:20 | Metrika ДЕМО · `http://localhost:42080/chat?app=6e985f49-ae84-4f6d-b345-edb4ea0d95cf` | Открыть Product Manager из списка приложений, открыть `metrika-analyst`, нажать `Что изменилось?`, затем `Отправить сообщение` | Заголовок `Сравнение двух последних полных недель`, демо-источник, две недели, изменение `100 → 75`, `−25%`, ограничения и источник | 20 с | Если через 15 с остаётся `Выполнено … · run_js`, остановить ответ, перейти к следующему шагу и использовать зафиксированный демо-кадр. Это состояние наблюдалось на текущем владельце во время репетиции. |
| 00:20–00:45 | Карточка Product Manager · `http://product-manager.localhost:42080/?mode=demo&tab=hypothesis` | Нажать `Гипотеза`, выбрать карточку Kaspersky Small Office Security и открыть её | Название товара, `100 → 75`, `−25%`, источник `ДЕМО`, evidence и следующий шаг проверки | 5 с | Если список карточек пуст, открыть `http://product-manager.localhost:42080/?mode=demo&tab=overview`, нажать `Гипотеза` ещё раз, затем использовать карточку из кадра 02. |
| 00:45–01:05 | Доставка · `http://product-manager.localhost:42080/delivery` | Нажать `Сверить сейчас` один раз | Список Redmine, новая задача с читаемым заголовком, флаг `сгенерирована ИИ-помощником` и ссылка на issue | 15 с | В текущем runtime кнопка сформировала `/delivery/delivery/sync` и вернула `not found`. Зафиксировать этот UI defect, не нажимать повторно, открыть Redmine issue #8 по следующему шагу и проговорить, что сверка не подтянула сохранённый signal record. |
| 01:05–01:30 | Redmine · `http://redmine.localhost:42080/issues/8` | Открыть ссылку issue | `Сгенерирована ИИ-помощником: Да`, `ID сигнала: 7e6f0128-603b-87cf-9bc9-14680c36eb22`, продукт `kaspersky_small_office_security`, `100 → 75`, `−25%`, evidence и пометка `ДЕМО` в ограничениях | 3 с | Если #8 не открывается, проверить, что используется локальный Redmine host `redmine.localhost:42080`, и повторить открытие один раз. Если issue отсутствует, запись останавливается с честным сообщением об отсутствии durable receipt. |
| 01:30–01:55 | Dion · `http://product-manager.localhost:42080/delivery` | Нажать `Отправить сейчас` один раз | В секции `Сводка в Dion` появляется день и статус `отправлено`. В voice-over назвать durable receipt из job/MCP результата, если он доступен | 15 с | Если статус отправлен, но текст содержит старый watchdog signal, назвать это stale summary и закончить take. Если страница показывает ошибку или нет durable receipt, не повторять отправку. |

## Кадры

Кадр сохраняется сразу после завершения каждого шага. Имена файлов совпадают с timecode-порядком записи.

1. [Metrika ДЕМО](video-one-take-27-11/01-metrika-demo.png)
2. [Карточка Product Manager](video-one-take-27-11/02-product-manager-card.png)
3. [Доставка и Сверить сейчас](video-one-take-27-11/03-delivery-sync.png)
4. [Redmine issue #8](video-one-take-27-11/04-redmine-issue.png)
5. [Сообщение Dion](video-one-take-27-11/05-dion-message.png)

PNG-файлы взяты из проверенных кадров `docs/qa/scenes-27-11/` и скопированы в папку записи с именами шагов. Кадр Redmine показывает live issue #8 с новым читаемым шаблоном. Кадры доставки и Dion отражают состояние локального владельца на момент репетиции, поэтому перед финальной записью нужно сверить текст и статус на экране с текущим прогоном.

## Проверка хронометража

Основная дорожка занимает 115 секунд. Даже с одной recovery-паузой в 30 секунд общий take остаётся в пределах 02:25. Повторные клики по `Сверить сейчас` и `Отправить сейчас` в запись не входят.

## Репетиция независимых экранов · 28.11.2026

Эти проверки выполнены отдельно от PM deploy и не меняют Redmine или Dion.

| Экран | URL | Результат | Что изменилось в тексте сценария | Кадр |
|---|---|---|---|---|
| Login / app directory | `http://localhost:42080/agents` | Работает. Owner profile открыл каталог приложений, Product Manager и Metrika Analyst доступны | В начале записи оставлен короткий login кадр, после него сразу открывается Metrika | [08-login.png](video-one-take-27-11/08-login.png) |
| Metrika Analyst | `http://metrika-analyst.localhost:42080/watchdog` и `http://localhost:42080/chat?app=6e985f49-ae84-4f6d-b345-edb4ea0d95cf` | Страница watchdog открывается, но демо-запрос в чате ушёл в длинный `run_js` цикл после 20 шагов и был остановлен | Recovery теперь явно останавливает ответ через 15 секунд и продолжает с зафиксированным демо-кадром | [01-metrika-demo.png](video-one-take-27-11/01-metrika-demo.png) |
| Market Watch | `http://market-watch.localhost:42080/` | Работает. Видны 87 633 предложения, 6 магазинов, 67 568 лицензий и список цен | Сценарий оставляет Market Watch как самостоятельный preflight экран перед one-take | [06-market-watch.png](video-one-take-27-11/06-market-watch.png) |
| Baserow | `http://baserow.localhost:42080/all-workspaces` | Работает. Видно workspace Airlock и Market Watch Database | Добавлен отдельный кадр проверки каталога в Baserow | [07-baserow.png](video-one-take-27-11/07-baserow.png) |
| Redmine #8 | `http://redmine.localhost:42080/issues/8` | Работает. Видны AI flag, signal ID `7e6f0128-603b-87cf-9bc9-14680c36eb22`, `100 → 75`, `−25%` и демо-ограничения | Текст шага использует новый читаемый русский шаблон issue | [04-redmine-issue.png](video-one-take-27-11/04-redmine-issue.png) |
| Dion | `http://product-manager.localhost:42080/delivery` | Страница показывает `2026-11-27 · отправлено`, но сводка относится к старому watchdog signal | Recovery требует durable receipt и завершает take при отсутствии сообщения для нового signal ID | [05-dion-message.png](video-one-take-27-11/05-dion-message.png) |

Кадры в этой папке сохранены из проверенных scene references, чтобы запись имела один файл на каждый шаг. Текущие URL и видимые состояния выше взяты из браузерной репетиции.

## Живой прогон · 28.11.2026 · 01:00 UTC+5

Использован отдельный lease вкладки bezbrowser на профиле Mac M1 и ширина 1440 CSS px. Вход и первые сцены прошли на `42080`.

| Шаг | Фактическое состояние | Кадр |
|---|---|---|
| Вход и каталог | Каталог Airlock открылся. Product Manager и metrika-analyst показаны как работающие | [08-login-live-1440.png](video-one-take-27-11/08-login-live-1440.png) |
| Metrika Analyst | Пример «Что изменилось?» открылся, сообщение отправлено один раз. Через 10 секунд оставалось `Выполнено 7 · run_js`, полный ответ не появился | [01-metrika-live-1440.png](video-one-take-27-11/01-metrika-live-1440.png) |
| Product Manager | Карточка открылась. Видны Kaspersky Endpoint Security, Astra Linux Special Edition, график и evidence. Одновременно показаны `−33,9 %` и `−32,7 %`, блок цены задержки пуст, в задаче виден внутренний `PM-c0eeda9e06e2acf3` | [02-pm-live-1440.png](video-one-take-27-11/02-pm-live-1440.png) |
| Delivery | `/delivery` открылся. Формы уже имели абсолютные actions `/delivery/sync` и `/delivery/dion`. Кнопка «Сверить сейчас» нажата один раз. После клика вкладка потеряла соединение с bezbrowser, read-back результата не получен | [03-delivery-sync.png](video-one-take-27-11/03-delivery-sync.png) |
| Redmine | В этой попытке не открывался после sync, чтобы не повторять действие при неизвестном результате | [04-redmine-issue.png](video-one-take-27-11/04-redmine-issue.png) |
| Dion | Кнопка не нажималась после потери соединения. Новая отправка не подтверждена | [05-dion-message.png](video-one-take-27-11/05-dion-message.png) |

### Визуальные дефекты, найденные в живом кадре

1. В карточке две разные величины одного сигнала, `−33,9 %` в evidence и `−32,7 %` в соседней строке. Исправлено в коде дедупликацией одинаковых пар показателя и источника при построении pins. Кадр [02-pm-live-1440.png](video-one-take-27-11/02-pm-live-1440.png).
2. Блок «Цена задержки» не показывает значение и оставляет пустое пространство до раскрытия допущений. Добавлена короткая подсказка с требуемыми входами для расчёта ₽/неделю. Кадр [02-pm-live-1440.png](video-one-take-27-11/02-pm-live-1440.png).
3. Заголовок «Задача в бэклоге · Redmine» сопровождался локальным ID `PM-c0eeda9e06e2acf3`. Добавлена маркировка системы задачи, чтобы локальный бэклог не выдавался за Redmine. Кадр [02-pm-live-1440.png](video-one-take-27-11/02-pm-live-1440.png).
4. Форма доставки в исходнике использовала относительные paths `./delivery/sync` и `./delivery/dion`. Исправлено на `/delivery/sync` и `/delivery/dion`. Текущий live runtime уже показывал абсолютные actions до нажатия. Кадр [03-delivery-sync.png](video-one-take-27-11/03-delivery-sync.png).
5. В текущем delivery read-back оставались placeholder names `demo_target` и `demo_control`, старый сигнал `watchdog/39fec477…` и issue `#7`. Это относится к сохранённому старому состоянию, новая задача по текущему сигналу не подтверждена.

### Проверка исходника

- PM build passed at candidate `2ffb687` with `AIFENCE_PERSONAL_CACHE=/Users/user/AIfence-worktrees/personal-integration/.runtime/personal-release GOPROXY=off /opt/homebrew/bin/python3 scripts/personal-build-apps.py --commit HEAD --app product-manager --output .runtime/sol-e2e/build-pm-2ffb687`. SQLC, templ, Tailwind, Go tests and `go build` completed.
- The code changes are rebased onto the then-current `origin/main` and are in the two commits immediately ahead of it. No deploy was performed.
- The final scenes are unverified because the browser connection went offline after the single sync action. Redmine issue creation/read-back, duplicate-free second sync, Dion send/read-back and the current PM source after deployment remain open.
