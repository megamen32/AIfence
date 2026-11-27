package main

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func TestDeltas(t *testing.T) {
	a := Observation{ID: "a", MetricKey: "conversion", Unit: "ratio", Status: "observed", Value: num(.4)}
	b := a
	b.ID = "b"
	b.Value = num(.3)
	d := deltas(a, b)
	if d[0].Unit != "pp" || *d[0].Value > -9.99 || *d[0].Value < -10.01 || *d[1].Value > -24.99 || *d[1].Value < -25.01 {
		t.Fatalf("%+v", d)
	}
	a.Value = num(0)
	d = deltas(a, b)
	if d[1].Value != nil || d[0].Value == nil {
		t.Fatal("zero base")
	}
	a.Status = "missing"
	d = deltas(a, b)
	if d[0].Value != nil || d[1].Value != nil {
		t.Fatal("missing")
	}
}
func TestReport(t *testing.T) {
	q := Query{ProductKey: "store", From: "2026-01-01", To: "2026-01-07", Segment: "all"}
	o := report(json.RawMessage(`{"result":{"totals":{"ym:s:usersPurchasePercentage":4},"meta":{"sampled":false}}}`), q, "purchase_conversion", metrics["purchase_conversion"], Ref{"x", "q", "", ""})
	if o.Value == nil || *o.Value != .04 || o.Status != "observed" {
		t.Fatalf("%+v", o)
	}
	o = report(json.RawMessage(`{"totals":{"ym:s:ecommercePurchases":0},"meta":{"sampled":true}}`), q, "orders", metrics["orders"], Ref{})
	if o.Value == nil || *o.Value != 0 || o.Status != "estimated" {
		t.Fatalf("%+v", o)
	}
}
func TestHistorical(t *testing.T) {
	raw := []byte(`{"schema_version":"aifence.v1","source":"metrika","as_of":"2026-01-31T00:00:00Z","status":"ok","observations":[{"id":"a","kind":"metric","product_key":"store","metric_key":"visits","value":100,"unit":"count","period":{"from":"2026-01-01","to":"2026-01-07"},"source_ref":{"source_id":"synthetic","locator":"row 1"},"status":"observed"},{"id":"b","kind":"metric","product_key":"store","metric_key":"visits","value":80,"unit":"count","period":{"from":"2026-01-08","to":"2026-01-14"},"source_ref":{"source_id":"synthetic","locator":"row 2"},"status":"observed"}],"warnings":["synthetic"]}`)
	s := Service{config: func(context.Context) Settings { return Settings{EvidencePath: "fixture.json"} }, read: func(context.Context, string) ([]byte, error) { return raw, nil }, call: func(context.Context, string, any) (json.RawMessage, error) {
		t.Fatal("historical must not call live")
		return nil, nil
	}}
	r, e := s.Compare(context.Background(), Compare{ProductKey: "store", MetricKeys: []string{"visits", "orders"}, BaselineFrom: "2026-01-01", BaselineTo: "2026-01-07", CurrentFrom: "2026-01-08", CurrentTo: "2026-01-14"})
	if e != nil || r.Status != "partial" || len(r.Observations) != 8 || *r.Observations[3].Value != -20 || r.Observations[4].Value != nil {
		t.Fatalf("%+v %v", r, e)
	}
}
func TestCheck(t *testing.T) {
	s := Service{config: func(context.Context) Settings { return Settings{Counter: 42} }, call: func(_ context.Context, n string, _ any) (json.RawMessage, error) {
		if n != "get_metadata" {
			t.Fatal(n)
		}
		return json.RawMessage(`{"result":{"counters":[{"id":42}]}}`), nil
	}}
	r, e := s.Check(context.Background(), CheckInput{})
	if e != nil || r.Status != "ok" {
		t.Fatalf("%+v %v", r, e)
	}
}

func TestAgentDefinition(t *testing.T) {
	if newAgent() == nil {
		t.Fatal("missing agent definition")
	}
}

