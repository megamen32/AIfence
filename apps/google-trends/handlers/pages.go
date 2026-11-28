package handlers

import (
	"agent/trends"
	"agent/views"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Pages struct{ service *trends.Service }

func New(service *trends.Service) *Pages {
	if service == nil {
		panic("handlers: service required")
	}
	return &Pages{service: service}
}
func defaultForm(now time.Time) views.Form {
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	return views.Form{Mode: "compare", Terms: []string{"Kaspersky Endpoint Security", "Astra Linux"}, Geo: "RU", GeoLabel: views.GeoName("RU"), From: month.AddDate(-1, 0, 0).Format(time.DateOnly), To: month.AddDate(0, 0, -1).Format(time.DateOnly), Property: "web", Category: "0", Region: "region", Today: now.Format(time.DateOnly), PeriodPreset: "12m"}
}
func (p *Pages) Home(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	now := time.Now().UTC()
	form, ok := deepLinkForm(r.URL.Query(), now)
	if !ok {
		return views.Page(defaultForm(now), nil).Render(r.Context(), w)
	}
	query := trends.Query{Terms: form.Terms, Geo: form.Geo, From: form.From, To: form.To, Property: form.Property, Category: form.Category, Region: form.Region}
	var result trends.Result
	var err error
	switch form.Mode {
	case "compare":
		result, err = p.service.Compare(r.Context(), query)
	case "regions":
		result, err = p.service.Regional(r.Context(), query)
	case "related":
		result, err = p.service.Related(r.Context(), query)
	}
	if err != nil {
		result = trends.Result{Status: trends.UpstreamError, Mode: form.Mode, Request: trends.Request{Terms: form.Terms, Geo: form.Geo, From: form.From, To: form.To, Property: form.Property, Category: form.Category, Region: form.Region}, Message: "Источник не ответил. Статистика не получена."}
	}
	vm := resultModel(result)
	return views.Page(form, &vm).Render(r.Context(), w)
}

func deepLinkForm(values url.Values, now time.Time) (views.Form, bool) {
	form := defaultForm(now)
	rawQuery := strings.TrimSpace(values.Get("q"))
	if rawQuery == "" {
		return form, false
	}
	terms := strings.Split(rawQuery, ",")
	for i, term := range terms {
		terms[i] = strings.TrimSpace(term)
		if terms[i] == "" || strings.ContainsAny(terms[i], "\r\n\x00") {
			return defaultForm(now), false
		}
	}
	form.Mode = values.Get("view")
	if form.Mode == "" {
		form.Mode = "compare"
	}
	if form.Mode != "compare" && form.Mode != "regions" && form.Mode != "related" {
		return defaultForm(now), false
	}
	minTerms := 1
	if form.Mode == "compare" {
		minTerms = 2
	}
	if form.Mode == "related" {
		minTerms = 1
	}
	if len(terms) < minTerms || len(terms) > 5 {
		return defaultForm(now), false
	}
	form.Terms = terms
	if rawGeo, present := values["geo"]; present {
		if len(rawGeo) != 1 || !regexp.MustCompile(`^[A-Z]{2}(?:-[A-Za-z0-9-]{1,12})?$`).MatchString(rawGeo[0]) {
			return defaultForm(now), false
		}
		form.Geo = rawGeo[0]
	}
	form.GeoLabel = views.GeoName(form.Geo)
	from, hasFrom := values["from"]
	to, hasTo := values["to"]
	if hasFrom != hasTo || (hasFrom && (len(from) != 1 || len(to) != 1)) {
		return defaultForm(now), false
	}
	if hasFrom {
		form.From, form.To = from[0], to[0]
		start, startErr := time.Parse(time.DateOnly, form.From)
		end, endErr := time.Parse(time.DateOnly, form.To)
		if startErr != nil || endErr != nil || start.After(end) {
			return defaultForm(now), false
		}
		form.PeriodPreset = "custom"
	}
	if form.Mode == "related" && len(form.Terms) != 1 {
		return defaultForm(now), false
	}
	return form, true
}
func (p *Pages) Query(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("HX-Retarget", "#query-feedback")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("X-Trends-Result", "error")
		w.WriteHeader(http.StatusBadRequest)
		return views.Feedback(views.ResultModel{Status: trends.InvalidInput, Message: "Не удалось прочитать параметры. Попробуйте отправить форму еще раз."}).Render(r.Context(), w)
	}
	terms := []string{}
	for _, raw := range r.Form["terms"] {
		for _, line := range strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n") {
			if text := strings.TrimSpace(line); text != "" {
				terms = append(terms, text)
			}
		}
	}
	f := views.Form{Mode: r.Form.Get("mode"), Terms: terms, Geo: r.Form.Get("geo"), From: r.Form.Get("from"), To: r.Form.Get("to"), Property: r.Form.Get("property"), Category: r.Form.Get("category"), Region: r.Form.Get("region"), PeriodPreset: r.Form.Get("period_preset"), Today: time.Now().UTC().Format(time.DateOnly)}
	f.GeoLabel = views.GeoName(f.Geo)
	f.RelatedTerm, _ = strconv.Atoi(r.Form.Get("related_term"))
	queryTerms := terms
	if f.Mode == "related" && f.RelatedTerm >= 0 && f.RelatedTerm < len(terms) {
		queryTerms = []string{terms[f.RelatedTerm]}
	}
	q := trends.Query{Terms: queryTerms, Geo: f.Geo, From: f.From, To: f.To, Property: f.Property, Category: f.Category}
	var result trends.Result
	var err error
	switch f.Mode {
	case "compare":
		result, err = p.service.Compare(r.Context(), q)
	case "regions":
		q.Region = f.Region
		result, err = p.service.Regional(r.Context(), q)
	case "related":
		result, err = p.service.Related(r.Context(), q)
	default:
		result = trends.Result{Status: trends.InvalidInput, Message: "Выберите вид исследования."}
	}
	if err != nil {
		result = trends.Result{Status: trends.UpstreamError, Message: "Источник не ответил. Предыдущее сравнение сохранено."}
	}
	vm := resultModel(result)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if vm.IsError() {
		w.Header().Set("HX-Retarget", "#query-feedback")
		w.Header().Set("HX-Reswap", "innerHTML")
		w.Header().Set("X-Trends-Result", "error")
		status := http.StatusBadGateway
		if result.Status == trends.InvalidInput || result.Status == trends.UnavailablePeriod {
			status = http.StatusUnprocessableEntity
		}
		w.WriteHeader(status)
		if r.Header.Get("HX-Request") == "true" {
			return views.Feedback(vm).Render(r.Context(), w)
		}
		return views.Page(f, &vm).Render(r.Context(), w)
	}
	w.Header().Set("X-Trends-Result", "success")
	if r.Header.Get("HX-Request") == "true" {
		return views.Results(vm).Render(r.Context(), w)
	}
	return views.Page(f, &vm).Render(r.Context(), w)
}
func resultModel(r trends.Result) views.ResultModel {
	property := map[string]string{"web": "Веб-поиск", "youtube": "YouTube", "news": "Новости", "images": "Картинки", "froogle": "Покупки"}[r.Request.Property]
	if property == "" {
		property = r.Request.Property
	}
	title := map[string]string{"compare": "Интерес во времени", "regions": "Где интересуются чаще", "related": "Что ищут вместе с этим"}[r.Mode]
	snapshot := views.Snapshot{Mode: r.Mode, Terms: r.Request.Terms, Geo: r.Request.Geo, From: r.Request.From, To: r.Request.To, Property: r.Request.Property, Category: r.Request.Category, Region: r.Request.Region}
	v := views.ResultModel{Status: r.Status, Message: r.Message, Mode: r.Mode, Title: title, Terms: r.Request.Terms, Period: friendlyDate(r.Request.From) + " – " + friendlyDate(r.Request.To), Geo: views.GeoName(r.Request.Geo), Property: property, Category: r.Request.Category, Checked: r.Source.CheckedAt, ExploreURL: r.Source.ExploreURL, Notes: r.Notes, SourceDetails: "Google Trends · проверка источника " + r.Source.CheckedAt + ". Период, регион и тип поиска указаны рядом с графиком.", RequestJSON: views.JSON(snapshot), MetadataJSON: views.JSON(struct {
		Request trends.Request `json:"request"`
		Source  trends.Source  `json:"source"`
	}{r.Request, r.Source})}
	for i, s := range r.Summaries {
		value := "Нет общей выборки"
		if s.MeanIndex != nil {
			value = strings.ReplaceAll(fmt.Sprintf("%.1f", *s.MeanIndex), ".", ",")
		}
		v.Metrics = append(v.Metrics, views.Metric{Term: s.Term, Value: value, Coverage: fmt.Sprintf("%d из %d интервалов", s.CommonCompletePoints, s.CommonCompletePoints+s.ExcludedPoints), Index: i})
		v.CommonPoints = s.CommonCompletePoints
		v.ExcludedPoints = s.ExcludedPoints
	}
	chart := views.ChartData{Terms: r.Request.Terms, Points: []views.ChartPoint{}}
	for _, point := range r.Points {
		quality := "Завершенность неизвестна"
		if point.IsPartial != nil {
			quality = "Полный интервал"
			if *point.IsPartial {
				quality = "Незавершенный интервал"
			}
		}
		row := views.Row{Label: friendlyInterval(point.Date), Detail: quality}
		cp := views.ChartPoint{Date: row.Label, RawDate: point.Date, Timestamp: point.Timestamp, Partial: point.IsPartial, Quality: quality, Values: []views.ChartValue{}}
		for _, score := range point.Values {
			cell := scoreCell(score)
			row.Cells = append(row.Cells, cell)
			cp.Values = append(cp.Values, views.ChartValue{Term: score.Term, Label: cell.Label, Detail: cell.Detail, Index: score.Index, State: score.State, HasData: score.HasData})
		}
		v.Rows = append(v.Rows, row)
		chart.Points = append(chart.Points, cp)
	}
	if len(r.Points) > 0 {
		v.Series = chartSeries(r.Points, r.Request.Terms)
		v.ChartJSON = views.JSON(chart)
		v.TotalPoints = len(r.Points)
	}
	for _, region := range r.Regions {
		row := views.Row{Label: region.Name, Detail: region.Code}
		ranked := views.RankedRow{Label: region.Name}
		for _, score := range region.Values {
			cell := scoreCell(score)
			row.Cells = append(row.Cells, cell)
			ranked.Cells = append(ranked.Cells, cell)
		}
		v.Rows = append(v.Rows, row)
		if len(v.Regions) < 12 {
			v.Regions = append(v.Regions, ranked)
		}
	}
	if r.Related != nil {
		for _, group := range []struct {
			name string
			rows []trends.RelatedRow
		}{{"Популярные", r.Related.Top}, {"Растущие", r.Related.Rising}} {
			for _, item := range group.rows {
				value := item.Raw
				if value == "" && item.Value != nil {
					value = strconv.FormatFloat(*item.Value, 'f', -1, 64)
				}
				metric := "Индекс"
				if item.Metric == "growth_percent" {
					metric = "Рост, %"
				}
				if item.Metric == "breakout" {
					metric = "Резкий рост"
				}
				v.Rows = append(v.Rows, views.Row{Label: item.Query, Detail: group.name, Cells: []views.Cell{{Label: value, Detail: metric}}})
				related := views.RelatedItem{Query: item.Query, Value: value, Metric: metric, Index: item.Value}
				if group.name == "Популярные" {
					v.Popular = append(v.Popular, related)
				} else {
					v.Rising = append(v.Rising, related)
				}
			}
		}
		v.Terms = []string{"Значение"}
	}
	return v
}
func scoreCell(s trends.Score) views.Cell {
	switch s.State {
	case "missing":
		return views.Cell{Label: "Источник не сообщил значение", Detail: "Показатель отсутствует в ответе"}
	case "below_one":
		return views.Cell{Label: "<1", Detail: "Значение ниже единицы"}
	default:
		label := s.Raw
		if label == "" && s.Index != nil {
			label = strconv.FormatFloat(*s.Index, 'f', -1, 64)
		}
		detail := ""
		if s.HasData == nil {
			detail = "Статус показателя: источник не сообщил значение"
		}
		return views.Cell{Label: label, Detail: detail, Index: s.Index}
	}
}
func chartSeries(points []trends.Point, terms []string) []views.ChartSeries {
	out := make([]views.ChartSeries, 0, len(terms))
	for ti, term := range terms {
		series := views.ChartSeries{Name: term, Index: ti}
		segment := []string{}
		flush := func() {
			if len(segment) > 0 {
				series.Segments = append(series.Segments, strings.Join(segment, " "))
				segment = nil
			}
		}
		for i, p := range points {
			if p.IsPartial == nil || *p.IsPartial || ti >= len(p.Values) || p.Values[ti].HasData == nil || !*p.Values[ti].HasData || p.Values[ti].Index == nil {
				flush()
				continue
			}
			x := 0.0
			if len(points) > 1 {
				x = float64(i) * 1000 / float64(len(points)-1)
			}
			y := 300 - *p.Values[ti].Index*3
			segment = append(segment, fmt.Sprintf("%.2f,%.2f", x, y))
		}
		flush()
		out = append(out, series)
	}
	return out
}

