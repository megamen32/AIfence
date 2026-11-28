package main

import (
	_ "embed"
	"html/template"
	"net/http"
	"strings"

	"agent/competitor"
	"github.com/airlockrun/agentsdk"
)

//go:embed ui.html
var uiHTML string

var uiPage = template.Must(template.New("competitor-research").Parse(uiHTML))

func main() { newAgent().Serve() }

func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{
		Description: "Конкуренты: поиск по сайтам, проверяемые наблюдения и таблица с источниками.",
		Icon:        "🔎",
	})
	a.RegisterModel(&agentsdk.ModelSlot{Slug: "competitor_search", Capability: agentsdk.CapSearch, Description: "Web-поиск кандидатов и публичных источников конкурентов"})
	a.RegisterModel(&agentsdk.ModelSlot{Slug: "competitor_analysis", Capability: agentsdk.CapText, Description: "Синтез и объяснение результатов конкурентного исследования"})
	competitor.Register(a)
	a.RegisterRoute(&agentsdk.Route{Method: http.MethodGet, Path: "/", Access: agentsdk.AccessUser, Description: "Форма конкурентного исследования", Handler: competitorResearchPage})
	a.AddInstruction(&agentsdk.Instruction{Text: "Ты — отдельное приложение Competitive Research. Product Manager, OmniRoute и конкретные провайдеры тебе неизвестны и не должны упоминаться пользователю. Рабочий цикл: из обычного запроса самостоятельно извлеки компанию, точный сайт, рынок, предложение и цель; вызови plan_competitor_research. Если список конкурентов пуст, вызови discover_competitor_candidates через Airlock search slot: в query обязательно включай точный домен целевого сайта и слова «конкуренты интернет-магазина программного обеспечения», а если результаты не называют конкретные домены — повтори discovery с уточнённой коммерческой категорией. Не выдумывай домены и отделяй прямых конкурентов от связанных брендов и потенциальных кандидатов. После discovery подготовь для каждого найденного домена минимум по одной строке в шести блоках: search, price, purchase, account, service, ux; в каждой строке сохраняй прямой source_url и дату проверки. После discovery или если пользователь уже дал домены обязательно вызови inspect_competitor_sites с study_id и публичными доменами: он запускает встроенный headless Playwright и добавляет только наблюдаемые DOM-строки для search, purchase, account, service и ux; формы не отправляй, логины и cookies не используй. При объединении заменяй discovery-строки со status=unknown для того же competitor и block на строки Playwright, а не дублируй их. Цену это приложение не собирает: блок price получает проверенные строки из существующего внешнего источника Mac M1, а без них помечается status=not_connected, value=Н/Д и не влияет на score. Если блок не проверен, ставь status=unknown или missing, value=Н/Д, score не заполняй; отсутствие данных не превращай в ноль. Для score используй только шкалу 1–5 и только при наличии наблюдения. Сначала вызови summarize_competitor_matrix с study_id из плана и теми же rows; используй его weighted score, covered_weight и limitations, не выдавай unknown за ноль. Затем вызови export_competitor_csv со study_id из плана и этими rows: CSV должен быть приложен пользователю. Презентацию не создавай без отдельной просьбы. Airlock сам выбирает модели и провайдеры capability slots в админке."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Найти конкурентов", Prompt: "Я представляю такую-то компанию и продаю такой-то продукт. Найди моих основных конкурентов, объясни критерии отбора и подготовь CSV-таблицу сравнения с источниками."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Сравнить сайты", Prompt: "Сравни сайты этих компаний по ассортименту, цене, пути покупки, сервису и UX. Не отправляй формы и приложи CSV."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Обновить исследование", Prompt: "Обнови конкурентное исследование по новым публичным источникам и отметь, что изменилось с прошлой таблицей."})
	return a
}

type competitorResearchPageData struct {
	Company     string
	Site        string
	Market      string
	Offer       string
	Goal        string
	Competitors string
	Submitted   bool
}

func competitorResearchPage(w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	data := competitorResearchPageData{
		Company:     strings.TrimSpace(q.Get("company")),
		Site:        strings.TrimSpace(q.Get("site")),
		Market:      strings.TrimSpace(q.Get("market")),
		Offer:       strings.TrimSpace(q.Get("offer")),
		Goal:        strings.TrimSpace(q.Get("goal")),
		Competitors: strings.TrimSpace(q.Get("competitors")),
		Submitted:   q.Get("submitted") == "1",
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return uiPage.Execute(w, data)
}
