package main

import (
	"sort"
	"time"
)

type blindCauseSourceRow struct {
	ID        string  `json:"id"`
	Segment   string  `json:"segment"`
	Period    Period  `json:"period"`
	Value     float64 `json:"value"`
	SourceRef Ref     `json:"source_ref"`
}

type blindCauseSourceWindow struct {
	Period     Period                `json:"period"`
	WeekCount  int                   `json:"week_count"`
	Total      float64               `json:"total"`
	WeeklyMean float64               `json:"weekly_mean"`
	Rows       []blindCauseSourceRow `json:"rows"`
}

type blindCauseMetricProvenance struct {
	MetricKey string                 `json:"metric_key"`
	Segment   string                 `json:"segment"`
	Unit      string                 `json:"unit"`
	Baseline  blindCauseSourceWindow `json:"baseline"`
	Current   blindCauseSourceWindow `json:"current"`
}

// The frozen scan can label a candidate "all" while its measured evidence
// covers one device. Resolve the scope and totals of each measured series.
func (d blindDataset) blindCauseMeasurementProvenance(c BlindCandidate) ([]blindCauseMetricProvenance, []string) {
	if c.CauseType != "masked_multi_cause_regression" {
		return nil, nil
	}
	metrics := map[string]BlindCauseMetric{}
	for _, m := range c.CauseEvidence {
		if m.Segment != "" && (m.MetricKey == "checkouts" || m.MetricKey == "visits") {
			metrics[m.MetricKey] = m
		}
	}
	if len(metrics) == 0 {
		return nil, nil
	}

	cutoff := blindCutoff(d.series.AsOf)
	funnels, weeks := causeFunnels(d.measures(), cutoff)
	f := funnels[c.ProductKey]
	k := sort.SearchStrings(weeks, c.Period.From)
	if f == nil || k == 0 || k == len(weeks) || weeks[k] != c.Period.From {
		return nil, []string{"The aggregate measurement windows could not be resolved in the selected snapshot."}
	}
	refs := d.blindCauseSourceRows()
	segments := []string{}
	for segment := range f.segments {
		if segment != "all" {
			segments = append(segments, segment)
		}
	}
	sort.Strings(segments)

	var proofs []blindCauseMetricProvenance
	var warnings []string
	for _, key := range []string{"checkouts", "visits"} {
		m, exists := metrics[key]
		if !exists {
			continue
		}
		before, beforeOK := resolveBlindCauseWindow(f, key, m.Segment, cutoff, weeks[max(0, k-causeBaseWeeks):k], segments, refs)
		after, afterOK := resolveBlindCauseWindow(f, key, m.Segment, cutoff, weeks[k:], segments, refs)
		if !beforeOK || !afterOK || before.WeeklyMean <= 0 || round2(before.WeeklyMean) != m.Before ||
			round2(after.WeeklyMean) != m.After || round2((after.WeeklyMean/before.WeeklyMean-1)*100) != m.ChangePct {
			warnings = append(warnings, "The original rows could not verify the published values for "+key+" ("+m.Segment+").")
			continue
		}
		proofs = append(proofs, blindCauseMetricProvenance{MetricKey: key, Segment: m.Segment, Unit: "count", Baseline: before, Current: after})
	}
	return proofs, warnings
}

func resolveBlindCauseWindow(f *causeFunnel, metric, scope, cutoff string, weeks, components []string, refs map[Ref]*Observation) (blindCauseSourceWindow, bool) {
	var result blindCauseSourceWindow
	for _, day := range weeks {
		expected, present := f.segments[scope][metric][day]
		if !present {
			continue
		}
		segments := []string{scope}
		if _, explicit := f.refs[scope+"/"+metric+"/"+day]; scope == "all" && !explicit {
			segments = components
		}
		if len(segments) == 0 {
			return blindCauseSourceWindow{}, false
		}
		at, _ := time.Parse(time.DateOnly, day)
		period := week(at)
		total := 0.0
		for _, segment := range segments {
			ref := f.refs[segment+"/"+metric+"/"+day]
			row := refs[ref]
			value, used := f.segments[segment][metric][day]
			if !used || row == nil || ref.SourceID == "" || ref.Locator == "" || row.ProductKey != f.product ||
				row.MetricKey != metric || chosen(row.Segment, "all") != segment || row.Unit != "count" ||
				row.Period != period || weeklyIssue(*row, cutoff) != "" || *row.Value != value {
				return blindCauseSourceWindow{}, false
			}
			total += *row.Value
			result.Rows = append(result.Rows, blindCauseSourceRow{ID: row.ID, Segment: segment, Period: period, Value: *row.Value, SourceRef: ref})
		}
		if total != expected {
			return blindCauseSourceWindow{}, false
		}
		result.Period.From = chosen(result.Period.From, period.From)
		result.Period.To = period.To
		result.Total += total
		result.WeekCount++
	}
	if result.WeekCount == 0 {
		return blindCauseSourceWindow{}, false
	}
	result.WeeklyMean = result.Total / float64(result.WeekCount)
	return result, true
}

