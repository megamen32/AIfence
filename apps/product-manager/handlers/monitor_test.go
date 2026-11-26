package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/internal/product"
	"agent/views"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestMonitorUsesObservedValuesAndActualDetection(t *testing.T) {
	point := func(v float64) *float64 { return &v }
	p := monitorObservation{
		Row: evidence.Row{ID: "synthetic-1", Product: "demo_target", Metric: watchdogMetric,
			Status: "observed", Value: "true", Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"}},
		snapshot: "demo",
		details: monitorDetails{Origin: "synthetic_fixture", Control: "demo_control",
			TargetCurrent: point(70), ControlCurrent: point(100), TargetBase: point(100), ControlBase: point(100),
			Baseline: evidence.Period{To: "2026-01-04"}},
	}
	_, cell := monitorCell(p)
	if cell.Tone != "alarm" {
		t.Fatal(cell)
	}
	v := investigation(p.Product, []monitorObservation{p}, nil)
	if v.Origin != "Учебные данные" || v.Detected != "Время обнаружения неизвестно" ||
		len(v.Chart.Dots) != 2 || v.Chart.Target == v.Chart.Control {
		t.Fatalf("unexpected monitor state: %+v", v)
	}
	if chart := plot(&p, []monitorObservation{p}, "2026-01-11"); chart.ReleaseX != "300.0" {
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
	num := func(v float64) *float64 { return &v }
	alert := monitorObservation{
		Row: evidence.Row{ID: "saved-alert", Product: "target", Metric: watchdogMetric,
			Status: "observed", Value: "true", Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"}},
		snapshot: "owner-snapshot",
		details: monitorDetails{Origin: "imported_historical", Control: "control",
			TargetBase: num(20), TargetCurrent: num(10), ControlBase: num(15), ControlCurrent: num(15),
			Baseline: evidence.Period{To: "2026-01-04"}},
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
		v.Selected.Detected != "Время обнаружения неизвестно" || v.Selected.AlertID != alert.ID {
		t.Fatalf("wrong opened investigation: %+v", v.Selected)
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
