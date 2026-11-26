package product

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"strings"
	"testing"
)

func TestProposeUsesComparableFullWeeks(t *testing.T) {
	row := func(name, variant, value string) evidence.Row {
		return evidence.Row{Source: "sales", Product: name, ID: name + "/" + variant, Metric: "family_orders_idx.relative_change_pct", Status: "observed", Period: evidence.Period{From: "2025-01-06", To: "2025-05-25"}, Details: `{"variant":"` + variant + `","baseline_mean":100,"current_mean":` + value + `,"current_weeks":6,"baseline_from":"2025-01-06","baseline_to":"2025-03-30","current_from":"2025-04-14"}`}
	}
	rows := []evidence.Row{row("dr_web_desktop_security_suite", "full_weeks", "103"), row("kaspersky_small_office_security", "all_reported_weeks", "20"), row("kaspersky_small_office_security", "full_weeks", "80")}
	h, err := proposeLegacy(rows, "fixture", "")
	if err != nil {
		t.Fatal(err)
	}
	if h.Hypothesis.ProductKey != "kaspersky_small_office_security" || h.Hypothesis.SignalIDs[0] != "kaspersky_small_office_security/full_weeks" || h.Hypothesis.SuccessCriterion.ControlProductKey != "dr_web_desktop_security_suite" || h.Hypothesis.SuccessCriterion.BaselineFrom != "2025-04-28" || h.Hypothesis.Status != "proposed" {
		t.Fatal(h)
	}
	_, err = proposeLegacy(rows[:2], "fixture", "")
	if err == nil {
		t.Fatal("partial week or growing series produced a decline")
	}
}

func TestInvestigateWatchdog(t *testing.T) {
	const detail = `{
		"metric_key":"orders_index","control_product_key":"dr_web_desktop_security_suite",
		"baseline_period":{"from":"2025-01-06","to":"2025-02-02"},
		"current_period":{"from":"2025-02-03","to":"2025-03-02"},
		"baseline_mean":10,"current_mean":5,"control_baseline_mean":10,"control_current_mean":10,
		"relative_change_pct":-50,"control_adjusted_change_pct":-50,
		"evidence_ids":["series/a"],"source_refs":[{"id":"series/a","source_ref":{"source_id":"fixture","locator":"row 1"}}]
	}`
	hit := evidence.Row{ID: "watchdog/week1", Kind: "metric", Source: "sales", Product: "kaspersky_small_office_security", Metric: WatchdogMetric, Status: "observed", Value: "true",
		Period: evidence.Period{From: "2025-02-24", To: "2025-03-02"}, Details: detail}
	release := evidence.Row{ID: "release/earlier", Source: "release", Product: hit.Product, Status: "observed",
		Period: evidence.Period{From: "2025-02-12", To: "2025-02-12"}}
	later := release
	later.ID = "release/future"
	later.Period = evidence.Period{From: "2025-04-01", To: "2025-04-01"}
	path := evidence.Row{ID: "browser/desktop", Kind: "path_check", Source: "browser", Product: hit.Product, Segment: "desktop",
		Status: "observed", Value: "ok", Period: later.Period}
	rows := []evidence.Row{hit, release, later, path}
	selected := InvestigateInput{SnapshotID: "fixture", ProductKey: hit.Product, WeekFrom: hit.Period.From}
	a, err := investigationDraft(rows, selected)
	if err != nil {
		t.Fatal(err)
	}
	h := a.Hypothesis
	if h.EvidenceLevel != "path_verified" || h.SuccessCriterion.MetricKey != "orders_index" ||
		h.SuccessCriterion.BaselineFrom != "2025-02-03" || h.SuccessCriterion.ControlProductKey != "dr_web_desktop_security_suite" ||
		len(h.BrowserEvidenceIDs) != 1 || len(a.ContextIDs) != 1 || a.ContextIDs[0] != release.ID {
		t.Fatal(a)
	}

	withAnotherSnapshot, err := investigationDraft(rows, InvestigateInput{SnapshotID: "another"})
	if err != nil || withAnotherSnapshot.Hypothesis.ID != h.ID {
		t.Fatalf("same weekly alert changed identity: %v, %v", withAnotherSnapshot, err)
	}
	second := hit
	second.ID = "watchdog/week2"
	second.Period = evidence.Period{From: "2025-03-03", To: "2025-03-09"}
	second.Details = strings.NewReplacer("2025-01-06", "2025-01-13", "2025-02-02", "2025-02-09", "2025-02-03", "2025-02-10", "2025-03-02", "2025-03-09").Replace(detail)
	newest, err := investigationDraft(append(rows, second), InvestigateInput{SnapshotID: "another"})
	if err != nil || newest.Hypothesis.ID == h.ID || newest.Hypothesis.SignalIDs[0] != second.ID {
		t.Fatalf("a different week must get its own card: %v, %v", newest, err)
	}
	legacy := evidence.Row{ID: "legacy/drop", Source: "sales", Metric: "family_orders_idx.relative_change_pct", Product: hit.Product, Status: "observed", Period: hit.Period,
		Details: `{"variant":"full_weeks","baseline_mean":10,"current_mean":5,"current_weeks":4}`}
	for _, value := range []string{"false", "null"} {
		quiet := hit
		quiet.Value = value
		if _, err := investigationDraft([]evidence.Row{quiet, legacy}, InvestigateInput{SnapshotID: "fixture"}); err == nil {
			t.Fatalf("watchdog result %q triggered a legacy hypothesis", value)
		}
	}
}

func TestSignalReadbackUsesExactSavedSignal(t *testing.T) {
	details := `{"baseline_period":{"from":"2025-01-06","to":"2025-02-02"},"current_period":{"from":"2025-02-03","to":"2025-03-02"},
			"baseline_mean":10,"current_mean":5,"control_baseline_mean":10,"control_current_mean":10,"relative_change_pct":-50}`
	h := decision.Hypothesis{ID: "watch-saved", ProductKey: "kaspersky_small_office_security", SignalIDs: []string{"watchdog/saved"}}
	row := evidence.Row{ID: "watchdog/other", Product: h.ProductKey, Source: "sales", Metric: WatchdogMetric, Status: "observed", Value: "true",
		Period: evidence.Period{From: "2025-02-24", To: "2025-03-02"}, Details: details}
	record := decision.Record{Hypothesis: h}
	missing := signalReadback(record, []evidence.Row{row})
	if !missing.SignalContrastUnverified || missing.SignalContrast != nil {
		t.Fatal("a different alert filled an old card")
	}
	row.ID = h.SignalIDs[0]
	got := signalReadback(record, []evidence.Row{row})
	if got.SignalContrastUnverified || got.SignalContrast == nil || got.SignalContrast.RelativeChangePct != -50 {
		t.Fatalf("exact signal failed readback: %+v", got)
	}
}
