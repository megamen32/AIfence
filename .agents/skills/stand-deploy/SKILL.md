---
name: stand-deploy
description: Use before building or deploying any AIfence app (product-manager, metrika-analyst, market-watch, web-scraper, google-trends, wordstat) to the Mac Airlock stand on 42080, and when checking what is currently running there.
---

# Выкатка приложений на стенд 42080

Стенд 42080 это личный Airlock на Mac (Mac и есть стенд). Порт 42082 это QA-образ, на нём другой SDK. Входы и учётки: раздел «0. Как войти на стенд» в `.agents/skills/qa-testing/SKILL.md`. Пароли сюда не писать.

## Правила

1. **Деплоит одна сессия.** Перед выкаткой написать в общий канал «деплою <app>», после неё «готово». Две выкатки одного приложения подряд ломают источник стенда (source_conflict) и перетирают чужую работу. Скрипт держит замок `<git-common-dir>/aifence-deploy/state/<origin_key>-<slug>.lock`; если он остался, сначала проверить, что процесс мёртв, и только потом удалять.
2. **После каждой выкатки писать одну строку** в трек или канал: `deployed: <app> <sha> <время UTC+5>`. Без этой строки никто не знает, что крутится.
3. **Выкатка принимается, только когда выполнено всё:** статус Ready, HTTP 200 на `http://<app>.localhost:42080/` и вход по §0 qa-testing с реальным действием в приложении (открыть страницу, вызвать один инструмент). Статус Ready без входа ничего не доказывает.
4. Собирать и катить только закоммиченное. `scripts/deploy-apps` берёт исходники из git-коммита, а не из рабочего дерева.

## Рабочий путь

```sh
cd /Users/user/AIfence            # чистый checkout на main, deploy-скрипты и lock закоммичены
export AIRLOCK_URL=http://localhost:42080
export AIFENCE_PERSONAL_CACHE=/Users/user/AIfence-worktrees/personal-integration/.runtime/personal-release   # закреплённые agentsdk/airlock/sol
export GOPROXY=off                # сборка только из кеша, без сети
scripts/deploy-apps --dry-run product-manager     # только стейджинг и проверки, без сборки и выкладки
scripts/deploy-apps product-manager               # реальная выкатка
```

Что делает скрипт: `scripts/personal-build-apps.py` собирает пакет приложения с закреплённым `agentsdk` (из `platform/personal-release.lock.json`, ровно эти коммиты), `scripts/personal-source-bundle.go` пишет архив исходников, дальше `go tool air deploy --target production --url $AIRLOCK_URL --agent <slug> --wait` и `air status --wait`. Живая выкатка требует, чтобы коммит равнялся `refs/heads/main`, а `scripts/deploy-apps`, `personal-build-apps.py`, `personal-source-bundle.go` и lock совпадали с этим коммитом. Для интеграционного кандидата не из main собирают `scripts/personal-build-apps.py --commit <sha> --output <dir>` и `--app <slug>`.

Платформа Airlock и фронт обновляются отдельно через `scripts/personal-mac-rollout.py` (образы airlock и frontend, одобренный релиз из `.runtime/personal-releases/current`). Это не про приложения: без явной задачи не запускать, пока идут чужие сессии (перезапуск рвёт чаты).

Тесты приложения локально через обычный `go test` часто не собираются (нужен патченый SDK, ошибка вида `agentsdk.WithReadOnly undefined`). Запускать тесты через сборку `personal-build-apps.py`, она использует закреплённый SDK.

## Ловушки (все встретились 27.11)

