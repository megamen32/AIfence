package handlers

import (
	"agent/trends"
	"agent/views"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type savedUIClient struct {
	body  []byte
	err   error
	calls int
}

func (c *savedUIClient) Call(context.Context, map[string]any) ([]byte, error) {
	c.calls++
	return c.body, c.err
}

func TestQueryErrorRetargetsFeedbackAndKeepsChartOutOfResponse(t *testing.T) {
	client := &savedUIClient{err: trends.ErrRateLimited}
	pages := New(trends.NewService(client, func() time.Time { return time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC) }))
	form := url.Values{"mode": {"compare"}, "terms": {"Kaspersky Endpoint Security", "Astra Linux"}, "geo": {"RU"}, "from": {"2026-01-01"}, "to": {"2026-10-31"}, "property": {"web"}, "category": {"0"}}
	req := httptest.NewRequest("POST", "/query", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("HX-Request", "true")
	out := httptest.NewRecorder()
	if err := pages.Query(out, req); err != nil {
		t.Fatal(err)
	}
	if out.Header().Get("HX-Retarget") != "#query-feedback" || out.Header().Get("X-Trends-Result") != "error" || strings.Contains(out.Body.String(), "data-result") {
		t.Fatal("error replaced result rather than feedback")
	}
	if client.calls != 1 {
		t.Fatal("unexpected retries")
	}
}
func TestNamedGeoAndIntervalDisplayPreserveSourceMeaning(t *testing.T) {
	if views.GeoName("RU-MOW") != "город Москва" || views.GeoName("RU-MOS") != "Московская область" || views.GeoName("KZ") != "Казахстан" {
		t.Fatal("catalog names")
	}
	if views.GeoName("UNKNOWN") != "UNKNOWN" {
		t.Fatal("unknown geography was changed")
	}
	for input, want := range map[string]string{
		"Dec 28, 2025\u2009–\u2009Jan 3, 2026": "28 дек 2025 – 3 янв 2026",
		"Oct 25\u2009–\u200931, 2026":          "25–31 окт 2026",
		"Mar 29\u2009–\u2009Apr 4, 2026":       "29 мар – 4 апр 2026",
	} {
		if got := friendlyInterval(input); got != want {
			t.Fatalf("%q => %q", input, got)
		}
	}
}

// Explicit offline renderer for browser interaction QA. The default SDK build
// does not load runtime files; this test runs only with an owner-selected saved
// payload and output directory. It never reads credentials or calls a provider.
func TestRenderExplorerInteractionFixtures(t *testing.T) {
	payloadPath, outDir := os.Getenv("TRENDS_UI_PAYLOAD"), os.Getenv("TRENDS_UI_OUTPUT")
	if payloadPath == "" || outDir == "" {
		t.Skip("offline browser fixture render is opt-in")
	}
	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.MkdirAll(outDir, 0700); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	client := &savedUIClient{body: raw}
	service := trends.NewService(client, func() time.Time { return now })
	result, err := service.Compare(context.Background(), trends.Query{Terms: []string{"Kaspersky Endpoint Security", "Astra Linux"}, Geo: "RU", From: "2026-01-01", To: "2026-10-31", Property: "web", Category: "0"})
	if err != nil || result.Status != trends.OK {
		t.Fatalf("saved payload did not parse: %s / %v", result.Status, err)
	}
	vm := resultModel(result)
	f := defaultForm(now)
	f.From, f.To, f.PeriodPreset = result.Request.From, result.Request.To, "custom"
	write := func(name string, b []byte) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(outDir, name), b, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	if err := views.Page(f, &vm).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	write("index.html", buf.Bytes())
	buf.Reset()
	if err := views.Results(vm).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	write("result.html", buf.Bytes())
	buf.Reset()
	if err := views.Page(defaultForm(now), nil).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	write("home.html", buf.Bytes())
	buf.Reset()
	if err := views.Feedback(views.ResultModel{Status: trends.RateLimited, Message: "Источник ограничил частоту запросов. Повторите позже."}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	write("error.html", buf.Bytes())
	buf.Reset()
	empty := result
	empty.Status = trends.InsufficientData
	empty.Points = nil
	empty.Summaries = nil
	if err := views.Results(resultModel(empty)).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	write("empty.html", buf.Bytes())
	buf.Reset()
	var edge trends.Result
	copied, _ := json.Marshal(result)
	if err = json.Unmarshal(copied, &edge); err != nil {
		t.Fatal(err)
	}
	if len(edge.Points) < 5 {
		t.Fatal("fixture needs at least five intervals")
	}
	yes, no := true, false
	zero := 0.0
	edge.Points[0].Values[0].Index = &zero
	edge.Points[0].Values[0].Raw = "0"
	edge.Points[0].Values[0].HasData = &yes
	edge.Points[0].Values[0].State = "observed"
	edge.Points[0].IsPartial = &no
	edge.Points[1].Values[0].Index = nil
	edge.Points[1].Values[0].Raw = "0"
	edge.Points[1].Values[0].HasData = &no
	edge.Points[1].Values[0].State = "missing"
	edge.Points[2].Values[0].Index = nil
	edge.Points[2].Values[0].Raw = "<1"
	edge.Points[2].Values[0].HasData = &yes
	edge.Points[2].Values[0].State = "below_one"
	edge.Points[3].IsPartial = &yes
	edge.Points[4].IsPartial = nil
	edge.Summaries = nil
	edge.Source.Status = "offline_fixture"
	edge.Source.RequestID = "offline-quality-flags"
	edgeModel := resultModel(edge)
	if err := views.Page(f, &edgeModel).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	write("edge.html", buf.Bytes())
	buf.Reset()
	write("app.css", views.AppCSS)
	write("explorer.js", views.AppJS)
	manifest := map[string]any{"kind": "offline_browser_fixture", "provider_calls": 0, "saved_payload_calls": client.calls, "pages": []string{"index.html", "home.html", "edge.html"}, "fragments": []string{"result.html", "error.html", "empty.html"}, "css_url": views.AppCSSPath, "js_url": views.AppJSPath, "real_source_points": len(result.Points), "edge_flags_modified": true}
	b, _ := json.MarshalIndent(manifest, "", "  ")
	write("fixture.json", b)
	if client.calls != 1 {
		t.Fatal(errors.New("renderer replay count mismatch"))
	}
}
