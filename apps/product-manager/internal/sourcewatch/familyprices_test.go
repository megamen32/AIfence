package sourcewatch

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

var (
	weekA = period{"2026-11-09", "2026-11-15"}
	weekB = period{"2026-11-16", "2026-11-22"}
)

func familySeries(seller, url string, base, cur float64, comparable bool, ids ...string) weekDeltaSeries {
	pct := 0.0
	if base > 0 {
		pct = (cur - base) / base * 100
	}
	s := weekDeltaSeries{
		Seller: seller, URL: url, PriceBasis: "per_unit", Currency: "RUB", Comparable: comparable,
		Baseline: &weekRead{CapturedAt: "2026-11-10T10:00:00Z", Value: base, Unit: "RUB", EvidenceID: ids[0], URL: url},
		Current:  &weekRead{CapturedAt: "2026-11-18T10:00:00Z", Value: cur, Unit: "RUB", EvidenceID: ids[1], URL: url},
		DeltaPct: &pct, EvidenceIDs: ids,
	}
	if !comparable {
		s.Reason = "quantity or price basis is not verified for a numeric comparison"
		s.DeltaPct, s.Delta = nil, nil
	}
	return s
}

// This fixture follows the published family_price_week_delta shape: one
// WeekDelta per SKU, counts and evidence IDs only at the family level.
func familyEnvelope() familyDelta {
	return familyDelta{
		Discovery: "explicit_skus", SKUs: []string{"KL4141RCEFS", "KL4210RAKDS"}, BaselineWeek: weekA, CurrentWeek: weekB,
		Coverage: familyCoverage{SelectedSKUs: 2, SKUsWithPairs: 2, ObservedSeries: 4, ComparablePairs: 2, MissingSeries: 1},
		Results: []weekDelta{
			{SKU: "KL4141RCEFS", BaselineWeek: weekA, CurrentWeek: weekB, Status: "partial", ComparablePairs: 1,
				Series: []weekDeltaSeries{
					familySeries("softline", "https://softline.ru/a/", 12490, 11490, true, "a-base", "a-cur"),
					// Same SKU, another seller, unverified quantity: must not become a price change.
					familySeries("softmap", "https://softmap.ru/b/", 12000, 9000, false, "b-base", "b-cur"),
				},
				MissingPeriods: []missingPeriod{{SKU: "KL4141RCEFS", Seller: "softmagazin", Period: "current", Reason: "no stored observation for this offer in the current week"}}},
			{SKU: "KL4210RAKDS", BaselineWeek: weekA, CurrentWeek: weekB, Status: "ok", ComparablePairs: 1,
				Series: []weekDeltaSeries{familySeries("allsoft", "https://allsoft.ru/c/", 45000, 40000, true, "c-base", "c-cur")}},
		},
		EvidenceIDs: []string{"a-base", "a-cur", "c-base", "c-cur"}, Status: "partial",
	}
}

func familyRead(f familyDelta) Read {
	return func(_ context.Context, binding string, input, output any) error {
		switch binding {
		case metBinding:
			*output.(*batch) = storeBatch(-21, "observed")
		case marketBinding:
			raw, _ := json.Marshal(input)
			var q map[string]json.RawMessage
			_ = json.Unmarshal(raw, &q)
			if _, explicit := q["skus"]; !explicit {
				tFatal(output, "family call must pass the explicit SKU set")
			}
			if q["baseline_from"] == nil || q["current_to"] == nil {
				tFatal(output, "family call must pass both complete weeks")
			}
			*output.(*familyDelta) = f
		}
		return nil
	}
}

func tFatal(v any, msg string) {
	if t, ok := v.(*testing.T); ok {
		t.Fatal(msg)
	}
}

