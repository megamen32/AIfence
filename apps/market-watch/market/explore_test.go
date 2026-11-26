package market

import (
	catalog "agent/collector"
	"testing"
)

func TestQuantityUsesDocumentedRangesAndExactTotals(t *testing.T) {
	one, five, ten, hundred := int64(1), int64(5), int64(10), int64(100)
	unit := catalog.Offer{Source: "softline", SKU: "TERM", Name: "New License 5 seats 1 year", Currency: "RUB", PriceMinor: 12345, PriceBasis: "per_unit", Quantity: &one, MaxQuantity: &ten}
	total := unit
	total.Source = "softmap"
	total.Quantity = &five
	total.MaxQuantity = nil
	total.PriceBasis = "displayed_total"
	total.PriceMinor = 60000
	next := unit
	next.Quantity = &ten
	next.MaxQuantity = &hundred
	next.PriceMinor = 10000
	unknown := total
	unknown.Source = "softmagazin"
	unknown.Quantity = nil
	unknown.PriceBasis = "displayed_offer_price"
	s := browseService(t, unit, total, next, unknown)
	result, err := s.QuoteSKU(t.Context(), "TERM", 5)
	if err != nil || len(result.Groups) != 1 || !result.Groups[0].Comparable || len(result.Groups[0].Rows) != 2 || result.Groups[0].Rows[0].Total != 600 || result.Groups[0].Rows[1].Total != 617.25 {
		t.Fatalf("wrong basket %+v %v", result, err)
	}
	result, err = s.QuoteSKU(t.Context(), "TERM", 11)
	if err != nil || len(result.Groups) != 1 || result.Groups[0].Comparable || len(result.Groups[0].Rows) != 1 || result.Groups[0].Rows[0].Total != 1100 {
		t.Fatalf("wrong tier %+v %v", result, err)
	}
	if result, err = s.QuoteSKU(t.Context(), "TERM", 101); err != nil || len(result.Groups) != 0 {
		t.Fatal(result, err)
	}
	if _, err = s.QuoteSKU(t.Context(), "TERM", 0); err == nil {
		t.Fatal("accepted quantity zero")
	}
}

func TestMatrixPreservesUnknownAndLimitsLeaderClaim(t *testing.T) {
	one, maxQty := int64(1), int64(9)
	a := catalog.Offer{Source: "softline", SKU: "X", Name: "New license 5 seats 1 year", Currency: "RUB", PriceMinor: 673920, PriceBasis: "per_unit", Quantity: &one, MaxQuantity: &maxQty, URL: "https://store.softline.ru/x", CapturedAt: "2026-11-26T05:00:00Z"}
	b := a
	b.Source = "softmap"
	b.PriceMinor = 673900
	b.PriceBasis = "displayed_total"
	b.MaxQuantity = nil
	b.URL = "https://softmap.ru/x"
	c := a
	c.Source = "softmagazin"
	c.PriceBasis = "displayed_offer_price"
	c.Quantity = nil
	c.MaxQuantity = nil
	c.PriceMinor = 748800
	oldA, oldB := a, b
	oldA.CapturedAt = "2026-11-25T05:00:00Z"
	oldB.CapturedAt = oldA.CapturedAt
	oldA.PriceMinor = 673800
	makeRows := func() [][]string {
		aa, bb := obs(oldA), obs(oldB)
		return matrixLines("sku:X", []matrixOffer{{obs(a), &aa}, {obs(b), &bb}, {obs(c), nil}}, "http://example.localhost")
	}
	rows := makeRows()
	if len(rows) != 2 || len(rows[0]) != len(matrixHeader) || rows[0][9] != "0.20" || rows[0][13] != "true" || rows[1][8] != "7488.00" || rows[1][9] != "" || rows[1][16] != "false" {
		t.Fatalf("incorrect matrix %+v", rows)
	}
	oldB.CapturedAt = "2026-11-21T05:00:00Z"
	if rows = makeRows(); rows[0][13] != "" {
		t.Fatal("asynchronous samples established a previous leader", rows)
	}
	oldB.Name = "Renewal 1 seat 3 years"
	if rows = makeRows(); rows[0][11] != "" || rows[0][13] != "" {
		t.Fatal("compared a different license", rows)
	}
}
