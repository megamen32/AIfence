package main

import (
	"context"
	"strings"
	"testing"
)

func TestDemoReplayShiftsFallOntoRequestedWeeks(t *testing.T) {
	s := Service{config: func(context.Context) Settings { return Settings{} }, read: readEmbeddedFixture}
	b, err := s.Compare(context.Background(), Compare{DemoReplay: true, ProductKey: demoReplayProduct, MetricKeys: []string{"orders_index"},
		BaselineFrom: "2026-11-09", BaselineTo: "2026-11-15", CurrentFrom: "2026-11-16", CurrentTo: "2026-11-22"})
	if err != nil {
		t.Fatal(err)
	}
	if b.Status != "ok" {
		t.Fatalf("status %s %v", b.Status, b.Warnings)
	}
	found := false
	for _, o := range b.Observations {
		if o.MetricKey == "orders_index.relative_change_pct" && o.Value != nil && *o.Value == -25 && o.Period.From == "2026-11-16" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no -25%% change in %+v", b.Observations)
	}
	if !strings.Contains(strings.Join(b.Warnings, " "), "ДЕМО") {
		t.Fatalf("missing demo label: %v", b.Warnings)
	}
	if _, err := s.Compare(context.Background(), Compare{DemoReplay: true, ProductKey: "store", MetricKeys: []string{"orders_index"},
		BaselineFrom: "2026-11-09", BaselineTo: "2026-11-15", CurrentFrom: "2026-11-16", CurrentTo: "2026-11-22"}); err == nil {
		t.Fatal("demo replay must refuse other products")
	}
}
