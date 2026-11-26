package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/airlockrun/agentsdk/sourcebundle"
)

func testWatch(t *testing.T, target, control []float64, mutate func(*Observation), facts ...ContextFact) Watch {
	t.Helper()
	src := batch()
	src.AsOf = "2034-06-01T00:00:00Z"
	start, _ := time.Parse(time.DateOnly, "2034-01-02")
	for product, series := range map[string][]float64{syntheticWatchProduct: target, syntheticWatchControl: control} {
		for i, v := range series {
			p := week(start.AddDate(0, 0, i*7))
			o := Observation{ID: product + "/" + p.From, Kind: "metric", ProductKey: product, MetricKey: "orders_index",
				Value: num(v), Unit: "index", Period: p, Segment: "self_service", Status: "observed",
				SourceRef: Ref{SourceID: "synthetic", Locator: product + "/" + p.From}, Details: map[string]any{"synthetic": true}}
			if mutate != nil {
				mutate(&o)
			}
			src.Observations = append(src.Observations, o)
		}
	}
	truth := true
	catalog := WatchContext{"aifence.watch-context.v1", &truth, map[string]ContextBatch{}}
	for _, f := range facts {
		source := map[string]string{"metric": "metrika", "research": "research", "task": "backlog", "release": "release"}[f.Kind]
		x := catalog.Batches[source]
		x.SchemaVersion, x.Source, x.AsOf, x.Status = "aifence.v1", source, src.AsOf, "ok"
		x.Observations = append(x.Observations, f)
		catalog.Batches[source] = x
	}
	read := func(_ context.Context, path string) ([]byte, error) {
		switch path {
		case "series.json":
			return json.Marshal(src)
		case "context.json":
			return json.Marshal(catalog)
		default:
			return nil, errors.New("not found")
		}
	}
	return Watch{Service{read: read, config: func(context.Context) Settings { return Settings{EvidencePath: "series.json"} }},
		func(context.Context) string { return "context.json" }}
}

func TestWatchdogScenarios(t *testing.T) {
	t.Parallel()
	steady := []float64{100, 100, 100, 100, 100, 100, 100, 100}
	spike := []float64{100, 100, 100, 5000, 100, 100, 100, 100}
	decline := []float64{100, 100, 100, 100, 63, 65, 64, 61}
	mark := func(o *Observation) {
		if o.Period.From == "2034-02-20" && o.ProductKey == syntheticWatchProduct {
			o.Details["partial_week"] = true
		}
	}
	for _, tt := range []struct {
		name            string
		target, control []float64
		mutate          func(*Observation)
		closed          string
		reason          string
		value           string
	}{
		{"target decline", decline, steady, nil, "", "target_drop_with_stable_control", "true"},
		{"spike in baseline", spike, steady, nil, "", "threshold_not_met", "false"},
		{"unstable control", decline, decline, nil, "", "unstable_control", "null"},
		{"missing control", decline, steady[:7], nil, "", "missing_or_duplicate_week", "null"},
		{"partial source flag", decline, steady, mark, "", "incomplete_week", "null"},
		{"period still open", decline, steady, nil, "2034-02-22", "incomplete_week", "null"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			w := testWatch(t, tt.target, tt.control, tt.mutate)
			in := SignalInput{ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl, MetricKey: "orders_index",
				From: "2034-02-13", To: "2034-02-26", AsOf: tt.closed}
			out, err := w.Replay(context.Background(), in)
			if err != nil || len(out.Observations) != 2 {
				t.Fatalf("replay: %v (%d observations)", err, len(out.Observations))
			}
			last := out.Observations[1]
			value, _ := json.Marshal(last.Value)
			links, _ := last.Details["source_refs"].([]evidenceLink)
			if last.Details["reason"] != tt.reason || string(value) != tt.value || last.MetricKey != watchMetric || len(links) < 14 {
				t.Fatalf("got %s (%v), %d source links", value, last.Details["reason"], len(links))
			}
			detected, err := w.Detect(context.Background(), in)
			if err != nil || len(detected.Observations) != 1 || detected.Observations[0].ID != last.ID {
				t.Fatalf("detect/replay mismatch: %v", err)
			}
			for _, link := range out.Observations[0].Details["source_refs"].([]evidenceLink) {
				if strings.HasSuffix(link.ID, "/2034-02-20") {
					t.Fatal("replay used future rows")
				}
			}
		})
	}
}

