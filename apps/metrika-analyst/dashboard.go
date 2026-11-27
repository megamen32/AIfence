package main

import (
	"context"
	"fmt"
	"math"
	"strconv"
	"strings"
	"sync"
	"time"
)

// The start page reads the last eight complete weeks from the configured
// source on open, so the owner sees the store's numbers without filling a form.

const dashWeeks = 8

var metricTitles = map[string]string{
	"visits": "Визиты", "users": "Посетители", "pageviews": "Просмотры",
	"orders": "Заказы", "revenue": "Выручка", "purchase_conversion": "Конверсия в покупку",
	"product_views": "Просмотры товаров", "add_to_cart": "Добавления в корзину", "average_order_value": "Средний чек",
	"orders_index": "Индекс заказов",
}

var monthsGen = []string{"", "января", "февраля", "марта", "апреля", "мая", "июня", "июля", "августа", "сентября", "октября", "ноября", "декабря"}
var monthsShort = []string{"", "янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

type dashWeek struct{ From, To time.Time }

type sparkPoint struct {
	X, Y         float64
	Label, Value string
}

type tickLine struct {
	Y     float64
	Label string
}

type metricTile struct {
	Key, Title, Value, Delta, Tone, Spark, Unit string
	Delay                                       int
}

type sourceCard struct {
	Mark, Title, Detail, Locator, Status, Tone string
}

type mainChart struct {
	Line, Area string
	Points     []sparkPoint
	Ticks      []tickLine
	Last       sparkPoint
	Prev       sparkPoint
}

type compareRow struct {
	Title, Before, After, Delta, Tone string
	BeforePct, AfterPct               float64
}

type compareView struct {
	Title, Period, Error string
	Rows                 []compareRow
	Warnings             []string
}

type dashView struct {
	State                string
	Eyebrow              string
	Headline, HeadTone   string
	Subline              string
	SourceChip           string
	Tiles                []metricTile
	Chart                *mainChart
	ChartTitle           string
	Sources              []sourceCard
	Notes                []string
	EmptyTitle           string
	EmptyText            string
	EmptyAction          string
	Compare              *compareView
	FormFrom, FormTo     string
	FormCurFrom, FormCur string
	ProductKey           string
	Metrics              string
}

func lastWeeks(now time.Time, n int) []dashWeek {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	wd := int(today.Weekday())
	if wd == 0 {
		wd = 7
	}
	lastSunday := today.AddDate(0, 0, -wd)
	out := make([]dashWeek, n)
	for i := 0; i < n; i++ {
		end := lastSunday.AddDate(0, 0, -7*(n-1-i))
		out[i] = dashWeek{end.AddDate(0, 0, -6), end}
	}
	return out
}

func weekLabel(w dashWeek) string {
	if w.From.Month() == w.To.Month() {
		return fmt.Sprintf("%d–%d %s", w.From.Day(), w.To.Day(), monthsGen[w.To.Month()])
	}
	return fmt.Sprintf("%d %s – %d %s", w.From.Day(), monthsGen[w.From.Month()], w.To.Day(), monthsGen[w.To.Month()])
}

func weekShort(w dashWeek) string {
	return fmt.Sprintf("%d %s", w.From.Day(), monthsShort[w.From.Month()])
}

func dashboardSandbox(c Settings) bool {
	path := strings.ToLower(c.EvidencePath)
	return strings.Contains(path, "aifence-sandbox.json")
}

func dashboardProduct(c Settings) string {
	if dashboardSandbox(c) {
		return "kaspersky_small_office_security"
	}
	return "store"
}

func dashboardWeeks(c Settings, now time.Time) []dashWeek {
	if dashboardSandbox(c) {
		return []dashWeek{
			{From: time.Date(2026, 9, 14, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 9, 20, 0, 0, 0, 0, time.UTC)},
			{From: time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC), To: time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC)},
		}
	}
	return lastWeeks(now, dashWeeks)
}

func dashMetrics(c Settings) []string {
	if dashboardSandbox(c) {
		return []string{"orders"}
	}
	if c.Ecommerce {
		return []string{"visits", "users", "orders", "purchase_conversion"}
	}
	return []string{"visits", "users", "pageviews"}
}

