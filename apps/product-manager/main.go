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
	p := product.Service{Decisions: d, Evidence: e}
	h := handlers.New(p)
	a.RegisterTool(tool.Typed[evidence.Empty, evidence.Index]("list_evidence").Description("List immutable evidence snapshots imported by this user, newest first.").Execute(e.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Query, evidence.Result]("read_evidence").Description("Read observations with source locators. Filter by source, IDs or page with offset and limit; total_matched includes all matching rows.").Execute(e.Read).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.ImportInput, evidence.Summary]("import_evidence").Description("Import authorized aifence.v1 observations, a case or shared evidence workflow document. Identical contents reuse the immutable ID.").Execute(e.Import).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.SaveInput, decision.Record]("save_hypothesis").Description("Write a proposal. Set hypothesis.schema_version to the exact string aifence.v1, status to proposed, economic_effect to {status:unknown,assumptions:[]}. Pass signal_ids, support_ids, counter_ids, unknowns as arrays (use [] when empty). Pass the immutable evidence_snapshot_id from list_evidence and expected_version=0 on create. A criterion has ISO baseline_from/to, observation_days, metric_key and rule. Rules: relative_change_pct >= N, or difference_in_relative_changes_pp >= N with control_product_key.").Execute(p.Save).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.GetInput, decision.Record]("get_hypothesis").Description("Read saved hypothesis, local backlog task and evaluation.").Execute(p.Get).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[evidence.Empty, decision.ListResult]("list_hypotheses").Description("List this user's records and status.").Execute(p.List).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[decision.SendInput, decision.Task]("send_to_backlog").Description("Only after explicit confirmation, create or return one tagged local backlog task. Criterion applies ONLY after a future corrective release. Return the persisted task. For effect status call evaluate_hypothesis.").Execute(p.Send).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.EvaluateInput, decision.Assessment]("evaluate_hypothesis").Description("Check for post-release effect. Supply only imported observation IDs when linking a corrective release or measured windows.").Execute(p.Evaluate).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[product.InvestigateInput, decision.Record]("investigate_orders").Description("Only for precomputed sales observations metric_key family_orders_idx.relative_change_pct with details.variant=full_weeks, baseline_mean and current_mean. For raw order-index rows, use read_evidence and save_hypothesis. Detect the strongest full-week drop and save a proposal.").Execute(p.Investigate).Build(), agentsdk.AccessUser)
	for _, r := range []*agentsdk.Route{
		{Method: "POST", Path: "/investigate", Handler: investigatePage(p), Description: "Разобрать изменение самостоятельных заказов"},
		{Method: "GET", Path: "/", Handler: h.Home, Description: "Гипотезы, источники и локальный бэклог"},
		{Method: "GET", Path: "/board", Handler: h.Board, Description: "Карточки продуктовых решений"},
		{Method: "POST", Path: "/edit", Handler: h.Edit, Description: "Отредактировать гипотезу перед подтверждением"},
		{Method: "POST", Path: "/approve", Handler: h.Approve, Description: "Подтвердить задачу локального бэклога"},
		{Method: "POST", Path: "/import", Handler: h.Import, Description: "Импортировать проверяемые наблюдения"},
	} {
		r.Access = agentsdk.AccessUser
		a.RegisterRoute(r)
	}
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppCSSName, ContentType: "text/css; charset=utf-8", Data: views.AppCSS})
	a.AddInstruction(&agentsdk.Instruction{Text: "Отвечай на языке пользователя. Изучи list_evidence, затем read_evidence по источникам с пагинацией. Для свежих данных используй доступные Market Watch и Metrika Analyst через platform.inspectApp и штатный collect-evidence workflow. Используй только разрешенные источники. Подбирай версии самостоятельно из наблюдений. Приводи источник и период у чисел. При сравнении в сохраненной карточке укажи ID обеих точек каждого ряда, включая контроль, либо ID вычисленного наблюдения с обеими выборками. Показывай ограничения и альтернативы. Сверяй бэклог, предлагай дешевую проверку. Недельный индекс заказов не выражает количество или прибыль. Сначала посмотри list_hypotheses, переиспользуй ID для того же решения. Исходное падение и будущий эффект исправления относятся к разным периодам. Никогда не сравнивай исходное падение с критерием будущего улучшения. Для любого вывода о выполнении критерия вызови evaluate_hypothesis. При awaiting_measurement напиши, какого наблюдения не хватает; сравнение с порогом пока невозможно. Для save_hypothesis всегда указывай hypothesis.schema_version точным значением aifence.v1, status proposed, economic_effect.status unknown, economic_effect.assumptions [], списки ID, даты и критерий будущего измерения. Используй difference_in_relative_changes_pp >= 10 при выбранном контроле или relative_change_pct >= 10 без контроля, порог выбирай по задаче. Создавай задачу только если пользователь явно подтвердил перенос. Указывай что бэклог локальный. Не приписывай результат без замера после корректирующего релиза."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Разобрать падение заказов", Prompt: "Что изменилось в самостоятельных заказах по доступным источникам? Сопоставь продажи, релизы, исследования и бэклог. Сохрани гипотезу с самым дешевым способом проверки."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Проверить эффект", Prompt: "По каким гипотезам уже можно оценить эффект релиза? Покажи критерии, доступные замеры и недостающие данные."})
	return a
}