func TestWatchContextAndBreakBoundary(t *testing.T) {
	base := func(kind, id, key, value string, p Period, details map[string]any) ContextFact {
		return ContextFact{ID: id, Kind: kind, ProductKey: "all", MetricKey: key, Value: &value, Unit: "text", Period: p,
			Status: "observed", SourceRef: Ref{SourceID: "synthetic", Locator: id}, Details: details}
	}
	period := Period{"2034-01-15", "2034-04-04"}
	facts := []ContextFact{
		base("metric", "sheet!A2", "data_break", "Changed event definition", period,
			map[string]any{"effect": "boundary", "metric_keys": []string{"orders_index"}}),
		base("task", "sheet!B2", "backlog_item", "Audit ordering", period,
			map[string]any{"metric_keys": []string{"family_orders_idx"}}),
		base("release", "sheet!C2", "release_log", "Changed ordering", Period{period.From, period.From}, nil),
		base("research", "sheet!D2", "interview_excerpt", strings.Repeat("x", 400)+" СЛОЖНО оформить заказ", period, nil),
	}
	steady := []float64{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}
	w := testWatch(t, steady, steady, nil, facts...)
	ctx := context.Background()
	window := ContextWindow{From: period.From, To: period.To}
	releases, err := w.Releases(ctx, window)
	if err != nil || len(releases.Observations) != 1 || releases.Observations[0].SourceRef.Locator != "sheet!C2" {
		t.Fatalf("release query: %v", err)
	}
	tasks, err := w.Backlog(ctx, BacklogFilter{MetricKeys: []string{"orders_index"}})
	if err != nil || len(tasks.Observations) != 1 {
		t.Fatalf("linked metric alias was not found: %v", err)
	}
	ints, err := w.Interviews(ctx, InterviewFilter{Keywords: []string{"сложно"}})
	if err != nil || len(ints.Observations) != 1 || !strings.Contains(*ints.Observations[0].Value, "СЛОЖНО") ||
		!strings.HasPrefix(*ints.Observations[0].Value, "…") {
		t.Fatalf("interview excerpt: %v", err)
	}
	breaks, err := w.Breaks(ctx, MetricWindow{ContextWindow: window, MetricKeys: []string{"orders_index"}})
	if err != nil || len(breaks.Observations) != 1 {
		t.Fatalf("break lookup: %v", err)
	}
	signals, err := w.Replay(ctx, SignalInput{ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl, MetricKey: "orders_index",
		From: "2034-02-20", To: "2034-03-12"})
	if err != nil || len(signals.Observations) != 3 || signals.Observations[0].Details["reason"] != "collection_break" ||
		signals.Observations[2].Value == nil || *signals.Observations[2].Value {
		t.Fatalf("boundary should only invalidate windows crossing the change: %v", err)
	}
}

func TestWatchToolsManifest(t *testing.T) {
	want := map[string]string{"detect_signal": "control_product_key", "replay_signals": "control_product_key",
		"releases_in_window": "from", "backlog_for_metrics": "metric_keys", "interview_search": "keywords", "data_breaks_in_window": "from"}
	for _, tool := range newAgent().Manifest().Tools {
		key, ok := want[tool.Name]
		if !ok {
			continue
		}
		if !strings.Contains(string(tool.InputSchema), key) || !strings.Contains(string(tool.OutputSchema), "observations") {
			t.Errorf("incomplete schema for %s", tool.Name)
		}
		delete(want, tool.Name)
	}
	if len(want) != 0 {
		t.Fatalf("missing tools: %v", want)
	}
}

