package main

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/goai/tool"
)

//go:embed index.html
var htmlPage string
var view = template.Must(template.New("scraper").Funcs(template.FuncMap{"num": number, "percent": percent, "when": when, "badge": badge, "state": stateName, "status": statusName, "field": fieldName, "ratio": ratio}).Parse(htmlPage))

type Rule struct {
	Name      string `json:"name"`
	Selector  string `json:"selector"`
	Attribute string `json:"attribute,omitempty"`
}
type Coverage struct {
	Label string `json:"label"`
	Done  int64  `json:"done"`
	Total int64  `json:"total"`
	Known bool   `json:"known"`
}
type State struct {
	Name  string `json:"name"`
	Count int64  `json:"count"`
}
type Source struct {
	ID              string            `json:"id"`
	Name            string            `json:"name"`
	Host            string            `json:"host"`
	Sitemap         string            `json:"sitemap"`
	Mode            string            `json:"mode"`
	Status          string            `json:"status"`
	UpdatedAt       string            `json:"updated_at"`
	InventoryAt     string            `json:"inventory_at"`
	Offers          int64             `json:"offers"`
	MissingSKU      *int64            `json:"missing_sku,omitempty"`
	Coverage        []Coverage        `json:"coverage"`
	States          []State           `json:"states"`
	Fields          []Rule            `json:"fields"`
	PagePattern     string            `json:"page_pattern"`
	Seeds           []string          `json:"seeds"`
	SampleURL       string            `json:"sample_url,omitempty"`
	SampleFields    map[string]string `json:"sample_fields,omitempty"`
	Note            string            `json:"note"`
	WorkerObserved  bool              `json:"worker_observed"`
	WorkerRunning   bool              `json:"worker_running"`
	IntervalSeconds int               `json:"interval_seconds"`
}
type Snapshot struct {
	GeneratedAt     string   `json:"generated_at"`
	Sources         []Source `json:"sources"`
	ExportRows      int64    `json:"export_rows"`
	ExportAt        string   `json:"export_at"`
	BaserowImported *int64   `json:"baserow_imported,omitempty"`
	BaserowAt       string   `json:"baserow_at,omitempty"`
}
type SourceDraft struct {
	URL      string `json:"url"`
	Fields   string `json:"fields"`
	Interval int    `json:"interval_hours"`
}
type Draft struct {
	SourceDraft
	SavedAt string `json:"saved_at"`
	Status  string `json:"status"`
}
type Storage interface {
	ReadFile(context.Context, string) ([]byte, error)
	WriteFile(context.Context, string, io.Reader, string) (agentsdk.FileInfo, error)
}
type Service struct {
	store Storage
	mu    sync.Mutex
}

const snapshotPath = "scraper/snapshot.json"

