package market

import (
	"testing"
	"time"

	catalog "agent/collector"
)

func TestFamilyDeltaCountsOnlyMatchedSeriesWithinSKU(t *testing.T) {
	baseline := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	current := baseline.AddDate(0, 0, 7)
	now := current.AddDate(0, 0, 14)
	a := deltaOffer("softline", baseline.AddDate(0, 0, 2), 10000, "https://store.softline.ru/a/")
	b := a
	b.CapturedAt = current.AddDate(0, 0, 2).Format(time.RFC3339)
	b.PriceMinor = 9000
	one := GroupWeekDelta([]catalog.Offer{a, b}, a.SKU, baseline, current, now)
	c := deltaOffer("softmap", current.AddDate(0, 0, 1), 45000, "https://softmap.ru/b/")
	c.SKU = "KL4210RAKDS"
	two := GroupWeekDelta([]catalog.Offer{c}, c.SKU, baseline, current, now)
	out := aggregateFamilyDelta(FamilyDelta{
		SKUs: []string{a.SKU, c.SKU}, Results: []WeekDelta{one, two},
		Coverage: FamilyCoverage{SelectedSKUs: 2}, EvidenceIDs: []string{}, Warnings: []string{},
	}, now)
	if out.Status != "partial" || out.Coverage.SelectedSKUs != 2 || out.Coverage.SKUsWithPairs != 1 ||
		out.Coverage.ObservedSeries != 2 || out.Coverage.ComparablePairs != 1 || out.Coverage.MissingSeries != 1 ||
		len(out.EvidenceIDs) != 1 || len(out.Evidence.Observations) != 1 || out.Evidence.Observations[0].ProductKey != a.SKU {
		t.Fatalf("wrong family coverage: %+v", out)
	}
	if out.Evidence.Observations[0].Value != -10 {
		t.Fatal("unlike SKU price entered the delta", out.Evidence)
	}
}
