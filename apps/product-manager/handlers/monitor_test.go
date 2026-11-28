package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/internal/product"
	"agent/views"
	"encoding/json"
	"math"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestMonitorUsesObservedValuesAndActualDetection(t *testing.T) {
	p := monitorObservation{
		Row: evidence.Row{ID: "synthetic-1", Product: "demo_target", Metric: watchdogMetric,
			Source: "sales", Status: "observed", Value: "true", Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"},
			Details: windowJSON(100, 70, 100, 100)},
		snapshot: "demo",
		details:  monitorDetails{Origin: "synthetic_fixture", Control: "demo_control"},
	}
	_, cell := monitorCell(p)
	if cell.Tone != "alarm" {
		t.Fatal(cell)
	}
	v := investigation(p.Product, []monitorObservation{p}, nil)
	if v.Origin != "ДЕМО · синтетический срез" || v.Detected != "Время обнаружения неизвестно" ||
		len(v.Chart.Dots) != 2 || v.Chart.Target == v.Chart.Control {
		t.Fatalf("unexpected monitor state: %+v", v)
	}
	if chart := plot(&p, []monitorObservation{p}, "2026-01-04"); chart.ReleaseX != "300.0" {
		t.Fatalf("release mark %q", chart.ReleaseX)
	}
	p.Status = "missing"
	if _, cell := monitorCell(p); cell.Tone != "" {
		t.Fatalf("missing observation shown as confirmed: %+v", cell)
	}
	if result := signalTime("2026-01-19T10:17:00+03:00", nil); !strings.Contains(result, "07:17 UTC") {
		t.Fatal(result)
	}
}

func TestMonitorCostNeedsBothInputs(t *testing.T) {
	var v views.Investigation
	monitorEconomics(&v, "3", "")
	if v.Cost != "" || v.CostError == "" {
		t.Fatal(v)
	}
	v = views.Investigation{}
	monitorEconomics(&v, "2", "30,5")
	if v.Cost != "61,00 ₽ / неделю" {
		t.Fatal(v)
	}
}

func TestSignalDeltaFallsBackWhenControlAdjustedDeltaIsUnavailable(t *testing.T) {
	window := &evidence.WindowContrast{
		BaselineMean: 100, CurrentMean: 67.3, ControlBaselineMean: math.NaN(), ControlCurrentMean: 100,
		RelativeChangePct: -32.7,
	}
	got := signalDelta(window)
	if got == nil || *got != -32.7 {
		t.Fatalf("signal delta = %v, want observed drop", got)
	}
}

func TestMonitorMergesActiveAlertsByProductAndKeepsOnset(t *testing.T) {
	row := func(id, scenario, detected string, after float64) monitorObservation {
		return monitorObservation{Row: evidence.Row{ID: id, Product: "kaspersky_endpoint_security", Source: "sales", Metric: watchdogMetric, Status: "observed", Value: "true", Period: evidence.Period{From: "2025-12-22", To: "2026-01-18"}, Details: strings.TrimSuffix(windowJSON(100, after, 100, 100), "}") + `,"scenario_id":"` + scenario + `"}`}, snapshot: "demo", details: monitorDetails{Origin: "synthetic_fixture", Control: "astra_linux_special_edition", Scenario: scenario, FirstDetected: detected}}
	}
	rows := []monitorObservation{
		row("alert-old", "old", "2026-10-12T06:00:00Z", 99),
		row("alert-mid", "mid", "2026-10-26T06:00:00Z", 98),
		row("alert-strong", "strong", "2026-11-02T06:00:00Z", 67.3),
	}
	view := monitorView(rows, nil, "", "", "", false)
	if len(view.Pulse) != 1 || view.Pulse[0].ProductKey != "kaspersky_endpoint_security" {
		t.Fatalf("pulse = %+v", view.Pulse)
	}
	if view.Pulse[0].FirstDetected != "Впервые обнаружено 12.10.2026, 06:00 UTC" {
		t.Fatalf("onset = %q", view.Pulse[0].FirstDetected)
	}
}

