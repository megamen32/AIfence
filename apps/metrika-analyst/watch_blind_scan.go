package main

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

func (d blindDataset) scan(ctx context.Context) (BlindScan, map[string]blindHit, error) {
	return d.scanMeasures(ctx, d.measures())
}

func (d blindDataset) scanMeasures(ctx context.Context, measures []*blindMeasure) (BlindScan, map[string]blindHit, error) {
	out := BlindScan{Status: "incomplete", DatasetID: d.id.String(), AsOf: d.series.AsOf, Origin: d.origin(),
		SeriesCount: len(measures), Candidates: []BlindCandidate{}, Warnings: []string{}}
	cutoff := blindCutoff(d.series.AsOf)
	groups := map[blindPeerKey][]*blindMeasure{}
	rowsByGroup := map[blindPeerKey]map[seriesKey][]Observation{}
	for _, m := range measures {
		groups[m.peer] = append(groups[m.peer], m)
		if rowsByGroup[m.peer] == nil {
			rowsByGroup[m.peer] = map[seriesKey][]Observation{}
		}
		for day, obs := range m.weeks {
			rowsByGroup[m.peer][seriesKey{product: m.meta.ProductKey, monday: day}] = obs
		}
	}
	var hits []blindHit
	add := func(m *blindMeasure, h BlindCandidate) {
		h.BlindDimensions = m.meta.BlindDimensions
		h.ID = hash([]any{out.DatasetID, m.meta.ProductKey, m.peer.metric, m.peer.segment, m.peer.unit,
			m.peer.currency, m.peer.base, m.peer.comparison, h.Kind, h.Period, h.Control, h.SourceRefs})
		h.SourceRefCount = len(h.SourceRefs)
		if len(h.SourceRefs) > 3 {
			h.SourceRefs = []Ref{h.SourceRefs[0], h.SourceRefs[1], h.SourceRefs[len(h.SourceRefs)-1]}
		}
		hits = append(hits, blindHit{h, m.peer})
	}
	registry := d.context.Batches["metrika"]
	if registry.Source == "" || registry.Status != "ok" {
		out.Warnings = append(out.Warnings, "The collection-break registry is absent or incomplete.")
	}
	origin := out.Origin
	releases := d.context.Batches["release"].Observations
	unsupported := 0
	var work int64
	for _, target := range measures {
		if err := ctx.Err(); err != nil {
			return out, nil, err
		}
		work += int64(len(registry.Observations)) + int64(len(target.weeks))*
			(int64(len(groups[target.peer]))+3*int64(len(registry.Observations))+int64(len(releases))+1)
		if work > maxBlindScanWork {
			return out, nil, fmt.Errorf("snapshot exceeds the blind scan work budget; select a smaller snapshot")
		}
		breakCount := 0
		for _, b := range registry.Observations {
			if b.MetricKey == "data_break" && matchesBlindFact(b, target, target.meta.Period, cutoff) {
				p := Period{max(b.Period.From, target.meta.Period.From), min(b.Period.To, target.meta.Period.To, cutoff)}
				if p.From <= p.To {
					add(target, BlindCandidate{Kind: "measurement_break", Period: p, Reason: "documented_collection_break", SourceRefs: []Ref{b.SourceRef}})
					breakCount++
				}
			}
		}
		if breakCount == 0 {
			for _, o := range target.rows {
				if o.Status == "incomparable" && o.Period.To <= cutoff {
					add(target, BlindCandidate{Kind: "measurement_break", Period: o.Period, Reason: "source_marked_week_incomparable",
						SourceRefs: []Ref{o.SourceRef}})
					break
				}
			}
		}
		if len(target.weeks) == 0 {
			unsupported++
			continue
		}
		dates := make([]string, 0, len(target.weeks))
		for day := range target.weeks {
			if day <= cutoff {
				dates = append(dates, day)
			}
		}
		if len(dates) == 0 {
			out.UncertainWindows++
			continue
		}
		sort.Strings(dates)
		first, _ := time.Parse(time.DateOnly, dates[0])
		last, _ := time.Parse(time.DateOnly, dates[len(dates)-1])
		controlledStart := first.AddDate(0, 0, (2*watchWeeks-1)*7)
		active := -1
		uncontrolled := false
		var previous time.Time
		reportedWindows := 0
		for _, day := range dates[min(watchWeeks, len(dates)):] {
			at, _ := time.Parse(time.DateOnly, day)
			if err := ctx.Err(); err != nil {
				return out, nil, err
			}
			p := week(at)
			if spike, found := blindSpike(target, at, cutoff, registry.Observations, releases); found {
				add(target, spike)
			}
			if at.Before(controlledStart) {
				continue
			}
			reportedWindows++
			if !previous.IsZero() && !at.Equal(previous.AddDate(0, 0, 7)) {
				active, uncontrolled = -1, false
			}
			previous = at
			control := blindControl(target, groups[target.peer], at, min(p.To, cutoff))
			if control == nil {
				active = -1
				out.UncertainWindows++
				if change, refs, ok := blindUncontrolled(target, at, min(p.To, cutoff), registry.Observations); ok {
					if !uncontrolled {
						add(target, BlindCandidate{Kind: "uncontrolled_decline", Period: p, Reason: "decline_without_independent_control",
							ChangePct: &change, SourceRefs: refs})
					}
					uncontrolled = true
				} else {
					uncontrolled = false
				}
				continue
			}
			sig := evaluateWeek(SignalInput{ProductKey: target.meta.ProductKey, ControlProductKey: control.meta.ProductKey,
				MetricKey: target.meta.MetricKey, Segment: target.meta.Segment, DropPct: watchDropPct,
				ControlMaxPct: watchControlMaxPct}, at, cutoff, privateSeries, origin, rowsByGroup[target.peer], registry.Observations)
			if sig.Value == nil {
				active = -1
				out.UncertainWindows++
				continue
			}
			out.ComparedWindows++
			if !*sig.Value {
				active = -1
				continue
			}
			if active == -1 || hits[active].candidate.Control != control.meta.ProductKey {
				magnitude := signalMagnitude(sig)
				add(target, BlindCandidate{
					Kind: "controlled_decline", Period: p, Control: control.meta.ProductKey,
					SignalID: sig.ID, Magnitude: magnitude,
					Reason:     "decline_with_stable_control_ranked_on_baseline",
					ControlPct: blindPct(sig, "control_change_pct"),
					Adjusted:   blindPct(sig, "control_adjusted_change_pct"), SourceRefs: blindSignalRefs(sig),
				})
				active = len(hits) - 1
			}
			candidate := &hits[active].candidate
			candidate.ConfirmedWindows++
			candidate.LastConfirmedWeek = &p
			candidate.LastConfirmationRefs = blindConfirmationRefs(target.weeks[day], control.weeks[day], min(p.To, cutoff))
		}
		if controlledStart.After(last) {
			out.UncertainWindows++
		} else {
			expectedWindows := 1 + int((last.Unix()-controlledStart.Unix())/(7*24*60*60))
			out.UncertainWindows += expectedWindows - reportedWindows
		}
	}
	if unsupported > 0 {
		out.UncertainWindows += unsupported
		out.Warnings = append(out.Warnings, "Some dimensions lack Monday–Sunday totals; daily values and rates were not aggregated.")
	}
	if d.series.Status != "ok" {
		out.UncertainWindows++
		out.Warnings = append(out.Warnings, "The selected series reports incomplete coverage.")
	}

	sort.SliceStable(hits, func(i, j int) bool {
		a, b := hits[i].candidate, hits[j].candidate
		if blindRank(a.Kind) != blindRank(b.Kind) {
			return blindRank(a.Kind) < blindRank(b.Kind)
		}
		if a.ConfirmedWindows != b.ConfirmedWindows {
			return a.ConfirmedWindows > b.ConfirmedWindows
		}
		if magnitudeA, magnitudeB := blindMagnitude(a), blindMagnitude(b); magnitudeA != magnitudeB {
			return magnitudeA > magnitudeB
		}
		if a.Period.From != b.Period.From {
			return a.Period.From < b.Period.From
		}
		return a.ID < b.ID
	})
	index := map[string]blindHit{}
	for _, hit := range hits {
		if _, exists := index[hit.candidate.ID]; exists {
			continue
		}
		index[hit.candidate.ID] = hit
		out.Candidates = append(out.Candidates, hit.candidate)
	}
	if d.record != nil {
		var ids []string
		for _, candidate := range out.Candidates {
			if candidate.SignalID != "" {
				ids = append(ids, candidate.SignalID)
			}
		}
		if len(ids) > 0 {
			found, err := d.record(ctx, out.DatasetID, ids)
			if err != nil {
				return out, nil, err
			}
			for i := range out.Candidates {
				candidate := &out.Candidates[i]
				candidate.watchDetection = found[candidate.SignalID]
			}
		}
	}
	out.CandidateCount = len(out.Candidates)
	if len(out.Candidates) > 0 {
		switch out.Candidates[0].Kind {
		case "controlled_decline":
			out.Status = "alert"
		case "measurement_break":
			out.Status = "measurement_issue"
		case "campaign_effect", "temporary_spike":
			out.Status = out.Candidates[0].Kind
		}
	} else if out.ComparedWindows > 0 && out.UncertainWindows == 0 {
		out.Status = "quiet"
	}
	return out, index, nil
}

