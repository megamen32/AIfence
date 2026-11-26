package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/internal/product"
	"agent/views"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type Handler struct{ svc product.Service }

func New(s product.Service) *Handler { return &Handler{svc: s} }

var sourceTitles = map[string]string{"sales": "Продажи", "metrika": "Метрика", "research": "Исследования", "market": "Конкуренты", "release": "Релизы", "backlog": "Бэклог", "economics": "Экономика"}
var statusTitles = map[string]string{"proposed": "На рассмотрении", "approved": "Подтверждена", "in_backlog": "В бэклоге", "awaiting_release": "Ожидает релиза", "awaiting_measurement": "Ожидает замера", "supported": "Критерий достигнут", "rejected": "Критерий не достигнут", "insufficient_data": "Недостаточно данных"}
var metricTitles = map[string]string{"family_orders_idx.relative_change_pct": "Изменение индекса самостоятельных заказов", "family_orders_idx": "Индекс самостоятельных заказов", "cfg_reach_rate.observed_rows": "Доля посещений конфигуратора", "cfg_add_to_cart_rate": "Добавления в корзину из конфигуратора", "release_log": "Изменение в релизе", "backlog_item": "Задача", "interview_excerpt": "Фрагмент интервью", "historical_competitor_price": "Сравнение цен", "store_context": "Вывод исследования магазинов"}

func label(m map[string]string, key string) string {
	if v, ok := m[key]; ok {
		return v
	}
	return key
}
func date(s string) string {
	if len(s) >= 10 {
		return s[8:10] + "." + s[5:7] + "." + s[:4]
	}
	return s
}
func span(from, to string) string {
	if from == to {
		return date(from)
	}
	return date(from) + " / " + date(to)
}
func href(kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	return "./?" + q.Encode()
}
func rub(n float64) string {
	x := strings.SplitN(strconv.FormatFloat(n, 'f', 2, 64), ".", 2)
	whole := x[0]
	for i := len(whole) - 3; i > 0; i -= 3 {
		whole = whole[:i] + " " + whole[i:]
	}
	return whole + "," + x[1] + " ₽"
}
func num(n float64) string { return strings.ReplaceAll(strconv.FormatFloat(n, 'f', 1, 64), ".", ",") }

type saleDetail struct {
	Variant       string  `json:"variant"`
	BaselineMean  float64 `json:"baseline_mean"`
	CurrentMean   float64 `json:"current_mean"`
	BaselineWeeks int     `json:"baseline_weeks"`
	CurrentWeeks  int     `json:"current_weeks"`
}

