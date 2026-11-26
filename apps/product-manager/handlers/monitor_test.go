package handlers

import (
	"agent/internal/evidence"
	"agent/views"
	"strings"
	"testing"
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
	if result := detectedAt("2026-01-19T10:17:00+03:00"); !strings.Contains(result, "07:17 UTC") {
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
