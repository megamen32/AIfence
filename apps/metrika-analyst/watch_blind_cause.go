package main

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Cause typing for the blind scan.
//
// The decline detector only says "this series fell against a stable peer".
// A product manager needs the likely cause. Each rule here works on scale-free
// features so that demand growth or seasonality does not look like a fault:
//
//   - volumes are compared with the median of the other products in the same
//     segment and week (peer-adjusted log values);
//   - conversion ratios (orders per visit, orders per checkout) are invariant to
//     traffic volume;
//   - every change is measured in noise units of its own 8-week baseline
//     (a two-sample z score), not as a fixed percentage;
//   - the onset week is the split that maximises that score (change point), so
//     it does not lag behind a confirmation window;
//   - a rule only runs when the snapshot has every metric it needs.
//
// The result names the cause type, the onset week, before/after numbers and a
// release of the same family around that week when the release log has one.

const (
	causeKind      = "cause_signal"
	causeBaseWeeks = 8
	causeMinBase   = 5
	// The scan covers the whole extract; every rule is scored in noise units of its own baseline.
	causeSearchWeeks = 40
	causeMinMissing  = 2 // consecutive weeks of missing orders for a delivery fault
	causeLateWeeks   = 3
)

// causeZ is the minimum score, in baseline noise units, for every condition
// of a rule. Calibrated on clean families only (no planted change).
var causeZ = 6.0

// BlindCauseMetric is one before/after measurement behind a cause.
type BlindCauseMetric struct {
	Segment   string  `json:"segment"`
	MetricKey string  `json:"metric_key"`
	Before    float64 `json:"before_mean"`
	After     float64 `json:"after_mean"`
	ChangePct float64 `json:"change_pct"`
	Z         float64 `json:"z_score,omitempty"`
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

var causeVolumes = []string{"visits", "card_views", "cart_additions", "checkouts", "orders", "revenue"}

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
		if len(s) > 0 {
			f.segments[seg][m.meta.MetricKey] = s
		}
	}
	// A summed segment catches changes split across devices. Rates are not summed.
	for _, f := range out {
		if len(f.segments) < 2 || f.segments["all"] != nil {
			continue
		}
		all := map[string]causeSeries{}
		for _, metric := range causeVolumes {
			sum := causeSeries{}
			complete := map[string]int{}
			for _, seg := range f.segments {
				for w, v := range seg[metric] {
					sum[w] += v
					complete[w]++
				}
			}
			for w, n := range complete {
				if n != len(f.segments) {
					delete(sum, w)
				}
			}
			if len(sum) > 0 {
				all[metric] = sum
			}
		}
		f.segments["all"] = all
	}
	weeks := make([]string, 0, len(weekSet))
	for w := range weekSet {
		weeks = append(weeks, w)
	}
	sort.Strings(weeks)
	return out, weeks
}

// causeFeatures turns one product segment into scale-free weekly features.
// Volumes are peer-adjusted: log value minus the median, over the other
// products with a full history, of their own centred log value that week.
func causeFeatures(funnels map[string]*causeFunnel, f *causeFunnel, seg string, weeks []string) map[string][]float64 {
	nan := math.NaN()
	series := f.segments[seg]
	if series == nil {
		return nil
	}
	logOf := func(s causeSeries, w string) float64 {
		if v, ok := s[w]; ok && v > 0 {
			return math.Log(v)
		}
		return nan
	}
	peer := func(metric string) []float64 {
		ref := make([]float64, len(weeks))
		var centred [][]float64
		for _, q := range funnels {
			if q == f || q.segments[seg] == nil || q.segments[seg][metric] == nil {
				continue
			}
			s := q.segments[seg][metric]
			if len(s) < len(weeks)*3/4 {
				continue // a key that appears late has no comparable history
			}
			vals := make([]float64, len(weeks))
			sum, n := 0.0, 0
			for i, w := range weeks {
				vals[i] = logOf(s, w)
				if !math.IsNaN(vals[i]) {
					sum += vals[i]
					n++
				}
			}
			if n == 0 {
				continue
			}
			for i := range vals {
				vals[i] -= sum / float64(n)
			}
			centred = append(centred, vals)
		}
		for i := range weeks {
			var col []float64
			for _, vals := range centred {
				if !math.IsNaN(vals[i]) {
					col = append(col, vals[i])
				}
			}
			if len(col) == 0 {
				ref[i] = 0
				continue
			}
			sort.Float64s(col)
			ref[i] = col[len(col)/2]
			if len(col)%2 == 0 {
				ref[i] = (col[len(col)/2-1] + col[len(col)/2]) / 2
			}
		}
		return ref
	}
	out := map[string][]float64{}
	rel := func(metric string) {
		s := series[metric]
		if s == nil {
			return
		}
		ref := peer(metric)
		vals := make([]float64, len(weeks))
		for i, w := range weeks {
			vals[i] = logOf(s, w) - ref[i]
		}
		out[metric] = vals
	}
	ratio := func(name, num, den string) {
		a, b := series[num], series[den]
		if a == nil || b == nil {
			return
		}
		vals := make([]float64, len(weeks))
		for i, w := range weeks {
			vals[i] = logOf(a, w) - logOf(b, w)
		}
		out[name] = vals
	}
	rel("visits")
	rel("checkouts")
	rel("orders")
	ratio("orders_per_visit", "orders", "visits")
	ratio("orders_per_checkout", "orders", "checkouts")
	if s := series["bounce_rate"]; s != nil {
		vals := make([]float64, len(weeks))
		for i, w := range weeks {
			if v, ok := s[w]; ok {
				vals[i] = v
			} else {
				vals[i] = nan
			}
		}
		out["bounce_rate"] = vals
	}
	if s := series["avg_visit_duration"]; s != nil {
		vals := make([]float64, len(weeks))
		for i, w := range weeks {
			vals[i] = logOf(s, w)
		}
		out["avg_visit_duration"] = vals
	}
	return out
}