func TestFamilyPriceDeltaKeepsPerSKUSeriesAndCoverage(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKUs: []string{"KL4141RCEFS", "KL4210RAKDS"}, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	env := familyEnvelope()
	var got Signal
	out, err := Check(t.Context(), w, true, familyRead(env), func(_ context.Context, s Signal) (string, error) {
		got = s
		return "run", nil
	})
	if err != nil || out.Status != "submitted" {
		t.Fatalf("%+v err=%v", out, err)
	}
	if len(got.PriceFamilies) != 2 {
		t.Fatalf("want the two comparable series only, got %+v", got.PriceFamilies)
	}
	first := got.PriceFamilies[0]
	if first.SKU != "KL4141RCEFS" || first.Seller != "softline" || first.BaselineID != "a-base" || first.CurrentID != "a-cur" {
		t.Fatalf("series identity lost: %+v", first)
	}
	if first.BaselineWeek != weekA || first.CurrentWeek != weekB {
		t.Fatalf("weeks lost: %+v", first)
	}
	second := got.PriceFamilies[1]
	// Roubles of different licences are never averaged: each keeps its own.
	if second.SKU != "KL4210RAKDS" || second.BaselineRUB != 45000 || second.CurrentRUB != 40000 {
		t.Fatalf("per-SKU roubles not preserved: %+v", second)
	}
	for _, f := range got.PriceFamilies {
		if f.Seller == "softmap" {
			t.Fatal("an incomparable series became a price change")
		}
	}
	if got.MarketStatus != "partial" || got.PriceCoverage == nil || got.PriceCoverage.ComparablePairs != 2 || got.PriceCoverage.MissingSeries != 1 {
		t.Fatalf("coverage lost: %+v", got.PriceCoverage)
	}
	if len(got.MissingPeriods) != 1 || got.MissingPeriods[0].SKU != "KL4141RCEFS" {
		t.Fatalf("missing period lost: %+v", got.MissingPeriods)
	}
}

func TestReviewAcceptsOnlyIDsTheFamilySignalCarries(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKUs: []string{"KL4141RCEFS", "KL4210RAKDS"}, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	var signal Signal
	_, err := Check(t.Context(), w, true, familyRead(familyEnvelope()), func(_ context.Context, s Signal) (string, error) {
		signal = s
		return "run", nil
	})
	if err != nil {
		t.Fatal(err)
	}
	allowed := ReviewSignalIDs(signal)
	want := []string{"a-base", "a-cur", "c-base", "c-cur", "store-change"}
	if !reflect.DeepEqual(allowed, want) {
		t.Fatalf("allowed IDs %v want %v", allowed, want)
	}
	// b-base/b-cur belong to an incomparable series and must stay uncitable.
	for _, id := range []string{"b-base", "b-cur"} {
		if slices.Contains(allowed, id) {
			t.Fatalf("incomparable series ID %q became citable", id)
		}
	}
	review := ReviewOutput{Hypothesis: "Проверить смену цены у двух артикулов", EvidenceIDs: []string{"a-base", "a-cur", "c-cur"}, Missing: []string{"наблюдение ожидается softmagazin"}, Test: ReviewTest{Metric: "displayed_price_week_delta", ThresholdPct: 5, Days: 14}}
	if _, err = ValidateReview(review, allowed); err != nil {
		t.Fatalf("valid family review rejected: %v", err)
	}
	review.EvidenceIDs = []string{"a-base", "b-cur"}
	if _, err = ValidateReview(review, allowed); err == nil {
		t.Fatal("review cited an ID of an incomparable series")
	}
}

func TestFamilySelectionValidation(t *testing.T) {
	base := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	for name, mutate := range map[string]func(*Window){
		"both_family_and_skus":   func(w *Window) { w.Family, w.SKUs = "Kaspersky", []string{"A"} },
		"family_with_exact_sku":  func(w *Window) { w.SKUs, w.SKU = []string{"A"}, "B" },
		"family_without_binding": func(w *Window) { w.SKUs, w.MarketBinding = []string{"A"}, "" },
		"duplicate_skus":         func(w *Window) { w.SKUs = []string{"A", "A"} },
		"too_many_skus":          func(w *Window) { w.SKUs = make([]string, 21) },
		"short_family_phrase":    func(w *Window) { w.Family = "ab" },
	} {
		w := base
		mutate(&w)
		if _, err := w.Validate(); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
	w := base
	w.SKUs = []string{"KL4141RCEFS", "KL4210RAKDS"}
	if _, err := w.Validate(); err != nil {
		t.Fatalf("explicit SKU set rejected: %v", err)
	}
}

// The family contract must report the exact requested weeks.
func TestFamilyRejectsDifferentWeeks(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKUs: []string{"KL4141RCEFS"}, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	env := familyEnvelope()
	env.CurrentWeek = period{"2026-11-23", "2026-11-29"}
	out, err := Check(t.Context(), w, true, familyRead(env), func(context.Context, Signal) (string, error) {
		t.Fatal("a mismatched week must not launch a review")
		return "", nil
	})
	if err == nil || out.Status != "" {
		t.Fatalf("mismatched weeks accepted: %+v err=%v", out, err)
	}
}
