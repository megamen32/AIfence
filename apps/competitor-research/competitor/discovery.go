package competitor

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

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
	Query       string                  `json:"query"`
	SearchText  string                  `json:"search_text"`
	Analysis    string                  `json:"analysis"`
	Limitations []string                `json:"limitations"`
	Rows        []competitorObservation `json:"rows,omitempty"`
}

var discoveryURLPattern = regexp.MustCompile("https?://[^\\s\\)\\]>\"]+")
var discoveryHostPattern = regexp.MustCompile("(?i)(?:www\\.)?[a-z0-9-]+\\.(?:ru|biz|com|net|io)\\b")

func extractDiscoveryRows(query, searchText, analysis string) []competitorObservation {
	// Only URLs present in the search payload become candidates. Every block
	// starts unknown until a dedicated collector or a human supplies evidence.
	text := searchText + "\n" + analysis
	seen := map[string]bool{}
	target := ""
	for _, token := range strings.Fields(query) {
		token = strings.Trim(token, " ,.;()[]{}")
		if strings.Contains(token, "://") {
			if parsed, err := url.Parse(token); err == nil {
				target = strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
			}
		}
	}
	sourceHosts := map[string]bool{
		"metronews.ru": true, "3dnews.ru": true, "cnews.ru": true,
		"expertnoeseo.ru": true, "google.com": true, "yandex.ru": true,
		"youtube.com": true, "wikipedia.org": true, "tadviser.ru": true,
		"raex-rr.com": true, "investfuture.ru": true, "it-stat.ru": true,
		"kommersant.ru": true, "softline.ru": true,
	}
	hosts := make([]string, 0)
	for _, raw := range append(discoveryURLPattern.FindAllString(text, -1), discoveryHostPattern.FindAllString(text, -1)...) {
		raw = strings.TrimRight(raw, ".,;:")
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
		if host == "" || host == target || sourceHosts[host] ||
			strings.HasSuffix(host, ".cnews.ru") || strings.HasSuffix(host, ".softline.ru") || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	rows := make([]competitorObservation, 0, len(hosts)*6)
	now := time.Now().UTC().Format(time.RFC3339)
	for _, host := range hosts {
		base := "https://" + host + "/"
		for _, block := range []string{"search", "price", "purchase", "account", "service", "ux"} {
			status := "unknown"
			notes := "Кандидат найден в публичном поиске; блок не проверен автоматически на этапе discovery."
			if block == "price" {
				status = "not_connected"
				notes = "Цена не собирается этим приложением; передайте проверенные строки из существующего источника Mac M1 или оставьте блок Н/Д."
			}
			rows = append(rows, competitorObservation{
				Competitor: host,
				URL:        base,
				Block:      block,
				Criterion:  "discovery candidate",
				Value:      "Н/Д",
				Status:     status,
				SourceURL:  base,
				CheckedAt:  now,
				Notes:      notes,
			})
		}
	}
	return rows
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
				Rows:       extractDiscoveryRows(query, searchText, summary.Text),
				Limitations: []string{
					"Кандидаты основаны на публичном поиске; включение в список не устанавливает коммерческую сопоставимость.",
					"Проверь домен и редакцию продукта; цена передаётся из существующего источника Mac M1, а без неё блок price остаётся not_connected.",
				},
			}, nil
		}).Build()
	a.RegisterTool(t, agentsdk.AccessUser)
}