func blindConfirmationRefs(target, control []Observation, cutoff string) []Ref {
	refs := make([]Ref, 0, 2)
	for _, rows := range [][]Observation{target, control} {
		for _, row := range rows {
			if weeklyIssue(row, cutoff) == "" {
				refs = append(refs, row.SourceRef)
				break
			}
		}
	}
	return refs
}

func blindRank(kind string) int {
	switch kind {
	case "controlled_decline":
		return 0
	case "measurement_break":
		return 1
	case "campaign_effect":
		return 2
	case "temporary_spike":
		return 3
	default:
		return 4
	}
}

func blindMagnitude(c BlindCandidate) float64 {
	if c.Adjusted != nil {
		return math.Abs(*c.Adjusted)
	}
	if c.ChangePct != nil {
		return math.Abs(*c.ChangePct)
	}
	return 0
}

func blindPct(sig Signal, name string) *float64 {
	if value, ok := sig.Details[name].(float64); ok {
		return &value
	}
	return nil
}

func blindSignalRefs(sig Signal) []Ref {
	links, _ := sig.Details["source_refs"].([]evidenceLink)
	refs := make([]Ref, 0, len(links))
	for _, link := range links {
		refs = append(refs, link.SourceRef)
	}
	return refs
}

func matchesBlindFact(f ContextFact, m *blindMeasure, window Period, cutoff string) bool {
	return overlaps(f.Period, window) && matchesProduct(f.ProductKey, m.meta.ProductKey) &&
		(f.Segment == "" || f.Segment == "all" || f.Segment == m.meta.Segment) &&
		(len(metricLinks(f)) == 0 || metricMatch(f, []string{m.meta.MetricKey})) &&
		detailString(f.Details, "known_from") <= cutoff
}

