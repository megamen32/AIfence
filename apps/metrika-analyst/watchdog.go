package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	watchMetric        = "watchdog.alert"
	watchWeeks         = 4
	watchDropPct       = 20
	watchControlMaxPct = 20
)

type SignalBatch = EvidenceBatch[bool]
type Signal = EvidenceObservation[bool]

type SignalInput struct {
	ProductKey        string  `json:"product_key"`
	ControlProductKey string  `json:"control_product_key"`
	MetricKey         string  `json:"metric_key"`
	From              string  `json:"from"`
	To                string  `json:"to"`
	AsOf              string  `json:"as_of,omitempty"`
	Segment           string  `json:"segment,omitempty"`
	SourcePath        string  `json:"source_path,omitempty"`
	ContextPath       string  `json:"context_path,omitempty"`
	ScenarioID        string  `json:"scenario_id,omitempty"`
	DropPct           float64 `json:"drop_pct,omitempty"`
	ControlMaxPct     float64 `json:"control_max_pct,omitempty"`
}

type seriesKey struct {
	product string
	monday  string
}

type evidenceLink struct {
	ID        string `json:"id"`
	SourceRef Ref    `json:"source_ref"`
}

func (w Watch) Detect(ctx context.Context, in SignalInput) (SignalBatch, error) {
	return w.run(ctx, in, false)
}

func (w Watch) Replay(ctx context.Context, in SignalInput) (SignalBatch, error) {
	return w.run(ctx, in, true)
}

func monday(d time.Time) time.Time {
	return d.AddDate(0, 0, -(int(d.Weekday())+6)%7)
}

func week(d time.Time) Period {
	return Period{d.Format(time.DateOnly), d.AddDate(0, 0, 6).Format(time.DateOnly)}
}

func (w Watch) run(ctx context.Context, in SignalInput, replay bool) (SignalBatch, error) {
	out := SignalBatch{"aifence.v1", "sales", stamp(), "ok", []Signal{}, []string{}}
	length, err := days(Period{in.From, in.To})
	if err != nil || length > 366*2 {
		return out, fmt.Errorf("select an inclusive period of at most two years")
	}
	if in.ProductKey == "" || in.ControlProductKey == "" || in.ProductKey == in.ControlProductKey || in.MetricKey == "" {
		return out, fmt.Errorf("set a metric and distinct target and control product keys")
	}
	if in.DropPct == 0 {
		in.DropPct = watchDropPct
	}
	if in.ControlMaxPct == 0 {
		in.ControlMaxPct = watchControlMaxPct
	}
	for _, pct := range []float64{in.DropPct, in.ControlMaxPct} {
		if pct <= 0 || pct >= 100 || math.IsNaN(pct) {
			return out, fmt.Errorf("thresholds must be between 0 and 100 percent")
		}
	}
	if in.Segment == "" {
		in.Segment = "all"
		if in.MetricKey == "orders_index" {
			in.Segment = "self_service"
		}
	}
	path := chosen(in.SourcePath, w.series.config(ctx).EvidencePath)
	if path == "" {
		out.Status = "unavailable"
		out.Warnings = append(out.Warnings, "Configure evidence_path or select an authorized historical weekly file.")
		return out, nil
	}
	src, err := w.series.historical(ctx, path)
	if err != nil {
		return out, err
	}
	out.AsOf = src.AsOf
	out.Warnings = append(out.Warnings, src.Warnings...)
	snapshot, _ := time.Parse(time.RFC3339, src.AsOf)
	cutoff := snapshot.UTC().Format(time.DateOnly)
	if in.AsOf != "" {
		if _, err := time.Parse(time.DateOnly, in.AsOf); err != nil {
			return out, fmt.Errorf("as_of must be an ISO date")
		}
		cutoff = min(cutoff, in.AsOf)
	}
	cutoff = min(cutoff, in.To)

	rows := map[seriesKey][]Observation{}
	var synthetic, imported, unknown bool
	for _, row := range src.Observations {
		if row.MetricKey != in.MetricKey || chosen(row.Segment, "all") != in.Segment ||
			(row.ProductKey != in.ProductKey && row.ProductKey != in.ControlProductKey) ||
			(in.ScenarioID != "" && detailString(row.Details, "scenario_id") != in.ScenarioID) {
			continue
		}
		mode, known := row.Details["synthetic"].(bool)
		synthetic = synthetic || known && mode
		imported = imported || known && !mode
		unknown = unknown || !known
		date, _ := time.Parse(time.DateOnly, row.Period.From)
		key := seriesKey{row.ProductKey, monday(date).Format(time.DateOnly)}
		rows[key] = append(rows[key], row)
	}
	if synthetic && (imported || unknown) {
		return out, fmt.Errorf("select a single data origin and scenario")
	}
	origin := "unknown"
	if synthetic {
		origin = "synthetic_fixture"
	} else if imported && !unknown {
		origin = "imported_historical"
	}

	var breaks []ContextFact
	contextPath := chosen(in.ContextPath, w.contextPath(ctx))
	if contextPath != "" {
		catalog, err := w.loadContext(ctx, contextPath)
		if err != nil {
			return out, err
		}
		if synthetic != *catalog.Synthetic {
			// Explicitly selected fixture series must stay independent of the default private registry.
			if in.SourcePath == "" || in.ContextPath != "" {
				return out, fmt.Errorf("weekly series and break registry use different origins")
			}
			out.Warnings = append(out.Warnings, "Default break registry has a different origin; using weekly source flags only.")
		} else if registry, ok := catalog.Batches["metrika"]; ok && registry.Status == "ok" {
			breaks = registry.Observations
		} else {
			out.Warnings = append(out.Warnings, "Break registry unavailable or incomplete; using weekly source flags only.")
		}
	} else {
		out.Warnings = append(out.Warnings, "No external break registry configured; using weekly source flags only.")
	}

	start, _ := time.Parse(time.DateOnly, in.From)
	end, _ := time.Parse(time.DateOnly, in.To)
	if !replay {
		start = end
	}
	for d := monday(start); !d.After(end); d = d.AddDate(0, 0, 7) {
		signal := evaluateWeek(in, d, cutoff, path, origin, rows, breaks)
		out.Observations = append(out.Observations, signal)
		if signal.Value == nil {
			out.Status = "partial"
		}
	}
	return out, nil
}

