package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestCollectWatchdogCase(t *testing.T) {
	w := Watch{Service{read: readEmbeddedFixture, config: func(context.Context) Settings { return Settings{} }},
		func(context.Context) string { return "" }}
	input := WatchCaseInput{
		Series: SignalInput{ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl, MetricKey: "orders_index",
			From: "2023-02-13", To: "2023-03-05", AsOf: "2023-03-03",
			SourcePath: "fixtures/synthetic-watch.json", ContextPath: "fixtures/synthetic-watch-context.json"},
		Keywords: []string{"called"},
	}
	got, err := w.CollectCase(t.Context(), input)
	if err != nil || got.Status != "alert" || got.FirstAlertWeek == nil || got.FirstAlertWeek.From != "2023-02-20" || got.Document == nil {
		t.Fatalf("first alert: %v", err)
	}
	assertTimeline := func(result WatchCaseResult, want string) {
		t.Helper()
		b, err := json.Marshal(result.Timeline)
		if err != nil || string(b) != want {
			t.Fatalf("timeline for %s: got %s, want %s (%v)", result.Status, b, want, err)
		}
	}
	const quietWeek = `{"week":"2023-02-13","alert":false,"reason":"threshold_not_met"}`
	const alertWeek = `{"week":"2023-02-20","alert":true,"reason":"target_drop_with_stable_control"}`
	const incompleteWeek = `{"week":"2023-02-27","alert":null,"reason":"incomplete_week"}`
	assertTimeline(got, "["+quietWeek+","+alertWeek+","+incompleteWeek+"]")
	doc := got.Document
	if doc.SchemaVersion != "aifence.case.v1" || doc.SourcePolicy != "" || len(doc.Batches) != 5 {
		t.Fatal("expected open synthetic case with all five source statuses")
	}
	for i, source := range []string{"sales", "release", "backlog", "metrika", "research"} {
		batch := doc.Batches[i]
		if batch.Source != source || !validBatchStatus(batch.Status) || len(batch.Observations) == 0 {
			t.Errorf("missing evidence for %s", source)
		}
	}
	sales := doc.Batches[0].Observations
	if len(sales) != 3 || *sales[0].(Signal).Value || !*sales[1].(Signal).Value || sales[2].(Signal).Value != nil {
		t.Fatal("case did not retain the full replay")
	}
	open, err := json.Marshal(doc)
	if err != nil || strings.Contains(string(open), "source_policy") || !strings.Contains(string(open), `"value":true`) {
		t.Fatalf("incorrect PM wire shape: %v", err)
	}

	quiet := input
	quiet.Series.To = "2023-02-19"
	quietResult, err := w.CollectCase(t.Context(), quiet)
	if err != nil || quietResult.Status != "quiet" || quietResult.Document != nil || quietResult.FirstAlertWeek != nil {
		t.Fatalf("quiet replay must not produce a case: %v", err)
	}
	assertTimeline(quietResult, "["+quietWeek+"]")
	openWeek := input
	openWeek.Series.From = "2023-02-27"
	incomplete, err := w.CollectCase(t.Context(), openWeek)
	if err != nil || incomplete.Status != "incomplete" || incomplete.Document != nil || incomplete.FirstAlertWeek != nil {
		t.Fatalf("incomplete replay must not produce a case: %v", err)
	}
	assertTimeline(incomplete, "["+incompleteWeek+"]")
}

func TestCollectWatchdogCaseSourcePolicy(t *testing.T) {
	decline := []float64{100, 100, 100, 100, 60, 60, 60, 60}
	control := []float64{100, 100, 100, 100, 100, 100, 100, 100}
	in := WatchCaseInput{Series: SignalInput{ProductKey: syntheticWatchProduct, ControlProductKey: syntheticWatchControl,
		MetricKey: "orders_index", From: "2034-02-20", To: "2034-02-26"}, Keywords: []string{"example"}}
	t.Run("historical", func(t *testing.T) {
		w := testWatch(t, decline, control, func(row *Observation) { row.Details["synthetic"] = false })
		w.contextPath = func(context.Context) string { return "" }
		got, err := w.CollectCase(t.Context(), in)
		if err != nil || got.Document == nil || got.Document.SourcePolicy != "local_project_only" {
			t.Fatalf("historical source policy: %v", err)
		}
	})
	t.Run("mixed context", func(t *testing.T) {
		text := "example historical interview"
		row := ContextFact{ID: "interview", Kind: "research", ProductKey: "all", MetricKey: "interview_excerpt",
			Value: &text, Unit: "text", Status: "observed", SourceRef: Ref{SourceID: "test", Locator: "Interview!B2"},
			Period: Period{"2034-02-24", "2034-02-24"}, Details: map[string]any{"synthetic": false}}
		future := row
		future.ID, future.Period = "future", Period{"2034-03-01", "2034-03-01"}
		w := testWatch(t, decline, control, nil, row, future)
		got, err := w.CollectCase(t.Context(), in)
		if err != nil || got.Document == nil || got.Document.SourcePolicy != "local_project_only" {
			t.Fatalf("mixed source policy: %v", err)
		}
		if research := got.Document.Batches[4]; len(research.Observations) != 1 ||
			research.Observations[0].(ContextFact).ID != row.ID {
			t.Fatal("interviews must be in the first alert window")
		}
	})
}

func TestCollectWatchdogCaseManifest(t *testing.T) {
	for _, tool := range newAgent().Manifest().Tools {
		if tool.Name == "collect_watchdog_case" && strings.Contains(string(tool.InputSchema), "context_metrics") &&
			strings.Contains(string(tool.OutputSchema), "first_alert_week") && strings.Contains(string(tool.OutputSchema), "timeline") {
			return
		}
	}
	t.Fatal("missing typed collector manifest")
}
