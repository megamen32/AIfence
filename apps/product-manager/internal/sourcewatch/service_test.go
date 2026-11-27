package sourcewatch

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"
)

const (
	metBinding    = "62057b9c-7d10-4439-9985-3888e7b259b3"
	marketBinding = "44e7584f-1caf-4f08-9bd9-b69006cdd0af"
	demandBinding = "00000000-0000-4000-8000-000000000002"
	trendsBinding = "00000000-0000-4000-8000-000000000003"
)

func storeBatch(change float64, status string) batch {
	return batch{SchemaVersion: "aifence.v1", Source: "metrika", Status: "ok", Observations: []observation{{
		ID: "store-change", ProductKey: "store", MetricKey: "visits.relative_change_pct", Status: status, Unit: "percent", Value: &change,
		Period: period{"2026-11-16", "2026-11-22"}, SourceRef: sourceRef{SourceID: "yandex-metrika:1337", Locator: "compare_periods/1337/visits"},
	}}}
}

func deltaEnvelope(comparable bool, pct float64) weekDelta {
	series := []weekDeltaSeries{{
		Seller: "allsoft", URL: "https://allsoft.example/kl4141", PriceBasis: "per_unit", Currency: "RUB",
		Baseline: &weekRead{CapturedAt: "2026-11-10T10:00:00Z", Value: 12490, Unit: "RUB", EvidenceID: "m-base", URL: "https://allsoft.example/kl4141"},
		Current:  &weekRead{CapturedAt: "2026-11-18T10:00:00Z", Value: 11490, Unit: "RUB", EvidenceID: "m-cur", URL: "https://allsoft.example/kl4141"},
		DeltaPct: &pct, Comparable: comparable, EvidenceIDs: []string{"m-base", "m-cur"},
	}}
	if !comparable {
		series[0].Reason = "quantity unknown"
		series[0].Current = nil
	}
	return weekDelta{SKU: "KL4141RCEFS", Status: "ok", ComparablePairs: 1, Series: series, MissingPeriods: []missingPeriod{}}
}

func TestCheckTriggersOnObservedStoreDropWithPriceDelta(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKU: "KL4141RCEFS", Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	var calls []string
	starts := 0
	read := func(_ context.Context, binding string, input, output any) error {
		calls = append(calls, binding)
		switch binding {
		case metBinding:
			*output.(*batch) = storeBatch(-21, "observed")
		case marketBinding:
			args, _ := json.Marshal(input)
			var q map[string]string
			_ = json.Unmarshal(args, &q)
			if q["product_key"] != w.SKU || q["baseline_from"] != "2026-11-09" || q["current_to"] != "2026-11-22" {
				t.Fatalf("price_week_delta scope %s", args)
			}
			*output.(*weekDelta) = deltaEnvelope(true, -8.0)
		default:
			t.Fatalf("unexpected binding %s", binding)
		}
		return nil
	}
	out, err := Check(t.Context(), w, true, read, func(_ context.Context, s Signal) (string, error) {
		starts++
		if s.ChangePct != -21 || len(s.PriceFamilies) != 1 || s.PriceFamilies[0].ChangePct == nil || *s.PriceFamilies[0].ChangePct != -8 {
			t.Fatalf("signal %+v", s)
		}
		if s.PriceFamilies[0].BaselineID != "m-base" || s.PriceFamilies[0].Locator == "" {
			t.Fatalf("price family lost locators: %+v", s.PriceFamilies[0])
		}
		return "run1", nil
	})
	if err != nil || out.Status != "submitted" || starts != 1 || len(calls) != 2 {
		t.Fatalf("%+v err=%v calls=%v starts=%d", out, err, calls, starts)
	}
}

func TestCheckRejectsIncomparableSeriesAndQuietStore(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change float64
		status string
		want   string
	}{
		{"quiet", -19, "observed", "quiet"},
		{"estimated_is_not_observed", -60, "estimated", "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKU: "KL4141RCEFS", Metric: "visits", Monday: "2026-11-23", DropPct: 20}
			read := func(_ context.Context, binding string, _ any, output any) error {
				if binding == metBinding {
					*output.(*batch) = storeBatch(tc.change, tc.status)
					return nil
				}
				*output.(*weekDelta) = deltaEnvelope(false, 0)
				return nil
			}
			out, err := Check(t.Context(), w, true, read, func(context.Context, Signal) (string, error) {
				t.Fatal("must not start")
				return "", nil
			})
			if err != nil || out.Status != tc.want {
				t.Fatalf("%+v err=%v", out, err)
			}
		})
	}
}

