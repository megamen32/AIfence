package handlers

import (
	"agent/demand"
	"agent/views"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

type Queries interface {
	Top(context.Context, demand.TopInput) (demand.Result, error)
	Dynamics(context.Context, demand.DynamicsInput) (demand.Result, error)
	Regions(context.Context, demand.RegionsInput) (demand.Result, error)
}
type Handler struct{ service Queries }

func New(s Queries) *Handler {
	if s == nil {
		panic("handlers: queries required")
	}
	return &Handler{service: s}
}
func defaults() views.Form {
	return views.Form{Phrase: "касперский для бизнеса", Kind: "top", RegionIDs: "225", RegionLabel: views.RegionName("225"), Device: "all", RegionMode: "regions", FromMonth: "2025-10", ToMonth: "2026-09", PeriodPreset: "12"}
}
func presets() []string {
	return []string{"касперский для бизнеса", "Kaspersky Endpoint Security", "Astra Linux", "антивирус для бизнеса"}
}
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.Index(views.HomeView{Form: defaults(), Presets: presets()}).Render(r.Context(), w)
}
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := r.ParseForm(); err != nil {
		return feedback(w, r, views.ResultView{Status: "invalid_request", Message: "Не удалось прочитать параметры. Проверьте фразу и повторите запрос."})
	}
	f := views.Form{Phrase: r.FormValue("phrase"), Kind: r.FormValue("kind"), RegionIDs: r.FormValue("region_ids"), Device: r.FormValue("device"), RegionMode: r.FormValue("region_mode"), FromMonth: r.FormValue("from_month"), ToMonth: r.FormValue("to_month"), PeriodPreset: r.FormValue("period_preset")}
	var ids []int
	if f.Kind != "regions" {
		for _, value := range strings.Split(f.RegionIDs, ",") {
			if strings.TrimSpace(value) == "" {
				continue
			}
			id, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || id < 1 {
				return feedback(w, r, views.ResultView{Status: "invalid_request", Message: "Выберите регион из списка."})
			}
			ids = append(ids, id)
		}
	}
	devices := []string{f.Device}
	if f.Device == "" {
		devices = nil
	}
	var out demand.Result
	var err error
	switch f.Kind {
	case "top":
		out, err = h.service.Top(r.Context(), demand.TopInput{Phrase: f.Phrase, RegionIDs: ids, Devices: devices, Limit: 20})
	case "dynamics":
		out, err = h.service.Dynamics(r.Context(), demand.DynamicsInput{Phrase: f.Phrase, FromMonth: f.FromMonth, ToMonth: f.ToMonth, RegionIDs: ids, Devices: devices})
	case "regions":
		out, err = h.service.Regions(r.Context(), demand.RegionsInput{Phrase: f.Phrase, RegionMode: f.RegionMode})
	default:
		return feedback(w, r, views.ResultView{Status: "invalid_request", Message: "Выберите вид исследования."})
	}
	if err != nil {
		return feedback(w, r, views.ResultView{Status: "request_error", Message: "Не удалось получить статистику. Предыдущий результат сохранён; повторите запрос позже."})
	}
	v := resultView(out)
	if v.IsError() {
		return feedback(w, r, v)
	}
	w.Header().Set("X-Wordstat-Result", "success")
	if r.Header.Get("HX-Request") == "true" {
		return views.Result(v).Render(r.Context(), w)
	}
	f.RegionLabel = views.RegionCaption(ids)
	return views.Index(views.HomeView{Form: f, Presets: presets(), Result: &v}).Render(r.Context(), w)
}
func feedback(w http.ResponseWriter, r *http.Request, v views.ResultView) error {
	w.Header().Set("HX-Retarget", "#query-feedback")
	w.Header().Set("HX-Reswap", "innerHTML")
	w.Header().Set("X-Wordstat-Result", "error")
	code := http.StatusBadGateway
	if v.Status == "invalid_request" || v.Status == "unavailable_period" {
		code = http.StatusUnprocessableEntity
	}
	w.WriteHeader(code)
	return views.Feedback(v).Render(r.Context(), w)
}
func resultView(out demand.Result) views.ResultView {
	v := views.ResultView{Status: out.Status, SourceStatus: out.SourceStatus, Message: out.Message, Kind: out.Kind, Phrase: out.Phrase, WindowNote: out.Source.WindowNote, SourceURL: out.Source.Documentation, CheckedAt: out.Source.CheckedAt, RegionLookupMessage: out.RegionLookupMessage}
	request := views.Snapshot{Kind: out.Kind, Phrase: out.Phrase}
	if out.Kind == "regions" {
		request.RegionMode = out.RegionMode
		v.Context = regionModeName(out.RegionMode) + " • Все устройства • Точное окно не сообщается"
	} else {
		request.RegionIDs = out.RegionIDs
		request.Device = normalizedDevice(out.Devices)
		v.Context = views.RegionCaption(out.RegionIDs) + " • " + deviceName(request.Device)
	}
	if out.RequestedPeriod != nil {
		request.FromMonth = out.RequestedPeriod.From
		request.ToMonth = out.RequestedPeriod.To
		v.Period = monthName(out.RequestedPeriod.From) + " – " + monthName(out.RequestedPeriod.To)
		v.Context += " • " + v.Period
	} else if out.Kind != "regions" {
		v.Context += " • Точное окно не сообщается"
	}
	v.RequestJSON = views.JSON(request)
	for _, row := range out.Rows {
		display := "Источник не сообщил частотность"
		if row.Count != nil {
			display = countText(*row.Count)
		}
		detail := ""
		if out.Kind == "regions" && row.RegionNameStatus != "resolved" {
			detail = "Название не получено • код " + row.RegionID
		}
		group := groupName(row.Group)
		if out.Kind == "regions" {
			group = "География"
		} else if out.Kind == "dynamics" {
			group = "По месяцам"
		}
		v.Rows = append(v.Rows, views.Row{Group: group, Label: row.Label, Count: display, RawCount: row.Count, RegionID: row.RegionID, RegionNameStatus: row.RegionNameStatus, Detail: detail})
	}
	if out.Kind == "top" {
		if total, ok := integer(out.Data["totalCount"]); ok && total >= 0 {
			v.HasTotal = true
			v.Total = countText(total)
		}
		v.TotalDetail = "Частотность исходной фразы. Точное окно API не сообщает."
		for _, row := range out.Rows {
			rr := rankRow(row)
			if row.Group == "associations" {
				v.Associated = append(v.Associated, rr)
			} else {
				v.Main = append(v.Main, rr)
			}
		}
		sortRanks(v.Main)
		sortRanks(v.Associated)
		v.MainMax = rankMax(v.Main)
		v.AssociatedMax = rankMax(v.Associated)
	} else if out.Kind == "regions" {
		for _, row := range out.Rows {
			rr := rankRow(row)
			if row.RegionNameStatus != "resolved" {
				rr.Label = "Название не получено"
				rr.Detail = "Код " + row.RegionID
			}
			v.Regions = append(v.Regions, rr)
		}
		sortRanks(v.Regions)
		v.RegionMax = rankMax(v.Regions)
	} else if out.Kind == "dynamics" {
		buildChart(&v, out)
	}
	if len(out.Data) > 0 {
		raw, _ := json.MarshalIndent(out.Data, "", "  ")
		v.Raw = string(raw)
	}
	v.SourceDetails = "Яндекс Wordstat · проверка источника " + out.Source.CheckedAt + ". Период, регион и устройство указаны рядом с результатом."
	metadata := map[string]any{"source": out.Source, "source_status": out.SourceStatus, "request": request, "region_lookup_status": out.RegionLookupStatus}
	raw, _ := json.MarshalIndent(metadata, "", "  ")
	v.Metadata = string(raw)
	return v
}
func normalizedDevice(devices []string) string {
	if len(devices) == 0 {
		return "all"
	}
	return strings.Join(devices, ",")
}
func deviceName(device string) string {
	switch device {
	case "desktop":
		return "Компьютеры"
	case "phone":
		return "Телефоны"
	case "tablet":
		return "Планшеты"
	default:
		return "Все устройства"
	}
}
func regionModeName(mode string) string {
	switch mode {
	case "cities":
		return "Города"
	case "all":
		return "Все уровни географии"
	default:
		return "Регионы"
	}
}
func groupName(group string) string {
	switch group {
	case "associations":
		return "Похожие запросы"
	case "topRequests", "results":
		return "Основные формулировки"
	case "dynamics":
		return "По месяцам"
	default:
		return group
	}
}
func integer(value any) (int64, bool) {
	if value == nil {
		return 0, false
	}
	n, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
	return n, err == nil
}
func countText(n int64) string {
	return groupedDigits(strconv.FormatInt(n, 10))
}
func axisCountText(n float64) string {
	return groupedDigits(strconv.FormatFloat(n, 'f', 0, 64))
}
func groupedDigits(s string) string {
	start := 0
	if strings.HasPrefix(s, "-") {
		start = 1
	}
	for i := len(s) - 3; i > start; i -= 3 {
		s = s[:i] + "\u00a0" + s[i:]
	}
	return s
}
func monthKey(value string) string {
	if len(value) >= 7 {
		if _, err := time.Parse("2006-01", value[:7]); err == nil {
			return value[:7]
		}
	}
	return ""
}
func monthName(value string) string {
	key := monthKey(value)
	if key == "" {
		return value
	}
	t, _ := time.Parse("2006-01", key)
	names := []string{"январь", "февраль", "март", "апрель", "май", "июнь", "июль", "август", "сентябрь", "октябрь", "ноябрь", "декабрь"}
	return names[int(t.Month())-1] + " " + strconv.Itoa(t.Year())
}
func rankRow(row demand.Row) views.RankedRow {
	v := views.RankedRow{Label: row.Label, RawCount: row.Count, Count: "Источник не сообщил частотность", BarValue: "0"}
	if row.Count != nil {
		v.Count = countText(*row.Count)
		v.BarValue = strconv.FormatInt(*row.Count, 10)
	}
	return v
}
func sortRanks(rows []views.RankedRow) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].RawCount == nil {
			return false
		}
		if rows[j].RawCount == nil {
			return true
		}
		return *rows[i].RawCount > *rows[j].RawCount
	})
}
func rankMax(rows []views.RankedRow) string {
	var max int64 = 1
	for _, row := range rows {
		if row.RawCount != nil && *row.RawCount > max {
			max = *row.RawCount
		}
	}
	return strconv.FormatInt(max, 10)
}
func buildChart(v *views.ResultView, out demand.Result) {
	if out.RequestedPeriod == nil {
		return
	}
	start, e1 := time.Parse("2006-01", out.RequestedPeriod.From)
	end, e2 := time.Parse("2006-01", out.RequestedPeriod.To)
	if e1 != nil || e2 != nil || end.Before(start) || end.Sub(start) > 4*366*24*time.Hour {
		return
	}
	byMonth := map[string]demand.Row{}
	ambiguous := map[string]bool{}
	for _, row := range out.Rows {
		key := monthKey(row.Label)
		if key == "" {
			continue
		}
		if _, exists := byMonth[key]; exists {
			ambiguous[key] = true
		}
		byMonth[key] = row
	}
	chart := views.Chart{Phrase: out.Phrase}
	var maxCount int64
	for month := start; !month.After(end); month = month.AddDate(0, 1, 0) {
		key := month.Format("2006-01")
		p := views.ChartPoint{Month: key, Label: monthName(key), Count: "Источник не сообщил частотность", State: "missing"}
		if row, ok := byMonth[key]; ok && !ambiguous[key] && row.Count != nil && *row.Count >= 0 {
			p.Count = countText(*row.Count)
			p.RawDate = row.Label
			p.State = "observed"
			v.ObservedCount++
			if *row.Count > maxCount {
				maxCount = *row.Count
			}
		} else if ambiguous[key] {
			p.State = "ambiguous"
		}
		chart.Points = append(chart.Points, p)
	}
	max, step := niceScale(maxCount)
	for i := range chart.Points {
		p := &chart.Points[i]
		if p.State != "observed" {
			continue
		}
		y := float64(*byMonth[p.Month].Count) / max
		p.Y = &y
	}
	for i := int(math.Round(max / step)); i >= 0; i-- {
		value := step * float64(i)
		v.Axis = append(v.Axis, axisCountText(value))
		v.GridY = append(v.GridY, fmt.Sprintf("%.2f", 300*(1-value/max)))
	}
	var segment []string
	flush := func() {
		if len(segment) > 0 {
			if len(segment) == 1 {
				coordinates := strings.Split(segment[0], ",")
				x, _ := strconv.ParseFloat(coordinates[0], 64)
				y, _ := strconv.ParseFloat(coordinates[1], 64)
				v.Dots = append(v.Dots, views.ChartDot{Left: fmt.Sprintf("%.4f", x/10), Top: fmt.Sprintf("%.4f", y/3)})
			}
			v.Segments = append(v.Segments, strings.Join(segment, " "))
			segment = nil
		}
	}
	var first, last, peak *views.ChartPoint
	for i := range chart.Points {
		p := &chart.Points[i]
		if p.Y == nil {
			flush()
			continue
		}
		x := 500.0
		if len(chart.Points) > 1 {
			x = float64(i) * 1000 / float64(len(chart.Points)-1)
		}
		segment = append(segment, fmt.Sprintf("%.2f,%.2f", x, 300*(1-*p.Y)))
		if first == nil {
			first = p
		}
		last = p
		if peak == nil || *p.Y > *peak.Y {
			peak = p
		}
	}
	flush()
	if first != nil {
		v.Metrics = []views.Metric{{Label: "Первый месяц с данными", Value: first.Count, Detail: first.Label}, {Label: "Последний месяц с данными", Value: last.Count, Detail: last.Label}, {Label: "Максимум среди полученных", Value: peak.Count, Detail: peak.Label}}
	}
	v.HasChart = true
	v.PointCount = len(chart.Points)
	v.ChartJSON = views.JSON(chart)
}
func niceScale(n int64) (max, step float64) {
	if n <= 4 {
		return math.Max(1, float64(n)), 1
	}
	raw := float64(n) / 4
	unit := math.Pow(10, math.Floor(math.Log10(raw)))
	for _, multiple := range []float64{1, 2, 2.5, 5, 10} {
		candidate := math.Max(1, math.Ceil(unit*multiple))
		if candidate >= raw {
			return math.Ceil(float64(n)/candidate) * candidate, candidate
		}
	}
	step = math.Ceil(unit * 10)
	return math.Ceil(float64(n)/step) * step, step
}
