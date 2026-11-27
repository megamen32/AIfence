package handlers

import (
	"agent/demand"
	"agent/views"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/airlockrun/agentsdk/testutil"
	"github.com/airlockrun/agentsdk/wire"
)

func countPointer(n int64) *int64 { return &n }
func TestTopTotalNeverComesFromOverlappingRows(t *testing.T) {
	for _, tc := range []struct {
		name  string
		data  map[string]any
		want  bool
		value string
	}{
		{"missing", map[string]any{}, false, ""},
		{"real_zero", map[string]any{"totalCount": "0"}, true, "0"},
		{"source_total", map[string]any{"totalCount": "842"}, true, "842"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := demand.Result{Status: "ok", Kind: "top", Phrase: "касперский для бизнеса", Data: tc.data, Rows: []demand.Row{{Group: "results", Label: "касперский для бизнеса", Count: countPointer(842)}, {Group: "associations", Label: "kaspersky removal tool", Count: countPointer(14715)}}}
			got := resultView(out)
			if got.HasTotal != tc.want || got.Total != tc.value || len(got.Main) != 1 || len(got.Associated) != 1 {
				t.Fatalf("incorrect frequency model: %+v", got)
			}
		})
	}
}
func TestDynamicsUsesAbsoluteCountsAndPreservesMissingMonth(t *testing.T) {
	out := demand.Result{Status: "ok", Kind: "dynamics", Phrase: "касперский для бизнеса", RequestedPeriod: &demand.Period{From: "2026-07", To: "2026-09", Granularity: "monthly"}, Rows: []demand.Row{{Label: "2026-07-01T00:00:00Z", Count: countPointer(1200)}, {Label: "2026-08-01T00:00:00Z", Count: countPointer(0)}}}
	view := resultView(out)
	var chart views.Chart
	if err := json.Unmarshal([]byte(view.ChartJSON), &chart); err != nil {
		t.Fatal(err)
	}
	if len(chart.Points) != 3 || view.ObservedCount != 2 {
		t.Fatalf("bad calendar spine: %+v", chart)
	}
	if chart.Points[0].Count != "1\u00a0200" || chart.Points[0].Y == nil || chart.Points[1].Count != "0" || chart.Points[1].Y == nil || *chart.Points[1].Y != 0 || chart.Points[2].Y != nil || chart.Points[2].State != "missing" {
		t.Fatalf("zero and missing collapsed: %+v", chart.Points)
	}
	if view.Axis[0] == "100" || view.Metrics[0].Value != "1\u00a0200" {
		t.Fatalf("absolute axis lost: %+v", view)
	}
	out.Rows = []demand.Row{{Label: "2026-07-01", Count: countPointer(1200)}, {Label: "2026-09-01", Count: countPointer(1300)}}
	if got := resultView(out); len(got.Segments) != 2 || len(got.Dots) != 2 {
		t.Fatalf("gap was connected: %+v", got.Segments)
	}
}
func TestRegionalUnknownNameKeepsRealZero(t *testing.T) {
	out := demand.Result{Status: "ok", Kind: "regions", Phrase: "касперский для бизнеса", RegionMode: "cities", RegionLookupStatus: "partial", RegionLookupMessage: "Название недоступно.", Rows: []demand.Row{{Label: "Регион 213", RegionID: "213", RegionNameStatus: "unknown", Count: countPointer(0)}}}
	got := resultView(out)
	if len(got.Regions) != 1 || got.Regions[0].Count != "0" || !strings.Contains(got.Regions[0].Detail, "213") || got.RegionLookupMessage == "" {
		t.Fatalf("lost regional evidence: %+v", got)
	}
	if strings.Contains(got.RequestJSON, "region_ids") || strings.Contains(got.RequestJSON, "device") {
		t.Fatalf("unsupported filters in geography snapshot: %s", got.RequestJSON)
	}
}

type handlerQueries struct {
	result   demand.Result
	err      error
	calls    int
	regions  demand.RegionsInput
	top      demand.TopInput
	dynamics demand.DynamicsInput
}