func TestWatchSourceBundle(t *testing.T) {
	files, err := sourcebundle.Files(".")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"fixtures/synthetic-watch.json": true, "fixtures/synthetic-after-fix.json": true,
		"fixtures/synthetic-watch-context.json": true,
		"INSTRUCTIONS.md":                       true, "index.html": true, "styles/app.css": true, "go.mod": true}
	for _, f := range files {
		if strings.HasPrefix(f.Path, "demo_sources/data/") {
			t.Fatal("private data directory entered the SDK bundle")
		}
		delete(want, f.Path)
	}
	if len(want) != 0 {
		t.Fatalf("missing source bundle inputs: %v", want)
	}
}

func readEmbeddedFixture(_ context.Context, path string) ([]byte, error) {
	f, ok := embeddedEvidence(path)
	if !ok {
		return nil, os.ErrNotExist
	}
	return f, nil
}

func TestWatchEmbeddedReplay(t *testing.T) {
	w := Watch{Service{read: readEmbeddedFixture, config: func(context.Context) Settings { return Settings{} }},
		func(context.Context) string { return "" }}
	out, err := w.Replay(context.Background(), SignalInput{
		ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl, MetricKey: "orders_index",
		From: "2023-02-13", To: "2023-03-05", AsOf: "2023-03-03", SourcePath: "fixtures/synthetic-watch.json", ContextPath: "fixtures/synthetic-watch-context.json",
	})
	if err != nil || len(out.Observations) != 3 {
		t.Fatalf("embedded replay: %v", err)
	}
	for i, want := range []string{"false", "true", "null"} {
		got, _ := json.Marshal(out.Observations[i].Value)
		if string(got) != want {
			t.Fatalf("week %d: got %s; want %s", i, got, want)
		}
	}
}

func TestWatchAfterFixFixture(t *testing.T) {
	s := Service{read: readEmbeddedFixture,
		config: func(context.Context) Settings { return Settings{EvidencePath: "fixtures/synthetic-after-fix.json"} }}
	out, err := s.Compare(context.Background(), Compare{ProductKey: syntheticWatchProduct, MetricKeys: []string{"orders_index"},
		BaselineFrom: "2023-01-30", BaselineTo: "2023-02-26", CurrentFrom: "2023-02-27", CurrentTo: "2023-03-26"})
	if err != nil || out.Status != "ok" || len(out.Observations) != 4 || *out.Observations[1].Value <= *out.Observations[0].Value {
		t.Fatalf("synthetic follow-up compare: %v", err)
	}
}

