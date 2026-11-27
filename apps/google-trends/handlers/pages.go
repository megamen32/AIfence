package handlers

import (
	"agent/trends"
	"agent/views"
	"fmt"
	"net/http"
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
func (p *Pages) Home(w http.ResponseWriter, r *http.Request) error {
	now := time.Now().UTC()
	month := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	f := views.Form{Mode: "compare", Terms: "Kaspersky Endpoint Security\nAstra Linux", Geo: "RU", From: month.AddDate(-1, 0, 0).Format(time.DateOnly), To: month.AddDate(0, 0, -1).Format(time.DateOnly), Property: "web", Category: "0", Region: "region"}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.Page(f, nil).Render(r.Context(), w)
}
func (p *Pages) Query(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		http.Error(w, "Не удалось прочитать форму.", http.StatusBadRequest)
		return nil
	}
	f := views.Form{Mode: r.Form.Get("mode"), Terms: r.Form.Get("terms"), Geo: r.Form.Get("geo"), From: r.Form.Get("from"), To: r.Form.Get("to"), Property: r.Form.Get("property"), Category: r.Form.Get("category"), Region: r.Form.Get("region")}
	lines := strings.Split(strings.ReplaceAll(f.Terms, "\r\n", "\n"), "\n")
	terms := make([]string, 0, len(lines))
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			terms = append(terms, strings.TrimSpace(line))
		}
	}
	q := trends.Query{Terms: terms, Geo: f.Geo, From: f.From, To: f.To, Property: f.Property, Category: f.Category}
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
		result = trends.Result{Status: trends.InvalidInput, Message: "Выберите режим исследования."}
	}
	if err != nil {
		result = trends.Result{Status: trends.UpstreamError, Message: "Источник не ответил."}
	}
	vm := resultModel(result)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if result.Status == trends.InvalidInput || result.Status == trends.UnavailablePeriod {
		w.WriteHeader(http.StatusUnprocessableEntity)
	}
	if r.Header.Get("HX-Request") == "true" {
		return views.Results(vm).Render(r.Context(), w)
	} else {
		return views.Page(f, &vm).Render(r.Context(), w)
	}
}
func resultModel(r trends.Result) views.ResultModel {
	geo := r.Request.Geo
	if geo == "" {
		geo = "Весь мир"
	}
	v := views.ResultModel{Status: r.Status, Message: r.Message, Mode: r.Mode, Terms: r.Request.Terms, Period: r.Request.From + " .. " + r.Request.To, Geo: geo, Property: r.Request.Property, Category: r.Request.Category, Checked: r.Source.CheckedAt, ExploreURL: r.Source.ExploreURL, Notes: r.Notes}
	for _, s := range r.Summaries {
		value := "Нет общей выборки"
		if s.MeanIndex != nil {
			value = fmt.Sprintf("%.1f", *s.MeanIndex)
		}
		v.Metrics = append(v.Metrics, views.Metric{Term: s.Term, Value: value, Coverage: fmt.Sprintf("%d общих интервалов; исключено %d", s.CommonCompletePoints, s.ExcludedPoints)})
	}
	for _, p := range r.Points {
		flag := "Завершенность неизвестна"
		if p.IsPartial != nil {
			flag = "Завершен"
			if *p.IsPartial {
				flag = "Частичный интервал"
			}
		}
		row := views.Row{Label: p.Date, Detail: flag}
		for _, score := range p.Values {
			row.Cells = append(row.Cells, scoreCell(score))
		}
		v.Rows = append(v.Rows, row)
	}
	if len(r.Points) > 0 {
		v.FirstInterval = r.Points[0].Date
		v.LastInterval = r.Points[len(r.Points)-1].Date
		v.Series = chartSeries(r.Points, r.Request.Terms)
	}
	for _, region := range r.Regions {
		row := views.Row{Label: region.Name, Detail: region.Code}
		for _, score := range region.Values {
			row.Cells = append(row.Cells, scoreCell(score))
		}
		v.Rows = append(v.Rows, row)
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
			}
		}
		v.Terms = []string{"Значение"}
	}
	return v
}
func scoreCell(s trends.Score) views.Cell {
	switch s.State {
	case "missing":
		return views.Cell{Label: "Нет данных"}
	case "below_one":
		return views.Cell{Label: "<1", Detail: "Ниже единицы"}
	default:
		label := s.Raw
		if label == "" && s.Index != nil {
			label = strconv.FormatFloat(*s.Index, 'f', -1, 64)
		}
		detail := ""
		if s.HasData == nil {
			detail = "Признак наличия данных неизвестен"
		}
		return views.Cell{Label: label, Detail: detail}
	}
}
func chartSeries(points []trends.Point, terms []string) []views.ChartSeries {
	classes := []string{"text-primary", "text-secondary", "text-accent", "text-info", "text-base-content"}
	out := make([]views.ChartSeries, 0, len(terms))
	for ti, term := range terms {
		series := views.ChartSeries{Name: term, Class: classes[ti%len(classes)]}
		segment := []string{}
		flush := func() {
			if len(segment) > 0 {
				series.Segments = append(series.Segments, strings.Join(segment, " "))
				segment = nil
			}
		}
		for i, p := range points {
			// A chart line uses only confirmed complete intervals with observed
			// numbers. Unknown, partial, missing and censored values remain
			// available in the table and break the line.
			if p.IsPartial == nil || *p.IsPartial || ti >= len(p.Values) || p.Values[ti].HasData == nil || !*p.Values[ti].HasData || p.Values[ti].Index == nil {
				flush()
				continue
			}
			x := 42.0
			if len(points) > 1 {
				x += float64(i) * 596 / float64(len(points)-1)
			}
			y := 184 - *p.Values[ti].Index*1.6
			segment = append(segment, fmt.Sprintf("%.2f,%.2f", x, y))
		}
		flush()
		out = append(out, series)
	}
	return out
}
