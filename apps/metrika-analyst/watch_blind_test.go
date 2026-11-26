package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	campaign := ContextFact{ID: "campaign", Kind: "release", ProductKey: syntheticWatchProduct, MetricKey: "release_log",
		Unit: "text", Period: Period{"2034-02-07", "2034-02-07"}, Status: "observed",
		SourceRef: Ref{SourceID: "synthetic", Locator: "events!B2"}, Details: map[string]any{"release_type": "campaign", "synthetic": true}}
	measurement := ContextFact{ID: "coverage", Kind: "metric", ProductKey: syntheticWatchProduct, MetricKey: "data_break",
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
			discovery, err := d.discover(t.Context(), BlindInput{})
			if err != nil {
				t.Fatal(err)
			}
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
				if c.Control != syntheticWatchControl || c.ProductKey != syntheticWatchProduct {
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

func TestBlindDiscoveryAndScanCoverWholeSnapshot(t *testing.T) {
	control := []float64{100, 100, 100, 100, 100, 100, 100, 100}
	d := blindTestDataset(t, []float64{100, 100, 100, 100, 40, 40, 40, 40}, control)
	first, _ := time.Parse(time.DateOnly, d.series.Observations[0].Period.From)
	late := first.AddDate(0, 0, 150*7)
	d.series.AsOf = late.AddDate(0, 0, 14).Format(time.RFC3339)
	for _, product := range []string{syntheticWatchProduct, syntheticWatchControl} {
		row := d.series.Observations[0]
		row.ProductKey, row.ID, row.Period = product, product+"/late", week(late)
		d.series.Observations = append(d.series.Observations, row)
	}
	for i := range 68 {
		for j := range 28 {
			row := d.series.Observations[0]
			row.MetricKey = fmt.Sprintf("metric_%02d", i/2)
			row.ID, row.Period = fmt.Sprintf("%d/%d", i, j), week(late.AddDate(0, 0, (j-27)*7))
			if i%2 == 0 {
				row.ProductKey = syntheticWatchProduct
			} else {
				row.ProductKey = syntheticWatchControl
			}
			d.series.Observations = append(d.series.Observations, row)
		}
	}
	firstPage, err := d.discover(t.Context(), BlindInput{})
	if err != nil || firstPage.Status != "ok" || firstPage.SeriesCount != 70 || firstPage.NextOffset == nil ||
		firstPage.ScanStatus != "alert" || len(firstPage.Findings) == 0 || firstPage.Findings[0].ProductKey != syntheticWatchProduct {
		t.Fatalf("first page lost a finding or full inventory count (err=%v, status=%s, total=%d)", err, firstPage.Status, firstPage.SeriesCount)
	}
	if got := firstPage.Findings[0]; len(got.SourceRefs) > 3 || got.SourceRefCount <= len(got.SourceRefs) {
		t.Fatal("findings must preview references and retain their full count")
	}
	tail, err := d.discover(t.Context(), BlindInput{Offset: 64})
	if err != nil || tail.Status != "ok" || tail.NextOffset != nil || len(tail.Series) != 6 {
		t.Fatalf("inventory tail missing: %v", err)
	}
	found := false
	for _, m := range tail.Series {
		found = found || m.ProductKey == syntheticWatchProduct && m.MetricKey == "orders_index"
	}
	if !found {
		t.Fatal("test target must occur after the previous 64-series cutoff")
	}
	modelBrief := newAgent().Manifest().Description
	if !strings.Contains(modelBrief, "В текущем чате") || !strings.Contains(modelBrief, "выбранный") {
		t.Fatal("model brief must describe in-chat discovery of the selected snapshot")
	}
}

func TestBlindDiscoveryMeasuredSmoke(t *testing.T) {
	dir := os.Getenv("AIFENCE_BLIND_SMOKE_PAIR")
	if dir == "" {
		t.Skip("optional synthetic smoke pair not configured")
	}
	series, err := os.ReadFile(filepath.Join(dir, privateSeries))
	if err != nil {
		t.Fatal("cannot read prepared smoke series")
	}
	raw, err := os.ReadFile(filepath.Join(dir, privateContext))
	if err != nil {
		t.Fatal("cannot read prepared smoke context")
	}
	d := blindDataset{id: uuid.New(), raw: raw}
	if json.Unmarshal(series, &d.series) != nil || json.Unmarshal(raw, &d.context) != nil {
		t.Fatal("invalid prepared synthetic smoke pair")
	}
	start := time.Now()
	discovery, err := d.discover(t.Context(), BlindInput{})
	discoveryTime := time.Since(start)
	start = time.Now()
	scan, _, scanErr := d.scan(t.Context())
	scanTime := time.Since(start)
	if err != nil || scanErr != nil || discovery.Status != "ok" || scan.SeriesCount != discovery.SeriesCount {
		t.Fatalf("smoke census does not match scan: discovery=%v scan=%v", err, scanErr)
	}
	scan.Candidates, scan.NextOffset = blindPage(scan.Candidates, BlindInput{}, 12)
	out, _ := json.Marshal(discovery)
	candidates, _ := json.Marshal(scan)
	t.Logf("rows=%d dimensions=%d compared=%d, discovery=%d bytes in %s, scan=%d bytes in %s",
		len(d.series.Observations), discovery.SeriesCount, scan.ComparedWindows, len(out), discoveryTime, len(candidates), scanTime)
}

func TestBlindRankingPrefersRepeatedControlledWindows(t *testing.T) {
	series := func(v float64, n int) []float64 {
		x := make([]float64, n)
		for i := range x {
			x[i] = v
		}
		return x
	}
	isolated := blindTestDataset(t, []float64{100, 100, 100, 100, 35, 35, 35, 35}, series(100, 8))
	repeated := blindTestDataset(t, []float64{100, 100, 100, 100, 70, 70, 70, 70, 70, 70}, series(100, 10))
	historical := false
	isolated.context.Synthetic = &historical
	for _, row := range repeated.series.Observations {
		row.MetricKey = "repeat_metric"
		row.ID = "repeat/" + row.ID
		isolated.series.Observations = append(isolated.series.Observations, row)
	}
	scan, _, err := isolated.scan(t.Context())
	if err != nil || len(scan.Candidates) != 2 {
		t.Fatalf("scan: %v", err)
	}
	top, second := scan.Candidates[0], scan.Candidates[1]
	if top.MetricKey != "repeat_metric" || top.ConfirmedWindows != 2 ||
		top.LastConfirmedWeek == nil || top.LastConfirmedWeek.From != "2034-02-27" ||
		len(top.LastConfirmationRefs) != 2 || second.ConfirmedWindows != 1 ||
		blindMagnitude(top) >= blindMagnitude(second) {
		t.Fatal("repeated, smaller decline must precede larger isolated decline and carry the second week's sources")
	}
	discovery, err := isolated.discover(t.Context(), BlindInput{})
	if err != nil || scan.Origin != "imported_historical" || discovery.Origin != scan.Origin ||
		discovery.DatasetID != scan.DatasetID || len(discovery.Findings) < 1 || discovery.Findings[0].ID != top.ID {
		t.Fatalf("discovery and scan must agree on provenance, dataset and leading candidate: %v", err)
	}
}
