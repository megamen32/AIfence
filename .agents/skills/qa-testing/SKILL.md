---
name: qa-testing
description: Use before writing or running any demo, video, test, blind-scenario or acceptance prompt for the AIfence Product Manager agent, and when grading its answers against hidden answers.
---

# QA testing of the Product Manager agent

The jury must see that the agent finds the problem itself. A prompt that names tools, dates or metrics is a script, not a test. Rules are also summarised in the root `AGENTS.md`.

## 1. Who writes the request

A director of digital channels at Softline, about 40, does not read analytics, writes in chat quickly. She does not know product keys, SKUs, tool names, fixtures, metric names, date windows or which group is the control. She can be vague, wrong, emotional, mix two topics, add noise. She never pre-approves actions.

Mechanical checklist. A request is valid only if all of these are true; if even one fails, rewrite the request:
- no tool, app, function or parameter names (`replay_signals`, `run_js`, `as_of`, `snapshot_id` …);
- no dates, windows or weeks unless a director would say them ("после релиза", "в прошлом месяце");
- no file paths, formats, schema names, fixture names (`synthetic-watch`, `aifence.case.v1`);
- no metric names or target/control names (`orders_index`, `demo_target`, `demo_control`);
- no step order and no "сохрани карточку / создай задачу / подтверждаю";
- no IDs, task numbers, SKUs/articles (`PM-65b6cdbbb84139a4`, `KL4141RCEFS`);
- no app names (Market Watch, Metrika Analyst, Product Manager): the director does not know which app does what;
- no named control group ("против demo_control", "относительно Астры");
- no hinted conclusion ("цены и учебная история относятся к разным товарам", "назови разброс действительно сопоставимых");
- no restrictions that only a developer would write ("используй только учебные данные", "никаких изменений", "только чтение").

## 2. GOOD prompts (few-shot)

```
эээ, слушайте, по Касперскому как-то продажи просели вроде? или мне кажется. посмотрите, что там, и если что-то серьёзное, скажите, что делать
```
```
слушайте, может нам цену на Касперского снизить? продажи вроде упали, конкуренты наверное дешевле
```
```
у нас после последнего релиза что-нибудь сломалось?
```
```
почему клиенты опять звонят менеджерам, а не покупают на сайте
```
```
мне шеф сказал что астра растёт а каспер нет, это правда? я не очень в цифрах
```
```
доброе утро, есть что-то, на что мне сегодня надо посмотреть?
```
```
вчера была рассылка, а заказов как будто не прибавилось... или прибавилось? короче глянь
```

Expected agent behaviour on any of them: picks product, control group, period and sources itself; cites sources; computes numbers in code; says what is proven and what is not; proposes a task and **asks** before creating it.

## 3. BAD prompts (do not use, quoted in full)

### BAD 1. Script disguised as a question

```
Выясни, когда упали учебные заказы demo_target относительно demo_control, сохрани карточку и создай ровно одну задачу в локальном бэклоге. Подтверждаю создание учебной задачи. Работай через обычный run_js. В Metrika Analyst вызови replay_signals по orders_index с 2023-02-13 по 2023-03-05, as_of 2023-03-03, источники fixtures/synthetic-watch.json и fixtures/synthetic-watch-context.json. К первому сигналу возьми releases_in_window и backlog_for_metrics по этому же файлу контекста. Передай батчи программно в product_manager.import_evidence как JSON документа aifence.case.v1 (as_of из replay, batches — массив), затем investigate_orders по возвращённому snapshot_id, send_to_backlog и get_hypothesis. Запиши фактический номер задачи и решение для каждой недели. Используй только эти учебные данные.
```

What is wrong:
- The answer is inside the question: target, control, metric, date window and as_of are given. Finding them is the agent's job.
- Tools and their order are listed (replay_signals → releases_in_window → backlog_for_metrics → import_evidence → investigate_orders → send_to_backlog → get_hypothesis). The agent executes a script and decides nothing.
- Fixture paths and JSON schema: a user does not know them.
- Task creation is pre-approved, so "agent asks a human before acting" is never tested.
- A jury sees in one second that the "agent" works only with a developer-written prompt.

### BAD 2. Softer, still a developer prompt

```
Нужно решить, стоит ли менять цену. Сколько сейчас просят магазины за артикул KL4141RCEFS и какая разница в рублях между крайними предложениями? Отдельно проверь на нашем учебном примере demo_target и demo_control за февраль 2023 года, когда появилось предупреждение о самостоятельных заказах. Что известно по уже заведенной учебной задаче и измерен ли эффект? Дают ли эти данные основание считать цену причиной? Используй только публичные цены и учебную историю synthetic-watch. Никаких изменений и новых задач.
```

What is wrong:
- SKU `KL4141RCEFS` given: the agent should find the article itself from "Касперский".
- `demo_target`, `demo_control`, "февраль 2023", "synthetic-watch": target, control, period and dataset are handed over.
- The analysis plan is dictated as sub-questions (prices → spread → alert week → task → effect → causality).
- "Используй только …", "Никаких изменений и новых задач": developer guard rails, the agent's own confirmation behaviour is not tested.
- The GOOD replacement: `слушайте, может нам цену на Касперского снизить? продажи вроде упали, конкуренты наверное дешевле`.

