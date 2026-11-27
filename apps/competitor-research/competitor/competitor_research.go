package competitor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

const competitorResearchDirectory = "tmp/competitor-research"

type competitorResearchInput struct {
	Company        string   `json:"company"`
	Site           string   `json:"site,omitempty"`
	Market         string   `json:"market,omitempty"`
	Offer          string   `json:"offer,omitempty"`
	Goal           string   `json:"goal,omitempty"`
	Competitors    []string `json:"competitors,omitempty"`
	Products       []string `json:"products,omitempty"`
	MaxCompetitors int      `json:"max_competitors,omitempty"`
}
type competitorResearchCriterion struct {
	Block       string  `json:"block"`
	Criterion   string  `json:"criterion"`
	Weight      float64 `json:"weight"`
	Collection  string  `json:"collection"`
	Description string  `json:"description"`
}
type competitorResearchPlan struct {
	StudyID     string                        `json:"study_id"`
	CreatedAt   string                        `json:"created_at"`
	Brief       string                        `json:"brief"`
	TargetSite  string                        `json:"target_site"`
	Market      string                        `json:"market,omitempty"`
	Competitors []string                      `json:"competitors,omitempty"`
	Products    []string                      `json:"products,omitempty"`
	Criteria    []competitorResearchCriterion `json:"criteria"`
	NextActions []string                      `json:"next_actions"`
	CSVColumns  []string                      `json:"csv_columns"`
	Limitations []string                      `json:"limitations"`
}
type competitorObservation struct {
	Competitor string   `json:"competitor"`
	URL        string   `json:"url,omitempty"`
	Product    string   `json:"product,omitempty"`
	Block      string   `json:"block"`
	Criterion  string   `json:"criterion"`
	Value      string   `json:"value,omitempty"`
	Score      *float64 `json:"score,omitempty"`
	Status     string   `json:"status,omitempty"`
	SourceURL  string   `json:"source_url,omitempty"`
	CheckedAt  string   `json:"checked_at,omitempty"`
	Notes      string   `json:"notes,omitempty"`
}
type competitorCSVInput struct {
	StudyID string                  `json:"study_id"`
	Rows    []competitorObservation `json:"rows"`
}
type competitorCSVResult struct {
	StudyID     string   `json:"study_id"`
	RowCount    int      `json:"row_count"`
	Filename    string   `json:"filename"`
	Columns     []string `json:"columns"`
	CSV         string   `json:"csv"`
	Limitations []string `json:"limitations"`
}

var competitorCSVColumns = []string{"study_id", "competitor", "url", "product", "block", "criterion", "value", "score_1_to_5", "status", "source_url", "checked_at", "notes"}

