package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"agent/handlers"
	"agent/internal/answer"
	"agent/internal/evidence"
)

func TestReviewAttachmentKeepsCanonicalCauseAfterDirectorReply(t *testing.T) {
	before, after, change, z := 10.0, 5.0, -50.0, -3.4
	brief := handlers.SalesBrief{
		Replies: []answer.Input{{Decision: "Для решения о цене нужны сопоставимые предложения того же артикула."}},
		Causes: []handlers.CauseReading{{
			CauseType: "masked_multi_cause_regression", Confirmation: "candidate", Status: "observed",
			Product: "Kaspersky Small Office Security", Segment: "mobile",
			Period:     evidence.Period{From: "2026-10-26", To: "2026-11-01"},
			SourceRef:  evidence.SourceRef{SourceID: "blind-metrika", Locator: "week=24;metric=checkouts"},
			SourceRefs: []evidence.SourceRef{{SourceID: "blind-metrika", Locator: "week=24;metric=visits"}},
			CauseEvidence: []handlers.CauseMetric{
				{Segment: "mobile", MetricKey: "checkouts", Before: &before, After: &after, ChangePct: &change, ZScore: &z},
				{Segment: "mobile", MetricKey: "orders", Before: nil, After: nil, ChangePct: nil},
			},
			CauseRelease: &handlers.CauseRelease{ID: "R-24", Period: evidence.Period{From: "2026-10-26", To: "2026-10-26"},
				Value:     json.RawMessage(`"Изменение карточки продукта"`),
				SourceRef: evidence.SourceRef{SourceID: "synthetic-release", Locator: "releases.json#/24"}},
		}, {CauseType: "traffic_quality", Product: "Kaspersky Endpoint Security", Period: evidence.Period{From: "2026-10-26", To: "2026-11-01"},
			SourceRef:  evidence.SourceRef{SourceID: "blind-metrika", Locator: "candidate=aggregate"},
			SourceRefs: []evidence.SourceRef{{SourceID: "synthetic-release", Locator: "releases.json#/24"}},
			CauseRelease: &handlers.CauseRelease{ID: "R-24", Period: evidence.Period{From: "2026-10-26", To: "2026-10-26"},
				SourceRef: evidence.SourceRef{SourceID: "synthetic-release", Locator: "releases.json#/24"}}}},
	}
	raw, err := json.Marshal(brief)
	if err != nil {
		t.Fatal(err)
	}
	attachment := reviewAttachment(string(raw))
	for _, want := range []string{"снижение оформлений", "10.00", "5.00", "−50,0%", "z=−3,40", "это не величина изменения", "значение отсутствует",
		"week=24;metric=visits", "Релиз в ту же неделю", "releases.json#/24", "Локаторы исходных измерений не предоставлены"} {
		if !strings.Contains(attachment, want) {
			t.Fatalf("attachment lost %q: %s", want, attachment)
		}
	}
	if strings.Contains(attachment, "Локатор измерения: synthetic-release") {
		t.Fatalf("release locator was presented as a measurement: %s", attachment)
	}
}

func TestReviewAttachmentFromProvenanceFiles(t *testing.T) {
	paths := os.Getenv("PM_CAUSE_DOCS")
	if paths == "" {
		t.Skip("set PM_CAUSE_DOCS to source-resolved evidence documents")
	}
	for _, path := range strings.Split(paths, ":") {
		t.Run(path, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var doc struct {
				Batches []struct {
					Observations []json.RawMessage `json:"observations"`
				} `json:"batches"`
			}
			if err := json.Unmarshal(data, &doc); err != nil {
				t.Fatal(err)
			}
			for _, batch := range doc.Batches {
				for _, raw := range batch.Observations {
					var row struct {
						Metric  string          `json:"metric_key"`
						Product string          `json:"product_key"`
						Period  evidence.Period `json:"period"`
						Details json.RawMessage `json:"details"`
					}
					if err := json.Unmarshal(raw, &row); err != nil {
						t.Fatal(err)
					}
					var cause handlers.CauseReading
					if err := json.Unmarshal(row.Details, &cause); err != nil {
						t.Fatal(err)
					}
					cause.CauseType, cause.Family, cause.Period = row.Metric, row.Product, row.Period
					brief, _ := json.Marshal(handlers.SalesBrief{Causes: []handlers.CauseReading{cause}})
					attachment := reviewAttachment(string(brief))
					if len(attachment) > 24*1024 || !strings.Contains(attachment, cause.Period.From) ||
						cause.CauseRelease == nil || !strings.Contains(attachment, cause.CauseRelease.SourceRef.Locator) {
						t.Fatalf("cause or release unavailable in attachment (%d bytes)", len(attachment))
					}
					for _, m := range cause.MeasurementProvenance {
						for _, value := range []*float64{m.Baseline.Total, m.Current.Total} {
							if value == nil || !strings.Contains(attachment, fmt.Sprintf("%.2f", *value)) {
								t.Fatalf("measurement total missing from attachment: %+v", m)
							}
						}
						if !strings.Contains(attachment, m.Baseline.Period.From) || !strings.Contains(attachment, m.Current.Period.To) {
							t.Fatal("measurement windows missing")
						}
					}
					if onset := cause.MigrationOnset; onset != nil {
						for _, week := range []handlers.CauseMigrationWeek{onset.OldKeyLastWeek, onset.NewKeyFirstWeek} {
							if week.Total == nil || !strings.Contains(attachment, fmt.Sprintf("%.2f", *week.Total)) ||
								!strings.Contains(attachment, week.Period.From) || len(week.Rows) == 0 ||
								!strings.Contains(attachment, week.Rows[0].SourceRef.Locator) {
								t.Fatal("source-located onset week missing")
							}
						}
					}
					t.Logf("attachment=%d bytes, cause=%s, origin=%s", len(attachment), cause.CauseType, cause.Origin)
				}
			}
		})
	}
}
