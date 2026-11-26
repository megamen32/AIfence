package handlers

import (
	"agent/internal/sourcewatch"
	"agent/views"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

type SourceWatches struct{ Service *sourcewatch.Service }

func (h SourceWatches) Page(w http.ResponseWriter, r *http.Request) error {
	var findings []string
	if id := r.URL.Query().Get("review"); id != "" {
		run, err := h.Service.Review(r.Context(), sourcewatch.WatchRef{WatchID: id})
		if err != nil {
			return err
		}
		findings = append(findings, "Состояние разбора: "+run.Status)
		if run.Result != nil {
			findings = append(findings, run.Result.Finding, run.Result.NextCheck)
		}
	}
	return h.page(w, r, "", findings)
}
func (h SourceWatches) Update(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	if err := r.ParseForm(); err != nil {
		return err
	}
	var err error
	switch r.PostForm.Get("action") {
	case "start":
		value, e := strconv.ParseFloat(r.PostForm.Get("drop_pct"), 64)
		if e != nil {
			w.WriteHeader(http.StatusBadRequest)
			return h.page(w, r, "Укажите числовой порог снижения.", nil)
		}
		demandDrop := 0.0
		if raw := r.PostForm.Get("demand_drop_pct"); raw != "" {
			var e error
			demandDrop, e = strconv.ParseFloat(raw, 64)
			if e != nil {
				w.WriteHeader(http.StatusBadRequest)
				return h.page(w, r, "Укажите числовой порог спроса.", nil)
			}
		}
		_, err = h.Service.Start(r.Context(), sourcewatch.Window{MetrikaBinding: r.PostForm.Get("metrika_binding"), MarketBinding: r.PostForm.Get("market_binding"), SKU: r.PostForm.Get("sku"), WordstatBinding: r.PostForm.Get("wordstat_binding"), Phrase: r.PostForm.Get("phrase"), DemandDropPct: demandDrop, Metric: r.PostForm.Get("metric"), Monday: r.PostForm.Get("monday"), DropPct: value})
	case "stop":
		_, err = h.Service.Stop(r.Context(), sourcewatch.WatchRef{WatchID: r.PostForm.Get("watch_id")})
	default:
		http.Error(w, "Неизвестное действие", http.StatusBadRequest)
		return nil
	}
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return h.page(w, r, err.Error(), nil)
	}
	http.Redirect(w, r, "./source-watch", http.StatusSeeOther)
	return nil
}
func (h SourceWatches) page(w http.ResponseWriter, r *http.Request, msg string, findings []string) error {
	list, err := h.Service.List(r.Context(), struct{}{})
	if err != nil {
		return err
	}
	rows := make([]views.SourceWatchRow, 0, len(list.Watches))
	labels := map[string]string{"": "Ожидает первого запуска", "scheduled": "Первая проверка запланирована", "enqueue_failed": "Проверка не запланирована. Повторите включение с теми же параметрами.", "schedule_failed": "Не удалось запланировать следующую неделю. Проверьте доступ и возобновите мониторинг.", "quiet": "Порог не достигнут", "incomplete": "Не хватает сопоставимых наблюдений", "submitted": "Сигнал передан агенту", "failed": "Ошибка проверки", "model_route_unapproved": "Для разбора нужен разрешённый маршрут модели"}
	for _, v := range list.Watches {
		label := labels[v.Status]
		if v.LastMonday != "" {
			label = v.LastMonday + " · " + label
		}
		if !v.Active {
			label += " · остановлено"
		}
		item := views.SourceWatchRow{ID: v.ID, Title: v.Config.SKU, Active: v.Active, Detail: fmt.Sprintf("%s · снижение от %g%%", v.Config.Metric, v.Config.DropPct), Status: label}
		if v.Config.Phrase != "" {
			item.Title = v.Config.Phrase
			if v.Config.SKU != "" {
				item.Title += " · " + v.Config.SKU
			}
			if v.Config.DemandDropPct > 0 {
				item.Detail += fmt.Sprintf(" · порог спроса %g%%", v.Config.DemandDropPct)
			}
		}
		if v.AnalysisRunID != "" {
			item.Review = "./source-watch?review=" + url.QueryEscape(v.ID)
		}
		rows = append(rows, item)
	}
	now := time.Now().UTC()
	next := now.AddDate(0, 0, (8-int(now.Weekday()))%7)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.SourceWatches(rows, msg, next.Format(time.DateOnly), findings).Render(r.Context(), w)
}
