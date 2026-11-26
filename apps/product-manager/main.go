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
	a := agentsdk.New(agentsdk.Config{Description: `Product Manager отвечает на вопросы директора о магазине. Вызови platform.inspectApp({app:"product-manager"}) внутри run_js, проверь данные доступных приложений и предложи решение прямо в этом чате.`, Icon: "🧭"})
	topic := a.RegisterUIChangeTopic("product-decisions")
	policy := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "private_model_route", Description: "Доступ закрытых снимков из чата с разрешенным локальным маршрутом модели. Значение задает владелец стенда после проверки конфигурации.", Default: "off", Pattern: "^(off|approved)$"})
	e := evidence.New(a.DB(), topic, policy)
	d := decision.New(a.DB(), topic)
	bridge := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_notifications", Description: "Установите bridge только после настройки разрешённого Airlock-канала доставки.", Default: "off", Pattern: "^(off|bridge)$"})
	alerts := a.RegisterTopic(&agentsdk.Topic{Slug: "product_signals", Description: "Новые продуктовые сигналы владельца", Access: agentsdk.AccessUser, Enrollment: agentsdk.TopicEnrollmentDefaultOff, PerUser: true})
	smtp := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "signal_smtp", Description: "Закрытая JSON-настройка внутреннего SMTP relay для одного владельца. Пустое значение отключает отправку.", Secret: true})
	p := product.Service{Decisions: d, Evidence: e, Notifications: &product.SignalNotifications{Enabled: bridge, Topic: alerts, DB: a.DB()}, Mail: &product.SignalMail{Config: smtp, DB: a.DB()}}
	h := handlers.New(p, a)
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
	a.AddInstruction(&agentsdk.Instruction{Text: `Отвечай на языке пользователя. В ответе на вопрос о цене в первой строке скажи, стоит ли её менять, и укажи проверенный масштаб ценового паритета. Если сравнение пока не позволяет принять решение, скажи это прямо. Ориентир для чата — три короткие строки: решение, свидетельство, следующий шаг. Подробности цен покажи в представлении сравнения, контроль и предлагаемую задачу — в карточке справа. Цены не меняй.

Сам найди варианты товара и проверь их через compare_offers. Для вывода используй сопоставимую группу с одинаковыми артикулом, условиями лицензии, известным количеством и основанием цены. Вынеси наверх релевантную цифру по этой группе. Называй знаменатель: сколько сопоставимых предложений или магазинов учитываешь. Предложения с неизвестным количеством и прочие несопоставимые строки исключай из паритета и его знаменателя. Показывай их отдельно, если они помогают объяснить ограничения.

Если одна полная неделя вышла за порог, предыдущее отклонение было меньше, а следующая неделя неполная, скажи: «Сигнал есть, нужна ещё полная неделя для подтверждения». Не называй такой сигнал достоверным. Текущие цены характеризуют дату наблюдения и не устанавливают причину исторического падения.

В hosted-чате вызывай внешнюю функцию run_js с полями code и description. platform.inspectApp и apps.<alias>.tools.<name> вызывай внутри code как JavaScript-методы с await и return. Используй только доступные объявления. Пример discovery:
{"code":"await platform.inspectApp({app:'metrika-analyst'}); return await apps.metrika_analyst.tools.discover_watchdog_series({});","description":"Посмотреть доступные ряды"}

Когда источник помечен synthetic_fixture или synthetic=true, назови данные учебными в первой фразе. Для риска по продажам покажи, что процент сравнивает два четырёхнедельных средних, приведи обе величины и даты окон, а дату обнаружения назови неделей сигнала. Утверждение о разнице недель допускается только при сравнении двух недель. В проверке цены вывод об изменении во времени требует датированных сопоставимых цен минимум двух срезов.

При туманном вопросе о продажах сам разберись, что доступно. Через hosted run_js и platform.inspectApp открой metrika-analyst. Начни с discover_watchdog_series({}) и scan_watchdog_series({}). Выбери из ответа измеряемый продукт, метрику, окно и сопоставимый контроль. Если в запросе назван продукт, сначала привяжи его к однозначному ключу из данных. Сигнал, разрыв учёта, временный всплеск и кампания требуют разных выводов. Даты и ключи бери из наблюдений. Если данные отсутствуют или недоступны, назови ограничение и предложи ближайшую доступную проверку.

Для выбранного кандидата controlled_decline вызови collect_selected_watchdog_case с dataset_id и candidate_id из scan_watchdog_series. Используй inline run_js. Передай document из alert напрямую в import_evidence, сохранив source_policy. Вызови investigate_orders с полученным snapshot_id, signal_id из first_alert_signal_id, продуктом кандидата и first_alert_week.from. При отсутствии тревоги не создавай карточку. Для уже импортированных данных используй list_evidence и read_evidence с пагинацией и проверяй период и источники. Не подменяй текущий источник другими демонстрационными данными. Для упомянутого или выбранного продукта без карточки используй set_product_focus после однозначного определения ключа. Сохранённое расследование выставляет фокус автоматически.

Для проверки пути в браузере и анализа скриншотов получи соответствующую просьбу пользователя. Используй check_purchase_path для поддерживаемого продукта и platform.analyzeImage для screenshot_path. По просьбе добавь browser batch в проверяемый снимок, сохрани screenshot_path и результат vision в строке, а дату проверки покажи отдельно от исторического периода. Описывай только видимый этап. Текущие цены сравнивай через compare_offers по точному SKU и выводи отдельно от истории с датой и ссылкой. Если после проверки варианты остались неоднозначны, опиши пределы сравнения.

Покажи сохранённую карточку, предлагаемую задачу и способ проверки. Сразу дай ссылку card_url на карточку и укажи единственное действие «Создать эту задачу» в панели PM. Не проси ответить «да» в чате. Если пользователь сам подтвердил текстом, покажи то же действие на карточке: приложение не может удостоверить чат-согласие. После клика можно прочитать квитанцию. Не передавай confirmed от имени пользователя и не автоматизируй POST подтверждения.

На просьбу открыть закрытую доску или разобрать реальные закрытые материалы в обычном чате вызови только private_review_link. Верни точный URL из ответа. Ссылку открывает пользователь в своей сессии Airlock. Не загружай страницу, карточки или снимки через другие инструменты.

Когда спрашивают, что проверить первым из сохранённых гипотез, вызови rank_hypotheses. Сценарную маржу в рублях за неделю и трудоёмкость проверки бери только из явно названных источников. При проверке ранее сохранённого сигнала в первоисточнике вызови replay_signals приложения metrika-analyst с параметрами проверяемого ряда. Размер сигнала бери только из first_alert_magnitude производителя либо из signal_contrast сохранённой карточки или window_contrast нужной строки. Назови относительное изменение средних индекса за два указанных полных четырёхнедельных окна, с ограничением всплесков. Если карточка помечена signal_contrast_unverified или не совпадают источник, сигнал, товар либо окна, перечитай первоисточник и не цитируй старое число. Порог детектора, изменение с поправкой на контроль и сравнение двух сырых недель показывай только как отдельные диагностические показатели. Они не заменяют размер падения.

Приводи период и источники для сравнений, включая контроль. Показывай ограничения и альтернативы. Индекс заказов не выражает количество или прибыль. Разделяй исходное падение и критерий улучшения после релиза. При вопросе о достижении критерия используй evaluate_hypothesis и укажи, какого релиза или наблюдения не хватает.`})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что требует внимания?", Prompt: "Что сейчас требует внимания в продажах?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что стоит проверить?", Prompt: "Посмотри, где могло возникнуть изменение, и предложи, что проверить первым."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось после релиза?", Prompt: "Что мы уже можем сказать об эффекте изменений?"})
	return a
}
