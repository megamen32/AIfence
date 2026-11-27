package main

import (
	"encoding/json"
	"strings"
	"testing"

	"agent/handlers"
	"agent/internal/answer"
	"agent/internal/evidence"
)

func TestStandaloneReviewAttachmentKeepsControlAndSource(t *testing.T) {
	r := handlers.ProductReading{Product: "Kaspersky Endpoint Security", Price: "Проверено два магазина.", Source: "synthetic/sales, row 8", Weekly: "Нужны количество заказов в штуках и вклад на заказ.",
		PriceEvidenceStatus: "insufficient_comparable_quotes", CheckedSKUs: 4, ComparableSKUs: 0,
		Comparison:   handlers.SalesComparison{Control: handlers.SeriesChange{Name: "Astra Linux Special Edition", Percent: 1.2}, Target: handlers.SeriesChange{Name: "Kaspersky Endpoint Security", Percent: -12.3}, Current: evidence.Period{From: "2025-11-03", To: "2025-11-30"}, Baseline: evidence.Period{From: "2025-10-06", To: "2025-11-02"}, WeeksPerWindow: 4},
		OfferExample: []handlers.BriefFact{{SKU: "KL4210RAKDE", Fact: "Allsoft, цена за лицензию", Locator: "https://allsoft.ru/"}}}
	data, _ := json.Marshal(handlers.SalesBrief{Readings: []handlers.ProductReading{r}})
	doc := reviewAttachment(string(data))
	for _, want := range []string{"Astra Linux Special Edition 1.2%", "Kaspersky Endpoint Security -12.3%", "2025-11-03", r.Source, r.Weekly, "сопоставимая пара не подтверждена (0 из 4 артикулов)"} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q from attachment", want)
		}
	}
	if !strings.Contains(doc, r.OfferExample[0].Locator) || !strings.Contains(doc, r.Price) {
		t.Fatal("ordinary attachment must retain detailed evidence")
	}
	if got := reviewAttachment(`{"observations":[]}`); got != `{"observations":[]}` {
		t.Fatal(got)
	}
}

func TestDirectorReplyUsesDecisionThenPairedFacts(t *testing.T) {
	change := -12.3
	r := answer.Input{Decision: "Цена остаётся прежней.", Change: &change, Control: &answer.Control{Product: "Astra Linux", Change: 1.2}, Evidence: []answer.Evidence{{Source: "synthetic", Ref: "row 8", Fact: "Одинаковые окна."}}}
	data, _ := json.Marshal(handlers.SalesBrief{Replies: []answer.Input{r}})
	lines := strings.Split(strings.TrimSpace(reviewAttachment(string(data))), "\n")
	if len(lines) != 3 || lines[0] != r.Decision || !strings.Contains(lines[1], "Astra Linux 1,2%") || !strings.Contains(lines[2], "row 8") {
		t.Fatal(lines)
	}
}

func TestStandaloneReviewAttachmentPutsPairedSalesBeforePriceDetails(t *testing.T) {
	r := handlers.ProductReading{
		Product:             "Measured family",
		PriceEvidenceStatus: "insufficient_comparable_quotes",
		Price:               "No comparable offer group.",
		Source:              "sales, row 4",
		Weekly:              "Missing order count.",
		Comparison: handlers.SalesComparison{
			Control:        handlers.SeriesChange{Name: "Control family", Percent: -4.5},
			Target:         handlers.SeriesChange{Name: "Measured family", Percent: -31.2},
			Current:        evidence.Period{From: "2026-09-28", To: "2026-10-25"},
			Baseline:       evidence.Period{From: "2026-08-31", To: "2026-09-27"},
			WeeksPerWindow: 4,
		},
	}
	doc, _ := json.Marshal(handlers.SalesBrief{Readings: []handlers.ProductReading{r}})
	text := reviewAttachment(string(doc))
	paired := strings.Index(text, "Control family -4.5%")
	price := strings.Index(text, "Сопоставимость цен:")
	if paired < 0 || price < 0 || paired > price {
		t.Fatalf("paired sales must precede price details: %s", text)
	}
}
