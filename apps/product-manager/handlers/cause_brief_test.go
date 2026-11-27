package handlers

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"agent/internal/evidence"
)

func TestCauseProjectionPreservesMissingAndAllRows(t *testing.T) {
	var rows []monitorObservation
	for i := 0; i < 5; i++ {
		rows = append(rows, monitorObservation{snapshot: "selected", Row: evidence.Row{
			ID: fmt.Sprintf("cause-%d", i), Kind: "metric", Source: "metrika", Product: "kaspersky_small_office_security",
			Metric: "masked_multi_cause_regression", Segment: "mobile", Status: "observed", Value: "42", Unit: "score",
			Period:  evidence.Period{From: "2026-10-26", To: "2026-11-01"},
			Ref:     evidence.SourceRef{SourceID: "blind-metrika", Locator: fmt.Sprintf("week=24;cause=%d", i)},
			Details: `{"kind":"cause_hint","confirmation":"candidate","cause_type":"masked_multi_cause_regression","cause_evidence":[{"segment":"mobile","metric_key":"checkouts","before_mean":10,"after_mean":null,"change_pct":null}],"source_refs":[{"source_id":"blind-metrika","locator":"week=24;metric=checkouts"}],"cause_release":{"id":"R-24","period":{"from":"2026-10-26","to":"2026-10-26"},"source_ref":{"source_id":"synthetic-release","locator":"releases.json#/24"}},"synthetic":true}`,
		}})
	}
	brief := salesBrief(rows, nil, BriefQuery{Summary: true})
	if len(brief.Causes) != 5 || brief.Causes[0].CauseEvidence[0].After != nil ||
		brief.Causes[0].CauseEvidence[0].ChangePct != nil ||
		brief.Causes[0].SourceRefs[0].Locator != "week=24;metric=checkouts" ||
		brief.Causes[0].CauseRelease.SourceRef.Locator != "releases.json#/24" ||
		strings.Contains(brief.DataState.NextStep, "Импортируйте документ") {
		t.Fatalf("cause projection lost data: %+v", brief)
	}
}

func TestCauseProjectionFromEvidenceDocuments(t *testing.T) {
	paths := os.Getenv("PM_CAUSE_DOCS")
	if paths == "" {
		t.Skip("set PM_CAUSE_DOCS to owner-scoped evidence documents")
	}
	for _, path := range strings.Split(paths, ":") {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc evidence.Document
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			var rows []monitorObservation
			want := map[string]json.RawMessage{}
			for _, batch := range doc.Batches {
				for _, raw := range batch.Observations {
					var obs struct {
						ID         string             `json:"id"`
						Kind       string             `json:"kind"`
						ProductKey string             `json:"product_key"`
						MetricKey  string             `json:"metric_key"`
						Value      json.RawMessage    `json:"value"`
						Unit       string             `json:"unit"`
						Period     evidence.Period    `json:"period"`
						Segment    string             `json:"segment"`
						SourceRef  evidence.SourceRef `json:"source_ref"`
						Status     string             `json:"status"`
						Details    json.RawMessage    `json:"details"`
					}
					if err := json.Unmarshal(raw, &obs); err != nil {
						t.Fatal(err)
					}
					row := evidence.Row{ID: obs.ID, Kind: obs.Kind, Source: batch.Source, Product: obs.ProductKey,
						Metric: obs.MetricKey, Value: string(obs.Value), Unit: obs.Unit, Period: obs.Period,
						Segment: obs.Segment, Ref: obs.SourceRef, Status: obs.Status, Details: string(obs.Details)}
					rows = append(rows, monitorObservation{Row: row, snapshot: path})
					var marker struct {
						Kind string `json:"kind"`
					}
					if batch.Source == "metrika" && obs.Kind == "metric" && json.Unmarshal(obs.Details, &marker) == nil && marker.Kind == "cause_hint" {
						want[obs.ID] = obs.Details
					}
				}
			}
			brief := salesBrief(rows, nil, BriefQuery{Summary: true})
			if len(brief.Causes) != len(want) || len(want) == 0 {
				t.Fatalf("projected %d of %d cause rows", len(brief.Causes), len(want))
			}
			if brief.DataState != nil && strings.Contains(brief.DataState.NextStep, "Импортируйте документ") {
				t.Fatal("summary asks to import an already present cause document")
			}
			for _, got := range brief.Causes {
				var original struct {
					CauseType             string                       `json:"cause_type"`
					CauseEvidence         []CauseMetric                `json:"cause_evidence"`
					MeasurementProvenance []CauseMeasurementProvenance `json:"measurement_provenance"`
					CheckoutPerVisit      *CauseCheckoutPerVisit       `json:"checkout_per_visit"`
					MigrationOnset        *CauseMigrationOnset         `json:"migration_onset"`
					CauseRelease          *CauseRelease                `json:"cause_release"`
					SourceRefs            []evidence.SourceRef         `json:"source_refs"`
					NativeSourceRefs      []evidence.SourceRef         `json:"native_source_refs"`
				}
				if err := json.Unmarshal(want[got.ID], &original); err != nil {
					t.Fatal(err)
				}
				if got.CauseType != original.CauseType || len(got.CauseEvidence) != len(original.CauseEvidence) ||
					len(got.SourceRefs) != len(original.SourceRefs) || (got.CauseRelease == nil) != (original.CauseRelease == nil) {
					t.Fatalf("cause fields lost: %+v", got)
				}
				if !reflect.DeepEqual(got.MeasurementProvenance, original.MeasurementProvenance) ||
					!reflect.DeepEqual(got.CheckoutPerVisit, original.CheckoutPerVisit) ||
					!reflect.DeepEqual(got.MigrationOnset, original.MigrationOnset) ||
					!reflect.DeepEqual(got.NativeSourceRefs, original.NativeSourceRefs) {
					t.Fatal("source-resolved windows, rows, ratio or native references changed")
				}
				for i, metric := range got.CauseEvidence {
					g, _ := json.Marshal(metric)
					w, _ := json.Marshal(original.CauseEvidence[i])
					if string(g) != string(w) {
						t.Fatalf("metric changed: %s != %s", g, w)
					}
					t.Logf("metric %s", g)
				}
				for i, ref := range got.SourceRefs {
					if ref != original.SourceRefs[i] {
						t.Fatalf("measurement locator changed: %+v", ref)
					}
				}
				if got.CauseRelease != nil && got.CauseRelease.SourceRef != original.CauseRelease.SourceRef {
					t.Fatal("release locator changed")
				}
				if got.CauseRelease != nil {
					t.Logf("release %s %s", got.CauseRelease.Period.From, got.CauseRelease.SourceRef.Locator)
				}
				for _, m := range got.MeasurementProvenance {
					t.Logf("provenance %s %s baseline=%v current=%v rows=%d+%d", m.MetricKey, m.Unit,
						m.Baseline.Total, m.Current.Total, len(m.Baseline.Rows), len(m.Current.Rows))
				}
				if got.MigrationOnset != nil {
					t.Logf("onset %s old=%v new=%v", got.MigrationOnset.MetricKey,
						got.MigrationOnset.OldKeyLastWeek.Total, got.MigrationOnset.NewKeyFirstWeek.Total)
				}
				t.Logf("%s %s week=%s metrics=%d refs=%d release=%t", got.Family, got.CauseType,
					got.Period.From, len(got.CauseEvidence), len(got.SourceRefs), got.CauseRelease != nil)
			}
		})
	}
}
