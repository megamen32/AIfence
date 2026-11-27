package sourcewatch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func allSourcesWindow() Window {
	return Window{
		MetrikaBinding: metBinding, MarketBinding: marketBinding,
		WordstatBinding: demandBinding, TrendsBinding: trendsBinding,
		SKU: "KL4141RCEFS", Phrase: "касперский", TrendsGeo: "RU",
		Metric: "visits", Monday: "2026-11-23", DropPct: 20, DemandDropPct: 20,
	}
}

func resilienceBatch(source string, baseline, current float64) batch {
	metric, unit, sourceID, prefix := "search_queries", "count", "yandex-wordstat:search-api", "w"
	if source == "research" {
		metric, unit, sourceID, prefix = "search_interest", "index", "google_trends", "t"
	}
	return batch{SchemaVersion: "aifence.v1", Source: source, Status: "ok", Observations: []observation{
		{ID: prefix + "-base", ProductKey: "касперский", MetricKey: metric, Status: "observed", Unit: unit, Value: &baseline, Period: period{"2026-11-09", "2026-11-15"}, SourceRef: sourceRef{SourceID: sourceID}},
		{ID: prefix + "-cur", ProductKey: "касперский", MetricKey: metric, Status: "observed", Unit: unit, Value: &current, Period: period{"2026-11-16", "2026-11-22"}, SourceRef: sourceRef{SourceID: sourceID}},
	}}
}

func TestCheckRejectsWrongMarketScope(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*weekDelta)
	}{
		{"sku", func(d *weekDelta) { d.SKU = "KL4861RAEFS" }},
		{"baseline_from", func(d *weekDelta) { d.BaselineWeek.From = "2026-11-02" }},
		{"baseline_to", func(d *weekDelta) { d.BaselineWeek.To = "2026-11-08" }},
		{"current_from", func(d *weekDelta) { d.CurrentWeek.From = "2026-11-09" }},
		{"current_to", func(d *weekDelta) { d.CurrentWeek.To = "2026-11-15" }},
		{"missing_weeks", func(d *weekDelta) { d.BaselineWeek, d.CurrentWeek = period{}, period{} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{MetrikaBinding: metBinding, MarketBinding: marketBinding, SKU: "KL4141RCEFS", Metric: "visits", Monday: "2026-11-23", DropPct: 20}
			read := func(_ context.Context, binding string, _ any, out any) error {
				if binding == metBinding {
					*out.(*batch) = storeBatch(-25, "observed")
					return nil
				}
				d := deltaEnvelope(true, -8)
				tc.mutate(&d)
				*out.(*weekDelta) = d
				return nil
			}
			out, err := Check(t.Context(), w, true, read, func(context.Context, Signal) (string, error) {
				t.Fatal("evidence for a different product or week must not launch a review")
				return "", nil
			})
			if err == nil || !strings.Contains(err.Error(), "SKU or comparison window") || out.Status != "" {
				t.Fatalf("mismatched market scope accepted: %+v, err=%v", out, err)
			}
		})
	}
}

func TestCheckPinsThresholdsAndEffectiveTrendsGeo(t *testing.T) {
	for _, tc := range []struct{ name, geo, want string }{
		{"configured", "KZ", "KZ"},
		{"default", "", "RU"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{MetrikaBinding: metBinding, TrendsBinding: trendsBinding, Phrase: "касперский", TrendsGeo: tc.geo, Metric: "visits", Monday: "2026-11-23", DropPct: 35, DemandDropPct: 28}
			read := func(_ context.Context, binding string, input, output any) error {
				if binding == metBinding {
					*output.(*batch) = storeBatch(-36, "observed")
					return nil
				}
				args := input.(map[string]string)
				if args["geo"] != tc.want {
					t.Fatalf("read geo = %q, want %q", args["geo"], tc.want)
				}
				*output.(*batch) = resilienceBatch("research", 80, 79)
				return nil
			}
			out, err := Check(t.Context(), w, true, read, func(_ context.Context, s Signal) (string, error) {
				if s.Trigger != "metrika" || s.Trends == nil || s.Trends.Geo != tc.want {
					t.Fatalf("pinned signal lost source scope: %+v", s)
				}
				if s.StoreDropThresholdPct != 35 || s.DemandDropThresholdPct != 28 {
					t.Fatalf("pinned thresholds do not match the watch: %+v", s)
				}
				raw, err := MarshalReview(ReviewInput{Signal: s, EvidenceIDs: ReviewSignalIDs(s)})
				if err != nil {
					t.Fatal(err)
				}
				var persisted struct {
					Signal map[string]json.RawMessage
				}
				if err := json.Unmarshal(raw, &persisted); err != nil {
					t.Fatal(err)
				}
				for field, want := range map[string]float64{"store_drop_threshold_pct": 35, "demand_drop_threshold_pct": 28} {
					var got float64
					if err := json.Unmarshal(persisted.Signal[field], &got); err != nil || got != want {
						t.Fatalf("review input %s = %s, err=%v", field, persisted.Signal[field], err)
					}
				}
				return "run", nil
			})
			if err != nil || out.Status != "submitted" {
				t.Fatalf("out=%+v, err=%v", out, err)
			}
		})
	}
}

