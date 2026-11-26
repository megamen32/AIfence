package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"testing"
)

func TestPricesKeepSnapshotSKUAndQuoteBasisSeparate(t *testing.T) {
	row := func(product, snapshot, seller, basis, qty string) monitorObservation {
		return monitorObservation{snapshot: snapshot, Row: evidence.Row{
			Source: "market", Kind: "offer", Metric: "displayed_price", Status: "observed", Product: product,
			Unit: "RUB", Value: "31.25", Period: evidence.Period{To: "2026-01-03"},
			Details: `{"seller":"` + seller + `","price_basis":"` + basis + `","quantity":` + qty +
				`,"max_quantity":3,"market_comparison":{"group":"source-group","comparable":true,"hypothesis_id":"owned"}}`,
		}}
	}
	const key = "DEMO-1"
	rows := []monitorObservation{
		row(key, "selected", "first", "per_unit", "1"),
		row("demo-1", "selected", "second", "per_unit", "1"),
		row(key, "selected", "third", "displayed_total", "3"),
		row(key, "selected", "fourth", "displayed_total", "null"),
		row("DEMO-2", "selected", "different-product", "per_unit", "1"),
		row(key, "older", "different-snapshot", "per_unit", "1"),
	}
	records := []decision.Record{
		{Hypothesis: decision.Hypothesis{ID: "owned", Status: "in_backlog"}, Task: &decision.Task{ID: "DEMO-TASK"}},
	}
	got := currentPrices(key, "selected", rows, records)
	if got == nil || len(got.Comparable) != 1 || len(got.Separate) != 2 ||
		len(got.Comparable[0].Offers) != 2 || got.Comparable[0].Offers[0].Date != "03.01.2026" ||
		got.TaskStatus != "Задача DEMO-TASK · В бэклоге" {
		t.Fatalf("incorrect market view: %+v", got)
	}
}