func TestDemoWeeklyLossUsesMarketPriceAndGap(t *testing.T) {
	signal := monitorObservation{Row: evidence.Row{Product: "kaspersky_endpoint_security"}, snapshot: "demo", details: monitorDetails{Control: "astra_linux_special_edition"}}
	market := monitorObservation{Row: evidence.Row{Product: demoKESSKU, Source: "market", Kind: "offer", Metric: "displayed_price", Status: "observed", Unit: "RUB", Value: "3500000", Period: evidence.Period{To: "2026-11-16"}}, snapshot: "demo"}
	weekly, formula := demoWeeklyLoss([]monitorObservation{signal, market}, signal, &evidence.WindowContrast{RelativeChangePct: -32.7})
	if weekly != "Потеря ≈ 1 144 500,00 ₽ в неделю" || !strings.Contains(formula, "ДЕМО") || !strings.Contains(formula, "3 500 000,00 ₽") {
		t.Fatalf("loss=%q formula=%q", weekly, formula)
	}
}

func TestDemandComparisonKeepsKESAndAstraTogether(t *testing.T) {
	row := func(product, source, metric, from, to, value string) monitorObservation {
		return monitorObservation{Row: evidence.Row{Product: product, Source: "research", Metric: metric, Value: value, Unit: "count", Status: "observed", Period: evidence.Period{From: from, To: to}, Ref: evidence.SourceRef{SourceID: source}}}
	}
	rows := []monitorObservation{
		row("kaspersky_endpoint_security", "wordstat", "search_queries", "2026-09-07", "2026-09-13", "12800"),
		row("kaspersky_endpoint_security", "wordstat", "search_queries", "2026-10-26", "2026-11-01", "8600"),
		row("astra_linux_special_edition", "wordstat", "search_queries", "2026-09-07", "2026-09-13", "7600"),
		row("astra_linux_special_edition", "wordstat", "search_queries", "2026-10-26", "2026-11-01", "7340"),
	}
	v := demandComparison("kaspersky_endpoint_security", "astra_linux_special_edition", rows)
	if len(v.Sources) != 2 || v.Sources[0].TargetCurrent != "8600" || v.Sources[0].ControlCurrent != "7340" {
		t.Fatalf("unexpected demand comparison: %+v", v)
	}
	if v.Sources[0].TargetChange != "−32,8 %" || v.Sources[0].ControlChange != "−3,4 %" {
		t.Fatalf("unexpected demand changes: %+v", v.Sources[0])
	}
	if v.Sources[1].Status != "Нет наблюдения" {
		t.Fatalf("missing Trends should remain explicit: %+v", v.Sources[1])
	}
}

func TestFramePathIsRestrictedToCallerAndGeneratedFiles(t *testing.T) {
	const root = "/apps/12324004-0746-47a6-8174-27dc3fb15759/tmp/path-check/"
	const leaf = "/12324004-0746-47a6-8174-27dc3fb15759/desktop.png"
	if got := callerFramePath(root+"user-1"+leaf, "1"); !strings.HasPrefix(got, "tmp/path-check/user-1/") {
		t.Fatal(got)
	}
	for _, bad := range []string{root + "user-2" + leaf, root + "user-1/../desktop.png", "https://example.test/desktop.png"} {
		if got := callerFramePath(bad, "1"); got != "" {
			t.Fatal(got)
		}
	}
}

func TestEarlierObservedAlertSurvivesIncompleteWeek(t *testing.T) {
	row := func(id, day, status, value string) monitorObservation {
		return monitorObservation{Row: evidence.Row{ID: id, Product: "demo", Metric: watchdogMetric,
			Status: status, Value: value, Period: evidence.Period{To: day}}, snapshot: "owner-snapshot"}
	}
	incomplete := row("open-window", "2026-01-25", "missing", "null")
	alert := row("saved-alert", "2026-01-18", "observed", "true")
	stable := row("no-alert", "2026-01-11", "observed", "false")
	fresh, active := watchdogState([]monitorObservation{incomplete, alert, stable})
	if fresh[seriesOf(alert)].ID != incomplete.ID || len(active) != 1 || active[0].ID != alert.ID {
		t.Fatalf("freshness replaced the open alert: %+v / %+v", fresh, active)
	}
	stable.Period.To = "2026-01-22"
	_, active = watchdogState([]monitorObservation{incomplete, stable, alert})
	if len(active) != 0 {
		t.Fatalf("a later observed stable result closes the detector alert: %+v", active)
	}
}

