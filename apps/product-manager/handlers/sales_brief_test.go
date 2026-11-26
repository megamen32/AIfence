package handlers

import (
	"encoding/json"
	"strings"
	"testing"

	"agent/internal/evidence"
)

func TestBriefUsesPanelWindowAndOnlyObservedMarketTerms(t *testing.T) {
	alert := monitorObservation{snapshot: "sales", details: monitorDetails{Origin: "imported_historical", Metric: "orders_index", Control: "astra_linux"},
		Row: evidence.Row{Product: "kaspersky_endpoint_security", Segment: "self_service", Source: "sales", Metric: watchdogMetric, Status: "observed", Value: "true", Details: windowJSON(80, 50, 70, 71),
			Ref: evidence.SourceRef{SourceID: "sales", Locator: "orders#19"}, Period: evidence.Period{From: "2026-01-12", To: "2026-01-18"}}}
	quote := monitorObservation{snapshot: "price", Row: evidence.Row{Source: "market", Kind: "offer", Metric: "displayed_price", Status: "observed", Product: "KL4210RAKDS", Value: "120", Unit: "RUB", Period: evidence.Period{To: "2026-02-10"},
		Ref: evidence.SourceRef{URL: "https://allsoft.ru/", SourceID: "allsoft", Locator: "offer"}, Details: `{"seller":"allsoft","name":"Kaspersky Endpoint Security","price_basis":"per_unit"}`}}
	demo := alert
	demo.snapshot = "fixture"
	demo.details.Origin = "synthetic_fixture"
	result := salesBrief([]monitorObservation{demo, quote, alert}, nil, 0)
	raw, _ := json.Marshal(result)
	if result.Total != 2 || result.NextOffset != nil {
		t.Fatal(string(raw))
	}
	for _, want := range []string{"−37,5 %", "два окна по 4 недели", "22.12.2025", "24.11.2025", "1,4 %", "orders#19", "Оценки в ₽ за неделю нет", "KL4210RAKDS", "Allsoft", "10.02.2026", "Нет подтверждённой пары магазинов"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %q in %s", want, raw)
		}
	}
	closed := alert
	closed.Value = "false"
	closed.Period.To = "2026-01-25"
	if v := salesBrief([]monitorObservation{alert, closed}, nil, 0); v.Total != 0 {
		t.Fatal(v)
	}
}