// ruNum formats a number the Russian way: thin-space thousands, decimal comma, true minus.
func ruNum(v float64, decimals int) string {
	neg := v < 0
	v = math.Abs(v)
	s := strconv.FormatFloat(v, 'f', decimals, 64)
	intPart, frac, _ := strings.Cut(s, ".")
	var b strings.Builder
	for i, r := range intPart {
		if i > 0 && (len(intPart)-i)%3 == 0 {
			b.WriteRune(' ')
		}
		b.WriteRune(r)
	}
	out := b.String()
	if frac != "" {
		out += "," + frac
	}
	if neg {
		out = "−" + out
	}
	return out
}

func valueText(v float64, unit string) string {
	switch unit {
	case "ratio":
		return ruNum(v*100, 2) + " %"
	case "RUB":
		if math.Abs(v) >= 1e6 {
			return ruNum(v/1e6, 2) + " млн ₽"
		}
		return ruNum(v, 0) + " ₽"
	case "percent":
		return ruNum(v, 1) + " %"
	}
	if v == math.Trunc(v) {
		return ruNum(v, 0)
	}
	return ruNum(v, 1)
}

func deltaText(pct float64) string {
	sign := "+"
	if pct < 0 {
		sign = ""
	}
	if math.Abs(pct) < 0.05 {
		return "0,0 %"
	}
	return sign + ruNum(pct, 1) + " %"
}

func deltaTone(pct float64) string {
	switch {
	case pct <= -5:
		return "down"
	case pct >= 5:
		return "up"
	}
	return "flat"
}

// series lays values out in a box; missing values break the line.
func series(vals []*float64, w, h, padX, padTop, padBottom float64) ([]sparkPoint, float64, float64) {
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, v := range vals {
		if v != nil {
			lo, hi = math.Min(lo, *v), math.Max(hi, *v)
		}
	}
	if math.IsInf(lo, 1) {
		return nil, 0, 0
	}
	if hi == lo {
		hi, lo = hi+1, lo-1
	}
	span := hi - lo
	lo -= span * 0.15
	hi += span * 0.1
	step := (w - 2*padX) / float64(max(len(vals)-1, 1))
	pts := make([]sparkPoint, 0, len(vals))
	for i, v := range vals {
		if v == nil {
			continue
		}
		y := padTop + (h-padTop-padBottom)*(1-(*v-lo)/(hi-lo))
		pts = append(pts, sparkPoint{X: math.Round((padX+step*float64(i))*10) / 10, Y: math.Round(y*10) / 10})
	}
	return pts, lo, hi
}

func pathOf(pts []sparkPoint) string {
	var b strings.Builder
	for i, p := range pts {
		if i == 0 {
			fmt.Fprintf(&b, "M%.1f %.1f", p.X, p.Y)
		} else {
			fmt.Fprintf(&b, " L%.1f %.1f", p.X, p.Y)
		}
	}
	return b.String()
}

func niceTicks(lo, hi float64, n int) []float64 {
	if hi <= lo {
		return nil
	}
	raw := (hi - lo) / float64(n)
	mag := math.Pow(10, math.Floor(math.Log10(raw)))
	step := mag
	for _, m := range []float64{1, 2, 2.5, 5, 10} {
		if raw <= m*mag {
			step = m * mag
			break
		}
	}
	var out []float64
	for v := math.Ceil(lo/step) * step; v <= hi; v += step {
		out = append(out, v)
	}
	return out
}