func blindPoint(m *blindMeasure, at time.Time, cutoff string) (Observation, bool) {
	rows := m.weeks[at.Format(time.DateOnly)]
	if len(rows) != 1 || weeklyIssue(rows[0], cutoff) != "" {
		return Observation{}, false
	}
	return rows[0], true
}

func blindWindow(m *blindMeasure, first time.Time, cutoff string) ([]float64, []Ref, bool) {
	values, refs := make([]float64, 0, watchWeeks), make([]Ref, 0, watchWeeks)
	for i := range watchWeeks {
		o, ok := blindPoint(m, first.AddDate(0, 0, i*7), cutoff)
		if !ok {
			return nil, nil, false
		}
		values, refs = append(values, *o.Value), append(refs, o.SourceRef)
	}
	return values, refs, true
}

func blindControl(target *blindMeasure, group []*blindMeasure, at time.Time, cutoff string) *blindMeasure {
	if target.meta.ProductKey == "store" || target.meta.ProductKey == "all" {
		return nil
	}
	first := at.AddDate(0, 0, -(2*watchWeeks-1)*7)
	base, _, ok := blindWindow(target, first, cutoff)
	if !ok || cappedMean(base) == 0 {
		return nil
	}
	targetMean := cappedMean(base)
	best := math.Inf(1)
	var control *blindMeasure
	for _, peer := range group {
		if peer.meta.ProductKey == target.meta.ProductKey || peer.meta.ProductKey == "store" || peer.meta.ProductKey == "all" {
			continue
		}
		rows, _, ok := blindWindow(peer, first, cutoff)
		if !ok || cappedMean(rows) == 0 {
			continue
		}
		mean := cappedMean(rows)
		// Rank on baseline shape and stability alone. Outcome weeks never select the control.
		score := 0.0
		for i, v := range rows {
			score += math.Abs(v/mean-base[i]/targetMean) + math.Abs(v/mean-1)
		}
		if score < best || (score == best && control != nil && peer.meta.ProductKey < control.meta.ProductKey) {
			best, control = score, peer
		}
	}
	return control
}

