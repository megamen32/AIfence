package main

import (
	"bytes"
	"context"
	"encoding/json"
	"html/template"
	"os"
	"strings"
	"testing"
	"time"
)

func fakeMetrika(values map[string]float64) Service {
	return Service{
		config: func(context.Context) Settings { return Settings{Counter: 12345} },
		call: func(_ context.Context, _ string, args any) (json.RawMessage, error) {
			a := args.(map[string]any)
			api := a["metrics"].([]string)[0]
			d, _ := time.Parse(time.DateOnly, a["date1"].(string))
			v := values[api] * (1 + float64(d.YearDay()%5)/40)
			if d.YearDay() > 320 {
				v *= 0.82
			}
			b, _ := json.Marshal(map[string]any{"totals": map[string]float64{api: v}, "meta": map[string]any{"sampled": false}})
			return b, nil
		},
	}
}

func renderDashboard(t *testing.T, s Service) string {
	t.Helper()
	page := template.Must(template.ParseFS(assets, "index.html"))
	var buf bytes.Buffer
	if err := page.ExecuteTemplate(&buf, "index.html", s.dashboard(context.Background(), time.Date(2026, 11, 27, 9, 0, 0, 0, time.UTC))); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestDashboardOpensOnRecentWeeks(t *testing.T) {
	html := renderDashboard(t, fakeMetrika(map[string]float64{"ym:s:visits": 14200, "ym:s:users": 9800, "ym:s:pageviews": 51000}))
	for _, want := range []string{"Визиты", "16–22 ноября", "class=\"line\"", "счётчик 12345", "к прошлой неделе"} {
		if !strings.Contains(html, want) {
			t.Fatalf("dashboard lacks %q", want)
		}
	}
	for _, banned := range []string{"Деморежим", "демо", "Продукт", "<form method=\"get\"><div"} {
		if strings.Contains(html, banned) {
			t.Fatalf("dashboard contains %q", banned)
		}
	}
	if p := os.Getenv("DASH_OUT"); p != "" {
		os.WriteFile(p, []byte(html), 0o644)
	}
}

func TestDashboardExplainsMissingCounter(t *testing.T) {
	html := renderDashboard(t, Service{config: func(context.Context) Settings { return Settings{} }})
	if !strings.Contains(html, "Счётчик Метрики не подключён") || !strings.Contains(html, "counter_id") {
		t.Fatal("setup state does not say what to do")
	}
}

func TestLastWeeksEndOnCompletedSunday(t *testing.T) {
	w := lastWeeks(time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC), 2)
	if w[1].From.Format(time.DateOnly) != "2026-11-16" || w[1].To.Format(time.DateOnly) != "2026-11-22" || w[0].To.Format(time.DateOnly) != "2026-11-15" {
		t.Fatalf("weeks %v", w)
	}
}

func TestRuNum(t *testing.T) {
	if got := ruNum(-12345.67, 1); got != "−12 345,7" {
		t.Fatalf("got %q", got)
	}
}