func (s Service) dashboard(ctx context.Context, now time.Time) dashView {
	c := s.config(ctx)
	weeks := dashboardWeeks(c, now)
	keys := dashMetrics(c)
	productKey := dashboardProduct(c)
	v := dashView{ProductKey: productKey, Metrics: strings.Join(keys, ",")}
	last, prev := weeks[len(weeks)-1], weeks[len(weeks)-2]
	v.FormFrom, v.FormTo = prev.From.Format(time.DateOnly), prev.To.Format(time.DateOnly)
	v.FormCurFrom, v.FormCur = last.From.Format(time.DateOnly), last.To.Format(time.DateOnly)

	path := c.EvidencePath
	switch {
	case path != "":
		v.SourceChip = "Выгрузка Метрики · " + path
	case c.Counter > 0:
		v.SourceChip = "Яндекс Метрика · подключённый счётчик"
	default:
		v.State = "setup"
		v.EmptyTitle = "Счётчик Метрики не подключён"
		v.EmptyText = "Приложение читает визиты, посетителей и заказы магазина напрямую из Яндекс Метрики. Пока не указан номер счётчика, показывать нечего."
		v.EmptyAction = "Откройте настройки приложения (шестерёнка справа вверху), укажите counter_id и подключите MCP Яндекс Метрики. После этого здесь сразу появятся последние восемь недель."
		return v
	}

	batches := make([]Batch, len(weeks))
	errs := make([]error, len(weeks))
	var wg sync.WaitGroup
	for i, w := range weeks {
		wg.Add(1)
		go func(i int, w dashWeek) {
			defer wg.Done()
			batches[i], errs[i] = s.query(ctx, Query{ProductKey: productKey, MetricKeys: keys, From: w.From.Format(time.DateOnly), To: w.To.Format(time.DateOnly), Segment: "all"}, c)
		}(i, w)
	}
	wg.Wait()

	values := map[string][]*float64{}
	units := map[string]string{}
	refs := map[string]Observation{}
	observedWeeks := 0
	estimated := false
	for i := range weeks {
		got := false
		if errs[i] == nil {
			for _, o := range batches[i].Observations {
				if values[o.MetricKey] == nil {
					values[o.MetricKey] = make([]*float64, len(weeks))
				}
				if o.Unit != "" {
					units[o.MetricKey] = o.Unit
				}
				if (o.Status == "observed" || o.Status == "estimated") && o.Value != nil {
					x := *o.Value
					values[o.MetricKey][i] = &x
					refs[o.MetricKey] = o
					got = true
					estimated = estimated || o.Status == "estimated"
				}
			}
		}
		if got {
			observedWeeks++
		}
	}
	if observedWeeks == 0 {
		v.State = "nodata"
		v.EmptyTitle = "Метрика не вернула данные за последние недели"
		reason := "Источник ответил, но значений за полные недели нет."
		for _, e := range errs {
			if e != nil {
				reason = "Источник не ответил: " + e.Error() + "."
				break
			}
		}
		if path != "" {
			reason = "В выбранной выгрузке нет недельных итогов за последние восемь недель."
		}
		v.EmptyText = reason
		v.EmptyAction = "Проверьте подключение в чате приложения: «Проверь подключение Метрики». Если нужен другой период, сравните его ниже."
		return v
	}
	v.State = "ok"

	for i, k := range keys {
		vals := values[k]
		t := metricTile{Key: k, Title: metricTitles[k], Unit: units[k], Value: "нет данных", Tone: "flat", Delay: i * 90}
		if vals != nil {
			if vals[len(vals)-1] != nil {
				t.Value = valueText(*vals[len(vals)-1], units[k])
			}
			if vals[len(vals)-1] != nil && vals[len(vals)-2] != nil && *vals[len(vals)-2] != 0 {
				pct := (*vals[len(vals)-1] - *vals[len(vals)-2]) / math.Abs(*vals[len(vals)-2]) * 100
				t.Delta, t.Tone = deltaText(pct)+" к прошлой неделе", deltaTone(pct)
			}
			pts, _, _ := series(vals, 120, 34, 2, 4, 4)
			t.Spark = pathOf(pts)
		}
		v.Tiles = append(v.Tiles, t)
	}

	lead := keys[0]
	vals := values[lead]
	pts, lo, hi := series(vals, 680, 230, 34, 26, 30)
	idx := 0
	for i, val := range vals {
		if val == nil {
			continue
		}
		pts[idx].Label = weekShort(weeks[i])
		pts[idx].Value = valueText(*val, units[lead])
		idx++
	}
	if len(pts) > 0 {
		ch := &mainChart{Points: pts, Line: pathOf(pts), Last: pts[len(pts)-1]}
		if len(pts) > 1 {
			ch.Prev = pts[len(pts)-2]
		}
		ch.Area = ch.Line + fmt.Sprintf(" L%.1f %.1f L%.1f %.1f Z", pts[len(pts)-1].X, 230-30.0, pts[0].X, 230-30.0)
		for _, t := range niceTicks(lo, hi, 4) {
			y := 26 + (230-26-30)*(1-(t-lo)/(hi-lo))
			ch.Ticks = append(ch.Ticks, tickLine{Y: math.Round(y*10) / 10, Label: valueText(t, units[lead])})
		}
		v.Chart = ch
		v.ChartTitle = metricTitles[lead] + " по неделям"
	}

	v.Eyebrow = "МАГАЗИН · НЕДЕЛЯ " + strings.ToUpper(weekLabel(last))
	if dashboardSandbox(c) {
		v.Eyebrow = "KASPERSKY SMALL OFFICE SECURITY · ПЕРИОД " + strings.ToUpper(weekLabel(last))
	}
	if a, b := vals[len(vals)-1], vals[len(vals)-2]; a != nil && b != nil && *b != 0 {
		pct := (*a - *b) / math.Abs(*b) * 100
		v.HeadTone = deltaTone(pct)
		switch v.HeadTone {
		case "down":
			v.Headline = fmt.Sprintf("%s упали на %s за неделю", metricTitles[lead], strings.TrimPrefix(ruNum(math.Abs(pct), 1), "−")+" %")
		case "up":
			v.Headline = fmt.Sprintf("%s выросли на %s за неделю", metricTitles[lead], ruNum(pct, 1)+" %")
		default:
			v.Headline = fmt.Sprintf("%s на уровне прошлой недели", metricTitles[lead])
		}
		v.Subline = fmt.Sprintf("%s: %s против %s неделей раньше (%s).", weekLabel(last), valueText(*a, units[lead]), valueText(*b, units[lead]), weekLabel(prev))
	} else {
		v.Headline = metricTitles[lead] + " за последние недели"
		v.Subline = "За одну из двух последних недель значение не получено, сравнение недоступно."
	}

	for _, k := range keys {
		o, ok := refs[k]
		if !ok {
			v.Sources = append(v.Sources, sourceCard{Mark: "М", Title: metricTitles[k], Detail: "не получено", Status: "нет данных", Tone: "amber"})
			continue
		}
		card := sourceCard{Mark: "М", Title: metricTitles[k], Detail: "Яндекс Метрика", Locator: "Период и показатель из источника", Status: "наблюдение", Tone: "green"}
		if o.Status == "estimated" {
			card.Status, card.Tone = "выборка", "amber"
		}
		if d, ok := metrics[k]; ok && strings.HasPrefix(o.SourceRef.Locator, "run_report") {

			card.Detail = "Отчёт Метрики · счётчик " + strconv.FormatInt(c.Counter, 10)
			card.Locator = fmt.Sprintf("run_report %s %s…%s", d.api, o.Period.From, o.Period.To)
		} else if len(card.Locator) > 120 {
			card.Locator = card.Locator[:117] + "…"
		}
		v.Sources = append(v.Sources, card)
	}
	if !c.Ecommerce && !dashboardSandbox(c) {
		v.Notes = append(v.Notes, "Заказы и конверсия скрыты: e-commerce в счётчике не подтверждён (настройка ecommerce_confirmed).")
	}
	if estimated {
		v.Notes = append(v.Notes, "Часть значений посчитана Метрикой по выборке, они помечены жёлтым.")
	}
	if observedWeeks < len(weeks) {
		v.Notes = append(v.Notes, fmt.Sprintf("Получено %d из %d недель, пропуски на графике не заполняются.", observedWeeks, len(weeks)))
	}
	return v
}

