package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestReleaseHistoryUsesRatiosOfSumsAndKnownRelease(t *testing.T) {
	start, _ := time.Parse(time.DateOnly, "2026-09-28")
	var rows []monitorObservation
	for n, o := range []int{347, 339, 340, 342, 218, 221, 220, 213} {
		d := start.AddDate(0, 0, n*7)
		p := evidence.Period{From: d.Format(time.DateOnly), To: d.AddDate(0, 0, 6).Format(time.DateOnly)}
		for k, v := range map[string]int{"checkouts": o, "cart_additions": []int{606, 582, 573, 589, 795, 805, 807, 775}[n]} {
			rows = append(rows, monitorObservation{snapshot: "example", Row: evidence.Row{ID: fmt.Sprintf("%s-%d", k, n), Product: "product", Kind: "metric", Status: "observed", Source: "metrika", Metric: k, Value: fmt.Sprint(v), Unit: "count", Segment: "desktop", Period: p}})
		}
	}
	rows = append(rows, monitorObservation{snapshot: "example", Row: evidence.Row{Product: "product", Kind: "release", Status: "observed", Source: "release", Value: "Campaign", Details: "{\"release_id\":\"R-25\",\"release_date\":\"2026-10-26\",\"status\":\"выпущен\"}"}})
	h := nativeReleaseHistory("product", rows, "example")
	if h == nil || h.Pivot != "R-25" || h.BeforeRate != "58,2" || h.AfterRate != "27,4" || h.BeforeCount != "342" || h.AfterCount != "218" || len(h.Dots) != 8 {
		t.Fatalf("bad comparable release: %+v", h)
	}
	if nativeReleaseHistory("product", rows, "different") != nil {
		t.Fatal("included a different snapshot")
	}
	rows = rows[:3]
	h = nativeReleaseHistory("product", rows, "example")
	if h == nil || len(h.Dots) != 1 || h.Pivot != "" {
		t.Fatalf("unexpected comparison with incomplete data: %+v", h)
	}
}
func TestCorrectionMeasurementIndependentOfReleaseHistory(t *testing.T) {
	a, b := 32., 46.
	r := decision.Record{Hypothesis: decision.Hypothesis{ID: "test", ProductKey: "product"}, Task: &decision.Task{ID: "PM-test"}}
	v := savedCard(r, nil)
	if !strings.Contains(v.CorrectionStatus, "PM-test") || v.After != "" {
		t.Fatalf("unexpected pending data: %+v", v)
	}
	r.Assessment = &decision.Assessment{Reason: "Measured", Value: &b, ReleasedOn: "2026-11-01", Measurement: &decision.Measurement{Baseline: decision.PeriodValue{Complete: true, Value: &a, Unit: "count"}, Current: decision.PeriodValue{Complete: true, Value: &b, Unit: "count"}}}
	v = savedCard(r, nil)
	if v.MeasuredAfterWidth != "100.0" || v.CorrectionStatus != "" || v.Before == "" || v.After == "" {
		t.Fatalf("measured values were not shown: %+v", v)
	}
}