func TestWatchAuthorizedCase(t *testing.T) {
	root := os.Getenv("AIFENCE_WATCH_CASE")
	if root == "" {
		t.Skip("private canary files not configured")
	}
	var spec struct {
		Signal                        SignalInput
		SpikeMonday, FirstAlertMonday string
		ReleaseWindow                 ContextWindow
		ReleaseIDs                    []string
		Backlog                       BacklogFilter
		Interview                     InterviewFilter
		Breaks                        MetricWindow
		Comparison                    Compare
		BaselineMean, CurrentMean     float64
	}
	raw, err := os.ReadFile(filepath.Join(root, "oracle.private.json"))
	if err != nil || json.Unmarshal(raw, &spec) != nil {
		t.Fatal("cannot read private oracles")
	}
	w := Watch{Service{read: func(_ context.Context, p string) ([]byte, error) { return os.ReadFile(filepath.Join(root, p)) },
		config: func(context.Context) Settings { return Settings{EvidencePath: "watch-series.json"} }},
		func(context.Context) string { return "watch-context.json" }}
	ctx := context.Background()
	result, err := w.Replay(ctx, spec.Signal)
	if err != nil {
		t.Fatalf("private replay failed: %v", err)
	}
	first := ""
	for _, row := range result.Observations {
		if row.Period.From == spec.SpikeMonday && (row.Value == nil || *row.Value) {
			t.Fatal("expected a quiet spike week")
		}
		if row.Value != nil && *row.Value && first == "" {
			first = row.Period.From
		}
	}
	if first != spec.FirstAlertMonday || result.Observations[len(result.Observations)-1].Value != nil {
		t.Fatal("first alert or incomplete week differs from oracle")
	}
	control := spec.Signal
	control.ProductKey, control.ControlProductKey = control.ControlProductKey, control.ProductKey
	swapped, err := w.Replay(ctx, control)
	if err != nil {
		t.Fatalf("private control replay failed: %v", err)
	}
	for _, row := range swapped.Observations {
		if row.Value != nil && *row.Value {
			t.Fatal("control emitted an alert")
		}
	}
	releases, err := w.Releases(ctx, spec.ReleaseWindow)
	if err != nil || len(releases.Observations) != len(spec.ReleaseIDs) {
		t.Fatalf("wrong release window: %v", err)
	}
	for i, r := range releases.Observations {
		if r.Details["release_id"] != spec.ReleaseIDs[i] {
			t.Fatal("unexpected release ID")
		}
	}
	tasks, err := w.Backlog(ctx, spec.Backlog)
	if err != nil || len(tasks.Observations) == 0 {
		t.Fatalf("task evidence unavailable: %v", err)
	}
	interviews, err := w.Interviews(ctx, spec.Interview)
	if err != nil || len(interviews.Observations) == 0 {
		t.Fatalf("interview evidence unavailable: %v", err)
	}
	breaks, err := w.Breaks(ctx, spec.Breaks)
	if err != nil || len(breaks.Observations) == 0 {
		t.Fatalf("collection break evidence unavailable: %v", err)
	}
	strict, err := w.series.Compare(ctx, spec.Comparison)
	if err != nil || len(strict.Observations) < 2 || strict.Observations[1].Value != nil {
		t.Fatalf("strict comparison accepted an incomplete week: %v", err)
	}
	spec.Comparison.IncludeIncomplete = true
	reported, err := w.series.Compare(ctx, spec.Comparison)
	if err != nil || len(reported.Observations) < 2 || reported.Observations[1].Status != "estimated" ||
		reported.Observations[0].Value == nil || reported.Observations[1].Value == nil ||
		math.Abs(*reported.Observations[0].Value-spec.BaselineMean) > .01 ||
		math.Abs(*reported.Observations[1].Value-spec.CurrentMean) > .01 {
		t.Fatalf("private reported comparison differs from oracle: %v", err)
	}
}

