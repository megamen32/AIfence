---
name: stand-accelerators
description: Ready commands for the frequent stand paths on 42080 (login and 1440/420 screenshots, one question to the PM chat and saving the answer, seeding DEMO data into the PM snapshot). Use after a deploy, for a visual check, or when a task needs a live answer from the stand. Deploy itself is in stand-deploy.
---

# Ускорители для стенда 42080

Выкатка приложения, source_conflict и `--force` описаны в `.agents/skills/stand-deploy/SKILL.md`. Здесь только частые пути после неё. Вход и учётки: `.agents/skills/qa-testing/SKILL.md` §0. Правило «работа, а не проверки»: `.agents/skills/work-not-checks/SKILL.md`.

## 1. Вход под владельцем и скриншоты 1440 и 420

Делать один раз на шаг, не на каждый клик.

1. Вызвать `browser_status`, затем `browser_profiles` с `action: "list"`. Взять профиль, в котором уже есть сессия. Если профиля нет, использовать Playwright с флагами из AGENTS.md («Unattended runs»), отдельный профиль.
2. Вход: `POST http://localhost:42080/auth/login` с учёткой из `qa-testing` §0, либо `storage_state` из уже открытой сессии владельца. Пароль в вывод не печатать.
3. Для каждого экрана два снимка:
   - ширина 1440 px, `deviceScaleFactor: 2` (это то, что видит человек);
   - ширина 420 px, тоже `deviceScaleFactor: 2`.
4. Страницы: `http://product-manager.localhost:42080/` (панель PM) и `http://metrika-analyst.localhost:42080/`, `http://market-watch.localhost:42080/` по нужному приложению.
5. Ответ `design-review` по каждому экрану: 8 пунктов, вердикт «стыдно показать / терпимо / красиво», список своих правок. Без этого шага визуальная проверка не закрыта (AGENTS.md, «Design review is mandatory»).

Скриншоты сохранять в `design/ui-audit/<дата>-<экран>-<ширина>.png` и прикладывать к отчёту.

## 2. Один вопрос в чат PM и сохранить ответ

Вопрос пишется как директор, без имён инструментов, SKU, дат и шагов (см. `qa-testing` §1 и «No solution prompts» в AGENTS.md).

1. Открыть `http://localhost:42080/` (чат Airlock) или чат панели PM, отправить вопрос текстом из §1 `qa-testing` (не придумывать новый).
2. Дождаться конца ответа: ждать до появления финального блока, один `sleep` 20–30 с, потом одна проверка. Опрос в цикле запрещён.
3. Сохранить текст ответа и скриншот в `.runtime/<трек>/pm-answers/<дата>-<n>.md` и `.png` (не в git, если в ответе есть данные клиента).
4. Проверить правила ответа из AGENTS.md: прямой ответ первой строкой, одна и та же цифра с определением, без «не подтверждено» и «нет данных».

## 3. Засеять ДЕМО-данные в снимок PM

Скрипт строит детерминированный пакет, он не ходит на стенд и не содержит учётных данных:

```sh
cd /Users/user/AIfence    # или worktree трека
python3 scripts/seed-demo-42080.py --out .runtime/sol-sessions/sol-seed
ls .runtime/sol-sessions/sol-seed     # pm-demo-*.json, watch-series-demo-*.json
python3 scripts/lint-placeholder-names.py    # обязательно, должен выйти с кодом 0
```

Загрузка в стенд идёт через UI приложения или инструменты Airlock, не прямым POST. Данные должны быть настоящими по названию (реальные продукты из каталога Market Watch), без слов «демо», «тестовый», «учебный» в интерфейсе: синтетика помечается только флагом или путём.

После загрузки: сделать шаг 1 (скриншоты) и шаг 2 (вопрос в чат), чтобы убедиться, что снимок виден приложению. Если ответ пришёл из фикстуры, а не из загруженного снимка, снимок не привязан к проекту: проверить, что снимок у проекта PM не `local_project_only`, и только потом смотреть ответ.

## Что не делать

- Не запускать `scripts/personal-mac-rollout.py` во время чужих сессий: перезапуск рвёт чаты.
- Не трогать `.airlock/`, замки и `deployed-commit` руками.
- Не вставлять токены и пароли в чат, в трек или в git.
