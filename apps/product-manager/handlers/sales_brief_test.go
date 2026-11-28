package handlers

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

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
	if len(summary.Readings) != 1 || summary.Readings[0].PriceEvidenceStatus != "insufficient_comparable_quotes" || !strings.Contains(summary.Readings[0].Price, "0 из 1") || len(summary.Readings[0].OfferExample) != 2 || !strings.Contains(summary.Readings[0].PriceTiming, "10.02.2026") || !strings.Contains(summary.Readings[0].Weekly, "₽") {
		t.Fatal(summary)
	}
	got := summary.Readings[0]
	if got.CheckedSKUs != 1 || got.ComparableSKUs != 0 || got.EstimatedWeeklyRUB != nil || got.Comparison.Control.Percent != 1.4 || got.Comparison.Target.Percent != -37.5 || got.Comparison.Current.To != "2026-01-18" {
		t.Fatal(got)
	}
	during := quote
	during.Period.To = "2026-01-14"
	overlap := salesBrief([]monitorObservation{during, alert}, nil, BriefQuery{Summary: true})
	if overlap.Readings[0].PriceTiming != "" {
		t.Fatal("an in-window quote was labeled post-period")
	}
	separate := ProductReading{Product: "Kaspersky Small Office Security"}
	addPriceContext(&separate, map[string]PriceComparison{"KL4210RAKDS": result.Comparisons[0]}, result.Facts)
	if len(separate.OfferExample) != 0 || !strings.Contains(separate.Price, "нет данных") {
		t.Fatal(separate)
	}
	eligible := PriceComparison{Product: "Kaspersky Endpoint Security", SKU: "KL4210RAKDS", ComparableGroups: 1}
	paired := ProductReading{Product: eligible.Product}
	addPriceContext(&paired, map[string]PriceComparison{eligible.SKU: eligible}, nil)
	if paired.PriceEvidenceStatus != "comparable_quotes_available" || strings.Contains(paired.Price, "Оснований менять цену") {
		t.Fatal(paired)
	}
	closed := alert
	closed.Value = "false"
	closed.Period.To = "2026-01-25"
	if v := salesBrief([]monitorObservation{alert, closed}, nil, BriefQuery{}); v.Total != 0 {
		t.Fatal(v)
	}
}

func TestDirectorDecisionFollowsComparableGroups(t *testing.T) {
	r := ProductReading{PriceEvidenceStatus: "insufficient_comparable_quotes", CheckedSKUs: 3, Sales: "Самостоятельные заказы −7,5%, контроль 1,2%.", Weekly: "Сумма неизвестна.", Source: "synthetic/sales"}
	brief := directorReply(r)
	if !strings.HasPrefix(brief.Decision, "Снижать цену пока не стоит") || brief.Control != nil {
		t.Fatal(brief)
	}
	r.PriceEvidenceStatus = "comparable_quotes_available"
	r.ComparableSKUs = 2
	if got := directorReply(r); !strings.Contains(got.Decision, "2 из 3") || strings.Contains(got.Decision, "не стоит") {
		t.Fatal(got)
	}
}

func TestBriefDropsDuplicateAndStaleAlerts(t *testing.T) {
	mk := func(snapshot, product, currentFrom string) monitorObservation {
		start, _ := time.Parse(time.DateOnly, currentFrom)
		end := start.AddDate(0, 0, 27)
		d := evidence.WindowContrast{
			Baseline:    evidence.Period{From: start.AddDate(0, 0, -28).Format(time.DateOnly), To: start.AddDate(0, 0, -1).Format(time.DateOnly)},
			Current:     evidence.Period{From: currentFrom, To: end.Format(time.DateOnly)},
			WindowWeeks: 4, BaselineMean: 110, CurrentMean: 74, ControlBaselineMean: 100, ControlCurrentMean: 97.4,
			RelativeChangePct: (74.0/110 - 1) * 100,
		}
		raw, _ := json.Marshal(d)
		return monitorObservation{snapshot: snapshot, details: monitorDetails{Origin: "synthetic_fixture", Metric: "orders_index", Control: "astra_linux_special_edition"},
			Row: evidence.Row{Product: product, Segment: "self_service", Source: "sales", Metric: watchdogMetric, Status: "observed", Value: "true", Details: string(raw),
				Ref:    evidence.SourceRef{SourceID: "sales", Locator: "orders#" + snapshot},
				Period: evidence.Period{From: end.AddDate(0, 0, -6).Format(time.DateOnly), To: end.Format(time.DateOnly)}}}
	}
	current := mk("a", "kaspersky_endpoint_security", "2026-10-12")
	again := mk("b", "kaspersky_endpoint_security", "2026-10-12")
	old := mk("c", "kaspersky_small_office_security", "2023-01-30")
	result := salesBrief([]monitorObservation{old, again, current}, nil, BriefQuery{Summary: true})
	if len(result.Readings) != 1 || result.Readings[0].Product != "Kaspersky Endpoint Security" {
		t.Fatalf("%+v", result.Readings)
	}
}
