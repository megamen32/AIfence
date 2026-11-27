package market

import (
	catalog "agent/collector"
	"testing"
	"time"
)

// weekDeltaFixture builds one stored observation per call.
func deltaOffer(seller string, at time.Time, minor int64, url string) catalog.Offer {
	one := int64(1)
	return catalog.Offer{Source: seller, SKU: "KL4141RCEFS", Name: "Kaspersky Small Office Security 5 seats 1 year", PriceMinor: minor,
		Currency: "RUB", PriceBasis: "per_unit", Quantity: &one, MaxQuantity: &one, URL: url, CapturedAt: at.UTC().Format(time.RFC3339)}
}

func TestWeekDeltaUsesLatestReadPerSeriesAndReportsMissingWeeks(t *testing.T) {
	// Two full weeks: 2026-11-09..15 and 2026-11-16..22.
	baselineStart := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	currentStart := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	a1 := deltaOffer("softline", baselineStart.Add(2*24*time.Hour), 673900, "https://store.softline.ru/kes/")
	a2 := deltaOffer("softline", baselineStart.Add(5*24*time.Hour), 670000, "https://store.softline.ru/kes/")
	b1 := deltaOffer("softline", currentStart.Add(26*time.Hour), 660000, "https://store.softline.ru/kes/")
	b1.Name = "Kaspersky Endpoint Security 5 seats 1 year"
	onlyBaseline := deltaOffer("softmap", baselineStart.Add(24*time.Hour), 673900, "https://softmap.ru/kes/")
	onlyCurrent := deltaOffer("softmagazin", currentStart.Add(70*time.Hour), 748800, "https://softmagazin.ru/kes/")
	rows := []catalog.Offer{a1, a2, b1, onlyBaseline, onlyCurrent}
	got := GroupWeekDelta(rows, "KL4141RCEFS", baselineStart, currentStart, time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC))
	if got.ComparablePairs != 1 || len(got.Series) != 3 || len(got.MissingPeriods) != 2 {
		t.Fatalf("unexpected delta summary: %+v", got)
	}
	for _, series := range got.Series {
		switch series.Seller {
		case "softline":
			if !series.Comparable || series.Delta == nil || *series.Delta != -100 || series.Baseline == nil || series.Baseline.Value != 6700 || series.Current == nil || series.Current.Value != 6600 {
				t.Fatalf("wrong softline series: %+v", series)
			}
			if *series.DeltaPct != -1.49 {
				t.Fatalf("wrong percent: %+v", series)
			}
		case "softmap":
			if series.Comparable || series.Current != nil || series.Reason != "no observation in the current week" {
				t.Fatalf("baseline-only must stay missing: %+v", series)
			}
		case "softmagazin":
			if series.Comparable || series.Baseline != nil || series.Reason != "no observation in the baseline week" {
				t.Fatalf("current-only must stay missing: %+v", series)
			}
		}
	}
	if got.Status != "partial" || got.Evidence.Status != "partial" || len(got.Evidence.Observations) != 1 || got.Evidence.Observations[0].Value != -100 {
		t.Fatalf("wrong evidence: %+v", got.Evidence)
	}
}

func TestWeekDeltaRejectsIncompleteOrOverlappingWeeks(t *testing.T) {
	s := New(memoryStore{})
	for _, in := range []WeekDeltaInput{
		{ProductKey: "KL4141RCEFS", BaselineFrom: "2026-11-10", BaselineTo: "2026-11-16", CurrentFrom: "2026-11-17", CurrentTo: "2026-11-23"},
		{ProductKey: "KL4141RCEFS", BaselineFrom: "2026-11-09", BaselineTo: "2026-11-15", CurrentFrom: "2026-11-12", CurrentTo: "2026-11-18"},
		{ProductKey: "KL4141RCEFS", BaselineFrom: "2026-11-09", BaselineTo: "2026-11-14", CurrentFrom: "2026-11-16", CurrentTo: "2026-11-22"},
		{ProductKey: "", BaselineFrom: "2026-11-09", BaselineTo: "2026-11-15", CurrentFrom: "2026-11-16", CurrentTo: "2026-11-22"},
	} {
		if _, err := s.WeekDelta(t.Context(), in); err == nil {
			t.Fatalf("accepted invalid input %+v", in)
		}
	}
}

func TestWeekDeltaDoesNotCountUnknownPriceBasis(t *testing.T) {
	baseline := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	current := baseline.AddDate(0, 0, 7)
	a := deltaOffer("softline", baseline, 10000, "https://store.softline.ru/kes/")
	b := a
	b.CapturedAt = current.Format(time.RFC3339)
	b.PriceMinor = 9000
	a.PriceBasis, b.PriceBasis = "displayed_offer_price", "displayed_offer_price"
	out := GroupWeekDelta([]catalog.Offer{a, b}, a.SKU, baseline, current, current.AddDate(0, 0, 7))
	if out.ComparablePairs != 0 || len(out.Evidence.Observations) != 0 || len(out.Series) != 1 || out.Series[0].Comparable {
		t.Fatalf("unknown price basis counted as a numeric change: %+v", out)
	}
}

func TestWeekDeltaUsesDisplayedRublesAtClosedWeekBoundaries(t *testing.T) {
	baseline := time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC)
	current := baseline.AddDate(0, 0, 7)
	lastBaseline := deltaOffer("softline", baseline.AddDate(0, 0, 6).Add(23*time.Hour+59*time.Minute), 10000, "https://store.softline.ru/kes/")
	lastCurrent := deltaOffer("softline", current.AddDate(0, 0, 6).Add(23*time.Hour+59*time.Minute), 11000, "https://store.softline.ru/kes/")
	firstAfterCurrent := deltaOffer("softline", current.AddDate(0, 0, 14), 12000, "https://store.softline.ru/kes/")

	out := GroupWeekDelta([]catalog.Offer{lastBaseline, lastCurrent, firstAfterCurrent}, lastBaseline.SKU, baseline, current, current.AddDate(0, 0, 14))
	if out.ComparablePairs != 1 || len(out.Evidence.Observations) != 1 {
		t.Fatalf("boundary observations were not paired exactly once: %+v", out)
	}
	series := out.Series[0]
	if series.Baseline == nil || series.Baseline.Value != 100 || series.Current == nil || series.Current.Value != 110 {
		t.Fatalf("minor units were not converted to displayed RUB: %+v", series)
	}
	if series.Delta == nil || *series.Delta != 10 || series.DeltaPct == nil || *series.DeltaPct != 10 {
		t.Fatalf("unexpected displayed-price delta: %+v", series)
	}
	if out.Evidence.Observations[0].TaxBasis != "unknown" {
		t.Fatalf("weekly delta must not invent a VAT adjustment: %+v", out.Evidence.Observations[0])
	}
}