// causeSplitZ is the two-sample z score of the mean after week k against the
// 8 weeks before it, with the baseline standard deviation as the noise unit.
func causeSplitZ(vals []float64, k int, floor float64) (float64, bool) {
	if vals == nil {
		return 0, false
	}
	var pre, post []float64
	for i := max(0, k-causeBaseWeeks); i < k; i++ {
		if !math.IsNaN(vals[i]) && !math.IsInf(vals[i], 0) {
			pre = append(pre, vals[i])
		}
	}
	for i := k; i < len(vals); i++ {
		if !math.IsNaN(vals[i]) && !math.IsInf(vals[i], 0) {
			post = append(post, vals[i])
		}
	}
	if len(pre) < causeMinBase || len(post) == 0 {
		return 0, false
	}
	mean := func(xs []float64) float64 {
		s := 0.0
		for _, x := range xs {
			s += x
		}
		return s / float64(len(xs))
	}
	a, b := mean(pre), mean(post)
	ss := 0.0
	for _, x := range pre {
		ss += (x - a) * (x - a)
	}
	sd := math.Max(math.Sqrt(ss/float64(len(pre)-1)), floor)
	return (b - a) / (sd * math.Sqrt(1/float64(len(pre))+1/float64(len(post)))), true
}

// causeOnset picks the step-change week: the split in [lo, n) that minimises
// the squared error of a two-level fit over the fixed window [lo-8, n), summed
// over the rule's features (each scaled by its window variance). Unlike the
// largest z score, this does not drift towards splits with more weeks after.
func causeOnset(features map[string][]float64, names []string, lo, n int) int {
	start := max(0, lo-causeBaseWeeks)
	best, at := math.Inf(1), -1
	for k := lo; k < n; k++ {
		total := 0.0
		for _, name := range names {
			vals := features[name]
			var all []float64
			sse := 0.0
			for _, part := range [][2]int{{start, k}, {k, n}} {
				sum, cnt := 0.0, 0
				for i := part[0]; i < part[1]; i++ {
					if v := vals[i]; !math.IsNaN(v) && !math.IsInf(v, 0) {
						sum += v
						cnt++
					}
				}
				if cnt == 0 {
					continue
				}
				m := sum / float64(cnt)
				for i := part[0]; i < part[1]; i++ {
					if v := vals[i]; !math.IsNaN(v) && !math.IsInf(v, 0) {
						sse += (v - m) * (v - m)
						all = append(all, v)
					}
				}
			}
			if len(all) < 2 {
				continue
			}
			mean, ss := 0.0, 0.0
			for _, v := range all {
				mean += v
			}
			mean /= float64(len(all))
			for _, v := range all {
				ss += (v - mean) * (v - mean)
			}
			if ss > 0 {
				total += sse / ss
			}
		}
		if total < best {
			best, at = total, k
		}
	}
	if at < 0 {
		return at
	}
	// A change that deepens over time (late events) fits best at its deepest
	// point. Walk back while the previous week already deviates from its own
	// baseline by 3 noise units in the same direction, for every feature that
	// moved at the split (a steady feature such as checkouts is skipped).
	for at > lo {
		ok, moved := true, 0
		for _, name := range names {
			vals := features[name]
			zStep, okStep := causeSplitZ(vals, at, causeFloor(name))
			if !okStep || math.Abs(zStep) < 3 {
				continue
			}
			moved++
			zPrev, okPrev := causePointZ(vals, at-1, causeFloor(name))
			if !okPrev || zStep*zPrev <= 0 || math.Abs(zPrev) < 3 {
				ok = false
				break
			}
		}
		if !ok || moved == 0 {
			break
		}
		at--
	}
	return at
}

