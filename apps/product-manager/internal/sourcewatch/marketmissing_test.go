package sourcewatch

import (
	"context"
	"testing"
)

// liveFamilyMissing follows family_price_week_delta as published for a real
// catalog with no stored history: every SKU lacks both weeks.
func liveFamilyMissing() familyDelta {
	return familyDelta{
		Discovery: "explicit_skus", SKUs: []string{"KL4141RCEFS", "KL4210RAKDS"},
		BaselineWeek: weekA, CurrentWeek: weekB,
		Coverage: familyCoverage{SelectedSKUs: 2, SKUsWithPairs: 0, ObservedSeries: 0, ComparablePairs: 0, MissingSeries: 0, MissingSKUWeeks: 4},
		Results: []weekDelta{
			{SKU: "KL4141RCEFS", BaselineWeek: weekA, CurrentWeek: weekB, Status: "unavailable", MissingPeriods: []missingPeriod{
				{SKU: "KL4141RCEFS", Period: "baseline", Reason: "no stored observation"},
				{SKU: "KL4141RCEFS", Period: "current", Reason: "no stored observation"},
			}},
			{SKU: "KL4210RAKDS", BaselineWeek: weekA, CurrentWeek: weekB, Status: "unavailable", MissingPeriods: []missingPeriod{
				{SKU: "KL4210RAKDS", Period: "baseline", Reason: "no stored observation"},
				{SKU: "KL4210RAKDS", Period: "current", Reason: "no stored observation"},
			}},
		},
		Status: "unavailable",
	}
}

func familyReadWith(storeChange float64, f familyDelta) Read {
	return func(_ context.Context, binding string, _, output any) error {
		switch binding {
		case metBinding:
			*output.(*batch) = storeBatch(storeChange, "observed")
		case marketBinding:
			*output.(*familyDelta) = f
		}
		return nil
	}
}

// A proven store signal survives absent price history, and the absence is
// reported as coverage instead of becoming a price cause.
func TestProvenSignalSurvivesMissingPriceWeeks(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKUs: []string{"KL4141RCEFS", "KL4210RAKDS"}, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	var got Signal
	out, err := Check(t.Context(), w, true, familyReadWith(-21, liveFamilyMissing()), func(_ context.Context, s Signal) (string, error) {
		got = s
		return "run", nil
	})
	if err != nil || out.Status != "submitted" {
		t.Fatalf("%+v err=%v", out, err)
	}
	if got.Trigger != "metrika" {
		t.Fatalf("missing prices must not create or rename a trigger: %q", got.Trigger)
	}
	if len(got.PriceFamilies) != 0 {
		t.Fatalf("absent history must not become a price change: %+v", got.PriceFamilies)
	}
	if got.PriceCoverage == nil || got.PriceCoverage.MissingSKUWeeks != 4 || got.PriceCoverage.ComparablePairs != 0 {
		t.Fatalf("missing SKU weeks must reach coverage: %+v", got.PriceCoverage)
	}
	if len(got.MissingPeriods) != 4 {
		t.Fatalf("missing periods must stay in the signal: %+v", got.MissingPeriods)
	}
}

// With no proven signal anywhere, absent price history is quiet, not an alert.
func TestMissingPriceWeeksAloneIsNotAnAlert(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKUs: []string{"KL4141RCEFS"}, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	out, err := Check(t.Context(), w, true, familyReadWith(-3, liveFamilyMissing()), func(context.Context, Signal) (string, error) {
		t.Fatal("absent prices alone must not launch a review")
		return "", nil
	})
	if err != nil || out.Status != "quiet" {
		t.Fatalf("%+v err=%v", out, err)
	}
}

// A malformed envelope is still incomplete; only a well-formed statement of
// absence is allowed through.
func TestMalformedFamilyEnvelopeStaysIncomplete(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKUs: []string{"KL4141RCEFS"}, Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	for name, bad := range map[string]familyDelta{
		"no_coverage":  {Status: "unavailable", BaselineWeek: weekA, CurrentWeek: weekB},
		"wrong_status": {Status: "garbage", BaselineWeek: weekA, CurrentWeek: weekB, Coverage: familyCoverage{SelectedSKUs: 1}},
		"wrong_weeks":  {Status: "unavailable", BaselineWeek: weekA, CurrentWeek: period{"2026-11-23", "2026-11-29"}, Coverage: familyCoverage{SelectedSKUs: 1}},
	} {
		t.Run(name, func(t *testing.T) {
			out, err := Check(t.Context(), w, true, familyReadWith(-30, bad), func(context.Context, Signal) (string, error) {
				t.Fatal("a malformed envelope must not launch a review")
				return "", nil
			})
			// A structural fault is an error; an unknown status is incomplete.
			// Neither may reach a review.
			if (name == "wrong_status" && (err != nil || out.Status != "incomplete")) || (name != "wrong_status" && (err == nil || out.Status != "")) {
				t.Fatalf("malformed envelope accepted: %+v err=%v", out, err)
			}
		})
	}
}
