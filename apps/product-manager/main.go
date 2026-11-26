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
	a := agentsdk.New(agentsdk.Config{Description: `Сводка продаж и предложение цены. Сводка только читает уже собранные сведения. Вызов run_js для чтения: {"code":"return await apps.product_manager.tools.review_sales_and_prices({summary:true});","description":"Прочитать наблюдения продаж и цен","request_confirmation":false}. Если imported_price_observations равно нулю, проверь предложения по названию продукта в каталоге. В чат директору отправь максимум три коротких предложения без вступления, заголовков, списков и внутренней терминологии. Вывод о цене основывай на price_evidence_status. При insufficient_comparable_quotes снижение цены данными не обосновано. Само падение продаж не подтверждает, что скидка поможет. Начни прямо с решения о цене. Обоснуй его условиями двух магазинов. Из paired_sales_comparison всегда называй контроль и основной товар в одной фразе: сравни их изменения за одни и те же окна. Используй одинаковое округление с панелью. Закончи предложением проверки; ущерб в рублях оценивай отдельно.`, Icon: "🧭"})
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
	a.RegisterTool(tool.Typed[handlers.BriefQuery, handlers.SalesBrief]("review_sales_and_prices").Description("Read the signed-in user’s active sales alerts and imported SKU quotes from the same evidence as the panel. Includes measured periods, source references and comparable license groups. Read-only; use request_confirmation=false. Hosted run_js code: return await apps.product_manager.tools.review_sales_and_prices({summary:true});").Execute(h.SalesBrief).Build(), agentsdk.AccessUser)
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
{"code":"return await apps.product_manager.tools.review_sales_and_prices({summary:true});","description":"Прочитать наблюдения продаж и цен","request_confirmation":false}
Пустой результат означает, что данные не получены. Проверь наличие return. Отвечай на текущем ходе. При отсутствии нужных наблюдений проверь источники через inspectApp: каталог по названию, затем сравнение точного артикула, для продаж — доступные ряды и полный период. Выбирай источники по вопросу и происхождению. При отказе предложи private_review_link. Синтетические данные используй только по явному выбору.

Ответ — три предложения директору. Первое начинается с рекомендации и кратко объясняет её проверенными условиями предложения. Во втором приведи названия и проценты обоих продуктов из paired_sales_comparison в одной фразе, с границами окон. Снижение основного ряда без сравнения с контролем вводит в заблуждение. В третьем предложи следующую проверку. Денежный эффект рассчитывай только при наличии объёма в штуках и вклада на заказ. Отсутствие денежной оценки само по себе не объясняет цену. Краткие источники бери из снимка. Не добавляй вступление, заголовки или перечень дальнейших исследований.

При расследовании нового сигнала импортируй выбранный документ с исходным source_policy и используй его идентификаторы. Для карточки проверь контекст релиза, источники и достаточность подтверждений. По запросу проверь путь покупки. Предлагай действие до выполнения. Для создания задачи дай ссылку на карточку с «Создать эту задачу». Не проси ответить «да» в чате. Квитанцию читай после отдельного подтверждения на карточке. Для приоритетов используй rank_hypotheses, для измерения результатов — evaluate_hypothesis.`})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что требует внимания?", Prompt: "Что сейчас требует внимания в продажах?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что стоит проверить?", Prompt: "Посмотри, где могло возникнуть изменение, и предложи, что проверить первым."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось после релиза?", Prompt: "Что мы уже можем сказать об эффекте изменений?"})
	return a
}