func ratio(states []State, n int64) float64 {
	var total int64
	for _, v := range states {
		total += v.Count
	}
	if total <= 0 {
		return 0
	}
	return float64(n) * 100 / float64(total)
}
func number(n int64) string {
	s := fmt.Sprint(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + " " + s[i:]
	}
	return s
}
func percent(c Coverage) float64 {
	if !c.Known || c.Total <= 0 {
		return 0
	}
	v := float64(c.Done) * 100 / float64(c.Total)
	if v > 100 {
		return 100
	}
	return v
}
func when(s string) string {
	t, e := time.Parse(time.RFC3339Nano, s)
	if e != nil {
		return "—"
	}
	return t.UTC().Format("02.01 15:04") + " UTC"
}
func stateName(s string) string {
	m := map[string]string{"priced": "С ценой", "no_public_price": "Без публичной цены", "unavailable": "Недоступны", "error": "Ошибки", "not_product": "Не товар", "out_of_scope": "Вне охвата", "unclassified": "Не распознано", "robots_excluded": "Исключены robots.txt", "pending": "Ожидает обработки", "blocked": "Заблокировано"}
	if v := m[s]; v != "" {
		return v
	}
	return s
}
func fieldName(s string) string {
	m := map[string]string{"row": "Блок предложения", "sku": "Артикул", "name": "Название", "price": "Цена", "old_price": "Старая цена", "link": "Ссылка", "quantity": "Количество", "price_basis": "Основание цены", "selector": "Правило поиска", "attribute": "Атрибут"}
	if v := m[s]; v != "" {
		return v
	}
	return s
}
func statusName(s string) string {
	switch s {
	case "running":
		return "Сбор идёт"
	case "complete":
		return "Завершён"
	case "needs_review":
		return "Нужна проверка"
	case "stopped":
		return "Не запущен"
	case "stale":
		return "Нет свежего отчёта"
	case "studying":
		return "Изучается"
	case "migration_staged":
		return "Подготовлен к переносу"
	default:
		return "Нет данных"
	}
}
func badge(s string) string {
	switch s {
	case "running":
		return "live"
	case "complete":
		return "done"
	case "needs_review", "stale":
		return "warn"
	default:
		return "muted"
	}
}
func validate(x Snapshot) error {
	if _, e := time.Parse(time.RFC3339Nano, x.GeneratedAt); e != nil {
		return fmt.Errorf("invalid snapshot time")
	}
	if len(x.Sources) > 100 || x.ExportRows < 0 {
		return fmt.Errorf("invalid snapshot size")
	}
	seen := map[string]bool{}
	for _, s := range x.Sources {
		if s.ID == "" || seen[s.ID] || s.Offers < 0 || len(s.Coverage) > 10 || len(s.Fields) > 50 {
			return fmt.Errorf("invalid source %q", s.ID)
		}
		seen[s.ID] = true
		for _, c := range s.Coverage {
			if c.Done < 0 || c.Total < 0 || c.Known && c.Done > c.Total {
				return fmt.Errorf("invalid coverage for %s", s.ID)
			}
		}
		for _, v := range s.States {
			if v.Count < 0 {
				return fmt.Errorf("invalid state")
			}
		}
	}
	return nil
}
func (s *Service) read(ctx context.Context) (Snapshot, error) {
	b, e := s.store.ReadFile(ctx, snapshotPath)
	if errors.Is(e, agentsdk.ErrNotFound) {
		return Snapshot{Sources: []Source{}}, nil
	}
	if e != nil {
		return Snapshot{}, e
	}
	var x Snapshot
	e = json.Unmarshal(b, &x)
	if e != nil {
		return x, e
	}
	for i := range x.Sources {
		p := &x.Sources[i]
		at, e := time.Parse(time.RFC3339Nano, p.UpdatedAt)
		if p.Status == "running" && (e != nil || time.Since(at) > 10*time.Minute) {
			p.Status = "stale"
		}
	}
	return x, nil
}
func (s *Service) write(ctx context.Context, x Snapshot) error {
	if e := validate(x); e != nil {
		return e
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	old, e := s.read(ctx)
	if e != nil {
		return e
	}
	if old.GeneratedAt != "" {
		a, _ := time.Parse(time.RFC3339Nano, old.GeneratedAt)
		b, _ := time.Parse(time.RFC3339Nano, x.GeneratedAt)
		if b.Before(a) {
			return fmt.Errorf("older snapshot rejected")
		}
	}
	raw, e := json.Marshal(x)
	if e != nil {
		return e
	}
	_, e = s.store.WriteFile(ctx, snapshotPath, bytes.NewReader(raw), "application/json")
	return e
}

type Query struct {
	Source string `json:"source,omitempty"`
}

func (s *Service) Status(ctx context.Context, q Query) (Snapshot, error) {
	x, e := s.read(ctx)
	if e != nil {
		return x, e
	}
	if q.Source == "" {
		return x, nil
	}
	for _, v := range x.Sources {
		if v.ID == q.Source {
			x.Sources = []Source{v}
			return x, nil
		}
	}
	return Snapshot{}, fmt.Errorf("unknown source")
}
func (s *Service) Draft(ctx context.Context, in SourceDraft) (Draft, error) {
	u, e := url.Parse(strings.TrimSpace(in.URL))
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return Draft{}, fmt.Errorf("enter an HTTPS site URL without credentials")
	}
	if len(in.Fields) > 2000 || in.Interval < 1 || in.Interval > 168 {
		return Draft{}, fmt.Errorf("invalid fields or interval")
	}
	d := Draft{SourceDraft: in, SavedAt: time.Now().UTC().Format(time.RFC3339), Status: "draft_not_activated"}
	// Draft belongs to the app's administrator, not to a running crawler.
	raw, _ := json.Marshal(d)
	_, e = s.store.WriteFile(ctx, "scraper/source-draft.json", bytes.NewReader(raw), "application/json")
	return d, e
}

type pageData struct {
	Snapshot
	Selected       *Source
	Active, Review int
	Offers         int64
	Tab, Error     string
	Draft          *Draft
}

