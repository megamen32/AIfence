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
	a.RegisterTool(tool.Typed[decision.SendInput, decision.Task]("send_to_backlog").Description("Only after explicit confirmation, create or return one tagged local backlog task. Criterion applies ONLY after a future corrective release. Return the persisted task. For effect status call evaluate_hypothesis.").Execute(p.Send).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.EvaluateInput, decision.Assessment]("evaluate_hypothesis").Description("Check for post-release effect. Supply only imported observation IDs when linking a corrective release or measured windows.").Execute(p.Evaluate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.InvestigateInput, decision.Record]("investigate_orders").Description("Save or read back a proposal from an imported sales watchdog.alert=true. Optional product_key and week_from (YYYY-MM-DD Monday) select the alert; otherwise use the latest. Uses the same snapshot for context and browser results. For older snapshots without watchdog rows, use precomputed full-week family_orders_idx.relative_change_pct.").Execute(p.Investigate).Build(), agentsdk.AccessUser)
	for _, r := range []*agentsdk.Route{
		{Method: "POST", Path: "/investigate", Handler: investigatePage(p), Description: "Разобрать изменение самостоятельных заказов"},
		{Method: "GET", Path: "/", Handler: h.Home, Description: "Гипотезы, источники и локальный бэклог"},
		{Method: "GET", Path: "/board", Handler: h.Board, Description: "Карточки продуктовых решений"},
		{Method: "POST", Path: "/edit", Handler: h.Edit, Description: "Отредактировать гипотезу перед подтверждением"},
		{Method: "POST", Path: "/approve", Handler: h.Approve, Description: "Подтвердить задачу локального бэклога"},
		{Method: "POST", Path: "/import", Handler: h.Import, Description: "Импортировать проверяемые наблюдения"},
	} {
		r.Access = agentsdk.AccessUser
		r.Handler = evidence.LocalHTTP(r.Handler)
		a.RegisterRoute(r)
	}
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppCSSName, ContentType: "text/css; charset=utf-8", Data: views.AppCSS})
	a.AddInstruction(&agentsdk.Instruction{Text: `Отвечай на языке пользователя.

При запросе сторожа, проверки сигнала или прогона по истории первым вызови top-level run_js_file для /home/Documents/AIfence-investigate-signal.js. Передай args.series с исходными product_key и control_product_key в snake_case, metric_key, from и to. Передавай check_browser=true только по просьбе пользователя, confirm_backlog=true только после явного подтверждения задачи. При недоступном workflow сообщи причину. False, null и несопоставимые недели не дают тревогу. Используй таймлайн и сохранённую карточку из ответа workflow. Browser path_check говорит о пройденном пути и наблюдении запроса в дату проверки. Продуктовый эффект требует отдельного измерения.

Для ручного разбора уже импортированных данных используй list_evidence и read_evidence с пагинацией. Для отдельного запроса свежих данных используй доступные Market Watch и Metrika Analyst через platform.inspectApp и collect-evidence. Используй разрешённые источники. Приводи период и источник у чисел. Ссылайся на обе точки каждого сравниваемого ряда, включая контроль, или на вычисленное наблюдение со всеми выборками. Учитывай ограничения, альтернативы и существующие задачи. Индекс заказов не выражает количество или прибыль.

Для ручного save_hypothesis сначала проверь list_hypotheses, затем заполни aifence.v1, proposed, списки ID, даты, критерий будущего измерения и economic_effect {status: unknown, assumptions: []}. Разделяй исходное падение и критерий улучшения после релиза. Для вывода о выполнении критерия вызывай evaluate_hypothesis. При awaiting_measurement укажи недостающий релиз или наблюдение. Создавай задачу только после явного подтверждения пользователя и помечай бэклог как локальный.`})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Разобрать падение заказов", Prompt: "Что изменилось в самостоятельных заказах по доступным источникам? Сопоставь продажи, релизы, исследования и бэклог. Сохрани гипотезу с самым дешевым способом проверки."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Проверить эффект", Prompt: "По каким гипотезам уже можно оценить эффект релиза? Покажи критерии, доступные замеры и недостающие данные."})
	return a
}