func TestCheckPreservesStoreAlertWhenOptionalReadFails(t *testing.T) {
	for _, source := range []struct{ name, binding, warning string }{
		{"market", marketBinding, "market_watch:"},
		{"wordstat", demandBinding, "wordstat:"},
		{"trends", trendsBinding, "google_trends:"},
	} {
		for _, change := range []struct {
			name   string
			value  float64
			status string
		}{
			{"store_alert", -25, "submitted"},
			{"quiet_store", -1, "incomplete"},
		} {
			t.Run(source.name+"/"+change.name, func(t *testing.T) {
				w := allSourcesWindow()
				var calls []string
				read := func(_ context.Context, binding string, _ any, output any) error {
					calls = append(calls, binding)
					switch binding {
					case metBinding:
						*output.(*batch) = storeBatch(change.value, "observed")
					case marketBinding:
						*output.(*weekDelta) = deltaEnvelope(true, -8)
					case demandBinding:
						*output.(*batch) = resilienceBatch("wordstat", 100, 99)
					case trendsBinding:
						*output.(*batch) = resilienceBatch("research", 80, 79)
					default:
						t.Fatalf("unexpected binding %s", binding)
					}
					// Simulate a provider which filled output before returning an error.
					// These rows must never become review evidence.
					if binding == source.binding {
						return errors.New("private provider detail must not reach the signal")
					}
					return nil
				}
				starts := 0
				var launched Signal
				out, err := Check(t.Context(), w, true, read, func(_ context.Context, s Signal) (string, error) {
					starts++
					launched = s
					return "run", nil
				})
				if err != nil || out.Status != change.status || len(out.Warnings) != 1 || !strings.HasPrefix(out.Warnings[0], source.warning) {
					t.Fatalf("out=%+v, err=%v", out, err)
				}
				if strings.Contains(strings.Join(out.Warnings, " "), "private provider") {
					t.Fatal("raw provider details escaped into public warnings")
				}
				if !reflect.DeepEqual(calls, []string{metBinding, marketBinding, demandBinding, trendsBinding}) {
					t.Fatalf("optional failure prevented other source reads: %v", calls)
				}
				if change.status == "incomplete" {
					if starts != 0 {
						t.Fatal("incomplete sources without an observed trigger started a review")
					}
					return
				}
				if starts != 1 || launched.Trigger != "metrika" || launched.ObservationID != "store-change" || !reflect.DeepEqual(launched.SourceWarnings, out.Warnings) {
					t.Fatalf("store alert or warning was lost: starts=%d signal=%+v", starts, launched)
				}
				switch source.binding {
				case marketBinding:
					if launched.MarketStatus != "unavailable" || len(launched.PriceFamilies) != 0 || len(launched.MissingPeriods) != 0 || launched.Demand == nil || launched.Trends == nil {
						t.Fatalf("partial failed market read retained as evidence: %+v", launched)
					}
				case demandBinding:
					if launched.WordstatStatus != "unavailable" || launched.Demand != nil || len(launched.PriceFamilies) != 1 || launched.Trends == nil {
						t.Fatalf("partial failed Wordstat read retained as evidence: %+v", launched)
					}
				case trendsBinding:
					if launched.TrendsStatus != "unavailable" || launched.Trends != nil || len(launched.PriceFamilies) != 1 || launched.Demand == nil {
						t.Fatalf("partial failed Trends read retained as evidence: %+v", launched)
					}
				}
			})
		}
	}
}

func TestCheckPropagatesOptionalReadCancellation(t *testing.T) {
	for _, binding := range []string{marketBinding, demandBinding, trendsBinding} {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"canceled", context.Canceled},
			{"deadline", context.DeadlineExceeded},
		} {
			t.Run(binding+"/"+tc.name, func(t *testing.T) {
				read := func(_ context.Context, got string, _ any, output any) error {
					if got == binding {
						return fmt.Errorf("source interrupted: %w", tc.err)
					}
					switch got {
					case metBinding:
						*output.(*batch) = storeBatch(-25, "observed")
					case marketBinding:
						*output.(*weekDelta) = deltaEnvelope(true, -8)
					case demandBinding:
						*output.(*batch) = resilienceBatch("wordstat", 100, 99)
					case trendsBinding:
						*output.(*batch) = resilienceBatch("research", 80, 79)
					default:
						t.Fatalf("unexpected binding %s", got)
					}
					return nil
				}
				out, err := Check(t.Context(), allSourcesWindow(), true, read, func(context.Context, Signal) (string, error) {
					t.Fatal("an interrupted check must not launch a review")
					return "", nil
				})
				if !errors.Is(err, tc.err) || out.Status != "" {
					t.Fatalf("interruption became a partial success: out=%+v, err=%v", out, err)
				}
			})
		}
	}
}

func TestCheckPropagatesCanceledContextWithOrdinaryProviderError(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	read := func(_ context.Context, binding string, _ any, output any) error {
		if binding == metBinding {
			*output.(*batch) = storeBatch(-25, "observed")
			return nil
		}
		cancel()
		return errors.New("provider disconnected")
	}
	out, err := Check(ctx, allSourcesWindow(), true, read, func(context.Context, Signal) (string, error) {
		t.Fatal("a canceled check must not launch a review")
		return "", nil
	})
	if !errors.Is(err, context.Canceled) || out.Status != "" {
		t.Fatalf("canceled context swallowed: out=%+v, err=%v", out, err)
	}
}