func TestCheckTrendsUsesSameWeekContract(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, TrendsBinding: trendsBinding, Phrase: "касперский", TrendsGeo: "RU", Metric: "visits", Monday: "2026-11-23", DropPct: 20, DemandDropPct: 20}
	read := func(_ context.Context, binding string, input, output any) error {
		if binding == metBinding {
			*output.(*batch) = storeBatch(3, "observed")
			return nil
		}
		args, _ := json.Marshal(input)
		var q map[string]string
		_ = json.Unmarshal(args, &q)
		if binding != trendsBinding || q["query"] != w.Phrase || q["geo"] != "RU" || q["monday"] != w.Monday {
			t.Fatalf("trends scope %s", args)
		}
		a, c := 80.0, 50.0
		*output.(*batch) = batch{SchemaVersion: "aifence.v1", Source: "research", Status: "ok", Observations: []observation{
			{ID: "t-base", ProductKey: w.Phrase, MetricKey: "search_interest", Status: "observed", Unit: "index", Value: &a, Period: period{"2026-11-09", "2026-11-15"}, SourceRef: sourceRef{SourceID: "google_trends", Locator: "trends/ru/1"}},
			{ID: "t-cur", ProductKey: w.Phrase, MetricKey: "search_interest", Status: "observed", Unit: "index", Value: &c, Period: period{"2026-11-16", "2026-11-22"}, SourceRef: sourceRef{SourceID: "google_trends", Locator: "trends/ru/2"}},
		}}
		return nil
	}
	out, err := Check(t.Context(), w, true, read, func(_ context.Context, s Signal) (string, error) {
		if s.Trigger != "trends" || s.Trends == nil || s.Trends.ChangePct == nil || math.Abs(*s.Trends.ChangePct+37.5) > 1e-9 {
			t.Fatalf("trends signal %+v", s)
		}
		if s.Trends.BaselineID != "t-base" || s.Trends.CurrentID != "t-cur" {
			t.Fatalf("trends ids %+v", s.Trends)
		}
		return "run", nil
	})
	if err != nil || out.Status != "submitted" {
		t.Fatalf("%+v err=%v", out, err)
	}
}

func TestCheckReportsUnsupportedHostWithoutFakeRead(t *testing.T) {
	w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKU: "KL4141RCEFS", Metric: "visits", Monday: "2026-11-23", DropPct: 20}
	out, err := Check(t.Context(), w, true, unsupportedRead, func(context.Context, Signal) (string, error) {
		t.Fatal("unsupported host must not launch a review")
		return "", nil
	})
	if err == nil || !strings.Contains(err.Error(), "unsupported") || out.Status != "" {
		t.Fatalf("expected explicit unsupported, got %+v err=%v", out, err)
	}
	if HostSupportsAppRead {
		t.Fatal("pinned SDK v0.11.0 has no JobContext.ReadAppTool")
	}
}

func TestUnsupportedIsADistinctTypedLimit(t *testing.T) {
	// The journal must tell a host limitation apart from an ordinary failure.
	if !errors.Is(unsupportedRead(context.Background(), metBinding, nil, nil), ErrUnsupported) {
		t.Fatal("unsupported read must wrap ErrUnsupported")
	}
	if errors.Is(errors.New("binding revoked"), ErrUnsupported) {
		t.Fatal("an unrelated read error must not be classified as unsupported")
	}
}

func TestValidateReviewRejectsUnknownEvidenceIDs(t *testing.T) {
	allowed := []string{"store-change", "m-base", "m-cur"}
	base := ReviewOutput{Hypothesis: "Проверить ставку", EvidenceIDs: []string{"store-change", "m-base"}, Missing: []string{"нет контроля"}, Test: ReviewTest{Metric: "visits", ThresholdPct: 10, Days: 14}}
	if _, err := ValidateReview(base, allowed); err != nil {
		t.Fatalf("valid review rejected: %v", err)
	}
	bad := base
	bad.EvidenceIDs = []string{"store-change", "invented-id"}
	if _, err := ValidateReview(bad, allowed); err == nil || !strings.Contains(err.Error(), "unknown evidence ID") {
		t.Fatalf("unknown ID accepted: %v", err)
	}
	for name, mutate := range map[string]func(*ReviewOutput){
		"empty_evidence":   func(r *ReviewOutput) { r.EvidenceIDs = nil },
		"empty_hypothesis": func(r *ReviewOutput) { r.Hypothesis = "  " },
		"zero_threshold":   func(r *ReviewOutput) { r.Test.ThresholdPct = 0 },
		"too_long_test":    func(r *ReviewOutput) { r.Test.Days = 91 },
	} {
		r := base
		mutate(&r)
		if _, err := ValidateReview(r, allowed); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

func TestReviewSignalIDsCoverEveryCarriedObservation(t *testing.T) {
	s := Signal{ObservationID: "store", Demand: &Demand{BaselineID: "w-base", CurrentID: "w-cur"}, Trends: &Trends{BaselineID: "t-base", CurrentID: "t-cur"}, PriceFamilies: []PriceFamily{{BaselineID: "m-base", CurrentID: "m-cur"}}}
	got := ReviewSignalIDs(s)
	want := []string{"m-base", "m-cur", "store", "t-base", "t-cur", "w-base", "w-cur"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ids %v want %v", got, want)
	}
}

func TestNextMondaySkipsMissedDeliveries(t *testing.T) {
	first, _ := time.Parse(time.DateOnly, "2026-11-16")
	for _, tc := range []struct{ now, want string }{
		{"2026-11-16T06:00:00Z", "2026-11-23"}, {"2026-12-07T05:59:59Z", "2026-12-07"}, {"2026-12-07T06:00:00Z", "2026-12-14"}, {"2026-12-06T23:59:59Z", "2026-12-07"}, {"2026-12-10T23:59:59Z", "2026-12-14"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if d := nextMonday(first, now).Format(time.DateOnly); d != tc.want {
			t.Errorf("%s: %s", tc.now, d)
		}
	}
}

func TestScopedIDsAreStable(t *testing.T) {
	in := JobInput{WatchID: "watch", Monday: "2026-11-23"}
	id := ID("alice", "delivery", in)
	if id != ID("alice", "delivery", in) || id == ID("bob", "delivery", in) || id == ID("alice", "signal", in) {
		t.Fatal("watch ID scope")
	}
	in.Monday = "2026-11-30"
	if id == ID("alice", "delivery", in) {
		t.Fatal("weeks must have different idempotency keys")
	}
}
