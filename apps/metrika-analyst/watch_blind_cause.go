package main

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Cause typing for the blind scan. The decline detector only answers "this
// series fell against a stable peer". A product manager needs the likely
// cause: bot traffic, a checkout regression hidden by a traffic lift, late
// purchase events or a catalogue key change. Each rule reads only the funnel
// that the snapshot itself contains (no labels, no answer fields) and returns
// the week the change starts, the numbers behind it and a release of the same
// family and week when the release log has one.

const (
	causeKind        = "cause_signal"
	causeBaseWeeks   = 8
	causeMinBase     = 4
	causeSearchWeeks = 8
)

// BlindCauseMetric is one before/after measurement behind a cause.
type BlindCauseMetric struct {
	Segment   string  `json:"segment"`
	MetricKey string  `json:"metric_key"`
	Before    float64 `json:"before_mean"`
	After     float64 `json:"after_mean"`
	ChangePct float64 `json:"change_pct"`
}

type causeSeries map[string]float64 // week Monday -> value

type causeFunnel struct {
	product  string
	segments map[string]map[string]causeSeries // segment -> metric -> series
	refs     map[string]Ref                    // "segment/metric/week" -> ref
	label    string
	sku      string
	first    string
	last     string
}

func causeFunnels(measures []*blindMeasure, cutoff string) (map[string]*causeFunnel, []string) {
	out := map[string]*causeFunnel{}
	weekSet := map[string]bool{}
	for _, m := range measures {
		p := m.meta.ProductKey
		if p == "" || p == "store" || p == "all" {
			continue
		}
		f := out[p]
		if f == nil {
			f = &causeFunnel{product: p, segments: map[string]map[string]causeSeries{}, refs: map[string]Ref{}}
			out[p] = f
		}
		seg := m.meta.Segment
		if f.segments[seg] == nil {
			f.segments[seg] = map[string]causeSeries{}
		}
		s := causeSeries{}
		for day, rows := range m.weeks {
			if len(rows) != 1 || weeklyIssue(rows[0], cutoff) != "" {
				continue
			}
			s[day] = *rows[0].Value
			weekSet[day] = true
			f.refs[seg+"/"+m.meta.MetricKey+"/"+day] = rows[0].SourceRef
			if f.first == "" || day < f.first {
				f.first = day
			}
			if day > f.last {
				f.last = day
			}
			if f.sku == "" {
				f.sku = detailString(rows[0].Details, "sku")
				f.label = detailString(rows[0].Details, "product_label")
			}
		}
		f.segments[seg][m.meta.MetricKey] = s
	}
	weeks := make([]string, 0, len(weekSet))
	for w := range weekSet {
		weeks = append(weeks, w)
	}
	sort.Strings(weeks)
	return out, weeks
}

func causeMean(s causeSeries, weeks []string) (float64, bool) {
	sum, n := 0.0, 0
	for _, w := range weeks {
		if v, ok := s[w]; ok {
			sum += v
			n++
		}
	}
	if n == 0 {
		return 0, false
	}
	return sum / float64(n), true
}

