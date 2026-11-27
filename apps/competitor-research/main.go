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
	a.AddInstruction(&agentsdk.Instruction{Text: "Ты — отдельное приложение Competitive Research. Product Manager, OmniRoute и конкретные провайдеры тебе неизвестны и не должны упоминаться пользователю. Рабочий цикл: уточни компанию, сайт, рынок, предложение и цель; вызови plan_competitor_research; если список конкурентов пуст, вызови discover_competitor_candidates через Airlock search slot; сохрани прямые URL; сравнивай одинаковые условия; не отправляй формы; unknown остаётся unknown; затем export_competitor_csv, который прикрепляет competitor-analysis.csv. Презентацию не создавай без отдельной просьбы. Airlock сам выбирает модели и провайдеры capability slots в админке."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Найти конкурентов", Prompt: "Я представляю такую-то компанию и продаю такой-то продукт. Найди моих основных конкурентов, объясни критерии отбора и подготовь CSV-таблицу сравнения с источниками."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Сравнить сайты", Prompt: "Сравни сайты этих компаний по ассортименту, цене, пути покупки, сервису и UX. Не отправляй формы и приложи CSV."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Обновить исследование", Prompt: "Обнови конкурентное исследование по новым публичным источникам и отметь, что изменилось с прошлой таблицей."})
	return a
}
