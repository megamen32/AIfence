package main

import (
	"agent/handlers"
	"agent/internal/decision"
	"agent/internal/evidence"
	privateui "agent/internal/handlers"
	"agent/internal/product"
	"agent/views"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: `Product Manager отвечает на вопросы директора о магазине. Открой PM через platform.inspectApp внутри run_js и сразу выполни разрешённые чтения. Для вопроса о цене сразу вызови apps.market_watch.tools.list_offers внутри run_js. Дай решение в текущем чате.`, Icon: "🧭"})
	topic := a.RegisterUIChangeTopic("product-decisions")
	policy := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "private_model_route", Description: "Доступ закрытых снимков из чата с разрешенным локальным маршрутом модели. Значение задает владелец стенда после проверки конфигурации.", Default: "off", Pattern: "^(off|approved)$"})
	e := evidence.New(a.DB(), topic, policy)
	d := decision.New(a.DB(), topic)
	bridge := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_notifications", Description: "Установите bridge только после настройки разрешённого Airlock-канала доставки.", Default: "off", Pattern: "^(off|bridge)$"})
	alerts := a.RegisterTopic(&agentsdk.Topic{Slug: "product_signals", Description: "Новые продуктовые сигналы владельца", Access: agentsdk.AccessUser, Enrollment: agentsdk.TopicEnrollmentDefaultOff, PerUser: true})
	smtp := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_smtp", Description: "Закрытая JSON-настройка внутреннего SMTP relay для одного владельца. Пустое значение отключает отправку.", Secret: true})
	p := product.Service{Decisions: d, Evidence: e, Notifications: &product.SignalNotifications{Enabled: bridge, Topic: alerts, DB: a.DB()}, Mail: &product.SignalMail{Config: smtp, DB: a.DB()}}
	h := handlers.New(p, a).WithCatalogOverview(registerCatalogOverview(a).Panel)
	registerPathCheck(a)
	a.RegisterTool(tool.Typed[evidence.Empty, evidence.Index]("list_evidence").Description("List immutable evidence snapshots imported by this user, newest first.").Execute(e.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Query, evidence.Result]("read_evidence").Description("Read observations with source locators. Filter by source, IDs or page with offset and limit; total_matched includes all matching rows.").Execute(e.Read).Build(), agentsdk.AccessUser)
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
	a.AddInstruction(&agentsdk.Instruction{Text: `Начинай разрешённые чтения сразу: вопрос пользователя уже поручает анализ. После inspectApp вызывай инструменты источника в этом же ходе. Если данные недоступны, дай результат доступной части и причину ограничения. План и вопрос о разрешении начать анализ не заменяют чтений.

В hosted-чате вызывай run_js с полями code и description. Объявленные методы platform и apps вызывай внутри code. Для вопроса о цене начни таким кодом, подставив каталоговое написание названного товара или бренда без падежного окончания:
const name = "<название из вопроса>";
await platform.inspectApp({app:"market-watch",section:"tools",operation:"list_offers"});
const page = await apps.market_watch.tools.list_offers({product_key:name,limit:12});
return {status:page.status,count:page.observations.length,warnings:page.warnings,offers:page.observations
  .filter(r=>r.status==="observed" && r.details?.match_status==="exact_sku" && r.product_key)
  .map(r=>({sku:r.product_key,title:r.details.name,url:r.source_ref?.url}))};

Если первый поиск пуст, попробуй другое написание производителя или короткое название без общих слов. При полной странице проверь следующие по offset. Возьми точный SKU из подходящих строк. До ответа выполни compare_offers({sku}) по релевантным артикулам через apps.market_watch.tools. Сравнивай каждый артикул отдельно. Для вывода используй сопоставимую группу с одинаковой лицензией, известным количеством и основанием цены. Поясняй, сколько сопоставимых предложений или магазинов в знаменателе. Строки с неизвестным количеством оставляй отдельно. Текущую цену датируй и показывай отдельно от истории.

Если вопрос также о продажах, в том же ходе открой metrika-analyst через platform.inspectApp и вызови scan_watchdog_series({}). Используй discover_watchdog_series({}) для привязки товара к рядам. Просматривай следующие страницы scan по next_offset. Сверяй dataset_id и origin между ответами. При рабочем разборе бери активный набор владельца с origin=imported_historical, отдавая приоритет повторившимся отклонениям по confirmed_windows. Синтетический набор используй только при явном выборе и называй его происхождение. При отсутствии данных или отказе назови ограничение, не подменяя набор старой карточкой или другим товаром. При отказе в доступе или явной просьбе открыть закрытую доску предложи ссылку private_review_link.

Для сопоставимого падения вызови collect_selected_watchdog_case с выбранными dataset_id и candidate_id. При alert передай document объектом в import_evidence, сохранив source_policy. После успешного импорта вызови investigate_orders с snapshot_id и signal_id из first_alert_signal_id. Об ошибке импорта скажи прямо. Выпуск, разрыв учёта или сбой корзины упоминай только по строкам актуального набора. По соответствующей просьбе используй check_purchase_path и platform.analyzeImage. Сохраняй дату проверки и ссылки.

Размер падения бери из first_alert_magnitude, signal_contrast или window_contrast точного сигнала. Это сравнение средних двух полных четырёхнедельных окон. Отдельно обозначай контроль, порог и сравнение двух сырых недель. Если превышение было одно, а следующая неделя неполная, скажи, что сигнал замечен и нужна ещё полная неделя. Сегодняшняя цена сама по себе не объясняет историческое падение. При несогласованной старой карточке используй первоисточник.

Ответ директору — простым русским без внутренних имён и статусов. Три короткие строки: решение, свидетельство, следующий шаг. На вопрос о цене в первой строке скажи, стоит ли менять её, и приведи проверенный масштаб паритета, если он есть. Детали наблюдений, сравнения и проверки оставь справа. Недельную сумму в ₽ рассчитывай только по проверенным потерянным за неделю заказам в штуках и подтверждённому вкладу или стоимости заказа. Если есть лишь индекс, назови эти недостающие данные. Недельная сумма и окно измерения после релиза — разные величины.

Цены не меняй. После предложения задачи сразу дай card_url с единственным действием «Создать эту задачу» на карточке. Не проси ответить «да» и не имитируй POST подтверждения. После клика прочитай квитанцию. Для очередности сохранённых проверок используй rank_hypotheses, для результата после релиза — evaluate_hypothesis.`})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что требует внимания?", Prompt: "Что сейчас требует внимания в продажах?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что стоит проверить?", Prompt: "Посмотри, где могло возникнуть изменение, и предложи, что проверить первым."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось после релиза?", Prompt: "Что мы уже можем сказать об эффекте изменений?"})
	return a
}