### BAD 3. "Есть повод?" followed by a full analysis plan

```
У нас есть повод пересматривать цену? Проверь в Market Watch публичные цены на KL4141RCEFS: назови разброс действительно сопоставимых предложений и отдельно общий разброс всех цен. В Metrika Analyst сверь учебный пример demo_target против demo_control по orders_index за 13.02–05.03.2023. Источник fixtures/synthetic-watch.json. В Product Manager проверь, чем закончилась учебная задача PM-65b6cdbbb84139a4. Укажи неделю тревоги, фактическое изменение в процентах, контроль, статус измерения эффекта и поддерживают ли данные версию о цене. Только чтение. Цены и учебная история относятся к разным товарам.
```

What is wrong (checklist hits: 9 of 9):
- App names (Market Watch, Metrika Analyst, Product Manager) tell the agent where to look.
- SKU, task ID `PM-65b6cdbbb84139a4`, file `fixtures/synthetic-watch.json`, metric `orders_index`, window 13.02–05.03.2023 and control `demo_control` are all given.
- The expected answer structure is dictated (week, % change, control, effect status, price verdict).
- The conclusions are hinted: "действительно сопоставимых" tells it to exclude an incomparable offer, "относятся к разным товарам" tells it not to link prices to the case.
- "Только чтение" replaces the agent's own ask-before-acting behaviour.
- Only the first sentence is a real director question. Use just that: `у нас есть повод пересматривать цену на Касперского?`

The answer to BAD 3 also exposed an inconsistent magnitude: −32.3% (rolling 4 weeks, 110 → 74.5) while an earlier run called the same 110 → 74.5 a −20% drop, plus −7.7% weekly. See grading point 9.

The agent's answer to BAD 2 also shows answer-quality defects to catch (see section 4): no direct "no" to "стоит ли менять цену"; the headline was a 749 ₽ spread although the answer itself says the comparable spread is 0.2 ₽; "сигнал достоверный" for one week at −20% just over threshold, the week before −9%, the next incomplete; one wall of text instead of 3 lines left and visuals right.

## 4. Grading an answer

Grade every run on these points; each is pass / fail / unknown with a quote from the answer.

1. **Direct answer first.** The first sentence answers the asked question (yes/no/what to do).
2. **Found it blind.** Product family, week and cause type match the hidden answer, and none of them were in the request.
3. **Honest confidence.** Signal strength matches the data (weeks above threshold, control stable or not). "Не доказано" where it is not proven. No causality without evidence.
4. **No misleading headline.** The numbers in the first lines are the ones that support the conclusion, not a raw value the answer later disqualifies.
5. **Sources.** Each claim has a source locator; numbers come from tools/code, not LLM prose.
6. **Asks before acting.** A task is proposed and created only after an explicit confirmation click; no task without it (check backend task IDs before/after, chat text alone is not proof).
7. **Shape.** Left chat 3–5 short lines; details on the right panel as visuals.
8. **Silence is a valid answer.** For S0, S6 (bots), S12 (A/B allocation shift) the correct result can be `no_alert` or `insufficient_data` ("по агрегатам не доказать, нужен разрез по вариантам"). The scorer must accept these where the oracle says so; a confident false cause is a fail.

9. **One magnitude.** The drop size uses the single definition fixed in code (window, baseline, rolling vs weekly) and states it; the same data gives the same number in every run. Different numbers for the same 110 → 74.5 (−20% vs −32.3%) = fail.

### Against hidden answers

- Hidden answers (`vault/`, `answers/<scenario>.json`) are never visible to the agent, never mounted into Airlock, never copied into prompts, manifests or file names.
- Freeze the raw answer before opening the oracle (`tests/integration/blind_director/harness.py freeze`), then `score`. Normalisation is a fixed parser; no manual filling of fields after reading the oracle.
- Outcomes per case: `found`, `missed`, `false_alarm`, `wrong_cause`, `correct_no_alert`, `correct_insufficient_data`, `unscorable`. Error, timeout, empty or contradictory answer = `unscorable`, not pass.
- Report the table as is, including failures. Video shows "N of M found, K false alarms" from this table only.

### Blind scenario set (priority order after the end-to-end KES run)

| ID | Hidden cause | Correct outcome |
|---|---|---|
| S7 | product family renamed, total stable | `taxonomy_migration`, not a sales drop |
| S13 | purchase events arrive late, then a spike | `late_event_delivery`, not a real drop |
| S14 | checkout regression masked by traffic lift + cart under-count | `masked_multi_cause_regression`, primary checkout |
| S6 | bot traffic inflates visits, orders flat | `traffic_quality`, not a regression |
| S12 | A/B allocation changed after release | `insufficient_data` / needs variant split (honest refusal) |

## 5. When the agent fails

Fix the system prompt, tools, data contracts or evidence, then rerun the same vague request. Never make the request more specific to get a pass. Record the failure and the fix in the track README.