func TestSavedLegacyCardPlotsOnlyItsComparableControl(t *testing.T) {
	point := func(id, product, numbers string) monitorObservation {
		return monitorObservation{Row: evidence.Row{
			ID: id, Product: product, Source: "sales", Metric: "family_orders_idx.relative_change_pct",
			Status: "observed", Period: evidence.Period{From: "2026-01-01", To: "2026-02-28"},
			Details: `{"variant":"full_weeks","baseline_from":"2026-01-01","baseline_to":"2026-01-31","current_from":"2026-02-01",` + numbers + "}",
		}, snapshot: "saved"}
	}
	target := point("target", "demo_target", `"baseline_mean":10,"current_mean":5`)
	control := point("control", "demo_control", `"baseline_mean":8,"current_mean":9`)
	record := decision.Record{EvidenceSnapshotID: "saved", Hypothesis: decision.Hypothesis{
		ID: "old-id", ProductKey: target.Product, SignalIDs: []string{target.ID}, CounterIDs: []string{control.ID},
		SuccessCriterion: decision.Criterion{ControlProductKey: control.Product},
	}}
	v := savedCard(record, []monitorObservation{target, control})
	if v.CardID != "old-id" || len(v.Chart.Dots) != 2 || v.Chart.Control == "" || v.Chart.Fill == "" {
		t.Fatalf("saved-card comparison unavailable: %+v", v)
	}
	control.Segment = "different"
	v = savedCard(record, []monitorObservation{target, control})
	if v.Chart.Target == "" || v.Chart.Control != "" || v.Chart.Fill != "" {
		t.Fatalf("incomparable control plotted: %+v", v.Chart)
	}
}

func TestNormalCardAndChatFocusChooseSameCardView(t *testing.T) {
	focus := product.Focus{HypothesisID: "saved-from-chat"}
	for path, want := range map[string]string{
		"/?id=old-bookmark": "old-bookmark",
		"/":                 "saved-from-chat",
		"/?overview=1":      "",
		"/?monitor=other":   "",
	} {
		q := httptest.NewRequest("GET", path, nil).URL.Query()
		if got := cardIDForView(q, focus); got != want {
			t.Fatalf("%s picked %q, want %q", path, got, want)
		}
	}
}

func TestMonitorOpensSavedAlertAlongsideNewerIncompleteWindow(t *testing.T) {
	alert := monitorObservation{
		Row: evidence.Row{ID: "saved-alert", Product: "target", Metric: watchdogMetric,
			Source: "sales", Status: "observed", Value: "true", Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"},
			Details: windowJSON(20, 10, 15, 15)},
		snapshot: "owner-snapshot",
		details:  monitorDetails{Origin: "imported_historical", Control: "control", FirstDetected: "2026-01-19T06:00:00Z"},
	}
	incomplete := alert
	incomplete.ID, incomplete.Period.To, incomplete.Status, incomplete.Value = "recent-window", "2026-01-25", "missing", "null"
	rows := []monitorObservation{alert, incomplete}
	v := monitorView(rows, nil, "", "", "", true)
	p := v.Products[0]
	if p.Cells[0].Label != "Открытая тревога" || p.AlertWeek != "18.01.2026" ||
		p.Freshness != "Неполный замер 25.01.2026" || !strings.Contains(p.URL, "alert=saved-alert") {
		t.Fatalf("map did not distinguish freshness and the alert: %+v", p)
	}
	if v.Selected == nil || v.Selected.Chart.Target == "" || v.Selected.Chart.Control == "" ||
		v.Selected.FirstDetected != "Впервые обнаружено 19.01.2026, 06:00 UTC" || v.Selected.AlertID != alert.ID {
		t.Fatalf("wrong opened investigation: %+v", v.Selected)
	}
}

func TestMonitorReadsAllEvidencePages(t *testing.T) {
	for _, tc := range []struct {
		offset, rows, total int
		done                bool
	}{
		{offset: 0, rows: 100, total: 201, done: false},
		{offset: 100, rows: 100, total: 201, done: false},
		{offset: 200, rows: 1, total: 201, done: true},
		{offset: 200, rows: 0, total: 201, done: true},
	} {
		if got := monitorPageComplete(tc.offset, tc.rows, tc.total); got != tc.done {
			t.Fatalf("page completion at %+v: got %v", tc, got)
		}
	}
}

func TestAlarmTimeSeparatesDetectionAndFirstRegistration(t *testing.T) {
	received := time.Date(2026, 1, 20, 9, 10, 11, 0, time.UTC)
	if got := signalTime("", &received); got != "получено в 09:10 UTC, 20.01.2026" {
		t.Fatal(got)
	}
	if got := signalTime("2026-01-19T10:17:00+03:00", &received); got != "Обнаружено 19.01.2026, 07:17 UTC" {
		t.Fatal(got)
	}
	if got := signalTime("", nil); got != "Время обнаружения неизвестно" {
		t.Fatal(got)
	}
}

