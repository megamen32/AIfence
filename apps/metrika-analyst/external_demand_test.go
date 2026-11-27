package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestExternalDemandManifest(t *testing.T) {
	want := map[string]bool{"compare_demand": true, "compare_wordstat": true, "compare_google_trends": true}
	for _, definition := range newAgent().Manifest().Tools {
		if !want[definition.Name] {
			continue
		}
		if len(definition.InputSchema) == 0 || !strings.Contains(string(definition.OutputSchema), "normalization") {
			t.Fatalf("expected typed provenance for %s", definition.Name)
		}
		delete(want, definition.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing registrations: %v", want)
	}
}

func TestExternalDemandWordstat(t *testing.T) {
	const query = "Kaspersky Endpoint Security"
	in := demandInput{ProductKey: query, Query: query, Geo: "ALL", Monday: "2026-09-28"}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	const first = `{"date":"2026-09-14T00:00:00Z","count":"103"}`
	const second = `{"date":"2026-09-21T00:00:00Z","count":"0"}`
	for _, tc := range []struct {
		name, body, status string
		err                error
		wantError          bool
	}{
		{name: "zero", body: `{"results":[` + second + "," + first + `]}`, status: "ok"},
		{name: "missing week", body: `{"results":[` + first + `]}`, status: "unavailable"},
		{name: "duplicate", body: `{"results":[` + first + "," + first + `]}`, status: "unavailable", wantError: true},
		{name: "invalid", body: `{"error":"unexpected body"}`, status: "unavailable", wantError: true},
		{name: "upstream error", status: "unavailable", err: errors.New("request failed"), wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			out, err := readDemand(t.Context(), in, wordstatSpec, "wordstat", now, nil, func(_ context.Context, req wordstatRequest) ([]byte, error) {
				calls++
				if req.Phrase != query || req.Period != "PERIOD_WEEKLY" || req.FromDate != "2026-09-14T00:00:00Z" ||
					req.ToDate != "2026-09-27T00:00:00Z" || len(req.RegionIDs) != 0 || len(req.Devices) != 1 || req.Devices[0] != "DEVICE_ALL" {
					t.Fatalf("wrong Wordstat query: %+v", req)
				}
				return []byte(tc.body), tc.err
			})
			if calls != 1 || (err != nil) != tc.wantError || out.SchemaVersion != "aifence.v1" || out.Source != "wordstat" || out.Status != tc.status {
				t.Fatalf("calls %d, result %+v, error %v", calls, out, err)
			}
			if tc.status == "ok" {
				a, b := out.Observations[0], out.Observations[1]
				if a.Value == nil || *a.Value != 103 || b.Value == nil || *b.Value != 0 || b.Status != "observed" ||
					a.ProductKey != query || a.SourceRef.SourceID != wordstatSpec.sourceID || a.SourceRef.Locator == b.SourceRef.Locator ||
					b.Details.Origin != "live_api" || b.Details.Synthetic {
					t.Fatalf("invalid Wordstat values or provenance: %+v", out)
				}
			} else if len(out.Observations) != 0 || out.Observations == nil || out.Warnings == nil || (err == nil && len(out.Warnings) == 0) {
				t.Fatalf("invalid unavailable envelope: %+v", out)
			}
		})
	}
	in.Monday = "2026-11-23"
	out, err := readDemand(t.Context(), in, wordstatSpec, "wordstat", now, nil, nil)
	if err != nil || out.Status != "unavailable" || len(out.Observations) != 0 || len(out.Warnings) != 1 {
		t.Fatalf("expected API cutoff with no upstream request: %+v, %v", out, err)
	}
}

func TestExternalDemandImports(t *testing.T) {
	in := demandInput{ProductKey: "kaspersky_endpoint_security", Query: "Kaspersky Endpoint Security", Geo: "RU", Monday: "2026-09-28", SourcePath: demandFixture}
	now := time.Date(2026, 11, 27, 0, 0, 0, 0, time.UTC)
	readFixture := func(_ context.Context, path string) ([]byte, error) { return assets.ReadFile(path) }
	for _, spec := range []demandSpec{wordstatSpec, trendsSpec} {
		out, err := readDemand(t.Context(), in, spec, "research", now, readFixture, nil)
		if err != nil || out.Status != "ok" || out.Source != "research" || len(out.Observations) != 2 || out.AsOf != "2026-09-28T00:00:00Z" {
			t.Fatalf("fixture result %+v, error %v", out, err)
		}
		a, b := out.Observations[0], out.Observations[1]
		if a.Unit != spec.unit || a.Details.Origin != "synthetic_fixture" || !a.Details.Synthetic ||
			b.SourceRef.Locator == a.SourceRef.Locator || b.Details.Normalization != a.Details.Normalization {
			t.Fatalf("invalid fixture provenance: %+v", out)
		}
	}

	in.SourcePath = "evidence/user-owner/demand.json"
	for _, tc := range []struct {
		name, status string
		edit         func(*demandObservation)
		wantError    bool
	}{
		{name: "authorized import", status: "ok", edit: func(r *demandObservation) {
			r.Value = num(0)
		}},
		{name: "separate normalization", status: "unavailable", edit: func(r *demandObservation) {
			r.Details.Normalization = "google_trends_0_100:other-export"
			r.SourceRef.Revision = "other-export"
		}},
		{name: "different geo", status: "unavailable", edit: func(r *demandObservation) {
			r.Details.Geo = "RU-MOW"
		}},
		{name: "missing", status: "unavailable", edit: func(r *demandObservation) {
			r.Status, r.Value = "missing", nil
		}},
		{name: "out of scale", status: "unavailable", wantError: true, edit: func(r *demandObservation) {
			r.Value = num(101)
		}},
		{name: "claims live", status: "unavailable", wantError: true, edit: func(r *demandObservation) {
			r.Details.Origin = "live_api"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := assets.ReadFile(demandFixture)
			if err != nil {
				t.Fatal(err)
			}
			var snapshot demandBatch
			if err := json.Unmarshal(raw, &snapshot); err != nil {
				t.Fatal(err)
			}
			for i := range snapshot.Observations {
				row := &snapshot.Observations[i]
				row.Details.Origin, row.Details.Synthetic = "authorized_import", false
				if row.Unit == trendsSpec.unit && row.Period.To == "2026-09-27" {
					tc.edit(row)
				}
			}
			out, err := readDemand(t.Context(), in, trendsSpec, "research", now, func(_ context.Context, path string) ([]byte, error) {
				if path != in.SourcePath {
					t.Fatalf("used a different snapshot: %s", path)
				}
				return json.Marshal(snapshot)
			}, nil)
			if (err != nil) != tc.wantError || out.Status != tc.status {
				t.Fatalf("result %+v, error %v", out, err)
			}
			if tc.status == "ok" {
				row := out.Observations[1]
				if row.Value == nil || *row.Value != 0 || row.Details.Origin != "authorized_import" || row.Details.Synthetic {
					t.Fatalf("invalid import provenance or zero: %+v", row)
				}
			} else if len(out.Observations) != 0 {
				t.Fatalf("returned incomparable evidence: %+v", out)
			}
		})
	}
}