func (q *handlerQueries) Top(_ context.Context, in demand.TopInput) (demand.Result, error) {
	q.calls++
	q.top = in
	return q.result, q.err
}
func (q *handlerQueries) Dynamics(_ context.Context, in demand.DynamicsInput) (demand.Result, error) {
	q.calls++
	q.dynamics = in
	return q.result, q.err
}
func (q *handlerQueries) Regions(_ context.Context, in demand.RegionsInput) (demand.Result, error) {
	q.calls++
	q.regions = in
	return q.result, q.err
}
func TestQueryErrorsRetargetFeedbackWithoutReplacingResult(t *testing.T) {
	q := &handlerQueries{result: demand.Result{Status: "unavailable_period", SourceStatus: "unavailable_period", Kind: "dynamics", Phrase: "касперский для бизнеса", Message: "Запрошенный период недоступен.", RequestedPeriod: &demand.Period{From: "2026-11", To: "2026-11"}}}
	form := url.Values{"kind": {"dynamics"}, "phrase": {"касперский для бизнеса"}, "from_month": {"2026-11"}, "to_month": {"2026-11"}}
	request := httptest.NewRequest("POST", "/query", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	if err := New(q).Query(response, request); err != nil {
		t.Fatal(err)
	}
	if response.Code != 422 || response.Header().Get("HX-Retarget") != "#query-feedback" || strings.Contains(response.Body.String(), "data-result") || q.calls != 1 || q.dynamics.ToMonth != "2026-11" {
		t.Fatalf("error erased result or period: %d %s", response.Code, response.Body.String())
	}
}
func TestGeographyIgnoresInapplicableFormFilters(t *testing.T) {
	q := &handlerQueries{result: demand.Result{Status: "ok", Kind: "regions", Phrase: "Astra Linux", RegionMode: "cities"}}
	form := url.Values{"kind": {"regions"}, "phrase": {"Astra Linux"}, "region_mode": {"cities"}, "region_ids": {"not-a-region"}, "device": {"phone"}, "from_month": {"2026-11"}}
	request := httptest.NewRequest("POST", "/query", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("HX-Request", "true")
	response := httptest.NewRecorder()
	if err := New(q).Query(response, request); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || q.calls != 1 || q.regions.RegionMode != "cities" {
		t.Fatalf("inactive controls affected geography: %d", response.Code)
	}
}

type replayCaller struct{ response *agentsdk.MCPToolCallResponse }

func (p replayCaller) CallTool(_ context.Context, name string, _ any) (*agentsdk.MCPToolCallResponse, error) {
	if name != "top_requests" {
		return nil, errors.New("offline replay has no such operation")
	}
	return p.response, nil
}
func fixtureCaller(t *testing.T) context.Context {
	user := wire.CallerUser{ID: "00000000-0000-0000-0000-000000000001", PlatformMember: true}
	return testutil.WithCaller(t.Context(), wire.Caller{Kind: "user", Access: wire.AccessUser, User: &user, Initiator: &user, Origin: wire.CallerOrigin{Interface: "mcp", Execution: "request"}})
}

// TestRenderWordstatInteractionFixtures is explicitly opt-in and never registered
// as an application route. It reads saved source responses, not credentials.
func TestRenderWordstatInteractionFixtures(t *testing.T) {
	topPath, regionPath, output := os.Getenv("WORDSTAT_UI_TOP"), os.Getenv("WORDSTAT_UI_REGIONS"), os.Getenv("WORDSTAT_UI_OUTPUT")
	if topPath == "" || regionPath == "" || output == "" {
		t.Skip("offline renderer requires explicit saved response paths and output directory")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		t.Fatal(err)
	}
	read := func(path string) []byte {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	var receipt struct {
		VerifiedAt string `json:"verified_at"`
		Call       struct {
			Arguments struct {
				Phrase string `json:"phrase"`
			} `json:"arguments"`
		} `json:"call"`
		Result agentsdk.MCPToolCallResponse `json:"result"`
	}
	if err := json.Unmarshal(read(topPath), &receipt); err != nil {
		t.Fatal(err)
	}
	top, err := demand.New(replayCaller{&receipt.Result}).Top(fixtureCaller(t), demand.TopInput{Phrase: receipt.Call.Arguments.Phrase, Limit: 3})
	if err != nil {
		t.Fatal(err)
	}
	top.Source.CheckedAt = receipt.VerifiedAt
	var wireReceipt struct {
		Data struct {
			Result struct {
				Output string `json:"output"`
			} `json:"result"`
		} `json:"data"`
	}
	if err := json.Unmarshal(read(regionPath), &wireReceipt); err != nil {
		t.Fatal(err)
	}
	var regional demand.Result
	if err := json.Unmarshal([]byte(wireReceipt.Data.Result.Output), &regional); err != nil {
		t.Fatal(err)
	}
	if regional.Status != "ok" || len(regional.Rows) == 0 {
		t.Fatal("saved regional response is not a successful result")
	}
	dynamics := demand.Result{Status: "ok", SourceStatus: "offline_fixture", Kind: "dynamics", Phrase: "касперский для бизнеса", RegionIDs: []int{225}, Devices: []string{"all"}, RequestedPeriod: &demand.Period{From: "2025-10", To: "2026-09", Granularity: "monthly"}, Source: demand.Source{Documentation: demand.DocumentationURL, DataAvailableThrough: demand.AvailableThrough, LastCompleteMonth: demand.LastCompleteMonth, WindowKnown: true, WindowNote: "Полные календарные месяцы. Пропуски остаются без значения."}}
	origin := "offline_quality_fixture"
	if path := os.Getenv("WORDSTAT_UI_DYNAMICS"); path != "" {
		var saved struct {
			Data struct {
				Result struct {
					Output string `json:"output"`
				} `json:"result"`
			} `json:"data"`
		}
		raw := read(path)
		if err := json.Unmarshal(raw, &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Data.Result.Output != "" {
			raw = []byte(saved.Data.Result.Output)
		}
		if err := json.Unmarshal(raw, &dynamics); err != nil {
			t.Fatal(err)
		}
		if dynamics.Status != "ok" || dynamics.Kind != "dynamics" {
			t.Fatal("saved dynamics result is not successful")
		}
		origin = "saved_provider_response"
	} else {
		start, _ := time.Parse("2006-01", "2025-10")
		for i, n := range []int64{754, 818, 903, 870, 761, 799, 823, 910, 844, 806, 875, 842} {
			dynamics.Rows = append(dynamics.Rows, demand.Row{Group: "dynamics", Label: start.AddDate(0, i, 0).Format(time.RFC3339), Count: countPointer(n)})
		}
	}
	write := func(name string, data []byte) {
		if err := os.WriteFile(filepath.Join(output, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	renderPage := func(name string, result demand.Result) {
		v := resultView(result)
		f := defaults()
		f.Kind = result.Kind
		f.Phrase = result.Phrase
		f.RegionIDs = ""
		if len(result.RegionIDs) > 0 {
			parts := make([]string, 0, len(result.RegionIDs))
			for _, id := range result.RegionIDs {
				parts = append(parts, strconv.Itoa(id))
			}
			f.RegionIDs = strings.Join(parts, ",")
		}
		f.RegionLabel = views.RegionCaption(result.RegionIDs)
		f.Device = normalizedDevice(result.Devices)
		f.RegionMode = result.RegionMode
		if result.RequestedPeriod != nil {
			f.FromMonth = result.RequestedPeriod.From
			f.ToMonth = result.RequestedPeriod.To
			f.PeriodPreset = "custom"
			if f.ToMonth == "2026-09" {
				switch f.FromMonth {
				case "2025-10":
					f.PeriodPreset = "12"
				case "2026-04":
					f.PeriodPreset = "6"
				case "2026-07":
					f.PeriodPreset = "3"
				}
			} else if f.FromMonth == "2025-01" && f.ToMonth == "2025-12" {
				f.PeriodPreset = "2025"
			}
		}
		var b bytes.Buffer
		if err := views.Index(views.HomeView{Form: f, Presets: presets(), Result: &v}).Render(t.Context(), &b); err != nil {
			t.Fatal(err)
		}
		write(name, b.Bytes())
	}
	renderPage("top.html", top)
	renderPage("index.html", dynamics)
	renderPage("regions.html", regional)
	edge := dynamics
	edge.SourceStatus = "offline_fixture"
	edge.Rows = append([]demand.Row(nil), dynamics.Rows...)
	if len(edge.Rows) > 3 {
		edge.Rows[0].Count = countPointer(0)
		edge.Rows = append(edge.Rows[:1], edge.Rows[2:]...)
	}
	renderPage("edge.html", edge)
	var b bytes.Buffer
	if err := views.Index(views.HomeView{Form: defaults(), Presets: presets()}).Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	write("home.html", b.Bytes())
	for name, result := range map[string]demand.Result{"top-result.html": top, "result.html": dynamics, "regions-result.html": regional} {
		b.Reset()
		if err := views.Result(resultView(result)).Render(t.Context(), &b); err != nil {
			t.Fatal(err)
		}
		write(name, b.Bytes())
	}
	unavailable := views.ResultView{Status: "unavailable_period", Message: "Период ноябрь 2026 недоступен. Последний полный месяц: сентябрь 2026. Запрошенный период сохранён.", Period: "ноябрь 2026 – ноябрь 2026"}
	b.Reset()
	if err := views.Feedback(unavailable).Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	write("error.html", b.Bytes())
	empty := top
	empty.Rows = nil
	empty.Data = map[string]any{}
	b.Reset()
	if err := views.Result(resultView(empty)).Render(t.Context(), &b); err != nil {
		t.Fatal(err)
	}
	write("empty.html", b.Bytes())
	write("app.css", views.AppCSS)
	write("explorer.js", views.AppJS)
	summary := map[string]any{"kind": "offline_browser_fixture", "provider_calls": 0, "top_source": "saved_mac_mcp_receipt", "region_source": "saved_native_response", "regions": len(regional.Rows), "dynamics_source": origin, "css_url": views.AppCSSPath, "js_url": views.AppJSPath, "pages": []string{"home.html", "top.html", "index.html", "regions.html", "edge.html"}}
	raw, _ := json.MarshalIndent(summary, "", "  ")
	write("fixture.json", raw)
}

func TestAbsoluteScaleTightCeilingAndIntegerLabels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		count int64
		upper string
		ticks int
	}{
		{"zero", 0, "1", 2},
		{"one", 1, "1", 2},
		{"ninety_percent", 910, "1\u00a0000", 5},
		{"invalid_negative", -1, "1", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := demand.Result{Status: "ok", Kind: "dynamics", Phrase: "касперский для бизнеса", RequestedPeriod: &demand.Period{From: "2026-09", To: "2026-09", Granularity: "monthly"}, Rows: []demand.Row{{Label: "2026-09-01", Count: countPointer(tc.count)}}}
			view := resultView(result)
			if len(view.Axis) != tc.ticks || view.Axis[0] != tc.upper {
				t.Fatalf("count %d: axis=%v, want upper %q with %d ticks", tc.count, view.Axis, tc.upper, tc.ticks)
			}
			if len(view.GridY) != len(view.Axis) || view.GridY[0] != "0.00" || view.GridY[len(view.GridY)-1] != "300.00" {
				t.Fatalf("grid and tick labels disagree: axis=%v grid=%v", view.Axis, view.GridY)
			}
			seen := map[string]bool{}
			for _, label := range view.Axis {
				clean := strings.ReplaceAll(label, "\u00a0", "")
				if clean == "" || strings.IndexFunc(clean, func(r rune) bool { return r < '0' || r > '9' }) >= 0 || seen[clean] {
					t.Fatalf("noninteger, negative or duplicate tick: %q", label)
				}
				seen[clean] = true
			}
			var chart views.Chart
			if err := json.Unmarshal([]byte(view.ChartJSON), &chart); err != nil {
				t.Fatal(err)
			}
			if tc.count >= 0 && (chart.Points[0].Y == nil || *chart.Points[0].Y < 0 || *chart.Points[0].Y > 1) {
				t.Fatalf("observation outside scale: %+v", chart.Points[0])
			}
			if tc.count < 0 && chart.Points[0].Y != nil {
				t.Fatal("invalid negative frequency was plotted")
			}
		})
	}
}
