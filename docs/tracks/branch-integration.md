# Реестр веток перед UI-интеграцией

Снимок 2026-11-27 11:02:07 (UTC+5). `main` `02e4f05`, `origin/main` `02e4f05`. После `fetch --prune origin` сверены **26 локальных веток, 25 веток origin и 19 worktree**. Tip `codex/submission-kit` снят до коммита этого файла.

В колонке «В main» знаки относятся к `main` и `origin/main` по порядку. При разных tip результат указан отдельно для локального и опубликованного. Тождество патча отдельно от достижимости. WIP указан как **изменённые / неотслеживаемые / игнорируемые**. `diff OK` означает проверку пробелов от общей базы с `origin/main`. Продуктовые прогоны выполняют владельцы соответствующих веток.

| Ветка | Владелец | Локальный tip | Опубликованный tip | В main | Проверка | Worktree · WIP |
| --- | --- | --- | --- | --- | --- | --- |
| `claude/nostalgic-kowalevski-ed20c5` | не установлен | `f8f4f8c` | `f8f4f8c` | ✓/✓ | предок main | `~/.claude worktree nostalgic-kowalevski-ed20c5` · 0/0/0 |
| `origin/claude/project-thread-s8sr76` | не установлен | — | `a1a8648` | origin ×/× | diff OK | `ui-qa` · 0/0/1 · защищённые данные |
| `codex/blind-catalog-verification` | КТ4 · Terra | `f390848` | `f390848` | ✓/✓ | предок main · проверка 30 входов и окон `sourcewatch.Check`; описание в `docs/qa/blind30-preparation-2026-11-27.md` на origin/main | `blind-catalog-verification` · 0/0/4 · защищённые данные |
| `codex/external-demand-20261127` | КТ2 · спрос | `304189f` | `6465b54` | лок. ×/× · origin ✓/✓ | diff OK · origin: предок main | `external-demand` · 0/0/14 |
| `codex/m1-sourcewatch-20261127` | КТ1 · Source Watch | `d1577ce` | `d1577ce` | ✓/✓ | предок main · локальное подтверждение и его границы: отчёт `2eb5b4a` | `m1-sourcewatch-20261127` · 4/3/0 |
| `codex/market-price-feed-first-20261127` | КТ3 · цены | `7787ccc` | `7787ccc` | ✓/✓ | предок main | `market-price-feed-first` · 0/0/8 · защищённые данные |
| `codex/sales-pulse` | Sol · «Пульс продаж» | `c0c73c8` | `c0c73c8` | ×/× | diff OK · референс `c0c73c8` проверен отдельно от работы UI | `sales-pulse` · 0/0/9 · защищённые данные |
| `codex/submission-final` | КТ5 · прежняя ветка | `37d5464` | `37d5464` | ×/× | diff OK · документы `37d5464` совпадают с публикацией `8fa0d69` | — |
| `codex/submission-kit` | КТ5 · комплект | `02e4f05` | `8fa0d69` | лок. ✓/✓ · origin ✓/✓ | предок main · origin: предок main · расписание, реплика и тайминг проверены для `8fa0d69` | `submission-final` · 0/1/0 |
| `external-demand` | КТ2 · спрос | `06f638b` | `06f638b` | ✓/✓ | предок main | — |
| `external-demand-2` | КТ2 · release | `7273a22` | `7273a22` | ✓/✓ | предок main · покрытие чтений: отчёт `26e6d5f` | `external-demand-release` · 0/0/1 · защищённые данные |
| `feat/owner-app-watchdog` | КТ1 · Source Watch | `4812e91` | `dd3d392` | лок. ×/× · origin ✓/✓ | diff OK · origin: предок main | `owner-app-watchdog` · 0/1/30 · защищённые данные |
| `feature/catalog-extractor` | не установлен | `ee2c5a6` | `ee2c5a6` | ×/× | diff OK | `catalog-extractor` · 0/0/1 |
| `integrate/m1-native-main-20261127` | не установлен | `f8f4f8c` | — | ✓/✓ | предок main | `integrate-m1-main-20261127` · 0/0/0 |
| `main` | Никита · интегратор | `02e4f05` | `02e4f05` | ✓/✓ | предок main | `~/.codex/worktrees/285d` · 0/0/1 · защищённые данные |
| `market-price-feed-first` | КТ3 · цены | `713ccd7` | `713ccd7` | ✓/✓ | предок main · покрытие чтений: отчёт `26e6d5f` | — |
| `parked/active-dashboard-writer` | не установлен | `13b005f` | `a4471a2` | лок. ×/× · origin ✓/✓ | diff OK · origin: предок main | `~/AIfence` · 0/0/63 · защищённые данные |
| `parked/competitive-matrix` | не установлен | `be5555d` | `be5555d` | ×/× | diff OK | — |
| `parked/dashboard-20261127` | не установлен | `3d28784` | `3d28784` | ×/× | diff OK | — |
| `parked/dashboard-current` | не установлен | `b866915` | `b866915` | ×/× | diff OK | `parked-analytics-dashboards` · 0/0/0 |
| `parked/external-demand-original` | не установлен | `06c5c68` | `06c5c68` | ×/× | diff: 1 замеч. в `apps/google-trends/THIRD_PARTY_NOTICES.generated.md` | — |
| `parked/price-collector-original` | не установлен | `12175b8` | `12175b8` | ×/× | diff OK | — |
| `parked/submission-kit-wip` | КТ5 · история. WIP — Terra | `2b4597a` | `2b4597a` | ×/× | diff OK | `submission-kit` · 1/0/1 |
| `parked/submission-plan-v2` | не установлен | `f09751a` | `f09751a` | ×/× | diff OK | — |
| `personal/integration` | не установлен | `33832be` | `33832be` · кеш personal-88 | ×/× | diff OK | `personal-integration` · 0/0/2 · защищённые данные |
| `personal/m1-native-sourcewatch-20261127` | не установлен | `491767f` | `491767f` | ×/× | diff OK | `m1-native-persist-20261127` · 0/0/0 |
| `price-feed-2` | КТ3 · release | `713ccd7` | `713ccd7` | ✓/✓ | предок main · покрытие чтений: отчёт `26e6d5f` | `market-price-feed-first-release` · 0/0/10 · защищённые данные |

## Перед UI-merge

- `origin/claude/project-thread-s8sr76` `a1a8648` · владелец не установлен · в main ×/×. `ui-qa` открыт на этом tip в detached HEAD. Его `.runtime` сохранить.
- `codex/sales-pulse` `c0c73c8` · Sol · в main ×/×. Референс и продуктовая UI-ветка имеют разные tip. Сверить оба с последним снимком перед применением.
- Сравнение с началом раунда. Ветка писателя конкурентного CSV дошла до `13b005f`, КТ2 — до `304189f`, КТ3 — до `7787ccc`. Колонка origin показывает, какие tip уже опубликованы.

## Сохранение рабочих деревьев

Безусловно безопасных к удалению worktree в этом снимке нет. В `external-demand-release` и `market-price-feed-first-release` tip уже в main и видимый Git WIP пуст, но внутри `.runtime` есть результаты QA. В `owner-app-watchdog` сохранить `.runtime` с состоянием выкладки. `main` и `submission-final` используются в раунде. Деревья, созданные от текущего main, могут использоваться для незавершённой интеграции.

Приватные QA-доказательства хранит **`blind-catalog-verification/.runtime/blind-director/`**. В `~/AIfence` сохранить runtime и материалы. В припаркованном `submission-kit` сохранить правку Terra. Деревья и ветки только сверены. Кеш `personal-88` прочитан локально.
