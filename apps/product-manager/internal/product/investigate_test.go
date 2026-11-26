package product

import (
	"agent/internal/evidence"
	"testing"
)

func TestProposeUsesComparableFullWeeks(t *testing.T) {
	row := func(name, variant, value string) evidence.Row {
		return evidence.Row{Source: "sales", Product: name, ID: name + "/" + variant, Metric: "family_orders_idx.relative_change_pct", Status: "observed", Period: evidence.Period{From: "2025-01-06", To: "2025-05-25"}, Details: `{"variant":"` + variant + `","baseline_mean":100,"current_mean":` + value + `,"current_weeks":6,"baseline_from":"2025-01-06","baseline_to":"2025-03-30","current_from":"2025-04-14"}`}
	}
	rows := []evidence.Row{row("Control", "full_weeks", "103"), row("Target", "all_reported_weeks", "20"), row("Target", "full_weeks", "80")}
	h, err := propose(rows, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if h.Hypothesis.ProductKey != "Target" || h.Hypothesis.SignalIDs[0] != "Target/full_weeks" || h.Hypothesis.SuccessCriterion.ControlProductKey != "Control" || h.Hypothesis.SuccessCriterion.BaselineFrom != "2025-04-28" || h.Hypothesis.Status != "proposed" {
		t.Fatal(h)
	}
	_, err = propose(rows[:2], "fixture")
	if err == nil {
		t.Fatal("partial week or growing series produced a decline")
	}
}