// causePointZ scores one week against the 8 weeks before it.
func causePointZ(vals []float64, i int, floor float64) (float64, bool) {
	if math.IsNaN(vals[i]) || math.IsInf(vals[i], 0) {
		return 0, false
	}
	var pre []float64
	for j := max(0, i-causeBaseWeeks); j < i; j++ {
		if !math.IsNaN(vals[j]) && !math.IsInf(vals[j], 0) {
			pre = append(pre, vals[j])
		}
	}
	if len(pre) < causeMinBase {
		return 0, false
	}
	m := 0.0
	for _, v := range pre {
		m += v
	}
	m /= float64(len(pre))
	ss := 0.0
	for _, v := range pre {
		ss += (v - m) * (v - m)
	}
	sd := math.Max(math.Sqrt(ss/float64(len(pre)-1)), floor)
	return (vals[i] - m) / sd, true
}

func causeFloor(feature string) float64 {
	if feature == "bounce_rate" {
		return 0.01
	}
	return 0.02
}

type causeRule struct {
	cause, reason string
	needs         []string
	// score returns the weakest of the rule's oriented conditions; the rule
	// holds when it reaches causeZ. used lists the metrics shown as evidence.
	score func(z map[string]float64, tail int) float64
	used  []string
}

var causeRules = []causeRule{
	{
		cause: "traffic_quality", reason: "visits_up_conversion_down_visit_quality_down",
		needs: []string{"visits", "orders_per_visit"},
		score: func(z map[string]float64, _ int) float64 {
			quality, ok := math.Inf(-1), false
			if v, has := z["bounce_rate"]; has {
				quality, ok = math.Max(quality, v), true
			}
			if v, has := z["avg_visit_duration"]; has {
				quality, ok = math.Max(quality, -v), true
			}
			if !ok {
				return 0 // without visit-quality metrics bots cannot be told from real traffic
			}
			return min(z["visits"], -z["orders_per_visit"], quality)
		},
		used: []string{"visits", "orders", "bounce_rate", "avg_visit_duration"},
	},
	{
		cause: "traffic_quality", reason: "visits_up_orders_flat_checkouts_flat_no_visit_quality_metrics",
		needs: []string{"visits", "checkouts", "orders_per_visit"},
		score: func(z map[string]float64, _ int) float64 {
			if _, has := z["bounce_rate"]; has {
				return 0 // visit-quality metrics exist: the rule above decides
			}
			if _, has := z["avg_visit_duration"]; has {
				return 0
			}
			// Traffic that grows several-fold while checkouts and orders do not
			// follow is not demand. Without quality metrics this is the weaker
			// reading, so checkouts must stay level (a fall is the masked regression).
			if math.Abs(z["checkouts"]) >= causeZ/2 {
				return 0
			}
			return min(z["visits"], -z["orders_per_visit"])
		},
		used: []string{"visits", "card_views", "cart_additions", "checkouts", "orders"},
	},
	{
		cause: "masked_multi_cause_regression", reason: "traffic_up_while_checkouts_fall",
		needs: []string{"visits", "checkouts", "orders_per_visit"},
		score: func(z map[string]float64, _ int) float64 {
			if v, has := z["bounce_rate"]; has && v >= causeZ {
				return 0 // degraded visit quality is the bot pattern
			}
			return min(z["visits"], -z["checkouts"], -z["orders_per_visit"])
		},
		used: []string{"visits", "cart_additions", "checkouts", "orders"},
	},
	{
		cause: "late_event_delivery", reason: "orders_per_checkout_fall_at_extract_tail",
		needs: []string{"checkouts", "orders_per_checkout"},
		score: func(z map[string]float64, tail int) float64 {
			if tail > causeLateWeeks || math.Abs(z["checkouts"]) >= causeZ {
				return 0
			}
			return -z["orders_per_checkout"]
		},
		used: []string{"checkouts", "orders", "revenue"},
	},
}

