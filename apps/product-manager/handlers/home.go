package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/internal/product"
	"agent/views"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/airlockrun/agentsdk"
)

type Handler struct {
	svc   product.Service
	files *agentsdk.Agent
}

func New(s product.Service, agent ...*agentsdk.Agent) *Handler {
	h := &Handler{svc: s}
	if len(agent) > 0 {
		h.files = agent[0]
	}
	return h
}

var browserVerdicts = map[string]string{
	"path_broken":              "Путь до корзины нарушен",
	"metrika_add_not_observed": "Запрос события добавления не наблюдался",
	"ok":                       "Путь пройден, запрос события наблюдался",
}

var evidenceLevels = map[string]string{"signal_only": "Сигнал", "correlation": "Наблюдаемая связь", "path_verified": "Путь проверен"}
var sourceTitles = map[string]string{"sales": "Продажи", "metrika": "Метрика", "research": "Исследования", "market": "Конкуренты", "release": "Релизы", "backlog": "Бэклог", "economics": "Экономика", "browser": "Браузер"}
var statusTitles = map[string]string{"proposed": "На рассмотрении", "approved": "Подтверждена", "in_backlog": "В бэклоге", "awaiting_release": "Ожидает релиза", "awaiting_measurement": "Ожидает замера", "supported": "Критерий достигнут", "rejected": "Критерий не достигнут", "insufficient_data": "Недостаточно данных"}

const watchdogMetric = product.WatchdogMetric

var watchdogReasons = map[string]string{
	"incomplete_week":                 "Неполная неделя",
	"collection_break":                "Разрыв учёта",
	"missing_or_duplicate_week":       "Пропущенная или повторная неделя",
	"unstable_control":                "Контроль нестабилен",
	"incompatible_control":            "Контроль несопоставим",
	"threshold_not_met":               "Порог не достигнут",
	"target_drop_with_stable_control": "Падение при стабильном контроле",
}