var russianMonths = []string{"", "янв", "фев", "мар", "апр", "мая", "июн", "июл", "авг", "сен", "окт", "ноя", "дек"}

func friendlyDate(iso string) string {
	d, err := time.Parse(time.DateOnly, iso)
	if err != nil {
		return iso
	}
	return fmt.Sprintf("%d %s %d", d.Day(), russianMonths[int(d.Month())], d.Year())
}

var monthNames = map[string]string{"Jan": "янв", "Feb": "фев", "Mar": "мар", "Apr": "апр", "May": "мая", "Jun": "июн", "Jul": "июл", "Aug": "авг", "Sep": "сен", "Oct": "окт", "Nov": "ноя", "Dec": "дек"}
var fullInterval = regexp.MustCompile(`^([A-Za-z]{3}) ([0-9]{1,2}), ([0-9]{4}) – ([A-Za-z]{3}) ([0-9]{1,2}), ([0-9]{4})$`)
var crossMonthInterval = regexp.MustCompile(`^([A-Za-z]{3}) ([0-9]{1,2}) – ([A-Za-z]{3}) ([0-9]{1,2}), ([0-9]{4})$`)
var withinMonthInterval = regexp.MustCompile(`^([A-Za-z]{3}) ([0-9]{1,2}) – ([0-9]{1,2}), ([0-9]{4})$`)

func friendlyInterval(raw string) string {
	s := strings.Join(strings.Fields(strings.ReplaceAll(raw, "\u2009", " ")), " ")
	if m := fullInterval.FindStringSubmatch(s); m != nil && monthNames[m[1]] != "" && monthNames[m[4]] != "" {
		return fmt.Sprintf("%s %s %s – %s %s %s", m[2], monthNames[m[1]], m[3], m[5], monthNames[m[4]], m[6])
	}
	if m := crossMonthInterval.FindStringSubmatch(s); m != nil && monthNames[m[1]] != "" && monthNames[m[3]] != "" {
		return fmt.Sprintf("%s %s – %s %s %s", m[2], monthNames[m[1]], m[4], monthNames[m[3]], m[5])
	}
	if m := withinMonthInterval.FindStringSubmatch(s); m != nil && monthNames[m[1]] != "" {
		return fmt.Sprintf("%s–%s %s %s", m[2], m[3], monthNames[m[1]], m[4])
	}
	return raw
}