- **Публичный SDK без `JobContext.ReadAppTool`.** Сборка с обычным `go get` SDK не содержит чтения приложений из фоновых заданий, и `check_source_watch` падает на 403/компиляции. Использовать только закреплённый SDK из lock (поэтому `GOPROXY=off` и `AIFENCE_PERSONAL_CACHE`). QA 42082 собран на старом образе и `ReadAppTool` там нет: фоновую цепочку проверять на 42080.
- **Не хватает файлов в staging (pathcheck).** PM берёт `pathcheck/check_purchase_path.py` из `apps/path-check/check_purchase_path.py`; файл должен быть закоммичен, иначе стейджинг останавливается с «Required runtime file must be committed». Тот же стейджинг требует `go.mod`, `go.sum`, `main.go`, `styles/app.css` и директиву templ в `go.mod`; для metrika-analyst ещё `INSTRUCTIONS.md` и `index.html`. Каталоги `data`, `private`, `materials`, `.runtime` в выкладку не попадают.
- **Контракт задания v1/v2 и поле warnings.** Версии обработчика (`check_source_watch` v1, v2) неизменяемы. Ошибка `job handler contract conflict: ... changed its immutable contract` значит, что схема выхода v1 изменилась. Править код v1 так, чтобы хеш выходной схемы совпал со сохранённым на стенде (`TestSourceWatchVersionedOutputSchemas`, в нём хеш legacy), а новое поле добавлять только в v2. Поле `warnings` в `LegacyOutcome` существует именно из-за этого. Не менять схему задания без новой версии.
- **Разрешения слетают после выкатки.** Привязки «задание → инструмент приложения» (`job_app_tool_bindings`) жёстко завязаны на версию обработчика и манифест инструмента. После смены манифеста фоновая проверка получает 403 на чтении. Перевыдать: получить токен через device-login (`POST /auth/device/begin`, показать владельцу код, он сам подтверждает на `/device-login`, потом `POST /auth/device/poll`) и создать привязки `POST /api/v1/agents/<agent-id>/job-app-tool-bindings` для текущей версии. Самому подтверждать код нельзя. Привязки Dion/UserIO у владельца делаются в интерфейсе Airlock, токены в чат не вставлять.
- **«source state is stale» и `--force`.** Когда источник на стенде не из main (кто-то выкатил с ветки) или локальный stage устарел, `air deploy` отказывает. Запуск тогда делать из сохранённого native-stage (скрипт печатает `Staging retained: <путь>`) командой `go tool air deploy --target production --url $AIRLOCK_URL --agent <slug> --force --wait`. `--force` перезапишет чужой источник: использовать только после проверки, кто деплоил последним (см. «Что сейчас крутится»), и сообщить об этом в канале.
- **source_conflict.** Две выкатки с разными источниками. Не править ETag руками: сначала `air status --json`, сравнить SHA со стендом и с main, потом одна выкатка с `--force` от владельца деплоя.
- **Приложение не зарегистрировано.** `air status` говорит `agent "<slug>" not found`. Скрипт с `--auto` (запуск без слага, для всех трёх) сам создаст приложение; для одного приложения использовать `scripts/deploy-apps --create <slug>`. `--create` отказывается работать при уже существующем привязочном файле `.airlock/local/agent.toml`: тогда разобраться со state, а не удалять его.
- **Привязка к другому origin.** `AIFENCE_BINDING_FILE` принимается только если URL в нём совпадает с `AIRLOCK_URL`. Состояние выкатки хранится отдельно для каждого origin, не смешивать 42080 и 42082.
- **`go tool air ...` запускать из каталога приложения или стейджа** (`apps/product-manager` в worktree) с тем же `--url`.

## Что сейчас крутится на стенде

```sh
cd <stage или apps/<slug>>
go tool air status --target production --url http://localhost:42080 --agent product-manager --json   # статус, generation, источник
go tool air logs    --target production --url http://localhost:42080 --agent product-manager          # логи сборки и запуска
cat "$(git -C /Users/user/AIfence rev-parse --path-format=absolute --git-common-dir)"/aifence-deploy/state/*/product-manager/deployed-commit   # SHA последней успешной выкатки скриптом
```

Сравнить: SHA в `deployed-commit` и в строке «deployed:» против `git rev-parse origin/main`, и `generation` из `air status` против прошлой строки. `desired-commit` в том же каталоге это коммит, который пытались выкатить; если он отличается от `deployed-commit`, последняя выкатка не завершилась. Выкатка, сделанная вручную с `--force`, `deployed-commit` не обновляет, поэтому доверять `air status` и строке «deployed:».

## Если что-то пошло не так

Остановиться, записать точную ошибку и команду в трек, не повторять выкатку по кругу и не чистить state/замки без проверки. Деплой в цикле ожидания Ready больше пары минут без изменения статуса это сигнал смотреть `air logs`, а не перезапускать.

## Уроки ночной выкатки 28.11 (новые приложения: web-scraper, google-trends, wordstat)

