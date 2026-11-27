package handlers

import (
	"context"
	"encoding/json"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/views"
)

type weekCounts struct {
	from, to        string
	checkout, carts map[string]float64
	ambiguous       bool
}
type weekValue struct {
	from, to                string
	checkouts, carts, value float64
	cohort                  string
}
type releaseEvent struct{ date, id, name string }

func copiesOriginal(original, candidate []monitorObservation) bool {
	if len(original) == 0 || len(candidate) <= len(original) {
		return false
	}
	byID := make(map[string]evidence.Row, len(candidate))
	for _, o := range candidate {
		if _, exists := byID[o.ID]; exists {
			return false
		}
		byID[o.ID] = o.Row
	}
	for _, o := range original {
		other, ok := byID[o.ID]
		if !ok || !reflect.DeepEqual(o.Row, other) {
			return false
		}
	}
	return true
}

// A saved proposal keeps its evidence snapshot. Only an owner-scoped immutable
// superset containing every original row can provide missing weekly metrics.
func (h *Handler) releaseRows(ctx context.Context, original []monitorObservation, rec decision.Record, snapshots []evidence.Summary) ([]monitorObservation, error) {
	if len(original) == 0 || len(rec.Hypothesis.SignalIDs) == 0 {
		return original, nil
	}
	for _, row := range original {
		if row.Product == rec.Hypothesis.ProductKey && row.Metric == "cart_additions" {
			return original, nil
		}
	}
	var asOf string
	for _, s := range snapshots {
		if s.SnapshotID == rec.EvidenceSnapshotID {
			asOf = s.AsOf
			break
		}
	}
	for _, s := range snapshots {
		if s.Count <= len(original) || s.AsOf != asOf {
			continue
		}
		signal, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: s.SnapshotID, IDs: rec.Hypothesis.SignalIDs, Limit: 1})
		if err != nil {
			return nil, err
		}
		if len(signal.Rows) == 0 {
			continue
		}
		next, err := h.readMonitorSnapshot(ctx, s.SnapshotID)
		if err != nil {
			return nil, err
		}
		if copiesOriginal(original, next) {
			return next, nil
		}
	}
	return original, nil
}

func nativeReleaseHistory(product string, rows []monitorObservation, snapshot string) *views.ReleaseHistory {
	weeks := map[string]*weekCounts{}
	var events []releaseEvent
	for _, o := range rows {
		if o.Product != product || o.snapshot != snapshot || o.Status != "observed" {
			continue
		}
		if o.Source == "release" && o.Kind == "release" {
			var d struct {
				ID    string `json:"release_id"`
				State string `json:"status"`
				Date  string `json:"release_date"`
			}
			if json.Unmarshal([]byte(o.Details), &d) == nil && (d.State == "выпущен" || d.State == "released") && d.ID != "" && d.Date != "" {
				events = append(events, releaseEvent{d.Date, d.ID, o.Value})
			}
			continue
		}
		if o.Source != "metrika" || o.Kind != "metric" || o.Unit != "count" || (o.Metric != "checkouts" && o.Metric != "cart_additions") {
			continue
		}
		from, e1 := time.Parse(time.DateOnly, o.Period.From)
		to, e2 := time.Parse(time.DateOnly, o.Period.To)
		if e1 != nil || e2 != nil || to.Sub(from) != 6*24*time.Hour {
			continue
		}
		v, err := strconv.ParseFloat(o.Value, 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			continue
		}
		k := o.Period.From
		w := weeks[k]
		if w == nil {
			w = &weekCounts{from: k, to: o.Period.To, checkout: map[string]float64{}, carts: map[string]float64{}}
			weeks[k] = w
		}
		target := w.checkout
		if o.Metric == "cart_additions" {
			target = w.carts
		}
		if _, seen := target[o.Segment]; seen {
			w.ambiguous = true
		}
		target[o.Segment] = v
	}
	var weekList []weekValue
	for _, w := range weeks {
		if w.ambiguous {
			continue
		}
		var a, c float64
		cohort := ""
		if val, ok := w.checkout["all"]; ok {
			if cart, ok := w.carts["all"]; ok {
				a = val
				c = cart
				cohort = "all"
			}
		}
		if cohort == "" && len(w.checkout) > 0 && len(w.checkout) == len(w.carts) {
			var devices []string
			for device, val := range w.checkout {
				n, ok := w.carts[device]
				if !ok || (device != "desktop" && device != "mobile" && device != "tablet") {
					devices = nil
					break
				}
				devices = append(devices, device)
				a += val
				c += n
			}
			sort.Strings(devices)
			cohort = strings.Join(devices, ",")
		}
		if cohort != "" && c > 0 {
			weekList = append(weekList, weekValue{w.from, w.to, a, c, 100 * a / c, cohort})
		}
	}
	if len(weekList) == 0 {
		return nil
	}
	sort.Slice(weekList, func(i, j int) bool { return weekList[i].from < weekList[j].from })
	if len(weekList) > 24 {
		weekList = weekList[len(weekList)-24:]
	}
	sort.SliceStable(events, func(i, j int) bool {
		if events[i].date == events[j].date {
			return events[i].id < events[j].id
		}
		return events[i].date < events[j].date
	})
	return buildReleaseHistory(weekList, events)
}

