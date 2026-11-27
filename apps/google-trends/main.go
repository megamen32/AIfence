package main

import (
	"agent/handlers"
	"agent/integrations"
	"agent/trends"
	"agent/views"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Google Trends: сравнение поискового интереса к продуктам, динамика и региональные гипотезы.", Icon: "📈"})
	mcp := a.RegisterMCP(&agentsdk.MCP{
		Slug: "hasdata_google_trends", Name: "Google Trends · HasData", URL: trends.Endpoint,
		AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingShared,
		AuthInjection: agentsdk.AuthInjection{Type: agentsdk.AuthInjectAPIKey, Name: "x-api-key"},
	})
	service := trends.NewService(integrations.NewHasData(mcp), nil)
	tools := trends.NewTools(service)
	pages := handlers.New(service)
	a.RegisterTool(tool.Typed[trends.Query, trends.Result]("compare_interest").Description("Сравнить 2–5 фраз одним запросом Google Trends в общей шкале 0–100. Один период, регион, категория и источник поиска. Возвращает исходные признаки пропусков и частичных интервалов; средние только по общему набору завершенных интервалов. Даты YYYY-MM-DD; без обеих дат последние 12 полных месяцев.").Execute(tools.Compare).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[trends.Query, trends.Result]("regional_interest").Description("География интереса к 1–5 фразам одним запросом Google Trends. Числа являются относительными индексами, не объемом рынка. Сохраняет geo, названия регионов и признаки отсутствующих данных.").Execute(tools.Regional).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[trends.Query, trends.Result]("related_queries").Description("Связанные запросы для одной фразы. Top: индекс 0–100; Rising: процент роста или Breakout, это разные показатели.").Execute(tools.Related).Build(), agentsdk.AccessUser)
	a.AddInstruction(&agentsdk.Instruction{Access: []agentsdk.Access{agentsdk.AccessUser, agentsdk.AccessAdmin}, Text: "Используй compare_interest, regional_interest и related_queries для выводов: они нормализуют готовый MCP HasData. Для сравнения отправляй все 2–5 фраз одним вызовом compare_interest. Не объединяй индексы разных запросов, периодов, регионов, свойств поиска и comparison_id, не складывай индексы и не переводи их в число запросов, покупателей, продажи или долю рынка. При вопросе о продукте на фоне рынка выбирай вместе название конкретного продукта и понятную рыночную формулировку; поясни, какие фразы сравниваешь. Если продукт или рынок неясен, уточни это. Используй фактические Request и Source. Без периода используются последние 12 полных месяцев; сообщай их явно. Google может вернуть неделю, пересекающую границу запрошенного диапазона: сохраняй исходные интервалы. has_data=false означает пропуск; подтвержденный ноль сохраняй нулем. Отсутствующие has_data/is_partial остаются неизвестными, <1 является цензурированным значением. Для сравнения средних используй только готовые summaries с общими завершенными интервалами и показывай coverage. Если средних нет, не считай их сам по разным наборам недель. Breakout не является индексом или точным процентом. Ошибки credentials_required, rate_limited, quota_exhausted и insufficient_data не означают нулевой интерес. Не повторяй запрос автоматически при429, не обходи ограничение другим IP, аккаунтом или cookies. Для общего вопроса достаточно одного релевантного метода; связанные запросы и географию запрашивай только по задаче. Внешний поисковый интерес помогает сформировать гипотезу о спросе. Для диагностики проблемы воронки нужны отдельные внутренние данные о трафике и конверсиях, не придумывай их и не выдавай корреляцию за причину. Не передавай источнику закрытые материалы, покупателей, интервью и финансовые данные. Отвечай по-русски, кратко, с периодом, регионом и ссылкой source.explore_url."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Спрос или воронка", Prompt: "Продажи просели. Стали ли меньше искать Kaspersky Endpoint Security и антивирусы для бизнеса?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Сравнить продукты", Prompt: "Как интерес к Kaspersky Endpoint Security менялся на фоне Astra Linux?"})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Региональные гипотезы", Prompt: "В каких регионах стоит проверить спрос на Astra Linux?"})
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/", Handler: pages.Home, Access: agentsdk.AccessUser, Description: "Исследование внешнего спроса"})
	a.RegisterRoute(&agentsdk.Route{Method: "POST", Path: "/query", Handler: pages.Query, Access: agentsdk.AccessUser, Description: "Запрос Google Trends с общей шкалой"})
	a.RegisterStaticAsset(&agentsdk.StaticAsset{Name: views.AppCSSName, ContentType: "text/css; charset=utf-8", Data: views.AppCSS})
	return a
}