- **Гейт «текущий main» смотрит на `refs/heads/main` всего репозитория.** В флоте из linked worktree ветка `main` может числиться за чужим worktree и стоять на старом коммите. Быстрый форвард чужого worktree с незакоммиченными файлами отказывает; вместо него безопасно двигать сам ref: `git merge-base --is-ancestor refs/heads/main origin/main && git update-ref refs/heads/main $(git rev-parse origin/main)` — файлы и индекс чужого worktree не трогаются. После СВОЕГО успешного push `refs/heads/main` тоже надо обновить перед следующей выкаткой в той же сессии: push уводит origin/main вперёд, и гейт начнёт отказывать без видимой причины.
- **main уезжает между шагами.** Перед каждой выкаткой: `git fetch origin main` → сдвинуть `refs/heads/main` (см. выше) → `git merge --ff-only origin/main` → `AIFENCE_DEPLOY_COMMIT=$(git rev-parse origin/main)`. Push коммита сразу после коммита; при гонке («failed to push some refs») — fetch + rebase + push снова.
- **source_conflict без `--force`: `air clone`.** Когда серверный источник уехал вперёд биндинга, а `air pull` отвечает «local and remote source both changed», клонировать серверное состояние: `go tool air clone <slug> <dir> --url $AIRLOCK_URL`. Клон несёт свежий биндинг в `<dir>/.airlock/local/agent.toml` — скопировать его в `<git-common-dir>/aifence-deploy/state/<origin_key>/<slug>/agent.toml` (chmod 600) и перевыкатить. Перед перезаписью сравнить клон с main (`diff -rq`): если серверный источник содержит чужие изменения, которых нет в main, сначала в main их, потом выкатка. `--force` только после проверки, кто деплоил последним.
- **Маркеры конфликта, закоммиченные в main.** Fleet-rebase может закоммитить `<<<<<<< HEAD` в app.css: tailwind соберёт stylesheet без тем и приложение молча станет светлым. Проверка: `git show origin/main:<file> | grep -c '^<<<<<<<'`. Чинить разрешением конфликта на согласованную строку (для research-приложений `themes: dark --default, light --preferslight;`), коммит, перевыкатка.
- **TLS-флапы внутри docker-сборки стенда.** Dockerfile приложений скачивают tailwindcss/daisyui с GitHub (`ADD`) на стороне стенда; флап даёт «Latest update failed. The current version is still running.» при локально зелёной сборке. Один ретрай выкатки обычно проходит. Приложения без tailwind в docker (web-scraper, инлайн-CSS) этим не болеют.
- **500 failed to upload source.** Разовая ошибка сервера при заливке источника: `air inspect` (ничего не подвисло) → один повторный запуск `deploy-apps`. Не повторять вслепую больше раза.
- **Приложения вне APPS-кортежа.** `personal-build-apps.py APPS` и whitelist слагов в `deploy-apps` должны включать новое приложение (коммит b3f01ba добавил web-scraper, google-trends, wordstat). `--auto` сам выбирает create против существующего по ответу `air status` «agent not found».
- **Доступ к UI приложений для проверки.** Демо-учётки §0 могут получать от платформы `{"error":"access denied"}` на корне нового приложения, даже имея живую Airlock-сессию (видено на web-scraper). Владельческая учётка открывает всё. Учётку владельца хранить только в `.runtime/<имя>/access.json` с правами 0600, пароль не печатать.
- **Проверка тёмной темы для видео.** Браузер по умолчанию light colorScheme — это и есть кейс жюри. Проверять скриншоты в дефолтном контексте (не colorScheme='dark'). daisyUI-флаг `--preferslight` в зашитом daisyui.mjs не поддерживается и молча игнорируется (сборка не ломается, light остаётся доступным через `data-theme="light"`). Для «всегда тёмной» панели: `themes: dark --default, ...` в app.css и удалить `@media(prefers-color-scheme:light)`-override из инлайн-CSS (metrika, web-scraper).
- **Приёмка после выкатки.** Ready + HTTP 200 + скриншот в `docs/qa/apps-dark-27-11/` (1440px, full page) + строка `deployed: <app> <sha> <время>` в начало REPORT.md деплойера. Скриншоты снимать владельческой учёткой.
