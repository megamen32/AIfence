package main

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestWordstatTwoCompletedWeeks(t *testing.T) {
	in := demandQuery{Phrase: "антивирус", Monday: "2026-09-28"}
	now := time.Date(2026, 9, 28, 15, 0, 0, 0, time.UTC)
	out, err := compareDemand(t.Context(), in, "folder", now, func(_ context.Context, r wordstatRequest) ([]byte, error) {
		if r.Phrase != in.Phrase || r.FolderID != "folder" || r.Period != "PERIOD_WEEKLY" || r.FromDate != "2026-09-14T00:00:00Z" || r.ToDate != "2026-09-27T00:00:00Z" || len(r.Devices) != 1 || r.Devices[0] != "DEVICE_ALL" {
			t.Fatalf("unexpected request %+v", r)
		}
		return []byte(`{"results":[{"date":"2026-09-21T00:00:00Z","count":"0"},{"date":"2026-09-14T00:00:00Z","count":"103"}]}`), nil
	})
	if err != nil || out.Status != "ok" || len(out.Observations) != 2 {
		t.Fatalf("status %s, err %v", out.Status, err)
	}
	a, b := out.Observations[0], out.Observations[1]
	if *a.Value != 103 || *b.Value != 0 || b.Status != "observed" || a.ID == b.ID || b.Period != (Period{"2026-09-21", "2026-09-27"}) || a.ProductKey != in.Phrase {
		t.Fatal(out)
	}
	if _, err := json.Marshal(out); err != nil {
		t.Fatal(err)
	}
}

func TestWordstatRejectsIncomparableWeeks(t *testing.T) {
	const first = `{"date":"2026-09-14T00:00:00Z","count":"100"}`
	for _, tc := range []struct {
		name, raw string
		wantError bool
	}{
		{"missing week", `{"results":[` + first + `]}`, false},
		{"empty report", `{"results":[]}`, false},
		{"duplicate", `{"results":[` + first + "," + first + `]}`, true},
		{"stale window", `{"results":[{"date":"2025-09-15T00:00:00Z","count":"4"}]}`, true},
		{"missing count", `{"results":[{"date":"2026-09-14T00:00:00Z"}]}`, true},
		{"invalid count", `{"results":[{"date":"2026-09-14T00:00:00Z","count":"-1"}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
			out, err := compareDemand(t.Context(), demandQuery{"антивирус", "2026-09-28"}, "folder", now, func(context.Context, wordstatRequest) ([]byte, error) { return []byte(tc.raw), nil })
			if (err != nil) != tc.wantError || out.Status != "unavailable" || len(out.Observations) != 0 {
				t.Errorf("status %s, err %v", out.Status, err)
			}
		})
	}
}

func TestWordstatFailsClosedAndRegisters(t *testing.T) {
	ctx := t.Context()
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	fetch := func(context.Context, wordstatRequest) ([]byte, error) {
		t.Fatal("unexpected upstream call")
		return nil, nil
	}
	if _, err := compareDemand(ctx, demandQuery{"антивирус", "2026-10-05"}, "folder", now, fetch); err == nil {
		t.Fatal("future date")
	}
	if out, err := compareDemand(ctx, demandQuery{"антивирус", "2026-09-28"}, "", now, fetch); err != nil || out.Status != "unavailable" {
		t.Fatal("missing configuration")
	}
	denied := errors.New("upstream unavailable")
	if _, err := compareDemand(ctx, demandQuery{"антивирус", "2026-09-28"}, "folder", now, func(context.Context, wordstatRequest) ([]byte, error) { return nil, denied }); !errors.Is(err, denied) {
		t.Fatal("lost upstream error")
	}
	for _, tool := range newAgent().Manifest().Tools {
		if tool.Name == "compare_demand" && tool.ReadOnly && len(tool.InputSchema) > 0 && len(tool.OutputSchema) > 0 {
			return
		}
	}
	t.Fatal("missing typed read-only tool")
}