var metricTitles = map[string]string{watchdogMetric: "Сигнал сторожа", "purchase_path": "Путь до корзины", "family_orders_idx.relative_change_pct": "Изменение индекса самостоятельных заказов", "family_orders_idx": "Индекс самостоятельных заказов", "cfg_reach_rate.observed_rows": "Доля посещений конфигуратора", "cfg_add_to_cart_rate": "Добавления в корзину из конфигуратора", "release_log": "Изменение в релизе", "backlog_item": "Задача", "interview_excerpt": "Фрагмент интервью", "historical_competitor_price": "Сравнение цен", "store_context": "Вывод исследования магазинов"}

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
	if r.Metric == watchdogMetric {
		watchdogRow(&v, r)
		return v
	}
	if r.Status == "missing" || r.Value == "null" {
		v.Value = "Нет наблюдения"
		v.Missing = true
		v.Summary = r.Segment
		return v
	}
	if r.Source == "browser" && r.Kind == "path_check" {
		v.Value, v.Summary = label(browserVerdicts, r.Value), r.Segment
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
func watchdogRow(v *views.EvidenceRow, r evidence.Row) {
	var d struct {
		Reason     string   `json:"reason"`
		Control    string   `json:"control_product_key"`
		Change     *float64 `json:"relative_change_pct"`
		Compared   *float64 `json:"control_change_pct"`
		Adjusted   *float64 `json:"control_adjusted_change_pct"`
		Origin     string   `json:"origin"`
		SourceRefs []struct {
			Ref evidence.SourceRef `json:"source_ref"`
		} `json:"source_refs"`
	}
	_ = json.Unmarshal([]byte(r.Details), &d)
	v.Value = "Недостаточно сопоставимых данных"
	if r.Status == "observed" {
		switch r.Value {
		case "true":
			v.Value = "Тревога"
		case "false":
			v.Value = "Без тревоги"
		}
	}
	v.Summary = label(watchdogReasons, d.Reason)
	if d.Change != nil && d.Compared != nil {
		v.Summary += fmt.Sprintf(" · %s %% · контроль %s %s %%", num(*d.Change), d.Control, num(*d.Compared))
	}
	if d.Adjusted != nil {
		v.Summary += fmt.Sprintf(" · с поправкой на контроль %s %%", num(*d.Adjusted))
	}
	if d.Origin != "" {
		v.Summary += " · " + d.Origin
	}
	for _, ref := range d.SourceRefs {
		if ref.Ref.SourceID != "" && ref.Ref.Locator != "" {
			v.Related = append(v.Related, ref.Ref.SourceID+" · "+ref.Ref.Locator)
		}
	}
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
	v.EvidenceLevel = label(evidenceLevels, p.EvidenceLevel)
	v.Forecast = p.Forecast
	v.CompetingExplanations = p.CompetingExplanations
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
		v.TaskID, v.TaskTag, v.TaskURL = r.Task.ID, r.Task.Tag, r.Task.URL
	}
	if r.Assessment != nil {
		v.Assessment = r.Assessment.Reason
		for _, ref := range append([]string{r.Assessment.ReleaseEvidenceRef}, r.Assessment.EvidenceRefs...) {
			if snapshot, id, ok := strings.Cut(ref, "/"); ok {
				v.MeasuredEvidence = append(v.MeasuredEvidence, views.Link{Label: id, URL: href("snapshot", snapshot, "obs", id)})
			}
		}
	}
	all := append([]string{}, p.SignalIDs...)
	all = append(all, p.SupportIDs...)
	all = append(all, p.CounterIDs...)
	all = append(all, p.BrowserEvidenceIDs...)
	all = append(all, r.ContextIDs...)
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
	}{{"Сигнал", p.SignalIDs}, {"Свидетельства в пользу гипотезы", p.SupportIDs}, {"Свидетельства против гипотезы", p.CounterIDs}, {"Проверка пути в браузере", p.BrowserEvidenceIDs}, {"Контекст сигнала · связь требует проверки", r.ContextIDs}} {
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
	focus, err := h.svc.GetFocus(ctx, evidence.Empty{})
	if err != nil {
		return views.HomeView{}, err
	}
	seen := focusFingerprint(focus)
	changed := q.Has("focus_seen") && q.Get("focus_seen") != seen
	if changed && focus.ProductKey != "" {
		// An explicit focus from chat resets only the panel's manual navigation.
		q = url.Values{}
	}
	q.Set("focus_seen", seen)
	v := views.HomeView{BoardURL: "./board?" + q.Encode()}
	id, snap, selected := q.Get("id"), q.Get("snapshot"), q.Get("monitor")
	for _, rec := range records.Records {
		p := rec.Hypothesis
		v.Records = append(v.Records, views.Link{Label: p.Title, URL: href("id", p.ID), Detail: label(statusTitles, p.Status), Active: p.ID == id})
	}
	if selected != "" || id == "" && snap == "" {
		if selected == "" && q.Get("overview") == "" {
			selected, snap = focus.ProductKey, focus.SnapshotID
		}
		for _, s := range index.Snapshots {
			v.Snapshots = append(v.Snapshots, views.Link{Label: date(s.AsOf), Detail: shortID(s.SnapshotID), URL: href("snapshot", s.SnapshotID)})
		}
		v.Monitor, err = h.monitor(ctx, index.Snapshots, records.Records, selected, snap)
		if err != nil {
			return v, err
		}
		if v.Monitor.Selected != nil {
			monitorEconomics(v.Monitor.Selected, q.Get("orders"), q.Get("margin"))
		}
		return v, nil
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
		if (r.Metric == "family_orders_idx.relative_change_pct" && r.Status == "observed") ||
			(r.Metric == watchdogMetric && r.Status == "observed" && r.Value == "true") {
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
	metric := ""
	if obs == "" && q.Get("metric") == watchdogMetric {
		metric = watchdogMetric
		v.Signals = nil
		v.CanInvestigate = false
		v.IsReplay = true
	}
	offset, _ := strconv.Atoi(q.Get("offset"))
	if offset < 0 || offset > 1000000 {
		offset = 0
	}
	req := evidence.Query{SnapshotID: snap, Source: source, MetricKey: metric, Offset: offset, Limit: 20}
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
	for _, k := range []string{"", "sales", "metrika", "release", "backlog", "research", "market", "economics", "browser"} {
		name := label(sourceTitles, k)
		if k == "" {
			name = "Все"
		}
		v.Filters = append(v.Filters, views.Link{Label: name, URL: href("snapshot", snap, "source", k), Active: k == source && obs == "" && !v.IsReplay})
	}
	if obs == "" {
		history, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: snap, MetricKey: watchdogMetric, Limit: 1})
		if err != nil {
			return v, err
		}
		if history.Total > 0 {
			if !v.IsReplay {
				v.CanInvestigate = false
				alerts, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: snap, MetricKey: watchdogMetric, Status: "observed", Value: "true", Limit: 1})
				if err != nil {
					return v, err
				}
				v.CanInvestigate = alerts.Total > 0
			}
			v.Filters = append(v.Filters, views.Link{Label: "Прогон по истории", URL: href("snapshot", snap, "metric", watchdogMetric), Active: v.IsReplay})
		}
	}
	for _, row := range data.Rows {
		v.Rows = append(v.Rows, rowView(snap, row))
	}
	if len(data.Rows) > 0 {
		v.RowsLabel = fmt.Sprintf("%d–%d из %d", req.Offset+1, req.Offset+len(data.Rows), data.Total)
	}
	if obs == "" && offset > 0 {
		v.Prev = href("snapshot", snap, "source", source, "metric", metric, "offset", strconv.Itoa(max(0, offset-req.Limit)))
	}
	if obs == "" && offset+len(data.Rows) < data.Total {
		v.Next = href("snapshot", snap, "source", source, "metric", metric, "offset", strconv.Itoa(offset+req.Limit))
	}
	return v, nil
}
func focusFingerprint(focus product.Focus) string {
	return fmt.Sprintf("%x", sha256.Sum256([]byte(focus.ProductKey+"\x00"+focus.SnapshotID+"\x00"+focus.HypothesisID)))
}

func shortID(s string) string {
	if len(s) > 11 {
		return s[3:11]
	}
	return s
}
func (h *Handler) Home(w http.ResponseWriter, r *http.Request) error {
	if r.URL.Query().Get("frame") != "" {
		return h.browserFrame(w, r)
	}
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
	return views.LiveBoard(v).Render(r.Context(), w)
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
