package evidence

import (
	"encoding/json"
	"math"
	"time"
)

// WindowContrast is the producer's comparison of the target's two complete
// windows. Control means describe the same windows and are never the target delta.
type WindowContrast struct {
	ProducerMagnitude   bool    `json:"-"`
	Basis               string  `json:"basis"`
	Baseline            Period  `json:"baseline_period"`
	Current             Period  `json:"current_period"`
	WindowWeeks         int     `json:"window_weeks"`
	BaselineMean        float64 `json:"baseline_mean"`
	CurrentMean         float64 `json:"current_mean"`
	ControlBaselineMean float64 `json:"control_baseline_mean"`
	ControlCurrentMean  float64 `json:"control_current_mean"`
	RelativeChangePct   float64 `json:"relative_change_pct"`
}

const watchdogWindowBasis = "capped_four_week_relative_change"

type watchdogWindowData struct {
	Magnitude           json.RawMessage `json:"magnitude"`
	Baseline            Period          `json:"baseline_period"`
	Current             Period          `json:"current_period"`
	WindowWeeks         int             `json:"window_weeks"`
	BaselineMean        *float64        `json:"baseline_mean"`
	CurrentMean         *float64        `json:"current_mean"`
	ControlBaselineMean *float64        `json:"control_baseline_mean"`
	ControlCurrentMean  *float64        `json:"control_current_mean"`
	RelativeChangePct   *float64        `json:"relative_change_pct"`
}

func WatchdogWindow(row Row) *WindowContrast {
	if row.Source != "sales" || row.Metric != "watchdog.alert" || row.Status != "observed" {
		return nil
	}
	var d watchdogWindowData
	if json.Unmarshal([]byte(row.Details), &d) != nil {
		return nil
	}
	if d.Magnitude != nil && !d.useMagnitude() {
		return nil
	}
	if !d.matches(row.Period) {
		return nil
	}
	return &WindowContrast{
		ProducerMagnitude: d.Magnitude != nil, Basis: watchdogWindowBasis, Baseline: d.Baseline, Current: d.Current, WindowWeeks: d.WindowWeeks,
		BaselineMean: *d.BaselineMean, CurrentMean: *d.CurrentMean,
		ControlBaselineMean: *d.ControlBaselineMean, ControlCurrentMean: *d.ControlCurrentMean,
		RelativeChangePct: *d.RelativeChangePct,
	}
}

func (d *watchdogWindowData) useMagnitude() bool {
	type window struct {
		Period Period   `json:"period"`
		Mean   *float64 `json:"capped_mean"`
	}
	var m struct {
		Basis    string   `json:"basis"`
		Unit     string   `json:"unit"`
		Baseline window   `json:"baseline"`
		Current  window   `json:"current"`
		Change   *float64 `json:"relative_change_pct"`
	}
	if json.Unmarshal(d.Magnitude, &m) != nil || m.Basis != watchdogWindowBasis || m.Unit != "percent" ||
		m.Baseline.Period != d.Baseline || m.Current.Period != d.Current {
		return false
	}
	d.BaselineMean, d.CurrentMean, d.RelativeChangePct = m.Baseline.Mean, m.Current.Mean, m.Change
	return true
}

func (d *watchdogWindowData) matches(week Period) bool {
	if d.WindowWeeks == 0 {
		// Older watchdog snapshots emitted these same four-week windows
		// without the window_weeks field.
		d.WindowWeeks = 4
	}
	if d.WindowWeeks != 4 || d.BaselineMean == nil || d.CurrentMean == nil ||
		d.ControlBaselineMean == nil || d.ControlCurrentMean == nil || d.RelativeChangePct == nil {
		return false
	}
	if *d.BaselineMean <= 0 || *d.CurrentMean < 0 || *d.ControlBaselineMean <= 0 || *d.ControlCurrentMean <= 0 {
		return false
	}
	start, err := time.Parse(time.DateOnly, d.Current.From)
	if err != nil || start.Weekday() != time.Monday {
		return false
	}
	end := start.AddDate(0, 0, 7*d.WindowWeeks-1)
	if d.Baseline.From != start.AddDate(0, 0, -7*d.WindowWeeks).Format(time.DateOnly) ||
		d.Baseline.To != start.AddDate(0, 0, -1).Format(time.DateOnly) ||
		d.Current.To != end.Format(time.DateOnly) ||
		week.To != d.Current.To || week.From != end.AddDate(0, 0, -6).Format(time.DateOnly) {
		return false
	}
	want := (*d.CurrentMean / *d.BaselineMean - 1) * 100
	return math.Abs(*d.RelativeChangePct-want) <= 1e-8
}

// Project the verified values into legacy read fields as well so older clients
// cannot quote a stale delta. The imported document stays immutable.
func projectWatchdogWindow(row Row) Row {
	if row.Source != "sales" || row.Metric != "watchdog.alert" {
		return row
	}
	row.WindowContrast = WatchdogWindow(row)
	var details map[string]json.RawMessage
	if json.Unmarshal([]byte(row.Details), &details) != nil || details == nil {
		return row
	}
	if c := row.WindowContrast; c != nil {
		for field, value := range map[string]float64{
			"baseline_mean": c.BaselineMean, "current_mean": c.CurrentMean, "relative_change_pct": c.RelativeChangePct,
		} {
			details[field], _ = json.Marshal(value)
		}
	} else {
		delete(details, "magnitude")
		delete(details, "relative_change_pct")
		delete(details, "baseline_mean")
		delete(details, "current_mean")
	}
	data, _ := json.Marshal(details)
	row.Details = string(data)
	return row
}
