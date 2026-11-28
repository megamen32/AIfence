package main

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
	"html/template"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed INSTRUCTIONS.md index.html fixtures/*.json
var assets embed.FS

func embeddedEvidence(path string) ([]byte, bool) {
	if !strings.HasPrefix(path, "fixtures/") {
		return nil, false
	}
	b, err := assets.ReadFile(path)
	return b, err == nil
}

func metricValue(o Observation) string {
	if o.Value == nil {
		return "—"
	}
	v := *o.Value
	if math.Abs(v) >= 1000 {
		return fmt.Sprintf("%.0f", v)
	}
	text := strconv.FormatFloat(v, 'f', 2, 64)
	text = strings.TrimRight(strings.TrimRight(text, "0"), ".")
	if text == "-0" {
		return "0"
	}
	return text
}

func metricStatus(status string) string {
	switch status {
	case "observed":
		return "Наблюдается"
	case "estimated":
		return "Оценка"
	case "incomparable":
		return "Несопоставимо"
	case "missing":
		return "Источник не сообщил показатель"
	default:
		return status
	}
}

func metricTone(o Observation) string {
	switch o.Status {
	case "observed":
		return "good"
	case "estimated", "incomparable":
		return "warn"
	default:
		return "muted"
	}
}

func chartX(i int) int { return 52 + i*82 }

func chartHeight(o Observation) int {
	if o.Value == nil {
		return 14
	}
	v := math.Abs(*o.Value)
	h := int(math.Round(28 + math.Min(150, math.Log10(v+1)*54)))
	if h < 18 {
		return 18
	}
	return h
}

func chartY(o Observation) int { return 222 - chartHeight(o) }

func chartLabel(o Observation) string {
	label := metricTitles[o.MetricKey]
	if label == "" {
		label = o.MetricKey
	}
	for suffix, title := range map[string]string{
		".absolute_change":     " · изменение",
		".relative_change_pct": " · изменение, %",
		".change_pp":           " · изменение, п.п.",
	} {
		if strings.HasSuffix(o.MetricKey, suffix) {
			base := strings.TrimSuffix(o.MetricKey, suffix)
			if name := metricTitles[base]; name != "" {
				label = name + title
			}
			break
		}
	}
	if len(label) > 12 {
		return label[:12] + "…"
	}
	return label
}

func main() { newAgent().Serve() }
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Яндекс Метрика: посещения, конверсии, изменения по неделям и ссылки на источники. В текущем чате можно открыть выбранный снимок: найти ряды и просканировать его целиком.", Icon: "📊"})
	m := a.RegisterMCP(&agentsdk.MCP{Slug: "yandex_metrika", Name: "Yandex Metrika (BoxLab, read-only MCP)", URL: "http://127.0.0.1:8000/mcp", AuthMode: agentsdk.MCPAuthToken, BindingMode: agentsdk.BindingShared})
	counter := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "counter_id", Description: "ID счётчика магазина", Pattern: "^[0-9]+$"})
	ec := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "ecommerce_confirmed", Description: "Подтверждена непрерывная инструментация e-commerce за анализируемые периоды", Default: "false", Pattern: "^(true|false)$"})
	file := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "evidence_path", Description: "Необязательный путь к явно выбранному evidence-v1 JSON в файлах Airlock; пусто — live MCP"})
	demoRoot := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "demo_data_root", Description: "Разрешённый каталог demo_sources/data с original/sandbox evidence-v1 JSON"})
	watchContext := a.RegisterEnvVar(&agentsdk.EnvVar{Slug: "watch_context_path", Description: "Путь к авторизованному JSON с релизами, задачами, разрывами и интервью"})
	a.RegisterDirectory("evidence", agentsdk.DirectoryOpts{
		Read: agentsdk.AccessUser, Write: agentsdk.AccessAdmin, List: agentsdk.AccessUser, Scope: agentsdk.ScopeUser,
		Description: "Authorized evidence files provisioned for this caller or workspace",
	})
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
		if fixture, ok := embeddedEvidence(p); ok {
			return fixture, nil
		}
		root, _ := demoRoot.Get(ctx)
		if root != "" && strings.HasSuffix(strings.ToLower(p), ".json") {
			name := p
			if filepath.IsAbs(name) {
				if !filepath.IsAbs(root) {
					name = ""
				} else {
					name, _ = filepath.Rel(root, name)
				}
			}
			if name != "" && filepath.IsLocal(name) {
				r, err := os.OpenRoot(root)
				if err == nil {
					defer r.Close()
					f, err := r.Open(name)
					if err == nil {
						defer f.Close()
						info, err := f.Stat()
						if err == nil && info.Mode().IsRegular() {
							return io.ReadAll(io.LimitReader(f, (8<<20)+1))
						}
					}
				}
			}
		}
		resolved, err := a.ResolveFilePath(ctx, p, agentsdk.FileOperationRead)
		if err != nil {
			return nil, err
		}
		f, err := a.OpenFile(ctx, string(resolved))
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return io.ReadAll(io.LimitReader(f, (8<<20)+1))
	}}
	instructions, _ := assets.ReadFile("INSTRUCTIONS.md")
	a.AddInstruction(&agentsdk.Instruction{Text: string(instructions)})
	a.RegisterTool(tool.Typed[Query, Batch]("query_metrics").Description("Read exact period metrics from configured MCP or explicitly selected historical evidence file.").Execute(s.Query).Build(), agentsdk.AccessUser)
	a.RegisterTool(tool.Typed[Compare, Batch]("compare_periods").Description("Compare equal non-overlapping periods; output evidence-v1 including changes.").Execute(s.Compare).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
	a.RegisterTool(tool.Typed[CheckInput, CheckResult]("check_metrika").Description("Check authorized counter or historical file. Never returns token.").Execute(s.Check).Build(), agentsdk.AccessUser)
	registerWatch(a, Watch{series: s, contextPath: func(ctx context.Context) string {
		p, _ := watchContext.Get(ctx)
		return p
	}})
	registerWatchSchedule(a)
	registerExternalDemand(a)
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Что изменилось?", Prompt: "Сравни две последние полные недели. Покажи источники, отклонения и ограничения."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Оценить релиз", Prompt: "Помоги оценить результат релиза: уточни дату, критерий успеха и сопоставимые периоды."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Проверить источник", Prompt: "Проверь подключение Метрики или выбранную выгрузку."})
	page := template.Must(template.ParseFS(assets, "index.html"))
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/", Access: agentsdk.AccessUser, Description: "Неделя магазина по данным Метрики", Handler: func(w http.ResponseWriter, r *http.Request) error {
		data := s.dashboard(r.Context(), time.Now().UTC())
		q := r.URL.Query()
		if q.Get("from") != "" {
			segment := "all"
			if data.ProductKey == "kaspersky_endpoint_security" {
				segment = "self_service"
			}
			sourcePath := ""
			if data.ProductKey == "kaspersky_endpoint_security" {
				sourcePath = dashboardKESFixture
			}
			in := Compare{ProductKey: data.ProductKey, MetricKeys: strings.Split(data.Metrics, ","), BaselineFrom: q.Get("from"), BaselineTo: q.Get("to"), CurrentFrom: q.Get("current_from"), CurrentTo: q.Get("current_to"), Segment: segment, SourcePath: sourcePath}
			data.FormFrom, data.FormTo, data.FormCurFrom, data.FormCur = in.BaselineFrom, in.BaselineTo, in.CurrentFrom, in.CurrentTo
			if b, e := s.Compare(r.Context(), in); e != nil {
				data.Compare = &compareView{Title: "Ваше сравнение", Error: "Сравнение не выполнено: " + e.Error()}
			} else {
				data.Compare = compareFromBatch(b, in)
			}
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		return page.ExecuteTemplate(w, "index.html", data)
	}})
	return a
}
