package handlers

import (
	"agent/demand"
	"agent/views"
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
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
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) error {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.Index(views.HomeView{Presets: []string{"Kaspersky Endpoint Security", "касперский для бизнеса", "Astra Linux", "астра линукс купить", "антивирус для бизнеса"}}).Render(r.Context(), w)
}
func (h *Handler) Query(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		return err
	}
	phrase := r.FormValue("phrase")
	kind := r.FormValue("kind")
	var ids []int
	for _, s := range strings.Split(r.FormValue("region_ids"), ",") {
		if strings.TrimSpace(s) == "" {
			continue
		}
		id, err := strconv.Atoi(strings.TrimSpace(s))
		if err != nil {
			return views.Result(views.ResultView{Status: "invalid_request", Message: "Коды регионов должны быть числами через запятую."}).Render(r.Context(), w)
		}
		ids = append(ids, id)
	}
	devices := []string{r.FormValue("device")}
	if devices[0] == "" {
		devices = nil
	}
	var out demand.Result
	var err error
	switch kind {
	case "top":
		out, err = h.service.Top(r.Context(), demand.TopInput{Phrase: phrase, RegionIDs: ids, Devices: devices, Limit: 20})
	case "dynamics":
		out, err = h.service.Dynamics(r.Context(), demand.DynamicsInput{Phrase: phrase, FromMonth: r.FormValue("from_month"), ToMonth: r.FormValue("to_month"), RegionIDs: ids, Devices: devices})
	case "regions":
		out, err = h.service.Regions(r.Context(), demand.RegionsInput{Phrase: phrase, RegionMode: r.FormValue("region_mode")})
	default:
		return views.Result(views.ResultView{Status: "invalid_request", Message: "Выберите вид исследования."}).Render(r.Context(), w)
	}
	if err != nil {
		return err
	}
	v := views.ResultView{Status: out.Status, Message: out.Message, Phrase: out.Phrase, WindowNote: out.Source.WindowNote, SourceURL: out.Source.Documentation, CheckedAt: out.Source.CheckedAt, SourceStatus: out.SourceStatus}
	if out.RequestedPeriod != nil {
		v.Period = out.RequestedPeriod.From + " – " + out.RequestedPeriod.To
	}
	for _, row := range out.Rows {
		count := ""
		if row.Count != nil {
			count = strconv.FormatInt(*row.Count, 10)
		}
		v.Rows = append(v.Rows, views.Row{Group: row.Group, Label: row.Label, Count: count})
	}
	if len(out.Data) > 0 {
		b, _ := json.MarshalIndent(out.Data, "", "  ")
		v.Raw = string(b)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.Result(v).Render(r.Context(), w)
}
