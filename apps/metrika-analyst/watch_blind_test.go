package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func blindTestDataset(t *testing.T, target, control []float64, facts ...ContextFact) blindDataset {
	t.Helper()
	w := testWatch(t, target, control, nil, facts...)
	ctx := t.Context()
	series, err := w.series.historical(ctx, "series.json")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := w.loadContext(ctx, "context.json")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := w.series.read(ctx, "context.json")
	if err != nil {
		t.Fatal(err)
	}
	return blindDataset{id: uuid.New(), series: series, context: catalog, raw: raw}
}

func TestBlindWatchdogScan(t *testing.T) {
	steady := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}
	fall := []float64{100, 100, 100, 100, 100, 55, 55, 55, 55, 55}
	spike := []float64{100, 100, 100, 100, 100, 190, 100, 100, 100, 100}
	campaign := ContextFact{ID: "campaign", Kind: "release", ProductKey: "target", MetricKey: "release_log",
		Unit: "text", Period: Period{"2034-02-07", "2034-02-07"}, Status: "observed",
		SourceRef: Ref{SourceID: "synthetic", Locator: "events!B2"}, Details: map[string]any{"release_type": "campaign", "synthetic": true}}
	measurement := ContextFact{ID: "coverage", Kind: "metric", ProductKey: "target", MetricKey: "data_break",
		Unit: "text", Period: Period{"2034-02-01", "2034-03-15"}, Status: "incomparable",
		SourceRef: Ref{SourceID: "synthetic", Locator: "coverage!B2"}, Details: map[string]any{"effect": "exclude", "synthetic": true}}
	for _, tt := range []struct {
		name, status    string
		target, control []float64
		context         []ContextFact
		top             string
	}{
		{"quiet", "quiet", steady, steady, nil, ""},
		{"decline", "alert", fall, steady, nil, "controlled_decline"},
		{"campaign", "campaign_effect", spike, steady, []ContextFact{campaign}, "campaign_effect"},
		{"unattributed spike", "temporary_spike", spike, steady, nil, "temporary_spike"},
		{"measurement", "measurement_issue", fall, steady, []ContextFact{measurement}, "measurement_break"},
		{"shared change", "incomplete", fall, fall, nil, ""},
		{"no control", "incomplete", fall, nil, nil, "uncontrolled_decline"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := blindTestDataset(t, tt.target, tt.control, tt.context...)
			discovery := d.discover()
			if len(discovery.Series) == 0 || discovery.Series[0].MetricKey != "orders_index" ||
				discovery.Series[0].WeeklyRows != len(tt.target) {
				t.Fatal("dimension inventory must come from actual observations")
			}
			out, _, err := d.scan(t.Context())
			if err != nil || out.Status != tt.status {
				t.Fatalf("scan status %q, want %q (%v)", out.Status, tt.status, err)
			}
			if tt.top != "" {
				if len(out.Candidates) == 0 || out.Candidates[0].Kind != tt.top || len(out.Candidates[0].SourceRefs) == 0 {
					t.Fatalf("missing sourced %s candidate", tt.top)
				}
			} else if len(out.Candidates) != 0 {
				t.Fatal("invented a candidate")
			}
			if tt.top == "controlled_decline" {
				c := out.Candidates[0]
				if c.Control != "control" || c.ProductKey != "target" {
					t.Fatal("wrong target/control by observed behavior")
				}
				in := BlindCaseInput{DatasetID: d.id.String(), CandidateID: c.ID}
				document, err := d.collect(t.Context(), in)
				if err != nil || document.Document == nil || document.Status != "alert" || *document.FirstAlertWeek != c.Period {
					t.Fatalf("collector must reproduce this candidate: %v", err)
				}
				d.id = uuid.New()
				if _, err := d.collect(t.Context(), in); err == nil {
					t.Fatal("candidate from an older snapshot was accepted")
				}
			}
		})
	}
}

func TestBlindWatchdogSeriesOnlyUpload(t *testing.T) {
	raw, ok := embeddedEvidence("fixtures/synthetic-watch.json")
	if !ok {
		t.Fatal("test series unavailable")
	}
	pair := watchPair{privateSeries: raw}
	if err := validateWatchPair(t.Context(), pair); err != nil {
		t.Fatal(err)
	}
	var registry WatchContext
	if err := json.Unmarshal(pair[privateContext], &registry); err != nil ||
		registry.Synthetic == nil || !*registry.Synthetic || len(registry.Batches) != 0 {
		t.Fatal("series-only upload must create an empty registry with the same origin")
	}
}

func TestBlindWatchdogToolsManifest(t *testing.T) {
	want := map[string]bool{"discover_watchdog_series": true, "scan_watchdog_series": true, "collect_selected_watchdog_case": true}
	for _, decl := range newAgent().Manifest().Tools {
		if want[decl.Name] && !strings.Contains(string(decl.InputSchema), "source_path") {
			delete(want, decl.Name)
		}
	}
	if len(want) > 0 {
		t.Fatalf("missing blind tools or a tool accepts paths: %v", want)
	}
	s := privateWatch{origin: func(context.Context) string { return "" }}
	if _, err := s.loadBlind(t.Context(), false); err != errBlindSelection {
		t.Fatal("an unconfigured deployment exposed blind dataset access")
	}
}
