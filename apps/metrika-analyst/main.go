package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	"html/template"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

//go:embed INSTRUCTIONS.md index.html
var assets embed.FS

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Аналитика интернет-магазина: динамика, воронка и измерение релизов.", Icon: "📊"})
	m := a.RegisterMCP(&agentsdk.MCP{Slug: "yandex_metrika", Name: "Yandex Metrika (BoxLab, read-only MCP)", URL: "http://metrika-mcp:8000/mcp", AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingShared})
	counter := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "counter_id", Description: "ID счётчика магазина", Pattern: "^[0-9]+$"})
	ec := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "ecommerce_confirmed", Description: "Подтверждена непрерывная инструментация e-commerce за анализируемые периоды", Default: "false", Pattern: "^(true|false)$"})
	file := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "evidence_path", Description: "Необязательный путь к явно выбранному evidence-v1 JSON в файлах Airlock; пусто — live MCP"})
	demoRoot := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "demo_data_root", Description: "Разрешённый каталог demo_sources/data с original/sandbox evidence-v1 JSON"})
	s := Service{call: func(ctx context.Context, n string, args any) (json.RawMessage, error) {
		r, e := m.CallTool(ctx, n, args)
		if e != nil || r == nil || r.IsError {
			return nil, fmt.Errorf("MCP unavailable or unauthorized: %s", n)
		}
		if len(r.StructuredContent) > 0 {
			return r.StructuredContent, nil
		}
		for _, c := range r.Content {
			if c.Type == "text" && json.Valid([]byte(c.Text)) {
				return json.RawMessage(c.Text), nil
			}
		}
		return nil, fmt.Errorf("MCP returned no JSON")
	}, config: func(ctx context.Context) Settings {
		id, _ := counter.Get(ctx)
		n, _ := strconv.ParseInt(id, 10, 64)
		e, _ := ec.Get(ctx)
		p, _ := file.Get(ctx)
		return Settings{n, e == "true", p}
	}, read: func(ctx context.Context, p string) ([]byte, error) {
		root, _ := demoRoot.Get(ctx)
		if root == "" {
			root = filepath.Join("demo_sources", "data")
		}
		base, _ := filepath.Abs(root)
		candidate := p
		if !filepath.IsAbs(candidate) {
			candidate = filepath.Join(base, candidate)
		}
		candidate, _ = filepath.Abs(candidate)
		allowed := candidate == base || strings.HasPrefix(candidate, base+string(os.PathSeparator))
		if allowed && strings.HasSuffix(strings.ToLower(candidate), ".json") {
			if b, err := os.ReadFile(candidate); err == nil {
				return b, nil
			}
		}
		return a.ReadFile(ctx, p)
	}}
	instructions, _ := assets.ReadFile("INSTRUCTIONS.md")
	a.AddInstruction(&agentsdk.Instruction{Text: string(instructions)})
	a.RegisterTool(tool.Typed[Query, Batch]("query_metrics").Description("Read exact period metrics from configured MCP or explicitly selected historical evidence file.").Execute(s.Query).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[Compare, Batch]("compare_periods").Description("Compare equal non-overlapping periods; output evidence-v1 including changes.").Execute(s.Compare).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[CheckInput, CheckResult]("check_metrika").Description("Check authorized counter or historical file. Never returns token.").Execute(s.Check).Build(), agentsdk.AccessUser)
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось?", Prompt: "Сравни две последние полные недели. Покажи источники, отклонения и ограничения."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Оценить релиз", Prompt: "Помоги оценить результат релиза: уточни дату, критерий успеха и сопоставимые периоды."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Проверить источник", Prompt: "Проверь подключение Метрики или выбранную выгрузку."})
	page := template.Must(template.ParseFS(assets, "index.html"))
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/", Access: agentsdk.AccessUser, Description: "Сравнение показателей", Handler: func(w http.ResponseWriter, r *http.Request) error {
		data := struct {
			Batch *Batch
			Error string
		}{}
		q := r.URL.Query()
		if q.Get("from") != "" {
			b, e := s.Compare(r.Context(), Compare{ProductKey: q.Get("product"), MetricKeys: strings.Split(q.Get("metrics"), ","), BaselineFrom: q.Get("from"), BaselineTo: q.Get("to"), CurrentFrom: q.Get("current_from"), CurrentTo: q.Get("current_to"), Segment: q.Get("segment"), SourcePath: q.Get("source_path")})
			if e != nil {
				data.Error = e.Error()
			} else {
				data.Batch = &b
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return page.ExecuteTemplate(w, "index.html", data)
	}})
	return a
}