func rowView(id string, r evidence.Row) views.EvidenceRow {
	v := views.EvidenceRow{ID: r.ID, Metric: label(metricTitles, r.Metric), Product: r.Product, Source: label(sourceTitles, r.Source), Value: r.Value, Date: span(r.Period.From, r.Period.To), SourceName: r.Ref.SourceID, Locator: r.Ref.Locator, OpenURL: href("snapshot", id, "obs", r.ID), Note: strings.Join(r.Caveats, " ")}
	if u, err := url.Parse(r.Ref.URL); err == nil && u.Host != "" && (u.Scheme == "https" || u.Scheme == "http") {
		v.SourceURL = u.String()
	}
	if r.Status == "missing" || r.Value == "null" {
		v.Value = "Нет наблюдения"
		v.Missing = true
		return v
	}
	if f, err := strconv.ParseFloat(r.Value, 64); err == nil {
		switch r.Unit {
		case "ratio":
			v.Value = num(100*f) + " %"
		case "percent", "%", "pct":
			v.Value = num(f) + " %"
		case "RUB", "rub":
			v.Value = num(f) + " ₽"
		default:
			if r.Unit != "" {
				v.Value += " " + r.Unit
			}
		}
	}
	if r.Segment != "" {
		v.Summary = r.Segment
	}
	var d saleDetail
	if json.Unmarshal([]byte(r.Details), &d) == nil && d.BaselineWeeks > 0 && d.CurrentWeeks > 0 {
		v.Summary = fmt.Sprintf("Средний индекс %s → %s · %d и %d нед.", num(d.BaselineMean), num(d.CurrentMean), d.BaselineWeeks, d.CurrentWeeks)
	}
	if r.Metric == "historical_competitor_price" {
		var d struct {
			Seller  string   `json:"seller"`
			Store   *float64 `json:"store_price"`
			Edition string   `json:"edition"`
			Terms   string   `json:"term_quantity"`
		}
		if json.Unmarshal([]byte(r.Details), &d) == nil {
			if f, e := strconv.ParseFloat(r.Value, 64); e == nil && d.Seller != "" {
				v.Value = d.Seller + " " + rub(f)
			}
			if d.Store != nil {
				v.Summary = "Softline Store " + rub(*d.Store) + " · проверка " + date(r.Period.From)
			}
			if d.Edition != "" {
				v.Summary += " · " + d.Edition
			}
			if d.Terms != "" {
				v.Summary += " · " + d.Terms
			}
		}
	}
	if r.Metric == "interview_excerpt" || r.Metric == "store_context" {
		var d struct {
			Basis string `json:"period_basis"`
			Scope string `json:"scope"`
			Level string `json:"source_level"`
		}
		if json.Unmarshal([]byte(r.Details), &d) == nil && d.Basis != "" {
			v.Date = "Срез " + date(r.Period.To)
			v.Note = strings.TrimSpace(strings.Join([]string{v.Note, d.Basis, d.Scope, d.Level}, " "))
		}
	}

	return v
}
func signalCards(data evidence.Result) []views.Signal {
	var out []views.Signal
	for _, r := range data.Rows {
		var d saleDetail
		if json.Unmarshal([]byte(r.Details), &d) != nil || d.Variant != "full_weeks" || d.BaselineWeeks < 1 || d.CurrentWeeks < 1 {
			continue
		}
		row := rowView(data.SnapshotID, r)
		out = append(out, views.Signal{Product: r.Product, Change: row.Value, Before: num(d.BaselineMean), After: num(d.CurrentMean), Period: fmt.Sprintf("%d и %d полных нед.", d.BaselineWeeks, d.CurrentWeeks), URL: row.OpenURL})
	}
	return out
}
func rule(c decision.Criterion) string {
	r := strings.NewReplacer("difference_in_relative_changes_pp", "Изменение к контролю, п.п.", "relative_change_pct", "Изменение к базе, %", ">=", "≥", "<=", "≤").Replace(c.Rule)
	return r
}
func (h *Handler) detail(ctx context.Context, r decision.Record) (*views.Decision, error) {
	p, c := r.Hypothesis, r.Hypothesis.SuccessCriterion
	v := &views.Decision{ID: p.ID, Title: p.Title, Product: p.ProductKey, Status: label(statusTitles, p.Status), Action: p.ProposedAction, Unknowns: p.Unknowns, Metric: label(metricTitles, c.MetricKey), Baseline: span(c.BaselineFrom, c.BaselineTo), Rule: rule(c), Window: fmt.Sprintf("%d дн.", c.ObservationDays), Control: c.ControlProductKey, Version: strconv.FormatInt(r.Version, 10), SnapshotURL: href("snapshot", r.EvidenceSnapshotID)}
	for _, op := range []string{">=", "<="} {
		if formula, n, ok := strings.Cut(c.Rule, op); ok {
			v.Operator = op
			v.Threshold = strings.TrimSpace(n)
			v.RuleLabel = "Изменение к базе, %"
			if strings.TrimSpace(formula) == "difference_in_relative_changes_pp" {
				v.RuleLabel = "Изменение относительно контроля, п.п."
			}
			break
		}
	}
	if r.Task != nil {
		v.TaskID = r.Task.ID
	}
	if r.Assessment != nil {
		v.Assessment = r.Assessment.Reason
	}
	all := append([]string{}, p.SignalIDs...)
	all = append(all, p.SupportIDs...)
	all = append(all, p.CounterIDs...)
	data, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: r.EvidenceSnapshotID, IDs: all, Limit: 100})
	if err != nil {
		return nil, err
	}
	lookup := map[string]views.EvidenceRow{}
	for _, o := range data.Rows {
		lookup[o.ID] = rowView(r.EvidenceSnapshotID, o)
	}
	for _, g := range []struct {
		name string
		ids  []string
	}{{"Сигнал", p.SignalIDs}, {"Свидетельства в пользу гипотезы", p.SupportIDs}, {"Альтернативные объяснения", p.CounterIDs}} {
		group := views.EvidenceGroup{Title: g.name}
		for _, id := range g.ids {
			if row, ok := lookup[id]; ok {
				group.Rows = append(group.Rows, row)
			}
		}
		if len(group.Rows) > 0 {
			v.Groups = append(v.Groups, group)
		}
	}
	return v, nil
}
func (h *Handler) model(r *http.Request) (views.HomeView, error) {
	ctx := r.Context()
	q := r.URL.Query()
	records, err := h.svc.List(ctx, evidence.Empty{})
	if err != nil {
		return views.HomeView{}, err
	}
	index, err := h.svc.Evidence.List(ctx, evidence.Empty{})
	if err != nil {
		return views.HomeView{}, err
	}
	v := views.HomeView{BoardURL: "./board?" + q.Encode()}
	id, snap := q.Get("id"), q.Get("snapshot")
	if id == "" && snap == "" && len(records.Records) > 0 {
		id = records.Records[0].Hypothesis.ID
	}
	for _, rec := range records.Records {
		p := rec.Hypothesis
		v.Records = append(v.Records, views.Link{Label: p.Title, URL: href("id", p.ID), Detail: label(statusTitles, p.Status), Active: p.ID == id})
	}
	if id != "" {
		rec, err := h.svc.Get(ctx, decision.GetInput{HypothesisID: id})
		if err != nil {
			return v, err
		}
		v.Detail, err = h.detail(ctx, rec)
		if err != nil {
			return v, err
		}
		snap = rec.EvidenceSnapshotID
	}
	if snap == "" && len(index.Snapshots) > 0 {
		snap = index.Snapshots[0].SnapshotID
	}
	for _, s := range index.Snapshots {
		active := s.SnapshotID == snap
		v.Snapshots = append(v.Snapshots, views.Link{Label: date(s.AsOf), Detail: fmt.Sprintf("%d наблюдений · %s", s.Count, shortID(s.SnapshotID)), URL: href("snapshot", s.SnapshotID), Active: active && v.Detail == nil})
	}
	if snap == "" {
		return v, nil
	}
	v.SnapshotID = snap
	sales, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: snap, Source: "sales", Limit: 100})
	if err != nil {
		return v, err
	}
	v.Signals = signalCards(sales)
	for _, r := range sales.Rows {
		if r.Metric == "family_orders_idx.relative_change_pct" && r.Status == "observed" {
			v.CanInvestigate = true
			break
		}
	}
	if v.Detail != nil {
		filtered := v.Signals[:0]
		for _, sig := range v.Signals {
			if sig.Product == v.Detail.Product || sig.Product == v.Detail.Control {
				filtered = append(filtered, sig)
			}
		}
		v.Signals = filtered
		return v, nil
	}
	v.ShowSources = true
	source, obs := q.Get("source"), q.Get("obs")
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 || offset > 1000000 {
		offset = 0
	}
	req := evidence.Query{SnapshotID: snap, Source: source, Offset: offset, Limit: 20}
	if obs != "" {
		v.Signals = nil
		v.CanInvestigate = false
		req.IDs = []string{obs}
		req.Offset = 0
	}
	data, err := h.svc.Evidence.Read(ctx, req)
	if err != nil {
		return v, err
	}
	v.AsOf = date(data.AsOf)
	v.Warnings = data.Warnings
	// Navigation preserves the selected immutable snapshot.
	for _, k := range []string{"", "sales", "metrika", "release", "backlog", "research", "market", "economics"} {
		name := label(sourceTitles, k)
		if k == "" {
			name = "Все"
		}
		v.Filters = append(v.Filters, views.Link{Label: name, URL: href("snapshot", snap, "source", k), Active: k == source && obs == ""})
	}
	for _, row := range data.Rows {
		v.Rows = append(v.Rows, rowView(snap, row))
	}
	if len(data.Rows) > 0 {
		v.RowsLabel = fmt.Sprintf("%d–%d из %d", req.Offset+1, req.Offset+len(data.Rows), data.Total)
	}
	if obs == "" && offset > 0 {
		v.Prev = href("snapshot", snap, "source", source, "offset", strconv.Itoa(max(0, offset-req.Limit)))
	}
	if obs == "" && offset+len(data.Rows) < data.Total {
		v.Next = href("snapshot", snap, "source", source, "offset", strconv.Itoa(offset+req.Limit))
	}
	return v, nil
}
func shortID(s string) string {
	if len(s) > 11 {
		return s[3:11]
	}
	return s
}
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) error {
	v, err := h.model(r)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.Index(v).Render(r.Context(), w)
}
func (h *Handler) Board(w http.ResponseWriter, r *http.Request) error {
	v, err := h.model(r)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return views.Board(v).Render(r.Context(), w)
}
func (h *Handler) Approve(w http.ResponseWriter, r *http.Request) error {
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	if err := r.ParseForm(); err != nil {
		return err
	}
	id := r.PostForm.Get("id")
	if id == "" || r.PostForm.Get("confirmed") != "yes" {
		http.Error(w, "Подтвердите выбранную задачу.", 400)
		return nil
	}
	version, err := strconv.ParseInt(r.PostForm.Get("version"), 10, 64)
	if err != nil || version < 1 {
		http.Error(w, "Откройте актуальную версию карточки.", 400)
		return nil
	}
	_, err = h.svc.Send(r.Context(), decision.SendInput{HypothesisID: id, Confirmed: true, IdempotencyKey: "backlog/" + id, ExpectedVersion: &version})
	if err != nil {
		return err
	}
	http.Redirect(w, r, href("id", id), http.StatusSeeOther)
	return nil
}
func (h *Handler) Import(w http.ResponseWriter, r *http.Request) error {
	media, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		http.Error(w, "Выберите файл JSON.", 415)
		return nil
	}
	var input io.Reader
	switch media {
	case "application/json":
		r.Body = http.MaxBytesReader(w, r.Body, evidence.MaxSize)
		input = r.Body
	case "multipart/form-data":
		r.Body = http.MaxBytesReader(w, r.Body, evidence.MaxSize+32768)
		if err := r.ParseMultipartForm(evidence.MaxSize + 32768); err != nil {
			http.Error(w, "Некорректная форма или слишком большой файл.", 400)
			return nil
		}
		if r.MultipartForm != nil {
			defer r.MultipartForm.RemoveAll()
		}
		f, _, err := r.FormFile("file")
		if err != nil {
			http.Error(w, "Выберите файл JSON.", 400)
			return nil
		}
		defer f.Close()
		input = io.LimitReader(f, evidence.MaxSize+1)
	default:
		http.Error(w, "Поддерживается application/json или загрузка файла.", 415)
		return nil
	}
	raw, err := io.ReadAll(input)
	var oversized *http.MaxBytesError
	if errors.As(err, &oversized) || len(raw) > evidence.MaxSize {
		http.Error(w, "Файл превышает 1 МиБ.", 413)
		return nil
	}
	if err != nil {
		return err
	}
	out, err := h.svc.Evidence.Import(r.Context(), evidence.ImportInput{Document: string(raw)})
	if err != nil {
		http.Error(w, err.Error(), 422)
		return nil
	}
	if media == "application/json" {
		w.Header().Set("Content-Type", "application/json")
		return json.NewEncoder(w).Encode(out)
	}
	http.Redirect(w, r, href("snapshot", out.SnapshotID), http.StatusSeeOther)
	return nil
}
