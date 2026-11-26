package main

const watchMagnitudeBasis = "capped_four_week_relative_change"

// WatchSignalMagnitude is the change to quote for a watchdog signal.
type WatchSignalMagnitude struct {
	Basis                  string               `json:"basis"`
	Unit                   string               `json:"unit"`
	RelativeChangePct      float64              `json:"relative_change_pct"`
	Baseline               WatchMagnitudeWindow `json:"baseline"`
	Current                WatchMagnitudeWindow `json:"current"`
	ValueUnit              string               `json:"value_unit"`
	SpikeCapMedianMultiple float64              `json:"spike_cap_median_multiple"`
}

type WatchMagnitudeWindow struct {
	Period     Period  `json:"period"`
	CappedMean float64 `json:"capped_mean"`
}

type watchDetectionThresholds struct {
	MinimumTargetAndControlAdjustedDropPct float64 `json:"minimum_target_and_control_adjusted_drop_pct"`
	MaxAbsoluteControlChangePct            float64 `json:"max_absolute_control_change_pct"`
}

type watchRawWeek struct {
	Period Period  `json:"period"`
	Value  float64 `json:"value"`
}

type watchRawWeekComparison struct {
	Basis             string       `json:"basis"`
	Previous          watchRawWeek `json:"previous"`
	Latest            watchRawWeek `json:"latest"`
	ValueUnit         string       `json:"value_unit"`
	RelativeChangePct *float64     `json:"relative_change_pct"`
}

func signalMagnitude(signal Signal) *WatchSignalMagnitude {
	magnitude, _ := signal.Details["magnitude"].(*WatchSignalMagnitude)
	return magnitude
}
