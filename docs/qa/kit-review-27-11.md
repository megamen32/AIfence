# Аудит комплекта подачи · 27.11.2026

Проверены `README.md`, все Markdown-файлы в `docs/submission/`, ссылки на QA,
рабочая книга KES, manifests и notices. 42082 не разворачивался, не
перезапускался и не перестраивался. Изменения внесены только в README и
`docs/submission/`; этот чеклист добавлен по заданию аудита.

## Проверки

- [x] **Локальные ссылки.** Скрипт обхода Markdown-ссылок для `README.md` и
  `docs/submission/**/*.md` не нашёл отсутствующих файлов. PNG/JPEG-кадры,
  названные в `docs/submission/demo.md:19-22`, существуют в
  `docs/qa/scenes-27-11/`.
- [x] **Placeholder lint.** `python3 scripts/lint-placeholder-names.py` и
  `python3 scripts/lint-placeholder-names.py docs/submission` вернули
  `No placeholder product names found.`
- [x] **Исключённый стенд.** В `README.md` и `docs/submission/` нет hostname
  исключённого внешнего стенда. Из `docs/submission/deployment.md:21` и
  `docs/submission/model-acceptance/QA-64K-20261126.md:33-35` убраны ссылки на
  операторский QA-runbook, который мог раскрыть внешний адрес. Исторические
  записи вне docs-зоны не менялись.
- [x] **Приватность источников.** `README.md:12-16`,
  `docs/submission/README.md:28`, `docs/submission/deployment.md:3-11` и
  `docs/submission/form.md:15` оставляют один временный маркер Mac Airlock
  tunnel; точного URL в отчёте `sol-stand` нет, поэтому не выдуман.

## Числа и источники

- [x] **Holdout.** `docs/qa/holdout-final-2026-11-27.md:11-16,99` даёт
  watchdog v2 **11/22 попаданий**, **0 ложных `cause_signal`** и **S14 = 0/6**.
  Эти значения стоят в `README.md:34`, `docs/submission/README.md:19`,
  `docs/submission/form.md:17`, `docs/submission/form-draft.md:14` и
  `docs/submission/demo.md:22`.
- [~] **Terra fast chat.** Значение **19/20 cause-first, 0 ложных тревог** в
  `README.md:35`, `docs/submission/README.md:20`, `docs/submission/form.md:17`,
  `docs/submission/form-draft.md:14` и `docs/submission/demo.md:22` взято из
  QA-записи координатора `terra-dev30-prompt-v4`. Файл этой записи не найден в
  checkout; документы теперь явно требуют приложить его перед подачей. Это не
  выдаётся за локально воспроизведённый результат.
- [x] **KES index.** Калькулятор чтения рабочей книги
  `/Users/user/Downloads/Датасет_ИИ-помощник_PM_v17.xlsx`, лист `Продажи`,
  подтвердил в `docs/submission/economics.md:67-86` средние KES 165,846,
  113,000 и 111,571, а также контроль Astra Linux Special Edition 98,538 и
  96,000. Производные −31,865%, −32,726%, 379,923 и 168,538 пересчитываются
  из этих строк; индекс не назван рублями.
- [x] **Рублёвый пример.** `docs/submission/economics.md:131-148` помечает
  100 заказов/неделю, чек 50 000 ₽, маржу 20%, падение 32,726%, недели 1 и 4,
  preventable fraction 100% как допущения. `economics.py` возвращает
  327 260 ₽ в неделю, 1 309 040 ₽ при обнаружении на четвёртой неделе и
  981 780 ₽ штрафа за задержку; smoke-check арифметики пройден.
- [x] **Видео.** `docs/submission/demo.md:1-3,19-24,39` содержит четыре сцены
  35 + 35 + 55 + 50 = **175 секунд (2:55)**; placeholders `[N из M]`, `[K]`,
  `[U]` и `[значение]` удалены. Кадры отмечены как `[SCENE]`, а отправка
  Redmine/Dion остаётся условной при отсутствии live receipt.

## Модели, API и библиотеки

- [x] Версии в `docs/submission/models-and-prior-work.md:51-83` сверены с
  `apps/*/go.mod`, `apps/*/package.json` и lock-файлами: `agentsdk v0.11.0`,
  `goai v0.3.4`, `templ v0.3.1020`, `goquery v1.14.0`, `robotstxt v1.1.2`,
  `mcp-remote 0.1.43`, `yandex-metrica-mcp 1.0.1`,
  `mcp-yandex-wordstat 2.5.0`, `supergateway 4.3.0`.
- [x] Модельный список объявляет Terra fast единственной продуктовой моделью;
  исторические Gemma/Ollama в `docs/submission/model-acceptance/` и legacy
  `scripts/stand/configure.py` явно исключены из состава конкурсного продукта.
  Actual slot read-back остаётся в `models-and-prior-work.md:20-39`:
  `combo/airlock-personal-exec-terra61-safe` и MiniMax failover.
- [ ] **Open-weights gate не закрыт.** Для Terra checkpoint и fallback
  checkpoint не представлены карточка весов и лицензия. До получения этих
  доказательств нельзя утверждать, что фактический fallback-маршрут полностью
  соответствует правилу open weights. Это отмечено в
  `models-and-prior-work.md:12,36-39,96-97` и в
  `economics-handoff.md:9-19`.
- [ ] **Notices.** `goquery` и `robotstxt` действительно импортируются
  Market Watch, но их блоки отсутствуют в
  `apps/market-watch/THIRD_PARTY_NOTICES.generated.md`; это зафиксировано в
  `models-and-prior-work.md:67-70` и требует regeneration вне docs-зоны.

## Доставка и ссылки

- [x] GitHub URL в `README.md:20-23` и `docs/submission/README.md:39` проверен
  анонимно: web/API возвращают HTTP 404. Документы честно оставляют репозиторий
  закрытым и передают решение владельцу; публикация не предпринималась.
- [ ] Точный jury tunnel URL отсутствует в
  `/Users/user/AIfence/.runtime/sol-sessions/sol-stand/REPORT.md`; до появления
  этой строки комплект не содержит открываемой ссылки на жюри.
- [x] Ранее указанная грамматическая ошибка в `docs/submission/README.md:10`
  исправлена; описание, экономика, модели/лицензии и четырёхсценный сценарий
  теперь согласованы с проверенными источниками и ограничениями.

## Ограничения аудита

- Проверка рабочей книги выполнена read-only через локальный парсер
  `apps/product-manager/research/xlsx.py`; исходный файл не копировался в Git.
- Не выполнялись deploy, restart, rebuild или live writes на 42082.
- Текущая проверка не закрывает open-weights gate и не подменяет отсутствующий
  файл `terra-dev30-prompt-v4` локальным прогоном.
