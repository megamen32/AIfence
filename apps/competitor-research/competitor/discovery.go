package competitor

import (
	"context"
	"errors"
	"strings"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/message"
	"github.com/airlockrun/goai/stream"
	"github.com/airlockrun/goai/tool"
	"github.com/airlockrun/sol/websearch"
)

type discoveryInput struct {
	Query string `json:"query" description:"Commercial query or market phrase to search"`
	Count int    `json:"count,omitempty" description:"Number of search results, from 3 to 10"`
}

type discoveryResult struct {
	Query       string   `json:"query"`
	SearchText  string   `json:"search_text"`
	Analysis    string   `json:"analysis"`
	Limitations []string `json:"limitations"`
}

func registerDiscovery(a *agentsdk.Agent) {
	t := tool.Typed[discoveryInput, discoveryResult]("discover_competitor_candidates").
		Description("Run Airlock web search for a commercial query and synthesize candidate competitors. Return direct source URLs from the search text; never invent domains.").
		Execute(func(ctx context.Context, in discoveryInput) (discoveryResult, error) {
			query := strings.TrimSpace(in.Query)
			if query == "" {
				return discoveryResult{}, errors.New("query is required")
			}
			count := in.Count
			if count == 0 {
				count = 8
			}
			if count < 3 || count > 10 {
				return discoveryResult{}, errors.New("count must be between 3 and 10")
			}
			a := agentsdk.AgentFromContext(ctx)
			found, err := a.WebSearch(ctx, "competitor_search", websearch.Request{Query: query, Count: count})
			if err != nil {
				return discoveryResult{Query: query, Limitations: []string{"Airlock search capability is not configured or is unavailable; select a provider for competitor_search in Airlock Models."}}, nil
			}
			searchText := found.Text()
			summary, err := a.GenerateText(ctx, stream.Input{
				Model: a.LLM(ctx, "competitor_analysis"),
				Messages: []message.Message{
					message.NewSystemMessage("Ты аналитик рынка. Используй только приведённые результаты поиска. Не выдумывай домены и явно отмечай неизвестное."),
					message.NewUserMessage("Запрос: " + query + "\n\nРезультаты поиска:\n" + searchText + "\n\nНазови кандидатов в конкуренты, объясни критерии отбора и сохрани прямые URL."),
				},
			})
			if err != nil {
				return discoveryResult{Query: query, SearchText: searchText, Limitations: []string{"Синтез текста недоступен; используй исходный search_text и проверь ссылки вручную."}}, nil
			}
			return discoveryResult{
				Query:      query,
				SearchText: searchText,
				Analysis:   summary.Text,
				Limitations: []string{
					"Кандидаты основаны на публичном поиске; включение в список не подтверждает коммерческую сопоставимость.",
					"Проверь домен, редакцию продукта и актуальность цены перед экспортом.",
				},
			}, nil
		}).Build()
	a.RegisterTool(t, agentsdk.AccessUser)
}
