package main

import (
	"agent/handlers"
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/internal/product"
	"agent/views"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Продуктовые решения с проверяемыми источниками, задачами и оценкой после релиза.", Icon: "🧭"})
	topic := a.RegisterUIChangeTopic("product-decisions")
	policy := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "private_model_route", Description: "Доступ закрытых снимков из чата с разрешенным локальным маршрутом модели. Значение задает владелец стенда после проверки конфигурации.", Default: "off", Pattern: "^(off|approved)$"})
	e := evidence.New(a.DB(), topic, policy)
	d := decision.New(a.DB(), topic)
	bridge := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_notifications", Description: "Установите bridge только после настройки разрешённого Airlock-канала доставки.", Default: "off", Pattern: "^(off|bridge)$"})
	alerts := a.RegisterTopic(&agentsdk.Topic{Slug: "product_signals", Description: "Новые продуктовые сигналы владельца", Access: agentsdk.AccessUser, Enrollment: agentsdk.TopicEnrollmentDefaultOff, PerUser: true})
	smtp := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_smtp", Description: "Закрытая JSON-настройка внутреннего SMTP relay для одного владельца. Пустое значение отключает отправку.", Secret: true})
	p := product.Service{Decisions: d, Evidence: e, Notifications: &product.SignalNotifications{Enabled: bridge, Topic: alerts, DB: a.DB()}, Mail: &product.SignalMail{Config: smtp, DB: a.DB()}}
	h := handlers.New(p)
	registerPathCheck(a)
	a.RegisterTool(tool.Typed[evidence.Empty, evidence.Index]("list_evidence").Description("List immutable evidence snapshots imported by this user, newest first.").Execute(e.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Query, evidence.Result]("read_evidence").Description("Read observations with source locators. Filter by source, IDs or page with offset and limit; total_matched includes all matching rows.").Execute(e.Read).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.ImportInput, evidence.Summary]("import_evidence").Description("Import authorized aifence.v1 observations, a case or shared evidence workflow document. Identical contents reuse the immutable ID.").Execute(e.Import).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.SaveInput, decision.Record]("save_hypothesis").Description("Write a proposal. Set hypothesis.schema_version to the exact string aifence.v1, status to proposed, economic_effect to {status:unknown,assumptions:[]}. Pass signal_ids, support_ids, counter_ids, unknowns as arrays (use [] when empty). Pass the immutable evidence_snapshot_id from list_evidence and expected_version=0 on create. Optional fields: forecast, competing_explanations, evidence_level and browser_evidence_ids. path_verified requires an observed browser path_check for the same product; it checks the path, not causality. A criterion has ISO baseline_from/to, observation_days, metric_key and rule. Rules: relative_change_pct >= N, or difference_in_relative_changes_pp >= N with control_product_key.").Execute(p.Save).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.GetInput, decision.Record]("get_hypothesis").Description("Read saved hypothesis, local backlog task and evaluation.").Execute(p.Get).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, decision.ListResult]("list_hypotheses").Description("List this user's records and status.").Execute(p.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.GetInput, decision.Task]("send_to_backlog").Description("Read the receipt of a task already approved separately in the Product Manager panel. This tool cannot create a task.").Execute(p.BacklogReceipt).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.EvaluateInput, decision.Assessment]("evaluate_hypothesis").Description("Check for post-release effect. Supply only imported observation IDs when linking a corrective release or measured windows.").Execute(p.Evaluate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.InvestigateInput, decision.Record]("investigate_orders").Description("Save or read back a proposal from an imported sales watchdog.alert=true. Optional product_key and week_from (YYYY-MM-DD Monday) select the alert; otherwise use the latest. Uses the same snapshot for context and browser results. For older snapshots without watchdog rows, use precomputed full-week family_orders_idx.relative_change_pct.").Execute(p.Investigate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, product.Focus]("get_product_focus").Description("Read your current Product Manager panel selection.").Execute(p.GetFocus).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.Focus, product.Focus]("set_product_focus").Description("Focus your Product Manager panel on a product established from evidence or explicitly named by the user. Supply its product_key, or the saved hypothesis_id.").Execute(p.SetFocus).Build(), agentsdk.AccessUser)
	for _, r := range []*agentsdk.Route{
		{Method: "POST", Path: "/investigate", Handler: investigatePage(p), Description: "Разобрать изменение самостоятельных заказов"},
		{Method: "GET", Path: "/", Handler: h.Home, Description: "Гипотезы, источники и локальный бэклог"},
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
	a.AddInstruction(&agentsdk.Instruction{Text: `Отвечай на языке пользователя.

При туманном вопросе о продажах сам разберись, что доступно. Через hosted run_js и platform.inspectApp открой metrika-analyst. Начни с discover_watchdog_series({}) и scan_watchdog_series({}). Выбери из ответа измеряемый продукт, метрику, окно и сопоставимый контроль. Если в запросе назван продукт, сначала привяжи его к однозначному ключу из данных. Сигнал, разрыв учёта, временный всплеск и кампания требуют разных выводов. Даты и ключи бери из наблюдений. Если данные отсутствуют или недоступны, назови ограничение и предложи ближайшую доступную проверку.

Для выбранного кандидата controlled_decline вызови collect_selected_watchdog_case с dataset_id и candidate_id из scan_watchdog_series. Используй inline run_js. Передай document из alert напрямую в import_evidence, сохранив source_policy. Вызови investigate_orders с полученным snapshot_id, продуктом кандидата и first_alert_week.from. При отсутствии тревоги не создавай карточку. Для уже импортированных данных используй list_evidence и read_evidence с пагинацией и проверяй период и источники. Не подменяй текущий источник другими демонстрационными данными. Для упомянутого или выбранного продукта без карточки используй set_product_focus после однозначного определения ключа. Сохранённое расследование выставляет фокус автоматически.

Для проверки пути в браузере и анализа скриншотов получи соответствующую просьбу пользователя. Используй check_purchase_path для поддерживаемого продукта и platform.analyzeImage для screenshot_path. По просьбе добавь browser batch в проверяемый снимок, сохрани screenshot_path и результат vision в строке, а дату проверки покажи отдельно от исторического периода. Описывай только видимый этап. Текущие цены сравнивай через compare_offers по точному SKU и выводи отдельно от истории с датой и ссылкой. При неоднозначном совпадении предложи уточнить вариант.

Сначала покажи сохранённую карточку, предлагаемую задачу и способ проверки. Затем попроси подтверждение отдельной репликой. Исходный запрос и выбор подсказки согласия не дают. После явного ответа направь пользователя на текущую карточку с кнопкой подтверждения в панели PM. Чат может прочитать квитанцию после подтверждения в панели. Не передавай confirmed от имени пользователя и не автоматизируй POST подтверждения.

Приводи период и источники для сравнений, включая контроль. Показывай ограничения и альтернативы. Индекс заказов не выражает количество или прибыль. Разделяй исходное падение и критерий улучшения после релиза. При вопросе о достижении критерия используй evaluate_hypothesis и укажи, какого релиза или наблюдения не хватает.`})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что требует внимания?", Prompt: "Что сейчас требует внимания в продажах?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что стоит проверить?", Prompt: "Посмотри, где могло возникнуть изменение, и предложи, что проверить первым."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось после релиза?", Prompt: "Что мы уже можем сказать об эффекте изменений?"})
	return a
}