func registerCompetitorResearch(a *agentsdk.Agent) {
	a.RegisterDirectory(competitorResearchDirectory, agentsdk.DirectoryOpts{
		Read: agentsdk.AccessUser, Write: agentsdk.AccessUser, List: agentsdk.AccessUser,
		Scope: agentsdk.ScopeConversation, RetentionHours: 72,
		Description: "Conversation-scoped competitor research CSV exports",
	})
	plan := tool.Typed[competitorResearchInput, competitorResearchPlan]("plan_competitor_research").
		Description("Create a bounded competitor-analysis plan from a company, site, market and offer. If competitors or products are omitted, keep them empty and make discovery the next action; do not invent them.").
		Execute(func(ctx context.Context, in competitorResearchInput) (competitorResearchPlan, error) {
			return buildCompetitorPlan(in)
		}).Build()
	a.RegisterTool(plan, agentsdk.AccessUser)

	export := tool.Typed[competitorCSVInput, competitorCSVResult]("export_competitor_csv").
		Description("Validate observations and export deterministic competitor-analysis.csv. Every row keeps competitor, product, criterion, value, score, status, source URL, date and notes. Unknown evidence stays unknown/missing and is never converted to zero. In chat the CSV is attached.").
		Execute(func(ctx context.Context, in competitorCSVInput) (competitorCSVResult, error) {
			return buildCompetitorCSV(in)
		}).Build()
	read := export.Execute
	export.Execute = func(ctx context.Context, args json.RawMessage, opts tool.CallOptions) (tool.Result, error) {
		result, err := read(ctx, args, opts)
		if err != nil {
			return result, err
		}
		var payload competitorCSVResult
		if json.Unmarshal([]byte(result.Output), &payload) != nil || payload.CSV == "" {
			return result, nil
		}
		caller := agentsdk.CallerFromContext(ctx)
		if _, ok := caller.User(); !ok || caller.Origin().Interface != agentsdk.InterfaceChat {
			return result, nil
		}
		logical := fmt.Sprintf("%s/%x.csv", competitorResearchDirectory, sha256.Sum256([]byte(payload.StudyID+payload.CSV)))
		resolved, err := a.ResolveFilePath(ctx, logical, agentsdk.FileOperationWrite)
		if err != nil {
			return result, nil
		}
		info, err := a.WriteFile(ctx, string(resolved), strings.NewReader(payload.CSV), "text/csv; charset=utf-8")
		if err != nil {
			return result, nil
		}
		result.Attachments = append(result.Attachments, tool.Attachment{Data: "s3ref:" + string(info.Path), Filename: payload.Filename, MimeType: "text/csv"})
		return result, nil
	}
	a.RegisterTool(export, agentsdk.AccessUser)
}

func buildCompetitorPlan(in competitorResearchInput) (competitorResearchPlan, error) {
	company := strings.TrimSpace(in.Company)
	if company == "" {
		return competitorResearchPlan{}, errors.New("company is required")
	}
	site, market, offer, goal := normalizeResearchURL(in.Site), strings.TrimSpace(in.Market), strings.TrimSpace(in.Offer), strings.TrimSpace(in.Goal)
	if goal == "" {
		goal = "найти основных конкурентов и сравнить предложение, цены, путь покупки, сервис и UX"
	}
	max := in.MaxCompetitors
	if max == 0 {
		max = 10
	}
	if max < 1 || max > 20 {
		return competitorResearchPlan{}, errors.New("max_competitors must be between 1 and 20")
	}
	competitors := uniqueNonEmpty(in.Competitors)
	if len(competitors) > max {
		competitors = competitors[:max]
	}
	products := uniqueNonEmpty(in.Products)
	digest := sha256.Sum256([]byte(strings.ToLower(strings.Join([]string{company, site, market, offer, goal}, "|"))))
	brief := company
	if site != "" {
		brief += " (" + site + ")"
	}
	criteria := []competitorResearchCriterion{
		{"Выбор конкурентов", "Присутствие в TOP-поиске", 0, "search", "Кто регулярно появляется по коммерческим запросам целевого предложения"},
		{"Ассортимент", "Пересечение с предложением", 0, "website", "Какие продукты, категории и редакции совпадают"},
		{"Цена", "Сопоставимая цена", 25, "offer", "Только одинаковая редакция, срок, количество и условия лицензии"},
		{"Карточка товара", "Полнота полей", 0, "website", "Редакция, срок, устройства, требования, наличие и срок выдачи"},
		{"Покупка и счёт", "Путь до оформления", 25, "browser", "Шаги, регистрация и онлайн-счёт без менеджера; формы не отправлять"},
		{"Личный кабинет", "Самообслуживание", 20, "manual", "Закрытые функции только при наличии разрешённой учётной записи"},
		{"Сервис", "Публичные преимущества", 10, "website", "Скидки, консультации, отсрочка, SLA и программы лояльности"},
		{"UX сайта", "Удобство выбора", 10, "browser", "Поиск, фильтры, навигация, сравнение и мобильная версия"},
		{"Поиск и спрос", "Видимость и спрос", 10, "search", "Частоты и позиции только при доступном провайдере"},
	}
	return competitorResearchPlan{
		StudyID: "cr-" + fmt.Sprintf("%x", digest[:6]), CreatedAt: time.Now().UTC().Format(time.RFC3339),
		Brief: brief + ". Цель: " + goal, TargetSite: site, Market: market, Competitors: competitors, Products: products,
		Criteria: criteria,
		NextActions: []string{
			"Проверить основной сайт и выделить 3–10 коммерческих продуктовых кластеров.",
			"Найти кандидатов через доступный web-поиск, объединить домены и объяснить выбор.",
			"Собрать по каждому конкуренту URL, наблюдения и дату; сохранять прямые источники.",
			"Для сопоставимых SKU проверить цену и условия; для UX/покупки использовать браузер без отправки заявок.",
			"Заполнить наблюдения через export_competitor_csv и оставить неизвестные поля со статусом missing/unknown.",
		},
		CSVColumns: competitorCSVColumns,
		Limitations: []string{
			"Платные кабинеты, СПАРК и переписка с менеджерами не считаются автоматически подтверждёнными.",
			"Поиск и SEO-частоты зависят от подключённого провайдера; отсутствие ключа сохраняется как not_connected.",
			"Цена сравнивается только при совпадении редакции, срока, количества и условий.",
			"Скриншоты и браузерные артефакты должны быть переданы отдельными источниками или инструментами браузера.",
		},
	}, nil
}