type causeFound struct {
	product, cause, reason, start string
	strength                      float64
	evidence                      []BlindCauseMetric
	refs                          []Ref
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

func causeEvidence(f *causeFunnel, seg string, metrics []string, weeks []string, k int, z map[string]float64) ([]BlindCauseMetric, []Ref) {
	var evidence []BlindCauseMetric
	var refs []Ref
	pre, post := weeks[max(0, k-causeBaseWeeks):k], weeks[k:]
	for _, metric := range metrics {
		s := f.segments[seg][metric]
		a, okA := causeMean(s, pre)
		b, okB := causeMean(s, post)
		if !okA || !okB || a == 0 {
			continue
		}
		evidence = append(evidence, BlindCauseMetric{Segment: seg, MetricKey: metric, Before: round2(a), After: round2(b),
			ChangePct: round2((b/a - 1) * 100), Z: round2(z[metric])})
		for _, w := range []string{weeks[k-1], weeks[k]} {
			if r, ok := f.refs[seg+"/"+metric+"/"+w]; ok {
				refs = append(refs, r)
			}
		}
	}
	return evidence, refs
}

func round2(v float64) float64 { return math.Round(v*100) / 100 }

func blindCauses(measures []*blindMeasure, cutoff string, releases []ContextFact) []BlindCandidate {
	funnels, weeks := causeFunnels(measures, cutoff)
	if len(weeks) < causeBaseWeeks+1 {
		return nil
	}
	products := make([]string, 0, len(funnels))
	for p := range funnels {
		products = append(products, p)
	}
	sort.Strings(products)
	var found []causeFound
	for _, p := range products {
		f := funnels[p]
		top := causeFound{}
		segs := make([]string, 0, len(f.segments))
		for s := range f.segments {
			segs = append(segs, s)
		}
		sort.Strings(segs)
		for _, seg := range segs {
			features := causeFeatures(funnels, f, seg, weeks)
			lo := max(causeBaseWeeks, len(weeks)-causeSearchWeeks)
			for _, rule := range causeRules {
				missing := false
				for _, need := range rule.needs {
					if features[need] == nil {
						missing = true
					}
				}
				if missing {
					continue
				}
				k := causeOnset(features, rule.needs, lo, len(weeks))
				if k < 0 {
					continue
				}
				z := map[string]float64{}
				for name, vals := range features {
					if v, ok := causeSplitZ(vals, k, causeFloor(name)); ok {
						z[name] = v
					}
				}
				score := rule.score(z, len(weeks)-k)
				if score < causeZ || score <= top.strength {
					continue
				}
				evidence, refs := causeEvidence(f, seg, rule.used, weeks, k, z)
				top = causeFound{p, rule.cause, rule.reason, weeks[k], score, evidence, refs}
			}
		}
		if top.cause != "" {
			found = append(found, top)
		}
	}
	found = append(found, causeKeyMigrations(funnels, weeks)...)
	found = append(found, causeMissingOrders(funnels, weeks)...)
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

// causeKeyMigrations finds a key that appears in the middle of the extract and
// continues an existing product: the same SKU or label in the row details, or,
// without them, the old key's orders fall at that week while old+new stay level.
// The old key may keep reporting for a while after the new one starts.
func causeKeyMigrations(funnels map[string]*causeFunnel, weeks []string) []causeFound {
	if len(weeks) < causeBaseWeeks+1 {
		return nil
	}
	index := map[string]int{}
	for i, w := range weeks {
		index[w] = i
	}
	var out []causeFound
	for _, next := range funnels {
		k, ok := index[next.first]
		if !ok || k < causeBaseWeeks {
			continue // present from the start: not a new key
		}
		for _, old := range funnels {
			if old == next || old.first >= next.first {
				continue
			}
			sameItem := (old.sku != "" && strings.EqualFold(old.sku, next.sku) && old.label == next.label) ||
				strings.EqualFold(next.product, old.sku)
			if !sameItem && !causeContinues(old, next, weeks, k) {
				continue
			}
			var evidence []BlindCauseMetric
			var refs []Ref
			pre := weeks[max(0, k-causeBaseWeeks):k]
			for _, seg := range []string{"desktop", "mobile", "all"} {
				a, okA := causeMean(old.segments[seg]["orders"], pre)
				b, okB := causeMean(next.segments[seg]["orders"], weeks[k:])
				c, _ := causeMean(old.segments[seg]["orders"], weeks[k:])
				if okA && okB && a > 0 {
					evidence = append(evidence, BlindCauseMetric{Segment: seg, MetricKey: "orders", Before: round2(a),
						After: round2(b + c), ChangePct: round2(((b+c)/a - 1) * 100)})
				}
				for _, ref := range []struct {
					f *causeFunnel
					w string
				}{{old, weeks[k-1]}, {next, weeks[k]}} {
					if r, ok := ref.f.refs[seg+"/orders/"+ref.w]; ok {
						refs = append(refs, r)
					}
				}
			}
			out = append(out, causeFound{old.product, "taxonomy_migration", "series_moves_to_new_product_key:" + next.product,
				next.first, 100, evidence, refs})
			break
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].product < out[j].product })
	return out
}

// causeContinues checks, without catalogue details, that old+new orders keep
// the old level while the old key alone falls at the week the new key starts.
func causeContinues(old, next *causeFunnel, weeks []string, k int) bool {
	o, n := old.segments["all"]["orders"], next.segments["all"]["orders"]
	if o == nil || n == nil {
		return false
	}
	alone := make([]float64, len(weeks))
	joint := make([]float64, len(weeks))
	for i, w := range weeks {
		a, b := o[w], n[w]
		alone[i], joint[i] = math.NaN(), math.NaN()
		if a > 0 {
			alone[i] = math.Log(a)
		}
		if a+b > 0 {
			joint[i] = math.Log(a + b)
		}
	}
	za, okA := causeSplitZ(alone, k, 0.02)
	if !okA && old.last < weeks[k] {
		za, okA = math.Inf(-1), true // the old key stopped reporting
	}
	zj, okJ := causeSplitZ(joint, k, 0.02)
	return okA && okJ && za <= -causeZ && math.Abs(zj) < causeZ/2
}

// causeMissingOrders finds orders that stop arriving while the funnel above
// them keeps reporting: at least causeMinMissing consecutive weeks where
// checkouts and visits are observed and orders are not. A single gap stays an
// honest "cannot tell" and is not typed.
func causeMissingOrders(funnels map[string]*causeFunnel, weeks []string) []causeFound {
	var out []causeFound
	products := make([]string, 0, len(funnels))
	for p := range funnels {
		products = append(products, p)
	}
	sort.Strings(products)
	for _, p := range products {
		f := funnels[p]
		var best []int
		for _, seg := range []string{"all", "desktop", "mobile"} {
			orders, checkouts, visits := f.segments[seg]["orders"], f.segments[seg]["checkouts"], f.segments[seg]["visits"]
			if orders == nil || checkouts == nil || visits == nil {
				continue
			}
			var run []int
			flush := func() {
				if len(run) >= causeMinMissing && len(run) > len(best) {
					best = append([]int(nil), run...)
				}
				run = run[:0]
			}
			for i, w := range weeks {
				_, hasO := orders[w]
				_, hasC := checkouts[w]
				_, hasV := visits[w]
				if !hasO && hasC && hasV && w >= f.first && w <= f.last {
					run = append(run, i)
				} else {
					flush()
				}
			}
			flush()
		}
		if len(best) == 0 || best[0] < causeMinBase {
			continue
		}
		k := best[0]
		var evidence []BlindCauseMetric
		var refs []Ref
		for _, seg := range []string{"all", "desktop", "mobile"} {
			pre := weeks[max(0, k-causeBaseWeeks):k]
			during := weeks[k : best[len(best)-1]+1]
			a, okA := causeMean(f.segments[seg]["checkouts"], pre)
			b, okB := causeMean(f.segments[seg]["checkouts"], during)
			if okA && okB && a > 0 {
				evidence = append(evidence, BlindCauseMetric{Segment: seg, MetricKey: "checkouts", Before: round2(a), After: round2(b), ChangePct: round2((b/a - 1) * 100)})
			}
			if a, ok := causeMean(f.segments[seg]["orders"], pre); ok {
				evidence = append(evidence, BlindCauseMetric{Segment: seg, MetricKey: "orders", Before: round2(a), After: 0, ChangePct: -100})
			}
			for _, w := range []string{weeks[k-1], weeks[k]} {
				if r, ok := f.refs[seg+"/checkouts/"+w]; ok {
					refs = append(refs, r)
				}
			}
		}
		out = append(out, causeFound{p, "late_event_delivery", "orders_missing_while_checkouts_observed", weeks[k], 50, evidence, refs})
	}
	return out
}
