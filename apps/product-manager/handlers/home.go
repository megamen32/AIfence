package handlers

import (
	"agent/internal/catalogoverview"
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
	"slices"
	"strconv"
	"strings"

	"github.com/airlockrun/agentsdk"
)

type Handler struct {
	svc             product.Service
	files           *agentsdk.Agent
	catalogOverview func(context.Context, catalogoverview.GetInput) (catalogoverview.Overview, error)
	delivery        Delivery
	baserowURL      *agentsdk.EnvVarHandle
}

func (h *Handler) WithBaserowURL(value *agentsdk.EnvVarHandle) *Handler {
	h.baserowURL = value
	return h
}

// Delivery pushes an approved card to the customer's tracker. Approve calls
// it only after the owner's explicit confirmation POST.
type Delivery interface {
	AfterApprove(ctx context.Context, r decision.Record)
	TaskLink(ctx context.Context, r decision.Record) (id, url string, ok bool)
}

func (h *Handler) WithDelivery(d Delivery) *Handler {
	h.delivery = d
	return h
}

func New(s product.Service, agent ...*agentsdk.Agent) *Handler {
	h := &Handler{svc: s}
	if len(agent) > 0 {
		h.files = agent[0]
	}
	return h
}

func (h *Handler) WithCatalogOverview(read func(context.Context, catalogoverview.GetInput) (catalogoverview.Overview, error)) *Handler {
	h.catalogOverview = read
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

func evidenceSourceName(id string) string {
	lower := strings.ToLower(id)
	switch {
	case strings.Contains(lower, "metrika"):
		return "Метрика"
	case strings.Contains(lower, "wordstat"):
		return "Wordstat"
	case strings.Contains(lower, "google") || strings.Contains(lower, "trends"):
		return "Google Trends"
	case strings.Contains(lower, "market") || strings.Contains(lower, "softline") || strings.Contains(lower, "softmap") || strings.Contains(lower, "softmagazin"):
		return "Market Watch"
	case strings.Contains(lower, "browser") || strings.Contains(lower, "path"):
		return "Проверка пути покупки"
	case strings.Contains(lower, "historical") || strings.Contains(lower, "file"):
		return "Сохранённый снимок"
	default:
		return "Источник данных"
	}
}

func evidenceLocator(_ string) string {
	return "Период и показатель из источника"
}

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
	v := views.EvidenceRow{ID: r.ID, Metric: label(metricTitles, r.Metric), Product: evidenceProductName(r.Product, r), Source: label(sourceTitles, r.Source), Value: r.Value, Date: span(r.Period.From, r.Period.To), SourceName: evidenceSourceName(r.Ref.SourceID), Locator: evidenceLocator(r.Ref.Locator), OpenURL: href("snapshot", id, "obs", r.ID), Note: strings.Join(r.Caveats, " ")}
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
	if magnitude := signalMagnitude(evidence.WatchdogWindow(r)); magnitude != nil {
		v.Summary += fmt.Sprintf(" · %s (%s) · база %s, текущее окно %s",
			magnitude.Change, magnitude.Caption, magnitude.Baseline, magnitude.Current)
		if d.Compared != nil {
			v.Summary += fmt.Sprintf(" · изменение контроля %s: %s", evidenceProductName(d.Control, r), signalPercent(*d.Compared))
		}
		if d.Adjusted != nil {
			v.Summary += " · с поправкой на контроль " + signalPercent(*d.Adjusted)
		}
	}
	if d.Origin != "" {
		v.Summary += " · " + d.Origin
	}
	for _, ref := range d.SourceRefs {
		if ref.Ref.SourceID != "" && ref.Ref.Locator != "" {
			v.Related = append(v.Related, evidenceSourceName(ref.Ref.SourceID)+" · "+evidenceLocator(ref.Ref.Locator))
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
		out = append(out, views.Signal{Product: row.Product, Change: row.Value, Before: num(d.BaselineMean), After: num(d.CurrentMean), Period: fmt.Sprintf("%d и %d полных нед.", d.BaselineWeeks, d.CurrentWeeks), URL: row.OpenURL})
	}
	return out
}
func rule(c decision.Criterion) string {
	r := strings.NewReplacer("difference_in_relative_changes_pp", "Изменение к контролю, п.п.", "relative_change_pct", "Изменение к базе, %", ">=", "≥", "<=", "≤").Replace(c.Rule)
	return r
}
func (h *Handler) detail(ctx context.Context, r decision.Record) (*views.Decision, error) {
	p, c := r.Hypothesis, r.Hypothesis.SuccessCriterion
	v := &views.Decision{ID: p.ID, Title: p.Title, Product: productName(p.ProductKey), Status: label(statusTitles, p.Status), Action: p.ProposedAction, Unknowns: p.Unknowns, Metric: label(metricTitles, c.MetricKey), Baseline: span(c.BaselineFrom, c.BaselineTo), Rule: rule(c), Window: fmt.Sprintf("%d дн.", c.ObservationDays), Control: productName(c.ControlProductKey), Version: strconv.FormatInt(r.Version, 10), SnapshotURL: href("snapshot", r.EvidenceSnapshotID)}
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
		v.TaskID, v.TaskTag, v.TaskURL, v.TaskSystem = r.Task.ID, r.Task.Tag, r.Task.URL, "локальный бэклог"
		if h.delivery != nil {
			if id, link, ok := h.delivery.TaskLink(ctx, r); ok {
				v.TaskID, v.TaskURL, v.TaskSystem = id, link, "Redmine"
			}
		}
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
		if o.Product == p.ProductKey && slices.Contains(p.SignalIDs, o.ID) {
			v.Product = evidenceProductName(p.ProductKey, o)
			v.Control = evidenceProductName(c.ControlProductKey, o)
		}
	}
	name := strings.NewReplacer(p.ProductKey, v.Product)
	v.Title, v.Action = name.Replace(v.Title), name.Replace(v.Action)
	if c.ControlProductKey != "" {
		name := strings.NewReplacer(c.ControlProductKey, v.Control)
		v.Title, v.Action = name.Replace(v.Title), name.Replace(v.Action)
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
	q, focus = panelFocusQuery(q, focus)
	v := views.HomeView{BoardURL: "./board?" + q.Encode()}
	id, snap, selected := cardIDForView(q, focus), q.Get("snapshot"), q.Get("monitor")
	for _, rec := range records.Records {
		p := rec.Hypothesis
		title := p.Title
		if legacyFixtureFamily(p.ProductKey) != "" {
			title = "Открыть сохранённую гипотезу"
		} else if display := catalogFamilyNames[p.ProductKey]; display != "" {
			title = strings.ReplaceAll(title, p.ProductKey, display)
		}
		v.Records = append(v.Records, views.Link{Label: title, URL: href("id", p.ID), Detail: label(statusTitles, p.Status), Active: p.ID == id})
	}
	if id == "" && (selected != "" || snap == "") {
		if selected == "" && q.Get("overview") == "" {
			selected, snap = focus.ProductKey, focus.SnapshotID
		}
		for _, s := range index.Snapshots {
			v.Snapshots = append(v.Snapshots, views.Link{Label: date(s.AsOf), Detail: shortID(s.SnapshotID), URL: href("snapshot", s.SnapshotID)})
		}
		demo := panelDemo(q, focus.Synthetic)
		panelQuery := clonePanelQuery(q)
		if selected != "" && panelQuery.Get("monitor") == "" {
			panelQuery.Set("monitor", selected)
			panelQuery.Set("snapshot", snap)
		}
		setupPanel(&v, panelQuery, demo, selected != "")
		v.Monitor, err = h.monitor(ctx, index.Snapshots, records.Records, selected, snap, q.Get("alert"), v.Tab != "overview" && q.Get("overview") == "", demo)
		if err != nil {
			return v, err
		}
		// The panel has one view. When the owner's live sources have produced
		// nothing yet, it opens on the loaded file sets instead of an empty screen.
		if !demo && q.Get("mode") == "" && monitorEmpty(v.Monitor) {
			v.Tabs = nil
			setupPanel(&v, panelQuery, true, selected != "")
			if alt, altErr := h.monitor(ctx, index.Snapshots, records.Records, selected, snap, q.Get("alert"), v.Tab != "overview" && q.Get("overview") == "", true); altErr == nil && !monitorEmpty(alt) {
				demo, v.Monitor = true, alt
			} else {
				v.Tabs = nil
				setupPanel(&v, panelQuery, false, selected != "")
			}
		}
		h.loadOverview(ctx, v.Monitor, q, v.Tab, v.Mode)
		if v.Monitor.Selected != nil {
			v.Monitor.Selected.Mode = panelMode(demo)
			v.Monitor.Selected.BackURL = href("tab", "overview", "mode", panelMode(demo))
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
		if h.baserowURL != nil {
			if value, getErr := h.baserowURL.Get(ctx); getErr == nil {
				v.Detail.BaserowURL = value
			}
		}
		var related []evidence.Summary
		if q.Get("tab") == "after" {
			related = index.Snapshots
		}
		v.Card, err = h.cardInvestigation(ctx, rec, q.Get("orders"), q.Get("margin"), related...)
		if err != nil {
			return v, err
		}
		demo := v.Card.Origin == preparedOrigin
		if q.Get("tab") == "overview" {
			demo = panelDemo(q, demo)
		}
		panelQuery := clonePanelQuery(q)
		panelQuery.Set("id", id)
		setupPanel(&v, panelQuery, demo, true)
		v.Card.Mode, v.Card.Title, v.Card.Status = panelMode(demo), v.Detail.Title, rec.Hypothesis.Status
		v.Card.BackURL = href("tab", "overview", "mode", panelMode(demo))
		if v.Tab == "overview" {
			v.Monitor, err = h.monitor(ctx, index.Snapshots, records.Records, "", "", "", false, demo)
			if err != nil {
				return v, err
			}
			h.loadOverview(ctx, v.Monitor, q, v.Tab, v.Mode)
		}
		snap = rec.EvidenceSnapshotID
		for _, s := range index.Snapshots {
			v.Snapshots = append(v.Snapshots, views.Link{
				Label: date(s.AsOf), Detail: shortID(s.SnapshotID), URL: href("snapshot", s.SnapshotID),
			})
		}
		return v, nil
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
func hasPanelSelection(q url.Values) bool {
	return q.Get("id") != "" || q.Get("monitor") != "" || q.Get("snapshot") != "" || q.Get("overview") != ""
}

func panelFocusQuery(q url.Values, focus product.Focus) (url.Values, product.Focus) {
	seen := focusFingerprint(focus)
	if q.Has("focus_seen") && q.Get("focus_seen") != seen && (focus.ProductKey != "" || q.Has("focus_open")) {
		q = url.Values{}
		if focus.ProductKey != "" {
			q.Set("focus_open", seen)
		}
	}
	q.Set("focus_seen", seen)
	if focus.Synthetic && q.Get("focus_open") != seen && !hasPanelSelection(q) {
		return q, product.Focus{}
	}
	return q, focus
}

func cardIDForView(q url.Values, focus product.Focus) string {
	if !hasPanelSelection(q) {
		return focus.HypothesisID
	}
	return q.Get("id")
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
	if r.Header.Get("HX-Request") != "true" {
		return views.Index(v).Render(r.Context(), w)
	}
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
	if h.delivery != nil {
		if rec, gerr := h.svc.Get(r.Context(), decision.GetInput{HypothesisID: id}); gerr == nil {
			h.delivery.AfterApprove(r.Context(), rec)
		}
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
	out, err := h.svc.Import(r.Context(), evidence.ImportInput{Document: string(raw)})
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