func buildCompetitorCSV(in competitorCSVInput) (competitorCSVResult, error) {
	studyID := strings.TrimSpace(in.StudyID)
	if studyID == "" {
		return competitorCSVResult{}, errors.New("study_id is required")
	}
	rows := append([]competitorObservation(nil), in.Rows...)
	for i := range rows {
		rows[i].Competitor, rows[i].Block, rows[i].Criterion, rows[i].Status = strings.TrimSpace(rows[i].Competitor), strings.TrimSpace(rows[i].Block), strings.TrimSpace(rows[i].Criterion), strings.TrimSpace(rows[i].Status)
		if rows[i].Status == "" {
			rows[i].Status = "observed"
		}
		if rows[i].Competitor == "" || rows[i].Block == "" || rows[i].Criterion == "" {
			return competitorCSVResult{}, fmt.Errorf("row %d requires competitor, block and criterion", i+1)
		}
		if rows[i].Score != nil && (*rows[i].Score < 1 || *rows[i].Score > 5) {
			return competitorCSVResult{}, fmt.Errorf("row %d score must be between 1 and 5", i+1)
		}
		if rows[i].CheckedAt == "" {
			rows[i].CheckedAt = time.Now().UTC().Format(time.RFC3339)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		return strings.Join([]string{a.Competitor, a.Block, a.Product, a.Criterion}, "\x00") < strings.Join([]string{b.Competitor, b.Block, b.Product, b.Criterion}, "\x00")
	})
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	if err := w.Write(competitorCSVColumns); err != nil {
		return competitorCSVResult{}, err
	}
	for _, row := range rows {
		score := ""
		if row.Score != nil {
			score = strconv.FormatFloat(*row.Score, 'g', -1, 64)
		}
		if err := w.Write([]string{studyID, row.Competitor, row.URL, row.Product, row.Block, row.Criterion, row.Value, score, row.Status, row.SourceURL, row.CheckedAt, row.Notes}); err != nil {
			return competitorCSVResult{}, err
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return competitorCSVResult{}, err
	}
	return competitorCSVResult{StudyID: studyID, RowCount: len(rows), Filename: "competitor-analysis.csv", Columns: competitorCSVColumns, CSV: buf.String(), Limitations: []string{"CSV содержит только переданные наблюдения; непереданные критерии не считаются проверенными."}}, nil
}

func normalizeResearchURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	return strings.TrimRight(raw, "/")
}
func uniqueNonEmpty(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