func TestWatchdogMagnitudeSameAcrossDetectReplayAndCollect(t *testing.T) {
	w := Watch{Service{read: readEmbeddedFixture, config: func(context.Context) Settings { return Settings{} }},
		func(context.Context) string { return "" }}
	in := SignalInput{
		ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl, MetricKey: "orders_index",
		From: "2023-02-13", To: "2023-03-05", AsOf: "2023-03-03",
		SourcePath: "fixtures/synthetic-watch.json", ContextPath: "fixtures/synthetic-watch-context.json",
	}
	ctx := t.Context()
	replay, err := w.Replay(ctx, in)
	if err != nil || len(replay.Observations) != 3 {
		t.Fatalf("replay: %v", err)
	}
	prior, alert, incomplete := replay.Observations[0], replay.Observations[1], replay.Observations[2]
	if prior.Value == nil || *prior.Value || alert.Value == nil || !*alert.Value || incomplete.Value != nil ||
		alert.Period != (Period{"2023-02-20", "2023-02-26"}) {
		t.Fatal("expected quiet, first alert, and incomplete week in chronological order")
	}
	if m := signalMagnitude(prior); m == nil || math.Abs(m.RelativeChangePct-(-9.375)) > 1e-12 {
		t.Fatal("prior week must not already cross the threshold")
	}
	if signalMagnitude(incomplete) != nil {
		t.Fatal("incomplete window produced a scale")
	}

	// Each window caps high weeks at 1.4 times its own median, then averages four weeks.
	want := WatchSignalMagnitude{
		Basis: "capped_four_week_relative_change", Unit: "percent", ValueUnit: "index", SpikeCapMedianMultiple: 1.4,
		Baseline: WatchMagnitudeWindow{Period: Period{"2023-01-02", "2023-01-29"}, CappedMean: (100.0 + 100 + 100 + 140) / 4},
		Current:  WatchMagnitudeWindow{Period: Period{"2023-01-30", "2023-02-26"}, CappedMean: (98.0 + 75 + 65 + 60) / 4},
	}
	want.RelativeChangePct = (want.Current.CappedMean/want.Baseline.CappedMean - 1) * 100

	detectInput := in
	detectInput.To = alert.Period.To
	detect, err := w.Detect(ctx, detectInput)
	if err != nil || len(detect.Observations) != 1 {
		t.Fatalf("detect: %v", err)
	}
	collected, err := w.CollectCase(ctx, WatchCaseInput{Series: in})
	if err != nil || collected.Document == nil || collected.FirstAlertWeek == nil || *collected.FirstAlertWeek != alert.Period {
		t.Fatalf("collect: %v", err)
	}
	caseSignal := collected.Document.Batches[0].Observations[1].(Signal)
	for surface, signal := range map[string]Signal{"Replay": alert, "Detect": detect.Observations[0], "Collect": caseSignal} {
		t.Run(surface, func(t *testing.T) {
			if signal.ID != alert.ID {
				t.Fatal("signal identity differs across entry points")
			}
			m := signalMagnitude(signal)
			if m == nil || *m != want || signal.Details["relative_change_pct"] != want.RelativeChangePct {
				t.Fatalf("magnitude = %+v, want %+v", m, want)
			}
			if signal.Details["control_baseline_mean"] != 90.0 || signal.Details["control_current_mean"] != 90.0 ||
				signal.Details["control_change_pct"] != 0.0 || signal.Details["control_adjusted_change_pct"] != want.RelativeChangePct {
				t.Fatal("expected unchanged control and equal target/adjusted window contrasts")
			}
			raw, ok := signal.Details["raw_week_comparison"].(watchRawWeekComparison)
			if !ok || raw.Basis != "adjacent_raw_weeks_diagnostic" || raw.Previous != (watchRawWeek{Period{"2023-02-13", "2023-02-19"}, 65}) ||
				raw.Latest != (watchRawWeek{Period{"2023-02-20", "2023-02-26"}, 60}) ||
				raw.RelativeChangePct == nil || math.Abs(*raw.RelativeChangePct-(60.0/65-1)*100) > 1e-12 {
				t.Fatal("adjacent raw weeks must be a separate, labelled diagnostic")
			}
			thresholds, ok := signal.Details["detection_thresholds"].(watchDetectionThresholds)
			if !ok || thresholds.MinimumTargetAndControlAdjustedDropPct != 20 || thresholds.MaxAbsoluteControlChangePct != 20 {
				t.Fatal("thresholds must be labelled separately from scale")
			}
			if _, exists := signal.Details["drop_pct"]; exists {
				t.Fatal("ambiguous flat drop_pct leaked into signal")
			}
		})
	}
	if collected.FirstAlertSignalID != alert.ID || collected.FirstAlertMagnitude == nil || *collected.FirstAlertMagnitude != want {
		t.Fatal("collect summary lost the first alert's canonical value")
	}
	wire, err := json.Marshal(collected)
	var decoded struct {
		FirstAlertSignalID  string               `json:"first_alert_signal_id"`
		FirstAlertMagnitude WatchSignalMagnitude `json:"first_alert_magnitude"`
	}
	if err != nil || json.Unmarshal(wire, &decoded) != nil || decoded.FirstAlertSignalID != alert.ID || decoded.FirstAlertMagnitude != want {
		t.Fatal("canonical signal ID and scale must survive serialization")
	}

	detectInput.DropPct = 40
	stricter, err := w.Detect(ctx, detectInput)
	if err != nil || len(stricter.Observations) != 1 {
		t.Fatalf("higher threshold detect: %v", err)
	}
	signal := stricter.Observations[0]
	if signal.ID != alert.ID || signal.Value == nil || *signal.Value || signalMagnitude(signal) == nil || *signalMagnitude(signal) != want {
		t.Fatal("changing the decision threshold changed the measured window contrast")
	}
}