func (s *Service) home(w http.ResponseWriter, r *http.Request) error {
	x, e := s.read(r.Context())
	if e != nil {
		return e
	}
	p := pageData{Snapshot: x, Tab: r.URL.Query().Get("tab")}
	if p.Tab == "" {
		p.Tab = "overview"
	}
	for i := range x.Sources {
		v := &x.Sources[i]
		p.Offers += v.Offers
		if v.Status == "running" {
			p.Active++
		}
		if v.Status == "needs_review" || v.Status == "stale" || v.Status == "stopped" {
			p.Review++
		}
		if v.ID == r.URL.Query().Get("source") {
			p.Selected = v
		}
	}
	if p.Selected != nil {
		p.Tab = "source"
	}
	if p.Tab == "new" {
		if b, e := s.store.ReadFile(r.Context(), "scraper/source-draft.json"); e == nil {
			var d Draft
			if json.Unmarshal(b, &d) == nil {
				p.Draft = &d
			}
		}
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	return view.Execute(w, p)
}
func newAgent() *agentsdk.Agent {
	a := agentsdk.New(agentsdk.Config{Description: "Web Scraper — sources, parser configuration, coverage and collection operations.", Icon: "🔎"})
	a.RegisterDirectory("scraper", agentsdk.DirectoryOpts{Read: agentsdk.AccessInternal, List: agentsdk.AccessInternal, Write: agentsdk.AccessInternal, Description: "Validated collection reports and configuration drafts"})
	s := &Service{store: a}
	a.RegisterTool(tool.Typed[Query, Snapshot]("get_scraper_status").Description("Actual collector progress, source rules, errors and export receipts. Source is optional. No fabricated or estimated coverage.").Execute(s.Status).Build(), agentsdk.AccessUser, agentsdk.WithReadOnly())
	a.RegisterTool(tool.Typed[SourceDraft, Draft]("save_source_draft").Description("Save a proposed new HTTPS source, desired fields and update interval; does not start requests or activate a parser.").Execute(s.Draft).Build(), agentsdk.AccessAdmin)
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/", Access: agentsdk.AccessUser, Handler: s.home, Description: "Web Scraper overview and source details"})
	a.RegisterRoute(&agentsdk.Route{Method: "GET", Path: "/status.json", Access: agentsdk.AccessUser, Description: "Read normalized collector report", Handler: func(w http.ResponseWriter, r *http.Request) error {
		x, e := s.Status(r.Context(), Query{Source: r.URL.Query().Get("source")})
		if e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(x)
	}})
	a.RegisterRoute(&agentsdk.Route{Method: "POST", Path: "/admin/snapshot", Access: agentsdk.AccessAdmin, Description: "Publish a validated collector snapshot", Logging: agentsdk.LoggingOff, Handler: func(w http.ResponseWriter, r *http.Request) error {
		var x Snapshot
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
		dec.DisallowUnknownFields()
		if e := dec.Decode(&x); e != nil {
			return agentsdk.NewHTTPError(400, "Invalid snapshot", nil)
		}
		if e := s.write(r.Context(), x); e != nil {
			return e
		}
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(map[string]any{"status": "ok", "generated_at": x.GeneratedAt, "sources": len(x.Sources)})
	}})
	a.RegisterRoute(&agentsdk.Route{Method: "POST", Path: "/draft", Access: agentsdk.AccessAdmin, Description: "Save a proposed source configuration", Handler: func(w http.ResponseWriter, r *http.Request) error {
		r.Body = http.MaxBytesReader(w, r.Body, 8192)
		if e := r.ParseForm(); e != nil {
			return agentsdk.NewHTTPError(400, "Invalid form", nil)
		}
		var hours int
		fmt.Sscan(r.FormValue("interval"), &hours)
		_, e := s.Draft(r.Context(), SourceDraft{URL: r.FormValue("url"), Fields: r.FormValue("fields"), Interval: hours})
		if e != nil {
			return agentsdk.NewHTTPError(400, e.Error(), nil)
		}
		http.Redirect(w, r, "/?tab=new", 303)
		return nil
	}})
	a.AddInstruction(&agentsdk.Instruction{Text: "Web Scraper monitors collection, not market analysis. Call get_scraper_status for real evidence. Distinguish family page coverage from individual license coverage and actual exported row count. A source draft is not an active parser. Existing collection workers remain externally managed. For catalog analysis use the dedicated data app. Never report a successful crawl from partial data."})
	a.RegisterSuggestedPrompt(agentsdk.SuggestedPrompt{Title: "Проверить парсеры", Prompt: "Покажи состояние всех источников и что мешает завершению сбора."})
	return a
}
func main() { newAgent().Serve() }