func windowRate(weeks []weekValue) (rate, average float64, ok bool) {
	if len(weeks) != 4 {
		return
	}
	var orders, carts float64
	for i, w := range weeks {
		if w.cohort != weeks[0].cohort {
			return
		}
		if i > 0 {
			dt, _ := time.Parse(time.DateOnly, weeks[i-1].to)
			if w.from != dt.AddDate(0, 0, 1).Format(time.DateOnly) {
				return
			}
		}
		orders += w.checkouts
		carts += w.carts
	}
	if carts == 0 {
		return
	}
	return orders * 100 / carts, orders / float64(len(weeks)), true
}
func shortDay(day string) string {
	t, e := time.Parse(time.DateOnly, day)
	if e != nil {
		return day
	}
	return t.Format("02.01")
}
func fp(n float64) string { return strconv.FormatFloat(n, 'f', 1, 64) }
func releaseLabels(sl []weekValue) string {
	return shortDay(sl[0].from) + " – " + shortDay(sl[len(sl)-1].to)
}
func buildReleaseHistory(ws []weekValue, events []releaseEvent) *views.ReleaseHistory {
	view := &views.ReleaseHistory{}
	max := 0.0
	for _, w := range ws {
		if w.value > max {
			max = w.value
		}
	}
	max = math.Max(10, math.Ceil(max/10)*10)
	for i := 0; i <= 3; i++ {
		v := max * float64(i) / 3
		view.Grid = append(view.Grid, views.ReleaseTick{Y: fp(176 - 133*v/max), Label: strings.TrimSuffix(num(v), ",0")})
	}
	first, _ := time.Parse(time.DateOnly, ws[0].from)
	last, _ := time.Parse(time.DateOnly, ws[len(ws)-1].from)
	x := func(d string) float64 {
		date, _ := time.Parse(time.DateOnly, d)
		if last.Equal(first) {
			return 342
		}
		return 54 + 650*date.Sub(first).Hours()/last.Sub(first).Hours()
	}
	var path []string
	var lastDay string
	var priorCohort string
	for _, w := range ws {
		xx, yy := x(w.from), 176-133*w.value/max
		seg := "M"
		if lastDay != "" {
			p, _ := time.Parse(time.DateOnly, lastDay)
			if p.AddDate(0, 0, 1).Format(time.DateOnly) == w.from && priorCohort == w.cohort {
				seg = "L"
			}
		}
		path = append(path, seg+fp(xx)+" "+fp(yy))
		lastDay = w.to
		priorCohort = w.cohort
		view.Dots = append(view.Dots, views.ReleasePoint{X: fp(xx), Y: fp(yy), Value: num(w.value), Period: shortDay(w.from) + " – " + shortDay(w.to), Detail: strings.TrimSuffix(num(w.checkouts), ",0") + " оформлений / " + strings.TrimSuffix(num(w.carts), ",0") + " добавлений"})
	}
	view.Path = strings.Join(path, " ")
	view.First = shortDay(ws[0].from)
	view.Last = shortDay(ws[len(ws)-1].to)
	seen := map[string]bool{}
	for _, ev := range events {
		if seen[ev.id] || ev.date < ws[0].from || ev.date > ws[len(ws)-1].to {
			continue
		}
		seen[ev.id] = true
		xx := math.Min(704, x(ev.date))
		view.Events = append(view.Events, views.ReleaseMarker{X: fp(xx), ID: ev.id, Name: ev.name, Date: shortDay(ev.date)})
		if view.Pivot != "" {
			continue
		}
		pivot := sort.Search(len(ws), func(i int) bool { return ws[i].from >= ev.date })
		if pivot < 4 || pivot+4 > len(ws) {
			continue
		}
		before, after := ws[pivot-4:pivot], ws[pivot:pivot+4]
		if before[3].to >= ev.date || after[0].from != ev.date || before[0].cohort != after[0].cohort {
			continue
		}
		v1, n1, ok1 := windowRate(before)
		v2, n2, ok2 := windowRate(after)
		if !ok1 || !ok2 {
			continue
		}
		view.Pivot = ev.id
		view.BeforeRate = num(v1)
		view.AfterRate = num(v2)
		view.BeforeRange = releaseLabels(before)
		view.AfterRange = releaseLabels(after)
		view.BeforeCount = strings.TrimSuffix(num(n1), ",0")
		view.AfterCount = strings.TrimSuffix(num(n2), ",0")
	}
	return view
}

func releaseMeasurement(v *views.Investigation, record decision.Record) {
	if record.Task != nil {
		v.CorrectionStatus = "Выпуск исправления по задаче " + record.Task.ID + " не подтверждён. Эффект исправления ещё не измерен."
	}
	a := record.Assessment
	if a == nil {
		return
	}
	v.Assessment = a.Reason
	if a.ReleasedOn != "" {
		v.CorrectionStatus = "Дата выпуска исправления: " + shortDay(a.ReleasedOn) + ". Ожидается сопоставимый замер."
	}
	m := a.Measurement
	if a.Value == nil || m == nil || !m.Baseline.Complete || !m.Current.Complete || m.Baseline.Value == nil || m.Current.Value == nil {
		return
	}
	before, after := *m.Baseline.Value, *m.Current.Value
	v.Before = num(before) + " " + m.Baseline.Unit
	v.After = num(after) + " " + m.Current.Unit
	v.CorrectionStatus = ""
	peak := math.Max(before, after)
	if before >= 0 && after >= 0 && peak > 0 {
		v.MeasuredBeforeWidth = fp(100 * before / peak)
		v.MeasuredAfterWidth = fp(100 * after / peak)
	}
}
