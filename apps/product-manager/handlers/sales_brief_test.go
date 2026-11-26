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
	other := quote
	other.Status = "incomparable"
	other.Ref.URL = "https://softline.ru/"
	other.Details = `{"seller":"softline","name":"Kaspersky Endpoint Security","price_basis":"displayed_offer_price"}`
	result := salesBrief([]monitorObservation{demo, quote, other, alert}, nil, BriefQuery{})
	raw, _ := json.Marshal(result)
	if result.Total != 3 || result.PriceCount != 2 || len(result.Comparisons) != 1 || len(result.Comparisons[0].Sellers) != 2 || result.Comparisons[0].ComparableGroups != 0 || result.NextOffset != nil {
		t.Fatal(string(raw))
	}
	for _, want := range []string{"−37,5 %", "два окна по 4 недели", "22.12.2025", "24.11.2025", "1,4 %", "orders#19", "Оценки в ₽ за неделю нет", "KL4210RAKDS", "Allsoft", "10.02.2026", "Нет подтверждённой пары магазинов"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("missing %q in %s", want, raw)
		}
	}
	summary := salesBrief([]monitorObservation{quote, other, alert}, nil, BriefQuery{Summary: true})
	if len(summary.Readings) != 1 || !strings.Contains(summary.Readings[0].Price, "0 из 1") || len(summary.Readings[0].OfferExample) != 2 || !strings.Contains(summary.Readings[0].Weekly, "₽") {
		t.Fatal(summary)
	}
	separate := ProductReading{Product: "Kaspersky Small Office Security"}
	addPriceContext(&separate, map[string]PriceComparison{"KL4210RAKDS": result.Comparisons[0]}, result.Facts)
	if len(separate.OfferExample) != 0 || !strings.Contains(separate.Price, "нет данных") {
		t.Fatal(separate)
	}
	closed := alert
	closed.Value = "false"
	closed.Period.To = "2026-01-25"
	if v := salesBrief([]monitorObservation{alert, closed}, nil, BriefQuery{}); v.Total != 0 {
		t.Fatal(v)
	}
}