// causeShift compares the mean of weeks [k-8,k) with [k,end] for one metric.
func causeShift(seg map[string]causeSeries, metric string, weeks []string, k int) (BlindCauseMetric, bool) {
	s := seg[metric]
	if s == nil {
		return BlindCauseMetric{}, false
	}
	pre := weeks[max(0, k-causeBaseWeeks):k]
	post := weeks[k:]
	n := 0
	for _, w := range pre {
		if _, ok := s[w]; ok {
			n++
		}
	}
	if n < causeMinBase {
		return BlindCauseMetric{}, false
	}
	a, okA := causeMean(s, pre)
	b, okB := causeMean(s, post)
	if !okA || !okB || a == 0 {
		return BlindCauseMetric{}, false
	}
	return BlindCauseMetric{MetricKey: metric, Before: round2(a), After: round2(b), ChangePct: round2((b/a - 1) * 100)}, true
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

type causeRule struct {
	cause, reason string
	// match returns a strength (>0) and the metrics that support it.
	match func(ch map[string]BlindCauseMetric, tail int) (float64, []string)
}

var causeRules = []causeRule{
	{"traffic_quality", "visits_up_with_bounce_up_and_flat_orders", func(ch map[string]BlindCauseMetric, _ int) (float64, []string) {
		v, b, d, o := ch["visits"], ch["bounce_rate"], ch["avg_visit_duration"], ch["orders"]
		if v.ChangePct >= 40 && b.ChangePct >= 30 && d.ChangePct <= -30 && math.Abs(o.ChangePct) <= 15 {
			return v.ChangePct, []string{"visits", "bounce_rate", "avg_visit_duration", "orders"}
		}
		return 0, nil
	}},
	{"masked_multi_cause_regression", "traffic_up_while_checkouts_fall", func(ch map[string]BlindCauseMetric, _ int) (float64, []string) {
		v, c, o, b := ch["visits"], ch["checkouts"], ch["orders"], ch["bounce_rate"]
		if v.ChangePct >= 25 && c.ChangePct <= -15 && o.ChangePct <= -15 && math.Abs(b.ChangePct) < 20 {
			return -c.ChangePct + v.ChangePct/4, []string{"visits", "cart_additions", "checkouts", "orders"}
		}
		return 0, nil
	}},
	{"late_event_delivery", "orders_fall_at_extract_tail_while_checkouts_hold", func(ch map[string]BlindCauseMetric, tail int) (float64, []string) {
		c, o := ch["checkouts"], ch["orders"]
		if tail <= 3 && o.ChangePct <= -30 && math.Abs(c.ChangePct) <= 15 {
			return -o.ChangePct, []string{"checkouts", "orders", "revenue"}
		}
		return 0, nil
	}},
}

var causeMetrics = []string{"visits", "card_views", "cart_additions", "checkouts", "orders", "revenue", "bounce_rate", "avg_visit_duration"}

type causeFound struct {
	product, cause, reason, start string
	strength                      float64
	evidence                      []BlindCauseMetric
	refs                          []Ref
}

func blindCauses(measures []*blindMeasure, cutoff string, releases []ContextFact) []BlindCandidate {
	funnels, weeks := causeFunnels(measures, cutoff)
	if len(weeks) < causeMinBase+1 {
		return nil
	}
	var found []causeFound
	products := make([]string, 0, len(funnels))
	for p := range funnels {
		products = append(products, p)
	}
	sort.Strings(products)
	for _, p := range products {
		f := funnels[p]
		best := map[string]causeFound{}
		for k := max(causeMinBase, len(weeks)-causeSearchWeeks); k < len(weeks); k++ {
			tail := len(weeks) - k
			segs := make([]string, 0, len(f.segments))
			for s := range f.segments {
				segs = append(segs, s)
			}
			sort.Strings(segs)
			for _, rule := range causeRules {
				total, hits := 0.0, 0
				var evidence []BlindCauseMetric
				var refs []Ref
				for _, seg := range segs {
					ch := map[string]BlindCauseMetric{}
					for _, m := range causeMetrics {
						if c, ok := causeShift(f.segments[seg], m, weeks, k); ok {
							c.Segment = seg
							ch[m] = c
						}
					}
					strength, used := rule.match(ch, tail)
					if strength <= 0 {
						continue
					}
					total += strength
					hits++
					for _, m := range used {
						if c, ok := ch[m]; ok {
							evidence = append(evidence, c)
							for _, w := range []string{weeks[k-1], weeks[k]} {
								if r, ok := f.refs[seg+"/"+m+"/"+w]; ok {
									refs = append(refs, r)
								}
							}
						}
					}
				}
				if hits == 0 {
					continue
				}
				// Weight by the number of weeks after the split so the onset week
				// wins over a later week that only holds the deepest point.
				total *= math.Sqrt(float64(tail))
				if cur, ok := best[rule.cause]; !ok || total > cur.strength {
					best[rule.cause] = causeFound{p, rule.cause, rule.reason, weeks[k], total, evidence, refs}
				}
			}
		}
		top := causeFound{}
		for _, c := range best {
			if c.strength > top.strength || (c.strength == top.strength && c.cause < top.cause) {
				top = c
			}
		}
		if top.cause != "" {
			found = append(found, top)
		}
	}
	found = append(found, causeKeyMigrations(funnels, weeks)...)
	out := make([]BlindCandidate, 0, len(found))
	for _, c := range found {
		start, _ := time.Parse(time.DateOnly, c.start)
		p := week(start)
		cand := BlindCandidate{
			BlindDimensions: BlindDimensions{ProductKey: c.product, MetricKey: "orders", Segment: "all", Unit: "count"},
			Kind:            causeKind, Period: p, Reason: c.reason, CauseType: c.cause,
			CauseEvidence: c.evidence, SourceRefs: c.refs, CauseStrength: round2(c.strength),
		}
		for _, r := range releases {
			if strings.EqualFold(r.ProductKey, c.product) && r.Period.From >= start.AddDate(0, 0, -7).Format(time.DateOnly) &&
				r.Period.From <= p.To && detailString(r.Details, "known_from") <= cutoff {
				rel := r
				cand.CauseRelease = &rel
				cand.SourceRefs = append(cand.SourceRefs, r.SourceRef)
				break
			}
		}
		out = append(out, cand)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CauseStrength > out[j].CauseStrength })
	return out
}

// causeKeyMigrations finds a product whose series stops before the extract
// ends while another key with the same SKU or label starts the next week.
func causeKeyMigrations(funnels map[string]*causeFunnel, weeks []string) []causeFound {
	if len(weeks) == 0 {
		return nil
	}
	end := weeks[len(weeks)-1]
	var out []causeFound
	for _, old := range funnels {
		if old.last == "" || old.last >= end {
			continue
		}
		for _, next := range funnels {
			if next == old || next.first <= old.last {
				continue
			}
			sameItem := (old.sku != "" && strings.EqualFold(old.sku, next.sku)) ||
				(old.label != "" && old.label == next.label) || strings.EqualFold(next.product, old.sku)
			gap, _ := time.Parse(time.DateOnly, next.first)
			prev, _ := time.Parse(time.DateOnly, old.last)
			if !sameItem || gap.Sub(prev) > 14*24*time.Hour {
				continue
			}
			var evidence []BlindCauseMetric
			var refs []Ref
			for seg, metrics := range old.segments {
				a, b := metrics["orders"][old.last], next.segments[seg]["orders"][next.first]
				if a > 0 {
					evidence = append(evidence, BlindCauseMetric{Segment: seg, MetricKey: "orders", Before: a, After: b,
						ChangePct: round2((b/a - 1) * 100)})
				}
				if r, ok := old.refs[seg+"/orders/"+old.last]; ok {
					refs = append(refs, r)
				}
				if r, ok := next.refs[seg+"/orders/"+next.first]; ok {
					refs = append(refs, r)
				}
			}
			sort.Slice(evidence, func(i, j int) bool { return evidence[i].Segment < evidence[j].Segment })
			out = append(out, causeFound{old.product, "taxonomy_migration", "series_moves_to_new_product_key:" + next.product,
				next.first, 100, evidence, refs})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].product < out[j].product })
	return out
}
