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

func TestFamilyDeltaReportsBothMissingWeeksForUnobservedSKUs(t *testing.T) {
	baseline := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	current := baseline.AddDate(0, 0, 7)
	now := current.AddDate(0, 0, 14)
	skus := []string{"KL4141RCEFR", "OS2304ELB81BOX000WS01-SO36", "CFQ7TTC0LCHC-0002-P1YY", "RH00003", "KL4210RAKDE", "LBW-BC-12M-100-A3", "X2-CLD-NB-3NUNL-A"}
	out := FamilyDelta{SKUs: skus, Coverage: FamilyCoverage{SelectedSKUs: len(skus)}, Results: []WeekDelta{}, EvidenceIDs: []string{}, Warnings: []string{}}
	for _, sku := range skus {
		out.Results = append(out.Results, GroupWeekDelta(nil, sku, baseline, current, now))
	}
	out = aggregateFamilyDelta(out, now)
	if out.Status != "unavailable" || out.Coverage.SelectedSKUs != 7 || out.Coverage.MissingSKUWeeks != 14 || out.Coverage.MissingSeries != 0 || out.Coverage.ComparablePairs != 0 || len(out.EvidenceIDs) != 0 {
		t.Fatalf("missing weeks were counted as prices: %+v", out)
	}
	for _, result := range out.Results {
		if len(result.MissingPeriods) != 2 || result.MissingPeriods[0].SKU != result.SKU || result.MissingPeriods[1].SKU != result.SKU {
			t.Fatalf("missing SKU-week detail: %+v", result)
		}
	}
}