func (d blindDataset) blindCauseSourceRows() map[Ref]*Observation {
	refs := make(map[Ref]*Observation, len(d.series.Observations))
	for i := range d.series.Observations {
		row := &d.series.Observations[i]
		if _, exists := refs[row.SourceRef]; exists {
			refs[row.SourceRef] = nil
		} else {
			refs[row.SourceRef] = row
		}
	}
	return refs
}

type blindCheckoutRatioWindow struct {
	Period    Period  `json:"period"`
	Checkouts float64 `json:"checkouts"`
	Visits    float64 `json:"visits"`
	Rate      float64 `json:"rate_pct"`
}

func blindCheckoutRatio(checkouts, visits blindCauseMetricProvenance) map[string]any {
	if checkouts.Segment != visits.Segment {
		return nil
	}
	paired := func(c, v blindCauseSourceWindow) (blindCheckoutRatioWindow, bool) {
		if c.Period != v.Period || c.WeekCount != v.WeekCount || len(c.Rows) != len(v.Rows) || v.Total <= 0 {
			return blindCheckoutRatioWindow{}, false
		}
		for i := range c.Rows {
			if c.Rows[i].Period != v.Rows[i].Period || c.Rows[i].Segment != v.Rows[i].Segment {
				return blindCheckoutRatioWindow{}, false
			}
		}
		return blindCheckoutRatioWindow{c.Period, c.Total, v.Total, 100 * c.Total / v.Total}, true
	}
	base, baseOK := paired(checkouts.Baseline, visits.Baseline)
	current, currentOK := paired(checkouts.Current, visits.Current)
	if !baseOK || !currentOK || base.Rate <= 0 {
		return nil
	}
	return map[string]any{
		"unit": "percent", "baseline": base, "current": current,
		"relative_change_pct": (current.Rate/base.Rate - 1) * 100,
	}
}

type blindMigrationSourceWeek struct {
	ProductKey string                `json:"product_key"`
	Period     Period                `json:"period"`
	Total      float64               `json:"total"`
	Rows       []blindCauseSourceRow `json:"rows"`
}

// The published migration metrics use multiweek means. The native locators
// can also support a distinct boundary-week reading when both keys and
// matching segments resolve to complete observed weeks.
func (d blindDataset) blindMigrationSourceOnset(c BlindCandidate) map[string]any {
	at, err := time.Parse(time.DateOnly, c.Period.From)
	if err != nil || week(at) != c.Period {
		return nil
	}
	old := blindMigrationSourceWeek{ProductKey: c.ProductKey, Period: week(at.AddDate(0, 0, -7))}
	next := blindMigrationSourceWeek{Period: c.Period}
	cutoff := blindCutoff(d.series.AsOf)
	refs := d.blindCauseSourceRows()
	seen := map[Period]map[string]Ref{old.Period: {}, next.Period: {}}
	for _, ref := range c.SourceRefs {
		if c.CauseRelease != nil && ref == c.CauseRelease.SourceRef {
			continue
		}
		row := refs[ref]
		if row == nil || ref.SourceID == "" || ref.Locator == "" || row.MetricKey != "orders" || row.Unit != "count" || weeklyIssue(*row, cutoff) != "" {
			return nil
		}
		w := &old
		switch row.Period {
		case old.Period:
		case next.Period:
			w = &next
			if w.ProductKey == "" {
				w.ProductKey = row.ProductKey
			}
		default:
			return nil
		}
		segment := chosen(row.Segment, "all")
		if row.ProductKey != w.ProductKey || seen[row.Period][segment].SourceID != "" {
			return nil
		}
		seen[row.Period][segment] = ref
		w.Total += *row.Value
		w.Rows = append(w.Rows, blindCauseSourceRow{ID: row.ID, Segment: segment, Period: row.Period, Value: *row.Value, SourceRef: ref})
	}
	if next.ProductKey == "" || next.ProductKey == old.ProductKey || len(old.Rows) == 0 || len(old.Rows) != len(next.Rows) {
		return nil
	}
	for _, row := range old.Rows {
		if seen[next.Period][row.Segment].SourceID == "" {
			return nil
		}
	}
	for _, w := range []blindMigrationSourceWeek{old, next} {
		if len(w.Rows) > 1 && seen[w.Period]["all"].SourceID != "" {
			return nil
		}
	}
	// Verify the labels against the actual extract, including segments not
	// cited by the classifier, before calling these the last and first weeks.
	for _, row := range d.series.Observations {
		if row.MetricKey != "orders" || row.Unit != "count" || weeklyIssue(row, cutoff) != "" {
			continue
		}
		at, err := time.Parse(time.DateOnly, row.Period.From)
		if err != nil || at.Weekday() != time.Monday || week(at) != row.Period {
			continue
		}
		var w blindMigrationSourceWeek
		switch row.ProductKey {
		case old.ProductKey:
			w = old
			if row.Period.From > old.Period.From {
				return nil
			}
		case next.ProductKey:
			w = next
			if row.Period.From < next.Period.From {
				return nil
			}
		default:
			continue
		}
		if row.Period == w.Period && seen[w.Period][chosen(row.Segment, "all")] != row.SourceRef {
			return nil
		}
	}
	return map[string]any{
		"metric_key": "orders", "unit": "count", "old_key_last_week": old, "new_key_first_week": next,
	}
}
