package sourcewatch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestWatchReadsBothBindingsAndStartsOnlyOnObservedDrop(t *testing.T) {
	const met = "62057b9c-7d10-4439-9985-3888e7b259b3"
	const market = "44e7584f-1caf-4f08-9bd9-b69006cdd0af"
	for _, tc := range []struct {
		name, metric, marketStatus, want string
		change                           float64
		denied, route                    bool
		starts                           int
	}{
		{"decline", "observed", "ok", "submitted", -21, false, true, 1},
		{"threshold_equal", "observed", "ok", "submitted", -20, false, true, 1},
		{"quiet", "observed", "ok", "quiet", -19, false, true, 0},
		{"missing", "estimated", "ok", "incomplete", -60, false, true, 0},
		{"denied", "observed", "ok", "", -80, true, true, 0},
		{"model_off", "observed", "ok", "model_route_unapproved", -90, false, false, 0},
		{"market_missing", "observed", "unavailable", "incomplete", -90, false, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := Window{MetrikaBinding: met, MarketBinding: market, SKU: "fixture-sku", Metric: "visits", DropPct: 20, Monday: "2026-11-23"}
			var calls []string
			starts := 0
			read := func(ctx context.Context, b string, input, output any) error {
				args, _ := json.Marshal(input)
				var query map[string]any
				_ = json.Unmarshal(args, &query)
				calls = append(calls, b)
				out := output.(*batch)
				if b == met {
					if query["product_key"] != "store" || query["baseline_from"] != "2026-11-09" || query["current_to"] != "2026-11-22" {
						t.Fatalf("wrong scope %s", args)
					}
					*out = batch{SchemaVersion: "aifence.v1", Source: "metrika", Status: "ok", Observations: []observation{{ID: "delta", ProductKey: "store", MetricKey: "visits.relative_change_pct", Unit: "percent", Status: tc.metric, Value: &tc.change, Period: period{"2026-11-16", "2026-11-22"}, SourceRef: sourceRef{"yandex-metrika:10"}}}}
				} else if b == market {
					if tc.denied {
						return errors.New("grant revoked")
					}
					if query["product_key"] != spec.SKU || query["exact_sku"] != true || query["limit"] != float64(5) {
						t.Fatalf("market scope %s", args)
					}
					*out = batch{SchemaVersion: "aifence.v1", Source: "market", Status: tc.marketStatus}
				} else {
					t.Fatal(b)
				}
				return nil
			}
			launch := func(ctx context.Context, signal Signal) (string, error) {
				starts++
				if signal.ChangePct != tc.change || signal.Metric != "visits" || signal.ObservationID != "delta" {
					t.Fatalf("%+v", signal)
				}
				return "run1", nil
			}
			got, err := Check(t.Context(), spec, tc.route, read, launch)
			if (err != nil) != tc.denied || got.Status != tc.want || len(calls) != 2 || starts != tc.starts {
				t.Fatalf("%+v err=%v calls=%v starts=%d", got, err, calls, starts)
			}
		})
	}
}

func TestWordstatPairsExactWeeksAndCanTriggerAlone(t *testing.T) {
	const met = "62057b9c-7d10-4439-9985-3888e7b259b3"
	const demand = "00000000-0000-4000-8000-000000000002"
	for _, tc := range []struct {
		name    string
		a, b    float64
		invalid string
		want    string
	}{
		{"demand_drop", 100, 80, "", "submitted"}, {"quiet", 100, 81, "", "quiet"}, {"zero_base", 0, 0, "", "quiet"},
		{"unavailable", 30, 0, "unavailable", "incomplete"}, {"wrong_week", 30, 4, "week", "incomplete"},
		{"wrong_phrase", 100, 10, "phrase", "incomplete"}, {"estimated", 100, 12, "estimated", "incomplete"}, {"negative", 100, -1, "", "incomplete"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{MetrikaBinding: met, WordstatBinding: demand, Phrase: "офисная лицензия", Metric: "visits", Monday: "2026-11-23", DropPct: 25, DemandDropPct: 20}
			storeChange := 3.0
			read := func(ctx context.Context, binding string, input, output any) error {
				result := output.(*batch)
				if binding == met {
					*result = batch{SchemaVersion: "aifence.v1", Source: "metrika", Status: "ok", Observations: []observation{{ID: "m", ProductKey: "store", MetricKey: "visits.relative_change_pct", Status: "observed", Period: period{"2026-11-16", "2026-11-22"}, SourceRef: sourceRef{"yandex-metrika:1337"}, Value: &storeChange, Unit: "percent"}}}
					return nil
				}
				if binding != demand {
					t.Fatal(binding)
				}
				raw, _ := json.Marshal(input)
				var params map[string]string
				_ = json.Unmarshal(raw, &params)
				if params["phrase"] != w.Phrase || params["monday"] != w.Monday || len(params) != 2 {
					t.Fatalf("%s", raw)
				}
				item := observation{ID: "w1", ProductKey: w.Phrase, MetricKey: "search_queries", Status: "observed", Unit: "count", Value: &tc.a, Period: period{"2026-11-09", "2026-11-15"}, SourceRef: sourceRef{"yandex-wordstat:search-api"}}
				newer := item
				newer.ID = "w2"
				newer.Value = &tc.b
				newer.Period = period{"2026-11-16", "2026-11-22"}
				switch tc.invalid {
				case "week":
					newer.Period = item.Period
				case "phrase":
					newer.ProductKey = "другая"
				case "estimated":
					newer.Status = "estimated"
				}
				*result = batch{SchemaVersion: "aifence.v1", Source: "wordstat", Status: "ok", Observations: []observation{newer, item}}
				if tc.invalid == "unavailable" {
					result.Status = "unavailable"
					result.Observations = nil
				}
				return nil
			}
			started := false
			out, e := Check(t.Context(), w, true, read, func(_ context.Context, s Signal) (string, error) {
				started = true
				if s.Demand == nil || s.Demand.CurrentCount != int64(tc.b) || s.Demand.BaselineCount != int64(tc.a) || s.Trigger != "wordstat" {
					t.Fatalf("%+v", s)
				}
				return "run", nil
			})
			if e != nil || out.Status != tc.want || started != (tc.want == "submitted") {
				t.Fatalf("%+v %v started=%v", out, e, started)
			}
		})
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