func evaluateWeek(in SignalInput, d time.Time, cutoff, path, origin string, rows map[seriesKey][]Observation, breaks []ContextFact) Signal {
	p := week(d)
	baselineFrom := d.AddDate(0, 0, -(2*watchWeeks-1)*7)
	currentFrom := d.AddDate(0, 0, -(watchWeeks-1)*7)
	baseline := Period{week(baselineFrom).From, currentFrom.AddDate(0, 0, -1).Format(time.DateOnly)}
	current := Period{week(currentFrom).From, p.To}
	id := "watchdog/" + hash([]string{in.ProductKey, in.ControlProductKey, in.MetricKey, in.Segment, p.From, origin, in.ScenarioID})
	ids := []string{}
	refs := []evidenceLink{}
	seen := map[string]bool{}
	add := func(id string, ref Ref) {
		if !seen[id] {
			ids = append(ids, id)
			refs = append(refs, evidenceLink{id, ref})
			seen[id] = true
		}
	}
	details := map[string]any{
		"control_product_key": in.ControlProductKey, "metric_key": in.MetricKey,
		"baseline_period": baseline, "current_period": current, "window_weeks": watchWeeks,
		"drop_pct": in.DropPct, "control_max_pct": in.ControlMaxPct, "spike_cap_median_multiple": spikeCap,
		"origin": origin, "evidence_direction": "unknown",
	}
	if in.ScenarioID != "" {
		details["scenario_id"] = in.ScenarioID
	}
	out := Signal{ID: id, Kind: "metric", ProductKey: in.ProductKey, MetricKey: watchMetric,
		Unit: "boolean", Period: p, Segment: in.Segment, Status: "missing", Details: details,
		SourceRef: Ref{SourceID: "aifence-watchdog", Locator: path + "#" + p.From}}
	finish := func(reason string) Signal {
		details["reason"], details["evidence_ids"], details["source_refs"] = reason, ids, refs
		return out
	}
	var target, control []float64
	reason := ""
	if p.To > cutoff {
		reason = "incomplete_week"
	}
	for _, fact := range breaks {
		if fact.MetricKey != "data_break" || !metricMatch(fact, []string{in.MetricKey}) ||
			!overlaps(fact.Period, Period{baseline.From, current.To}) ||
			(!matchesProduct(fact.ProductKey, in.ProductKey) && !matchesProduct(fact.ProductKey, in.ControlProductKey)) ||
			(fact.Segment != "" && fact.Segment != "all" && fact.Segment != in.Segment) ||
			detailString(fact.Details, "known_from") > min(cutoff, p.To) {
			continue
		}
		effect := detailString(fact.Details, "effect")
		if effect == "boundary" && baseline.From >= fact.Period.From {
			continue
		}
		add(fact.ID, fact.SourceRef)
		if reason == "" {
			reason = "collection_break"
			out.Status = "incomparable"
		}
	}
	var unit, currency, bases, comparability [2]string
	for t := baselineFrom; !t.After(d); t = t.AddDate(0, 0, 7) {
		window := week(t)
		for i, key := range []string{in.ProductKey, in.ControlProductKey} {
			candidates := rows[seriesKey{key, window.From}]
			var exact []Observation
			for _, row := range candidates {
				availableOn := detailString(row.Details, "available_on")
				if availableOn == "" {
					availableOn = row.Period.To
				}
				if availableOn <= min(cutoff, p.To) && row.Period.To <= p.To {
					add(row.ID, row.SourceRef)
					if row.Period == window {
						exact = append(exact, row)
					}
				}
			}
			if len(exact) != 1 {
				if reason == "" {
					reason = "missing_or_duplicate_week"
				}
				continue
			}
			row := exact[0]
			if issue := weeklyIssue(row, cutoff); issue != "" {
				if reason == "" {
					reason = issue
				}
				continue
			}
			base := detailString(row.Details, "base_period")
			if unit[i] != "" && (unit[i] != row.Unit || currency[i] != row.Currency || bases[i] != base || comparability[i] != row.ComparisonKey) {
				if reason == "" {
					reason = "incompatible_series"
				}
			}
			unit[i], currency[i], bases[i], comparability[i] = row.Unit, row.Currency, base, row.ComparisonKey
			if i == 0 {
				target = append(target, *row.Value)
			} else {
				control = append(control, *row.Value)
			}
		}
	}
	if reason != "" {
		return finish(reason)
	}
	if unit[0] != unit[1] || currency[0] != currency[1] || bases[0] != bases[1] || comparability[0] != comparability[1] {
		out.Status = "incomparable"
		return finish("incompatible_control")
	}

	targetBase, targetNow := cappedMean(target[:watchWeeks]), cappedMean(target[watchWeeks:])
	controlBase, controlNow := cappedMean(control[:watchWeeks]), cappedMean(control[watchWeeks:])
	details["baseline_mean"], details["current_mean"] = targetBase, targetNow
	details["control_baseline_mean"], details["control_current_mean"] = controlBase, controlNow
	if targetBase <= 0 || controlBase <= 0 || controlNow <= 0 {
		return finish("zero_reference")
	}
	targetRatio, controlRatio := targetNow/targetBase, controlNow/controlBase
	targetPct, controlPct := (targetRatio-1)*100, (controlRatio-1)*100
	adjusted := (targetRatio/controlRatio - 1) * 100
	if math.IsInf(adjusted, 0) || math.IsNaN(adjusted) || math.IsInf(targetPct, 0) || math.IsInf(controlPct, 0) {
		return finish("unusable_scale")
	}
	details["relative_change_pct"], details["control_change_pct"], details["control_adjusted_change_pct"] = targetPct, controlPct, adjusted
	if math.Abs(controlPct) > in.ControlMaxPct {
		out.Status = "incomparable"
		return finish("unstable_control")
	}
	alert := targetPct <= -in.DropPct && adjusted <= -in.DropPct
	out.Value, out.Status = &alert, "observed"
	if alert {
		return finish("target_drop_with_stable_control")
	}
	return finish("threshold_not_met")
}

// weeklyIssue rejects period flags even when a nominal Sunday has passed.
func weeklyIssue(row Observation, cutoff string) string {
	partial, _ := row.Details["partial_week"].(bool)
	if partial || row.Period.To > cutoff {
		return "incomplete_week"
	}
	if row.Status != "observed" || row.Value == nil || *row.Value < 0 {
		return "unusable_week"
	}
	if date := detailString(row.Details, "available_on"); date > cutoff {
		return "week_not_yet_available"
	}
	return ""
}

const spikeCap = 1.4

func cappedMean(values []float64) float64 {
	ordered := append([]float64{}, values...)
	sort.Float64s(ordered)
	median := ordered[len(ordered)/2-1]/2 + ordered[len(ordered)/2]/2
	sum := 0.0
	for _, value := range values {
		sum += min(value, spikeCap*median) / float64(len(values))
	}
	return sum
}