func TestLiveQuery(t *testing.T) {
	calls := 0
	s := Service{config: func(context.Context) Settings { return Settings{Counter: 42} }, call: func(_ context.Context, n string, args any) (json.RawMessage, error) {
		calls++
		if n != "run_report" {
			t.Fatal(n)
		}
		a := args.(map[string]any)
		if a["counterId"] != int64(42) || a["metrics"].([]string)[0] != "ym:s:visits" || a["date1"] != "2026-01-01" || a["date2"] != "2026-01-07" {
			t.Fatalf("wrong request: %+v", a)
		}
		return json.RawMessage(`{"totals":{"ym:s:visits":123},"meta":{"sampled":false}}`), nil
	}}
	b, e := s.Query(context.Background(), Query{ProductKey: "store", MetricKeys: []string{"visits", "orders"}, From: "2026-01-01", To: "2026-01-07"})
	if e != nil || calls != 1 || b.Status != "partial" || b.Observations[0].Value == nil || *b.Observations[0].Value != 123 || b.Observations[1].Value != nil {
		t.Fatalf("%+v %v", b, e)
	}
}

func TestBoxLabComparison(t *testing.T) {
	calls := 0
	s := Service{config: func(context.Context) Settings { return Settings{Counter: 42, Ecommerce: true} }, call: func(_ context.Context, n string, args any) (json.RawMessage, error) {
		calls++
		if n != "run_comparison" {
			t.Fatalf("expected native comparison, got %s", n)
		}
		a := args.(map[string]any)
		if a["date1A"] != "2026-01-01" || a["date1B"] != "2026-01-08" || a["accuracy"] != "full" {
			t.Fatalf("unexpected args %+v", a)
		}
		return json.RawMessage(`{"totals":{"ym:s:usersPurchasePercentage":{"a":4,"b":3,"delta":-1,"delta_pct":-25}},"meta":{"sampled":false}}`), nil
	}}
	b, e := s.Compare(context.Background(), Compare{ProductKey: "store", MetricKeys: []string{"purchase_conversion"}, BaselineFrom: "2026-01-01", BaselineTo: "2026-01-07", CurrentFrom: "2026-01-08", CurrentTo: "2026-01-14"})
	if e != nil || calls != 1 || b.Status != "ok" || len(b.Observations) != 4 || *b.Observations[0].Value != .04 || *b.Observations[1].Value != .03 || b.Observations[2].Unit != "pp" || *b.Observations[2].Value != -1 || *b.Observations[3].Value != -25 {
		t.Fatalf("%+v %v", b, e)
	}
}

func TestLiveComparisonDoesNotFollowDashboardEvidence(t *testing.T) {
	for _, counter := range []int64{42, 0} {
		t.Run(fmt.Sprint(counter), func(t *testing.T) {
			calls := 0
			s := Service{
				config: func(context.Context) Settings { return Settings{Counter: counter, EvidencePath: "dashboard.json"} },
				read: func(context.Context, string) ([]byte, error) {
					t.Fatal("live comparison read dashboard fixture")
					return nil, nil
				},
				call: func(_ context.Context, name string, args any) (json.RawMessage, error) {
					calls++
					if name != "run_comparison" || args.(map[string]any)["counterId"] != counter {
						t.Fatalf("unexpected live call: %s %#v", name, args)
					}
					return json.RawMessage(`{"totals":{"ym:s:visits":{"a":100,"b":80,"delta":-20,"delta_pct":-20}},"meta":{"sampled":false}}`), nil
				},
			}
			b, err := s.Compare(t.Context(), Compare{SourceMode: "live", ProductKey: "store", MetricKeys: []string{"visits"}, BaselineFrom: "2026-01-01", BaselineTo: "2026-01-07", CurrentFrom: "2026-01-08", CurrentTo: "2026-01-14"})
			if err != nil {
				t.Fatal(err)
			}
			if counter == 0 {
				if calls != 0 || b.Status != "unavailable" {
					t.Fatalf("missing counter fell back: %+v, calls=%d", b, calls)
				}
			} else if calls != 1 || b.Status != "ok" || b.Observations[3].SourceRef.SourceID != "yandex-metrika:42" || *b.Observations[3].Value != -20 {
				t.Fatalf("wrong source or comparison: %+v, calls=%d", b, calls)
			}
		})
	}
}

func TestLiveComparisonRejectsAmbiguousSource(t *testing.T) {
	s := Service{config: func(context.Context) Settings { return Settings{Counter: 42} }}
	for _, in := range []Compare{
		{SourceMode: "live", SourcePath: "explicit.json"},
		{SourceMode: "live", ProductKey: "product-family"},
		{SourceMode: "unknown"},
	} {
		if _, err := s.Compare(t.Context(), in); err == nil {
			t.Fatalf("accepted ambiguous source: %+v", in)
		}
	}
}
