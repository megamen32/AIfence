package main

import (
	"agent/demand"
	"agent/handlers"
	"agent/views"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func main() { newAgent().Serve() }

func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Яндекс Вордстат: поисковый спрос, динамика и регионы с указанием периода и состояния источника.", Icon: "📊"})
	mcp := a.RegisterMCP(&agentsdk.MCP{Slug: "wordstat", Name: "Яндекс Wordstat", URL: configuredMCPEndpoint(), AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingShared})
	service := demand.New(mcp)
	pages := handlers.New(service)
	a.RegisterTool(tool.Typed[demand.TopInput, demand.Result]("top_queries").Description("Запросы и похожие формулировки Wordstat. Всегда возвращает состояние источника. Точное окно top_requests после остановки обновлений неизвестно.").Execute(service.Top).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[demand.DynamicsInput, demand.Result]("demand_dynamics").Description("Помесячная динамика спроса для точной фразы. Задайте from_month/to_month в формате YYYY-MM. Последний доступный полный месяц: 2026-09. Более поздний период возвращает unavailable_period, без подмены дат.").Execute(service.Dynamics).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[demand.RegionsInput, demand.Result]("regional_demand").Description("Распределение спроса по регионам или городам. У этого метода нет фильтра устройств. Точное окно источника не сообщается.").Execute(service.Regions).Build(), agentsdk.AccessUser)
	a.AddInstruction(&agentsdk.Instruction{Access: []agentsdk.Access{agentsdk.AccessUser, agentsdk.AccessAdmin}, Text: "Используй top_queries, demand_dynamics и regional_demand для выводов о спросе: они проверяют период и статус источника. Выбирай только нужные методы: для общего вопроса о спросе начни с top_queries; динамику вызывай при вопросе об изменении во времени, а распределение по регионам при вопросе о географии. regional_demand самостоятельно получает справочник и возвращает region_id, region_name и region_lookup_status. Используй эти названия; отсутствие однозначного имени отмечай по region_name_status, не угадывай. При partial или unavailable заверши ответ с полученной статистикой и кодами регионов, объясни отсутствие названий; не ищи справочник через прямой MCP, веб-поиск или другие источники. Региональные строки могут включать вложенные территории; их частотности не складывай в общий спрос. Если region_ids не передан, региональный фильтр отсутствует: это все регионы, а не Россия. Указывай фактически использованные фильтры и известный период результата. На 27.11.2026 официальная документация Wordstat сообщает, что через API недоступны данные после 07.10.2026; последний полный месяц для сравнения 2026-09. Не называй top/regions данными за последние 30 дней от сегодняшней даты: точное окно неизвестно. Не подменяй запрошенный недоступный период сентябрём. При credentials_required или ошибке статистики нет, это не нулевой спрос. Поисковые запросы не равны покупателям, заказам или выручке. Никогда не складывай частотности разных основных и связанных фраз, включая суммы по «коммерческим» кластерам: фразы могут пересекаться. Слово «скачать» само по себе не подтверждает готовность купить. Сохраняй операторы, регион, устройства и период. Для общего вопроса о спросе достаточно частотности исходной фразы, фактического регионального охвата, периода и ограничений источника. Отвечай 3–5 короткими абзацами; не добавляй непрошенные кластерные суммы или таблицу топ-10, если детализация не запрошена. Для сравнения брендов сначала раздели коммерческие формулировки, версии и потребительские/корпоративные продукты. Не загружай сюда закрытые материалы, интервью, продажи и данные покупателей. Отвечай кратко, по-русски."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Спрос на продукт", Prompt: "Покажи спрос по фразе «касперский для бизнеса». Укажи период, регион и ограничения источника."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Как меняется интерес", Prompt: "Касперским вообще стали меньше интересоваться?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "География спроса", Prompt: "Касперского в каких городах чаще ищут?"})
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/", Handler: pages.Home, Access: agentsdk.AccessUser, Description: "Исследование поискового спроса"})
	a.RegisterRoute(&agentsdk.Route{Method: "POST", Path: "/query", Handler: pages.Query, Access: agentsdk.AccessUser, Description: "Запрос Wordstat с проверкой периода"})
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppCSSName, ContentType: "text/css; charset=utf-8", Data: views.AppCSS})
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppJSName, ContentType: "text/javascript; charset=utf-8", Data: views.AppJS})
	return a
}
