package main

import (
	"encoding/json"
	"strings"
	"testing"

	"agent/handlers"
	"agent/internal/evidence"
)

func TestStandaloneReviewAttachmentKeepsControlAndSource(t *testing.T) {
	r := handlers.ProductReading{Product: "Kaspersky Endpoint Security", Price: "Проверено два магазина.", Source: "synthetic/sales, row 8", Weekly: "Нужны количество заказов в штуках и вклад на заказ.",
		Comparison:   handlers.SalesComparison{Control: handlers.SeriesChange{Name: "Astra Linux Special Edition", Percent: 1.2}, Target: handlers.SeriesChange{Name: "Kaspersky Endpoint Security", Percent: -12.3}, Current: evidence.Period{From: "2025-11-03", To: "2025-11-30"}, Baseline: evidence.Period{From: "2025-10-06", To: "2025-11-02"}, WeeksPerWindow: 4},
		OfferExample: []handlers.BriefFact{{SKU: "KL4210RAKDE", Fact: "Allsoft, цена за лицензию", Locator: "https://allsoft.ru/"}}}
	data, _ := json.Marshal(handlers.SalesBrief{Readings: []handlers.ProductReading{r}})
	doc := reviewAttachment(string(data))
	for _, want := range []string{"Контрольный продукт Astra Linux Special Edition: 1.2%", "Kaspersky Endpoint Security: -12.3%", "2025-11-03", r.Source, r.Weekly, r.OfferExample[0].Locator} {
		if !strings.Contains(doc, want) {
			t.Errorf("missing %q from attachment", want)
		}
	}
	if got := reviewAttachment(`{"observations":[]}`); got != `{"observations":[]}` {
		t.Fatal(got)
	}
}
