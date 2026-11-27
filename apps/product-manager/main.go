package main

import (
	"agent/handlers"
	"agent/internal/decision"
	"agent/internal/evidence"
	privateui "agent/internal/handlers"
	"agent/internal/product"
	"agent/internal/sourcewatch"
	"agent/views"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: `Разбор продаж и продуктовых отклонений. На вопрос директора отвечай обычным русским текстом; submit_answer обязателен только при answer_mode=submit_answer_required. Обычное чтение: return await apps.product_manager.tools.review_sales_and_prices({summary:true,director:true}); запрос только читает, "request_confirmation":false. В строгом режиме director_replies содержат варианты в точной схеме submit_answer с доказательствами. Явно маркированную синтетику из снимка текущего владельца можно анализировать с пометкой «синтетический срез». Источники PM: read_wordstat (запросы), read_google_trends (относительный интерес), read_competitor_prices (предложения Market Watch). Без product_key они перечисляют товары, по точному product_key возвращают наблюдения. При imported_price_observations=0 проверь источники. В строгом режиме верни принятое поле «текст».`, Icon: "🧭"})
	topic := a.RegisterUIChangeTopic("product-decisions")
	policy := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "private_model_route", Description: "Доступ закрытых снимков из чата с разрешенным локальным маршрутом модели. Значение задает владелец стенда после проверки конфигурации.", Default: "off", Pattern: "^(off|approved)$"})
	strictReply := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "strict_submit_answer", Description: "Требовать финальный ответ через submit_answer. По умолчанию агент отвечает обычным текстом.", Default: "off", Pattern: "^(off|on)$"})
	e := evidence.New(a.DB(), topic, policy)
	d := decision.New(a.DB(), topic)
	bridge := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_notifications", Description: "Установите bridge только после настройки разрешённого Airlock-канала доставки.", Default: "off", Pattern: "^(off|bridge)$"})
	alerts := a.RegisterTopic(&agentsdk.Topic{Slug: "product_signals", Description: "Новые продуктовые сигналы владельца", Access: agentsdk.AccessUser, Enrollment: agentsdk.TopicEnrollmentDefaultOff, PerUser: true})
	smtp := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_smtp", Description: "Закрытая JSON-настройка внутреннего SMTP relay для одного владельца. Пустое значение отключает отправку.", Secret: true})
	p := product.Service{Decisions: d, Evidence: e, Notifications: &product.SignalNotifications{Enabled: bridge, Topic: alerts, DB: a.DB()}, Mail: &product.SignalMail{Config: smtp, DB: a.DB()}}
	h := handlers.New(p, a).WithCatalogOverview(registerCatalogOverview(a).Panel)
	registerPathCheck(a)
	registerMarketIntelligence(a, e)
	registerCompetitorResearch(a)
	watch := sourcewatch.Register(a)
	a.RegisterTool(tool.Typed[sourcewatch.Window, sourcewatch.WatchState]("start_source_watch").Description("Enable an owner-scoped weekly check. Use compare_periods for a store metric plus compare_demand for a Wordstat phrase, compare_google_trends for search interest, price_week_delta for one market SKU, family_price_week_delta for several SKUs or a family phrase, or several of these. Supply approved binding UUIDs, the percent drop threshold and the initial UTC Monday. Optional demand_drop_pct also triggers on falling search interest. Optional product_key and segment read an explicitly labelled product family snapshot instead of the live store; the family is never relabelled as store. A family price selection keeps one series per SKU and seller and never averages roubles across licences. Runs until stopped. The native job uses the owner’s explicitly bound read-only operations.").Execute(watch.Start).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[sourcewatch.RebindInput, sourcewatch.WatchState]("rebind_source_watch").Description("Replace selected binding IDs of an existing owner watch after reconnecting a source. Omit unchanged bindings. Preserves the watch, selected sources, metric, thresholds, queued check and history.").Execute(watch.Rebind).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[sourcewatch.WatchRef, sourcewatch.WatchRef]("stop_source_watch").Description("Stop the owner's weekly check by watch ID.").Execute(watch.Stop).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[struct{}, sourcewatch.SummaryList]("list_source_watches").Description("List this owner's watch IDs, active state and latest outcomes.").Execute(watch.Summaries).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[sourcewatch.WatchRef, sourcewatch.Review]("review_source_watch").Description("Read the owner's completed typed review of one watch. Every cited evidence ID is validated against the pinned signal.").Execute(watch.Review).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, evidence.Index]("list_evidence").Description("List immutable evidence snapshots imported by this user, newest first.").Execute(e.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Query, evidence.Result]("read_evidence").Description("Read observations with source locators. Filter by source, IDs or page with offset and limit; total_matched includes all matching rows.").Execute(e.Read).Build(), agentsdk.AccessUser)
	a.RegisterTool(salesReviewTool(a, h, strictReply), agentsdk.AccessUser)
	a.RegisterTool(submitAnswerTool(), agentsdk.AccessUser)
	a.RegisterTool(importEvidenceTool(p), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.SaveInput, decision.Record]("save_hypothesis").Description("Write a proposal. Set hypothesis.schema_version to the exact string aifence.v1, status to proposed, economic_effect to {status:unknown,assumptions:[]}. Pass signal_ids, support_ids, counter_ids, unknowns as arrays (use [] when empty). Pass the immutable evidence_snapshot_id from list_evidence and expected_version=0 on create. Optional fields: forecast, competing_explanations, evidence_level and browser_evidence_ids. path_verified requires an observed browser path_check for the same product; it checks the path, not causality. A criterion has ISO baseline_from/to, observation_days, metric_key and rule. Rules: relative_change_pct >= N, or difference_in_relative_changes_pp >= N with control_product_key.").Execute(p.Save).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.GetInput, decision.Record]("get_hypothesis").Description("Read saved hypothesis, local backlog task and evaluation.").Execute(p.Get).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, decision.ListResult]("list_hypotheses").Description("List this user's records and status.").Execute(p.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, product.PrivateReviewURL]("private_review_link").Description("Return the fixed link to the signed-in user’s protected board. No data lookup. For requests about private project inputs, provide this link without reading the board or the data through additional tools.").Execute(product.PrivateReviewLink).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, decision.Priorities]("rank_hypotheses").Description("Rank proposed checks by scenario weekly incremental margin in RUB per person-hour to test. Equal scores favor stronger evidence, then lower effort. needs_estimate has incomplete inputs. Never present scenario values as measured profit.").Execute(p.Rank).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.EstimateInput, decision.Record]("set_review_estimate").Description("Record only a user-supplied or explicitly sourced planning scenario. Supply the weekly incremental margin in RUB, validation effort in person-hours and their source or assumptions. Empty estimate clears it. Requires the current draft version.").Execute(p.SetEstimate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.GetInput, decision.Task]("send_to_backlog").Description("Read the receipt of a task already approved separately in the Product Manager panel. This tool cannot create a task.").Execute(p.BacklogReceipt).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.EvaluateInput, decision.Assessment]("evaluate_hypothesis").Description("Check for post-release effect. Supply only imported observation IDs when linking a corrective release or measured windows.").Execute(p.Evaluate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.InvestigateInput, decision.Record]("investigate_orders").Description("Save or read back a proposal from an imported sales watchdog.alert=true. Pass the producer first_alert_signal_id as signal_id to select the exact alert, with product_key and week_from when available. Returns the verified signal_contrast from its pinned evidence. Uses the same snapshot for context and browser results. For older snapshots without watchdog rows, use precomputed full-week family_orders_idx.relative_change_pct.").Execute(p.Investigate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, product.Focus]("get_product_focus").Description("Read your current Product Manager panel selection.").Execute(p.GetFocus).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.Focus, product.Focus]("set_product_focus").Description("Focus your Product Manager panel on a product established from evidence or explicitly named by the user. Supply its product_key, or the saved hypothesis_id.").Execute(p.SetFocus).Build(), agentsdk.AccessUser)
	for _, r := range []*agentsdk.Route{
		{Method: "POST", Path: "/investigate", Handler: investigatePage(p), Description: "Разобрать изменение самостоятельных заказов"},
		{Method: "GET", Path: "/", Handler: h.Home, Description: "Гипотезы, источники и локальный бэклог"},
		{Method: "GET", Path: "/private-review", Handler: privateui.PrivateReviewPage(a.DB()), Description: "Закрытые решения и снимки"},
		{Method: "GET", Path: "/priorities", Handler: h.Priorities, Description: "Приоритеты проверок"},
		{Method: "POST", Path: "/priorities", Handler: h.UpdateEstimate, Description: "Сохранить оценку проверки"},
		{Method: "GET", Path: "/board", Handler: h.Board, Description: "Карточки продуктовых решений"},
		{Method: "POST", Path: "/edit", Handler: h.Edit, Description: "Отредактировать гипотезу перед подтверждением"},
		{Method: "POST", Path: "/approve", Handler: product.BacklogApprovalHTTP(h.Approve), Description: "Подтвердить задачу локального бэклога"},
		{Method: "POST", Path: "/import", Handler: h.Import, Description: "Импортировать проверяемые наблюдения"},
	} {
		r.Access = agentsdk.AccessUser
		r.Handler = evidence.LocalHTTP(r.Handler)
		a.RegisterRoute(r)
	}
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppCSSName, ContentType: "text/css; charset=utf-8", Data: views.AppCSS})
	a.AddInstruction(&agentsdk.Instruction{Text: `Начинай разрешённые чтения сразу. Для вопроса о продажах и ценах вызови run_js с полями code и description и явным флагом штатного чтения:
{"code":"return await apps.product_manager.tools.review_sales_and_prices({summary:true,director:true});","description":"Прочитать краткие наблюдения продаж и цен","request_confirmation":false}
Пустой результат означает, что данные не получены. Проверь наличие return. Отвечай на текущем ходе. При отсутствии нужных наблюдений проверь источники через inspectApp: каталог по названию, затем сравнение точного артикула, для продаж — доступные ряды и полный период. Выбирай источники по вопросу и происхождению. При отказе предложи private_review_link. Явно маркированные синтетические наблюдения из снимка текущего владельца можно использовать для вывода с пометкой «синтетический срез».

	Для разбора отклонения проверь доступные источники снимка текущего владельца. read_wordstat показывает количество поисковых запросов, read_google_trends — нормированный интерес, read_competitor_prices — датированные предложения Market Watch. Каждый инструмент без product_key возвращает доступные продукты, с product_key — наблюдения и локаторы. Импортированные продажи и контекст доступны через list_evidence и read_evidence. Если ответ зависит от спроса и конкурентов, сравни относящиеся к товару ряды за одинаковые периоды, регион и сопоставимые условия лицензии. Отметь происхождение данных, пропуски и альтернативные причины. Называй использованными только прочитанные наблюдения; отсутствие источника — тоже результат.

По умолчанию отвечай обычным русским текстом. Ответь на вопрос директора и назови подтверждённый сигнал, его период и источник. Опирайся на сопоставимые измерения и не приписывай причинность без доказательств. Если результат чтения содержит answer_mode=submit_answer_required, сверь выбранную карточку director_replies с источниками, передай её программно в apps.product_manager.tools.submit_answer и верни принятое поле «текст». Если карточки нет, исследуй доступные ряды и самостоятельно передай вывод через submit_answer. Без измеренной пары величина и контроль могут быть null. В строгом режиме не заменяй контроль общим словом и не опускай его.

При расследовании нового сигнала импортируй выбранный документ с исходным source_policy и используй его идентификаторы. Для карточки проверь контекст релиза, источники и достаточность подтверждений. По запросу проверь путь покупки. Предлагай действие до выполнения. Для создания задачи дай ссылку на карточку с «Создать эту задачу». Не проси ответить «да» в чате. Квитанцию читай после отдельного подтверждения на карточке. Для приоритетов используй rank_hypotheses, для измерения результатов — evaluate_hypothesis. Для конкурентного анализа сначала вызови plan_competitor_research. Если список конкурентов пуст, используй доступный web-поиск или OmniRoute-поиск, объясни критерии отбора и не выдумывай домены. Для каждого факта сохраняй прямой URL и дату. Используй read_competitor_prices для доступных ценовых наблюдений, браузерные проверки только без отправки форм и заявок. Затем передай наблюдения в export_competitor_csv: он отсортирует строки, сохранит пропуски как missing/unknown и приложит competitor-analysis.csv. Никогда не превращай отсутствие данных в ноль и не считай сведения из загруженной таблицы заказчика результатом собственного поиска.`})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что требует внимания?", Prompt: "Что сейчас требует внимания в продажах?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что стоит проверить?", Prompt: "Посмотри, где могло возникнуть изменение, и предложи, что проверить первым."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось после релиза?", Prompt: "Что мы уже можем сказать об эффекте изменений?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Провести конкурентный анализ", Prompt: "Я представляю такую-то компанию и продаю такой-то продукт. Найди моих основных конкурентов, проведи конкурентный анализ и прикрепи итоговую CSV-таблицу с источниками."})
	return a
}
