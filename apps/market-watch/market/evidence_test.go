package market

import (
	catalog "agent/collector"
	"testing"
	"time"
)

func TestEvidence(t *testing.T) {
	at := time.Date(2026, 11, 26, 0, 0, 0, 0, time.UTC)
	var runs []catalog.Run
	for _, s := range []string{"softline", "softmap", "softmagazin"} {
		runs = append(runs, catalog.Run{Source: s, Status: "ok", Offers: []catalog.Offer{{Source: s, SKU: "KL4141RCEFS", Name: "Test", PriceMinor: 673920, Currency: "RUB", URL: "https://example.test/", CapturedAt: at.Format(time.RFC3339)}}})
	}
	ev := FromSnapshot(Snapshot{CapturedAt: at.Format(time.RFC3339), Runs: runs}, "KL4141RCEFS", 10, at)
	if ev.Status != "ok" || len(ev.Observations) != 3 || ev.Observations[0].Value != 6739.2 {
		t.Fatal(ev)
	}
	if FromSnapshot(Snapshot{Runs: runs}, "unknown", 10, at).Status != "unavailable" {
		t.Fatal("missing converted to price")
	}
}
func TestCompareQuantity(t *testing.T) {
	one, five := int64(1), int64(5)
	a := catalog.Offer{Source: "s", SKU: "sku", PriceMinor: 10000, Currency: "RUB", PriceBasis: "total", Quantity: &one, CapturedAt: "2026-11-25T00:00:00Z"}
	b := a
	b.PriceMinor = 20000
	b.Quantity = &five
	b.CapturedAt = "2026-11-26T00:00:00Z"
	snap := func(o catalog.Offer) Snapshot {
		return Snapshot{Runs: []catalog.Run{{Source: o.Source, Status: "ok", Offers: []catalog.Offer{o}}}}
	}
	ev := Compare([]Snapshot{snap(a), snap(b)}, ChangesInput{}, time.Now())
	if len(ev.Observations) != 0 {
		t.Fatal("compared unlike quantities")
	}
	b.Quantity = &one
	ev = Compare([]Snapshot{snap(a), snap(b)}, ChangesInput{}, time.Now())
	if len(ev.Observations) != 1 || ev.Observations[0].Value != 100 {
		t.Fatal(ev)
	}
}
