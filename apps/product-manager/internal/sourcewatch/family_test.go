package sourcewatch

import (
	"context"
	"testing"
)

func familyRow(id, segment, from, to string, value float64, locator string) observation {
	return observation{
		ID: id, Kind: "metric", ProductKey: "kes", MetricKey: "visits", Status: "observed", Unit: "count", Value: &value,
		Period: period{from, to}, Segment: segment,
		SourceRef: sourceRef{SourceID: "blind-metrika", Locator: locator, Revision: "synthetic-v1"},
		Details:   map[string]any{"synthetic": true},
	}
}

var (
	familyBefore  = period{"2026-03-30", "2026-04-05"}
	familyCurrent = period{"2026-04-06", "2026-04-12"}
)

// These rows are all incomparable. None may become a signal, and none may
// panic: a zero baseline has no relative percentage, two segments are not one
// series, and a computed change without a locator cannot be cited.
func TestFamilyRejectsIncomparablePairs(t *testing.T) {
	for name, rows := range map[string][]observation{
		"zero_baseline":                         {familyRow("a", "mobile", familyBefore.From, familyBefore.To, 0, "week=12"), familyRow("c", "mobile", familyCurrent.From, familyCurrent.To, 182, "week=13")},
		"mixed_segments_without_window_segment": {familyRow("a", "desktop", familyBefore.From, familyBefore.To, 228, "week=12"), familyRow("c", "mobile", familyCurrent.From, familyCurrent.To, 182, "week=13")},
		"empty_locator":                         {familyRow("a", "mobile", familyBefore.From, familyBefore.To, 228, ""), familyRow("c", "mobile", familyCurrent.From, familyCurrent.To, 182, "week=13")},
	} {
		t.Run(name, func(t *testing.T) {
			b := batch{SchemaVersion: "aifence.v1", Source: "metrika", Status: "ok", Observations: rows}
			if got := familyChange(b, "kes", "", "visits", familyBefore, familyCurrent); got != nil {
				t.Fatalf("incomparable pair produced a change: %+v", got)
			}
		})
	}
}

func TestProductDeltaRequiresALocator(t *testing.T) {
	pct := -25.0
	b := batch{SchemaVersion: "aifence.v1", Source: "metrika", Status: "ok", Observations: []observation{{
		ID: "delta", ProductKey: "kes", MetricKey: "visits.relative_change_pct", Status: "observed", Unit: "percent", Value: &pct,
		Period: familyCurrent, Segment: "mobile", SourceRef: sourceRef{SourceID: "blind-metrika", Revision: "synthetic-v1"},
	}}}
	if productDelta(b, "kes", "mobile", "visits", familyCurrent) != nil {
		t.Fatal("a computed change without a locator was accepted")
	}
	b.Observations[0].SourceRef.Locator = "compare_periods/kes/mobile/2026-04-13"
	if productDelta(b, "kes", "mobile", "visits", familyCurrent) == nil {
		t.Fatal("a computed change with a locator was rejected")
	}
}

// A zero baseline must end as incomplete, not as a nil dereference.
func TestCheckZeroBaselineIsIncompleteWithoutPanic(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKU: "KL4141RCEFS", ProductKey: "kes", Segment: "mobile", Metric: "visits", Monday: "2026-04-13", DropPct: 20}
	read := func(_ context.Context, binding string, _ any, output any) error {
		switch binding {
		case metBinding:
			*output.(*batch) = batch{SchemaVersion: "aifence.v1", Source: "metrika", Status: "ok", Observations: []observation{
				familyRow("a", "mobile", familyBefore.From, familyBefore.To, 0, "week=12"),
				familyRow("c", "mobile", familyCurrent.From, familyCurrent.To, 182, "week=13"),
			}}
		case marketBinding:
			market := deltaEnvelope(true, 0)
			market.BaselineWeek, market.CurrentWeek = familyBefore, familyCurrent
			*output.(*weekDelta) = market
		}
		return nil
	}
	out, err := Check(t.Context(), w, true, read, func(context.Context, Signal) (string, error) {
		t.Fatal("an undefined relative change must not launch a review")
		return "", nil
	})
	if err != nil || out.Status != "incomplete" {
		t.Fatalf("%+v err=%v", out, err)
	}
}
