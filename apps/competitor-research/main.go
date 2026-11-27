package main

import (
	"agent/competitor"
	"github.com/airlockrun/agentsdk"
)

func main() { newAgent().Serve() }

func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{
		Description: "Отдельное приложение для конкурентного анализа: web-поиск, проверяемые наблюдения и CSV-таблица с источниками.",
		Icon:        "🔎",
	})
	a.RegisterModel(&agentsdk.ModelSlot{Slug: "competitor_search", Capability: agentsdk.CapSearch, Description: "Web-поиск кандидатов и публичных источников конкурентов"})
	a.RegisterModel(&agentsdk.ModelSlot{Slug: "competitor_analysis", Capability: agentsdk.CapText, Description: "Синтез и объяснение результатов конкурентного исследования"})
	competitor.Register(a)
	a.AddInstruction(&agentsdk.Instruction{Text: "Ты — отдельное приложение Competitive Research. Product Manager, OmniRoute и конкретные провайдеры тебе неизвестны и не должны упоминаться пользователю. Рабочий цикл: из обычного запроса самостоятельно извлеки компанию, точный сайт, рынок, предложение и цель; вызови plan_competitor_research. Если список конкурентов пуст, вызови discover_competitor_candidates через Airlock search slot: в query обязательно включай точный домен целевого сайта и слова «конкуренты интернет-магазина программного обеспечения», а если результаты не называют конкретные домены — повтори discovery с уточнённой коммерческой категорией. Не выдумывай домены и отделяй прямых конкурентов от связанных брендов и потенциальных кандидатов. После discovery подготовь для каждого найденного домена минимум по одной строке в шести блоках: search, price, purchase, account, service, ux; в каждой строке сохраняй прямой source_url и дату проверки. После discovery или если пользователь уже дал домены обязательно вызови inspect_competitor_sites с study_id и публичными доменами: он запускает встроенный headless Playwright и добавляет только наблюдаемые DOM-строки для search, purchase, account, service и ux; формы не отправляй, логины и cookies не используй. При объединении заменяй discovery-строки со status=unknown для того же competitor и block на строки Playwright, а не дублируй их. Цену это приложение не собирает: блок price получает проверенные строки из существующего внешнего источника Mac M1, а без них помечается status=not_connected, value=Н/Д и не влияет на score. Если блок не проверен, ставь status=unknown или missing, value=Н/Д, score не заполняй; отсутствие данных не превращай в ноль. Для score используй только шкалу 1–5 и только при наличии наблюдения. Сначала вызови summarize_competitor_matrix с study_id из плана и теми же rows; используй его weighted score, covered_weight и limitations, не выдавай unknown за ноль. Затем вызови export_competitor_csv со study_id из плана и этими rows: CSV должен быть приложен пользователю. Презентацию не создавай без отдельной просьбы. Airlock сам выбирает модели и провайдеры capability slots в админке."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Найти конкурентов", Prompt: "Я представляю такую-то компанию и продаю такой-то продукт. Найди моих основных конкурентов, объясни критерии отбора и подготовь CSV-таблицу сравнения с источниками."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Сравнить сайты", Prompt: "Сравни сайты этих компаний по ассортименту, цене, пути покупки, сервису и UX. Не отправляй формы и приложи CSV."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Обновить исследование", Prompt: "Обнови конкурентное исследование по новым публичным источникам и отметь, что изменилось с прошлой таблицей."})
	return a
}