func blindBreakIn(m *blindMeasure, window Period, cutoff string, breaks []ContextFact) bool {
	for _, fact := range breaks {
		if fact.MetricKey == "data_break" && matchesBlindFact(fact, m, window, cutoff) &&
			(detailString(fact.Details, "effect") != "boundary" || window.From < fact.Period.From) {
			return true
		}
	}
	return false
}

func blindUncontrolled(target *blindMeasure, at time.Time, cutoff string, breaks []ContextFact) (float64, []Ref, bool) {
	first := at.AddDate(0, 0, -(2*watchWeeks-1)*7)
	if blindBreakIn(target, Period{week(first).From, week(at).To}, cutoff, breaks) {
		return 0, nil, false
	}
	b, left, ok := blindWindow(target, first, cutoff)
	if !ok || cappedMean(b) == 0 {
		return 0, nil, false
	}
	a, right, ok := blindWindow(target, first.AddDate(0, 0, watchWeeks*7), cutoff)
	if !ok {
		return 0, nil, false
	}
	delta := (cappedMean(a)/cappedMean(b) - 1) * 100
	return delta, append(left, right...), delta <= -watchDropPct
}

func blindSpike(target *blindMeasure, at time.Time, cutoff string, breaks, releases []ContextFact) (BlindCandidate, bool) {
	p := week(at)
	first, following := at.AddDate(0, 0, -watchWeeks*7), at.AddDate(0, 0, 7)
	if blindBreakIn(target, Period{week(first).From, week(following).To}, cutoff, breaks) {
		return BlindCandidate{}, false
	}
	base, refs, ok := blindWindow(target, first, cutoff)
	if !ok || cappedMean(base) == 0 {
		return BlindCandidate{}, false
	}
	now, currentOK := blindPoint(target, at, cutoff)
	next, nextOK := blindPoint(target, following, cutoff)
	mean := cappedMean(base)
	if !currentOK || !nextOK || *now.Value/mean < spikeCap || math.Abs(*next.Value/mean-1)*100 > watchDropPct {
		return BlindCandidate{}, false
	}
	delta := (*now.Value/mean - 1) * 100
	if math.IsInf(delta, 0) {
		return BlindCandidate{}, false
	}
	candidate := BlindCandidate{Kind: "temporary_spike", Period: p, Reason: "isolated_positive_spike_then_reversion",
		ChangePct: &delta, SourceRefs: append(refs, now.SourceRef, next.SourceRef)}
	for _, release := range releases {
		kind := strings.ToLower(detailString(release.Details, "release_type"))
		if (kind == "campaign" || kind == "marketing_campaign" || kind == "marketing" || kind == "email_campaign") &&
			matchesBlindFact(release, target, p, cutoff) {
			candidate.Kind = "campaign_effect"
			candidate.Reason = "temporary_lift_overlaps_documented_campaign"
			candidate.SourceRefs = append(candidate.SourceRefs, release.SourceRef)
			break
		}
	}
	return candidate, true
}
