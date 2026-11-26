package main

import (
	"context"
	"fmt"
	"time"
)

func (s Service) compareWeekly(ctx context.Context, in Compare, path string, baseline, current Query) (Batch, error) {
	out := batch()
	src, err := s.historical(ctx, path)
	if err != nil {
		return out, err
	}
	out.AsOf = src.AsOf
	out.Warnings = append(out.Warnings, src.Warnings...)
	out.Warnings = append(out.Warnings, "Weekly means of the self-service orders index. Index points do not measure order count, revenue or profit.")
	left, err := weeklyAverage(src, baseline, path, in.IncludeIncomplete)
	if err != nil {
		return out, err
	}
	right, err := weeklyAverage(src, current, path, in.IncludeIncomplete)
	if err != nil {
		return out, err
	}
	out.Observations = append(out.Observations, left, right)
	estimated := left.Status == "estimated" || right.Status == "estimated"
	if left.Status == "estimated" {
		left.Status = "observed"
	}
	if right.Status == "estimated" {
		right.Status = "observed"
	}
	for _, delta := range deltas(left, right) {
		if estimated && delta.Status == "observed" {
			delta.Status = "estimated"
			delta.Caveats = append(delta.Caveats, "Includes an explicitly requested incomplete reporting week.")
		}
		out.Observations = append(out.Observations, delta)
	}
	if src.Status != "ok" || left.Value == nil || right.Value == nil || estimated {
		out.Status = "partial"
	}
	for _, delta := range out.Observations[2:] {
		if delta.Status != "observed" {
			out.Status = "partial"
		}
	}
	return out, nil
}

func weeklyAverage(src Batch, q Query, path string, includeIncomplete bool) (Observation, error) {
	p := Period{q.From, q.To}
	start, _ := time.Parse(time.DateOnly, p.From)
	end, _ := time.Parse(time.DateOnly, p.To)
	if start.Weekday() != time.Monday || end.Weekday() != time.Sunday || end.Sub(start).Hours()/24 >= 366 {
		return Observation{}, fmt.Errorf("orders_index windows must span complete calendar weeks (Monday to Sunday), up to one year")
	}
	o := missing(q, "orders_index", Ref{SourceID: "historical-weekly-index", Locator: path + "#" + p.From + ".." + p.To}, "No unique usable weekly observation for each week.")
	o.Unit = "index"
	links := []evidenceLink{}
	o.Details = map[string]any{}
	defer func() { o.Details["source_refs"] = links }()
	candidates := map[Period][]Observation{}
	for _, row := range src.Observations {
		if row.ProductKey == q.ProductKey && row.MetricKey == "orders_index" && chosen(row.Segment, "all") == q.Segment {
			candidates[row.Period] = append(candidates[row.Period], row)
		}
	}
	cutoff, _ := time.Parse(time.RFC3339, src.AsOf)
	through := cutoff.UTC().Format(time.DateOnly)
	count := 0
	weight := 7 / (end.Sub(start).Hours()/24 + 1)
	estimated, invalid := false, false
	sum := 0.0
	var rowKey, basePeriod string
	var synthetic, originKnown bool
	for d := start; !d.After(end); d = d.AddDate(0, 0, 7) {
		p := week(d)
		rows := candidates[p]
		for _, row := range rows {
			links = append(links, evidenceLink{row.ID, row.SourceRef})
		}
		if len(rows) != 1 {
			invalid = true
			continue
		}
		row := rows[0]
		issue := weeklyIssue(row, through)
		partial, _ := row.Details["partial_week"].(bool)
		if row.Unit != "index" || (issue != "" && !(includeIncomplete && issue == "incomplete_week" && partial &&
			row.Status == "observed" && row.Value != nil && *row.Value >= 0 && detailString(row.Details, "available_on") <= through)) {
			invalid = true
			continue
		}
		estimated = estimated || issue != ""
		mode, known := row.Details["synthetic"].(bool)
		if count == 0 {
			rowKey, basePeriod, synthetic, originKnown = row.ComparisonKey, detailString(row.Details, "base_period"), mode, known
		} else if rowKey != row.ComparisonKey || basePeriod != detailString(row.Details, "base_period") ||
			synthetic != mode || originKnown != known {
			invalid = true
		}
		sum += *row.Value * weight
		count++
	}
	o.Details["usable_weeks"] = count
	if invalid || count == 0 {
		o.Caveats = []string{"Window contains missing, incomplete, or incomparable weekly observations."}
		return o, nil
	}
	o.ComparisonKey = hash([]any{rowKey, basePeriod, synthetic, originKnown})
	if originKnown {
		o.Details["synthetic"] = synthetic
	}
	o.Value, o.Status, o.Caveats = num(sum), "observed", nil
	if estimated {
		o.Status = "estimated"
		o.Caveats = []string{"Mean includes an explicitly requested published value for an incomplete week."}
	}
	return o, nil
}