func windowJSON(before, after, controlBefore, controlAfter float64) string {
	d := evidence.WindowContrast{
		Baseline:    evidence.Period{From: "2025-11-24", To: "2025-12-21"},
		Current:     evidence.Period{From: "2025-12-22", To: "2026-01-18"},
		WindowWeeks: 4, BaselineMean: before, CurrentMean: after,
		ControlBaselineMean: controlBefore, ControlCurrentMean: controlAfter,
		RelativeChangePct: (after/before - 1) * 100,
	}
	b, _ := json.Marshal(d)
	return string(b)
}

func TestWatchdogEvidenceUsesTheSameValidatedWindowAsPanel(t *testing.T) {
	row := evidence.Row{
		Source: "sales", Metric: watchdogMetric, Status: "observed", Value: "true",
		Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"},
		Details: strings.TrimSuffix(windowJSON(80, 50, 70, 71), "}") +
			`,"drop_pct":20,"control_adjusted_change_pct":-39.12,"raw_week_comparison":{"relative_change_pct":-8}}`,
	}
	view := rowView("snapshot", row)
	for _, text := range []string{"−37,5 %", "окну из 4 нед.", "24.11.2025 / 21.12.2025", "22.12.2025 / 18.01.2026", "с поправкой на контроль −39,1 %"} {
		if !strings.Contains(view.Summary, text) {
			t.Fatalf("missing %q from %q", text, view.Summary)
		}
	}
	if strings.Contains(view.Summary, "−20") || strings.Contains(view.Summary, "−8") {
		t.Fatal(view.Summary)
	}
	row.Details = strings.ReplaceAll(row.Details, "2025-12-22", "2025-12-29")
	if view := rowView("snapshot", row); strings.Contains(view.Summary, "%") {
		t.Fatalf("invalid window exposes competing percentages: %q", view.Summary)
	}
}

func TestColdOverviewAndChangedChatFocusAreDistinct(t *testing.T) {
	old := product.Focus{ProductKey: "demo_target", SnapshotID: "demo", HypothesisID: "demo-card", Synthetic: true}
	q, shown := panelFocusQuery(url.Values{}, old)
	if shown.ProductKey != "" || hasPanelSelection(q) {
		t.Fatal("cold entry must not resume a saved educational card")
	}
	if _, shown := panelFocusQuery(q, old); shown.ProductKey != "" {
		t.Fatal("initial subscription refresh resumed an unchanged educational focus")
	}
	explicit := url.Values{"id": {old.HypothesisID}}
	if !hasPanelSelection(explicit) || cardIDForView(explicit, old) != old.HypothesisID {
		t.Fatal("saved educational card must remain reachable by its normal link")
	}
	fresh := product.Focus{ProductKey: "new-training-product", SnapshotID: "case", HypothesisID: "new-asked-card", Synthetic: true}
	q, shown = panelFocusQuery(q, fresh)
	if shown.HypothesisID != fresh.HypothesisID {
		t.Fatal("explicit new focus from chat was lost")
	}
	if _, shown := panelFocusQuery(q, fresh); shown.HypothesisID != fresh.HypothesisID {
		t.Fatal("same open panel stopped following an active chat selection")
	}
	if updated, shown := panelFocusQuery(q, product.Focus{}); updated.Has("focus_open") || shown.ProductKey != "" {
		t.Fatal("cleared chat focus stayed open")
	}
}

func TestColdOverviewDoesNotExpandLegacyDemoRows(t *testing.T) {
	alert := monitorObservation{
		Row: evidence.Row{ID: "demo-alert", Product: "demo_target", Source: "sales", Metric: watchdogMetric,
			Status: "observed", Value: "true", Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"},
			Details: windowJSON(80, 55, 70, 71)},
		snapshot: "demo",
		details:  monitorDetails{Origin: "synthetic_fixture", Control: "demo_control"},
	}
	auto := monitorView([]monitorObservation{alert}, nil, "", "", "", true)
	if auto.Selected != nil || len(auto.Products) == 0 || auto.Products[0].Origin != "ДЕМО · синтетический срез" || auto.Products[0].Name != "Kaspersky Small Office Security" {
		t.Fatalf("legacy demo became the default view: %+v", auto)
	}
	explicit := monitorView([]monitorObservation{alert}, nil, alert.Product, alert.snapshot, alert.ID, true)
	if explicit.Selected == nil || explicit.Selected.Chart.Target == "" || explicit.Selected.Control != "Dr.Web Desktop Security Suite" {
		t.Fatal("selecting the educational alert should still open its graph")
	}
}