func compareFromBatch(b Batch, in Compare) *compareView {
	cv := &compareView{Title: "Ваше сравнение", Period: fmt.Sprintf("%s…%s → %s…%s", in.BaselineFrom, in.BaselineTo, in.CurrentFrom, in.CurrentTo)}
	before, after, pct := map[string]Observation{}, map[string]Observation{}, map[string]*float64{}
	var order []string
	for _, o := range b.Observations {
		switch {
		case strings.HasSuffix(o.MetricKey, ".relative_change_pct"):
			pct[o.ComparisonKey] = o.Value
		case strings.Contains(o.MetricKey, ".change") || strings.HasSuffix(o.MetricKey, ".absolute_change"):
		case o.Period.From == in.BaselineFrom:
			if _, seen := before[o.MetricKey]; !seen {
				order = append(order, o.MetricKey)
			}
			before[o.MetricKey] = o
		default:
			after[o.MetricKey] = o
		}
	}
	for _, k := range order {
		a, z := before[k], after[k]
		r := compareRow{Title: metricTitles[k], Before: "нет данных", After: "нет данных", Tone: "flat"}
		if r.Title == "" {
			r.Title = k
		}
		var av, zv float64
		if a.Value != nil {
			av = *a.Value
			r.Before = valueText(av, a.Unit)
		}
		if z.Value != nil {
			zv = *z.Value
			r.After = valueText(zv, z.Unit)
		}
		if p := pct[k]; p != nil {
			r.Delta, r.Tone = deltaText(*p), deltaTone(*p)
		}
		if m := math.Max(math.Abs(av), math.Abs(zv)); m > 0 {
			r.BeforePct, r.AfterPct = math.Abs(av)/m*100, math.Abs(zv)/m*100
		}
		cv.Rows = append(cv.Rows, r)
	}
	cv.Warnings = b.Warnings
	return cv
}
