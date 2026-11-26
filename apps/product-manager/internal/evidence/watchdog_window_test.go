package evidence

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestWatchdogWindowUsesProducerMagnitude(t *testing.T) {
	const old = `{
		"baseline_period":{"from":"2025-01-06","to":"2025-02-02"},
		"current_period":{"from":"2025-02-03","to":"2025-03-02"},
		"baseline_mean":125,"current_mean":85,"control_baseline_mean":100,"control_current_mean":90,
		"relative_change_pct":-20,"control_adjusted_change_pct":-24,
		"raw_week_comparison":{"relative_change_pct":-10},
		"detection_thresholds":{"minimum_target_and_control_adjusted_drop_pct":20}
	}`
	row := Row{Source: "sales", Metric: "watchdog.alert", Status: "observed", Value: "true",
		Period: Period{From: "2025-02-24", To: "2025-03-02"}, Details: old}
	if result := projectWatchdogWindow(row); result.WindowContrast != nil || strings.Contains(result.Details, `"relative_change_pct":-20`) {
		t.Fatal("a legacy delta inconsistent with the windows was exposed")
	}

	const magnitude = `"magnitude":{
		"basis":"capped_four_week_relative_change","unit":"percent","relative_change_pct":-32,
		"baseline":{"period":{"from":"2025-01-06","to":"2025-02-02"},"capped_mean":125},
		"current":{"period":{"from":"2025-02-03","to":"2025-03-02"},"capped_mean":85}
	}`
	row.Details = strings.TrimSuffix(old, "}") + "," + magnitude + "}"
	result := projectWatchdogWindow(row)
	got := result.WindowContrast
	if got == nil || got.RelativeChangePct != -32 || got.WindowWeeks != 4 || got.BaselineMean != 125 || got.CurrentMean != 85 ||
		got.ControlBaselineMean != 100 || got.ControlCurrentMean != 90 {
		t.Fatalf("unexpected contrast: %+v", got)
	}
	var details map[string]json.RawMessage
	if err := json.Unmarshal([]byte(result.Details), &details); err != nil ||
		string(details["relative_change_pct"]) != "-32" || string(details["control_adjusted_change_pct"]) != "-24" {
		t.Fatalf("legacy view failed to separate the target and adjusted deltas: %s", result.Details)
	}

	row.Details = strings.Replace(old, `"relative_change_pct":-20`, `"relative_change_pct":-32`, 1)
	if got := WatchdogWindow(row); got == nil || got.RelativeChangePct != -32 {
		t.Fatalf("coherent legacy snapshots must continue to read: %+v", got)
	}
	row.Details = strings.TrimSuffix(row.Details, "}") + `,"magnitude":null}`
	if WatchdogWindow(row) != nil {
		t.Fatal("invalid explicit producer magnitude fell back to an unrelated field")
	}
}
