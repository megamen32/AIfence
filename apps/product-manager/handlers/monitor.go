package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/views"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/airlockrun/agentsdk"
	"github.com/google/uuid"
)

type monitorObservation struct {
	evidence.Row
	snapshot string
	received *time.Time
	details  monitorDetails
}

type monitorDetails struct {
	Origin         string          `json:"origin"`
	Synthetic      bool            `json:"synthetic"`
	Detected       string          `json:"detected_at"`
	Metric         string          `json:"metric_key"`
	Scenario       string          `json:"scenario_id"`
	Control        string          `json:"control_product_key"`
	TargetCurrent  *float64        `json:"current_mean"`
	ControlCurrent *float64        `json:"control_current_mean"`
	TargetBase     *float64        `json:"baseline_mean"`
	ControlBase    *float64        `json:"control_baseline_mean"`
	Baseline       evidence.Period `json:"baseline_period"`
	Change         *float64        `json:"relative_change_pct"`
	Stage          string          `json:"stage"`
	Reason         string          `json:"reason"`
	Screenshot     string          `json:"screenshot_path"`
}

func productName(key string) string {
	switch key {
	case "kaspersky_endpoint_security":
		return "Kaspersky Endpoint Security"
	case "demo_target":
		return "Учебный продукт"
	case "demo_control":
		return "Учебный контроль"
	default:
		return strings.ReplaceAll(key, "_", " ")
	}
}

func originText(d monitorDetails) string {
	if d.Synthetic || d.Origin == "synthetic_fixture" {
		return "Учебные данные"
	}
	if d.Origin == "imported_historical" {
		return "Исторические данные"
	}
	return ""
}

func signalTime(detected string, received *time.Time) string {
	if t, err := time.Parse(time.RFC3339, detected); err == nil {
		return "Обнаружено " + t.UTC().Format("02.01.2006, 15:04 UTC")
	}
	if received != nil && !received.IsZero() {
		return "получено в " + received.UTC().Format("15:04 UTC, 02.01.2006")
	}
	return "Время обнаружения неизвестно"
}

func monitorCell(o monitorObservation) (column int, cell views.MonitorCell) {
	if o.Metric == watchdogMetric {
		if o.Status == "observed" && o.Value == "true" {
			return 0, views.MonitorCell{Label: "Тревога", Tone: "alarm"}
		}
		if o.Status == "observed" && o.Value == "false" {
			return 0, views.MonitorCell{Label: "Без тревоги", Tone: "quiet"}
		}
		return 0, views.MonitorCell{Label: "Неполный замер"}
	}
	if o.Source == "sales" && (o.Metric == "family_orders_idx" || o.Metric == "family_orders_idx.relative_change_pct" || o.Metric == "orders_index") {
		if o.Status == "observed" && o.Value != "null" {
			return 0, views.MonitorCell{Label: "Есть замер", Tone: "quiet"}
		}
		return 0, views.MonitorCell{Label: "Нет замера"}
	}
	if o.Metric == "cfg_reach_rate.observed_rows" || o.Metric == "cfg_add_to_cart_rate" {
		if o.Status == "observed" && o.Value != "null" {
			return 1, views.MonitorCell{Label: "Есть замер", Tone: "quiet"}
		}
		return 1, views.MonitorCell{Label: "Нет замера"}
	}
	if o.Source == "browser" && o.Kind == "path_check" {
		switch o.Segment {
		case "desktop":
			column = 2
		case "mobile":
			column = 3
		default:
			return -1, cell
		}
		if o.Status != "observed" {
			return column, views.MonitorCell{Label: "Не проверено"}
		}
		switch o.Value {
		case "ok":
			return column, views.MonitorCell{Label: "Путь пройден", Tone: "quiet"}
		case "path_broken", "metrika_add_not_observed":
			return column, views.MonitorCell{Label: "Есть сбой", Tone: "alarm"}
		default:
			return column, views.MonitorCell{Label: "Не проверено"}
		}
	}
	return -1, cell
}

func (h *Handler) monitor(ctx context.Context, snapshots []evidence.Summary, records []decision.Record, selected, pinned, alert string, autoOpen bool) (*views.Monitor, error) {
	// Look at recent owner snapshots; a pinned older source is still reachable.
	ids := []string{}
	if pinned != "" {
		ids = append(ids, pinned)
	}
	for _, s := range snapshots[:min(8, len(snapshots))] {
		if !slices.Contains(ids, s.SnapshotID) {
			ids = append(ids, s.SnapshotID)
		}
	}
	var rows []monitorObservation
	for _, id := range ids {
		data, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: id, Limit: 100})
		if err != nil {
			return nil, err
		}
		for _, row := range data.Rows {
			o := monitorObservation{Row: row, snapshot: id, received: data.ImportedAt}
			_ = json.Unmarshal([]byte(row.Details), &o.details)
			rows = append(rows, o)
		}
	}
	return monitorView(rows, records, selected, pinned, alert, autoOpen), nil
}

func monitorView(rows []monitorObservation, records []decision.Record, selected, pinned, alert string, autoOpen bool) *views.Monitor {
	slices.SortStableFunc(rows, func(a, b monitorObservation) int { return strings.Compare(b.Period.To, a.Period.To) })
	fresh, active := watchdogState(rows)
	if autoOpen && selected == "" {
		for _, row := range active {
			if originText(row.details) != "Учебные данные" {
				selected, pinned, alert = row.Product, row.snapshot, row.ID
				break
			}
		}
	}
	m := &views.Monitor{}
	products := map[string]int{}
	var focusRows []monitorObservation
	for _, row := range rows {
		col, cell := monitorCell(row)
		if selected != "" && (pinned == "" || row.snapshot == pinned) {
			focusRows = append(focusRows, row)
		}
		if col < 0 || row.Product == "" {
			continue
		}
		i, ok := products[row.Product]
		link := href("monitor", row.Product)
		if !ok {
			i = len(m.Products)
			products[row.Product] = i
			m.Products = append(m.Products, views.MonitorProduct{Name: productName(row.Product), URL: link, Origin: originText(row.details)})
		}
		p := &m.Products[i]
		if p.Origin == "" && originText(row.details) == "Учебные данные" {
			p.Origin = "Учебные данные"
		}
		if old := p.Cells[col].Label; old == "" || col == 0 && row.Metric == watchdogMetric &&
			(old == "Есть замер" || old == "Нет замера") {
			p.Cells[col] = cell
		}
	}
	for _, row := range active {
		p := &m.Products[products[row.Product]]
		change := ""
		if row.details.Change != nil {
			change = num(*row.details.Change) + " %"
		}
		link := href("monitor", row.Product, "snapshot", row.snapshot, "alert", row.ID)
		if p.AlertWeek == "" {
			p.AlertWeek, p.URL = date(row.Period.To), link
			p.Cells[0] = views.MonitorCell{Label: "Открытая тревога", Tone: "alarm"}
			if latest := fresh[seriesOf(row)]; latest.Status != "observed" || (latest.Value != "true" && latest.Value != "false") {
				p.Freshness = "Неполный замер " + date(latest.Period.To)
			}
		}
		m.Pulse = append(m.Pulse, views.PulseItem{
			Product: p.Name, URL: link,
			Week: span(row.Period.From, row.Period.To), Detected: signalTime(row.details.Detected, row.received),
			Change: change, Origin: originText(row.details),
		})
	}
	for _, row := range rows {
		key := row.details.Control
		if row.Metric == watchdogMetric && row.Status == "observed" && row.details.ControlCurrent != nil &&
			key != "" {
			if _, ok := products[key]; ok {
				continue
			}
			products[key] = len(m.Products)
			p := views.MonitorProduct{Name: productName(key), URL: href("monitor", row.Product, "snapshot", row.snapshot), Origin: originText(row.details)}
			p.Cells[0] = views.MonitorCell{Label: "Контроль", Tone: "quiet"}
			m.Products = append(m.Products, p)
		}
	}
	if selected != "" {
		if alert != "" {
			m.Selected = investigation(selected, focusRows, records, alert)
		} else {
			m.Selected = investigation(selected, focusRows, records)
		}
		m.Selected.ProductKey, m.Selected.SnapshotID, m.Selected.AlertID = selected, pinned, alert
	}
	return m
}

type watchdogSeries struct{ product, segment, metric, control, origin, scenario, source string }

func seriesOf(o monitorObservation) watchdogSeries {
	source, _, _ := strings.Cut(o.Ref.Locator, "#")
	return watchdogSeries{o.Product, o.Segment, o.details.Metric, o.details.Control, originText(o.details), o.details.Scenario, source}
}

// A missing week says nothing about recovery. Only compare observed decisions
// for the same monitored product, segment, metric, control and data origin.
func watchdogState(rows []monitorObservation) (map[watchdogSeries]monitorObservation, []monitorObservation) {
	fresh := map[watchdogSeries]monitorObservation{}
	definitive := map[watchdogSeries]bool{}
	var open []monitorObservation
	for _, row := range rows {
		if row.Metric != watchdogMetric || row.Product == "" {
			continue
		}
		key := seriesOf(row)
		if _, ok := fresh[key]; !ok {
			fresh[key] = row
		}
		if definitive[key] || row.Status != "observed" || (row.Value != "true" && row.Value != "false") {
			continue
		}
		definitive[key] = true
		if row.Value == "true" {
			open = append(open, row)
		}
	}
	return fresh, open
}

func (h *Handler) cardInvestigation(ctx context.Context, record decision.Record, orders, margin string) (*views.Investigation, error) {
	snapshot := record.EvidenceSnapshotID
	rows := []monitorObservation{}
	for offset := 0; ; offset += 100 {
		data, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: snapshot, Offset: offset, Limit: 100})
		if err != nil {
			return nil, err
		}
		for _, row := range data.Rows {
			o := monitorObservation{Row: row, snapshot: snapshot, received: data.ImportedAt}
			_ = json.Unmarshal([]byte(row.Details), &o.details)
			rows = append(rows, o)
		}
		if offset+len(data.Rows) >= data.Total || len(data.Rows) == 0 {
			break
		}
	}
	v := savedCard(record, rows)
	monitorEconomics(v, orders, margin)
	return v, nil
}

func savedCard(record decision.Record, rows []monitorObservation) *views.Investigation {
	key := record.Hypothesis.ProductKey
	v := investigation(key, rows, []decision.Record{record}, record.Hypothesis.SignalIDs...)
	v.CardID, v.RecordURL = record.Hypothesis.ID, ""
	v.ProductKey, v.SnapshotID = key, record.EvidenceSnapshotID
	return v
}

func investigation(key string, rows []monitorObservation, records []decision.Record, preferred ...string) *views.Investigation {
	v := &views.Investigation{Product: productName(key), BackURL: "./?overview=1", Scale: "Не доказано"}
	var signal *monitorObservation
	for i := range rows {
		row := rows[i]
		if row.Product == key && row.Metric == watchdogMetric && row.Status == "observed" &&
			(len(preferred) == 0 || slices.Contains(preferred, row.ID)) {
			if signal == nil || signal.Value != "true" && row.Value == "true" {
				signal = &rows[i]
			}
		}
	}
	var record *decision.Record
	for i := range records {
		if records[i].Hypothesis.ProductKey == key && (signal == nil || records[i].EvidenceSnapshotID == signal.snapshot) {
			record = &records[i]
			break
		}
	}
	if signal != nil {
		v.Origin, v.Detected, v.Week = originText(signal.details), signalTime(signal.details.Detected, signal.received), span(signal.Period.From, signal.Period.To)
		if signal.details.Control != "" {
			v.Control = productName(signal.details.Control)
		}
		v.SnapshotURL = href("snapshot", signal.snapshot, "obs", signal.ID)
	}
	for _, row := range rows {
		if row.Product != key || signal != nil && row.snapshot != signal.snapshot {
			continue
		}
		if len(v.Evidence) < 8 && row.Metric != watchdogMetric {
			v.Evidence = append(v.Evidence, rowView(row.snapshot, row.Row))
		}
		if row.Source == "release" && row.Status == "observed" && v.Release == "" {
			v.Release, v.ReleaseDate, v.ReleaseURL = row.Value, row.Period.From, href("snapshot", row.snapshot, "obs", row.ID)
		}
		if row.Source == "browser" && row.Kind == "path_check" {
			device := label(map[string]string{"desktop": "Desktop", "mobile": "Mobile"}, row.Segment)
			step := label(browserStages, row.details.Stage)
			verdict := "Результат неизвестен"
			if row.Status == "observed" && row.Value != "" {
				verdict = label(browserVerdicts, row.Value)
			}
			if row.details.Reason == "timeout" {
				verdict = "Таймаут"
			}
			img := ""
			if row.details.Screenshot != "" {
				img = href("snapshot", row.snapshot, "frame", row.ID)
			}
			v.Frames = append(v.Frames, views.BrowserFrame{Device: device, Step: step, Verdict: verdict, Image: img, SourceURL: href("snapshot", row.snapshot, "obs", row.ID)})
		}
	}
	if record != nil {
		v.RecordURL = href("id", record.Hypothesis.ID)
		if v.SnapshotURL == "" {
			v.SnapshotURL = href("snapshot", record.EvidenceSnapshotID)
		}
		if a := record.Assessment; a != nil {
			v.Assessment = a.Reason
			if a.Status == "supported" {
				v.Scale = "Подтверждено по замеру"
			} else if a.Status == "rejected" {
				v.Scale = "Критерий не достигнут"
			}
			if a.ReleasedOn != "" {
				v.Release, v.ReleaseDate = a.ReleaseID, a.ReleasedOn
				v.ReleaseURL = ""
				if snap, id, ok := strings.Cut(a.ReleaseEvidenceRef, "/"); ok {
					v.ReleaseURL = href("snapshot", snap, "obs", id)
				}
			}
			if m := a.Measurement; m != nil && a.Value != nil && m.Baseline.Complete && m.Current.Complete &&
				m.Baseline.Value != nil && m.Current.Value != nil {
				v.Before = num(*m.Baseline.Value) + " " + m.Baseline.Unit
				v.After = num(*m.Current.Value) + " " + m.Current.Unit
			}
		}
	}
	if signal != nil {
		v.Chart = plot(signal, rows, v.ReleaseDate)
	} else if record != nil {
		var source *monitorObservation
		v.Chart, v.Control, source = legacyTrend(*record, rows, v.ReleaseDate)
		if source != nil {
			v.Origin = originText(source.details)
			v.Detected = signalTime(source.details.Detected, source.received)
		}
	}
	return v
}

var browserStages = map[string]string{
	"": "Шаг неизвестен", "entry": "Карточка продукта", "browser": "Запуск браузера",
	"configuration": "Выбор лицензии", "add_to_cart": "Добавление в корзину",
	"open_cart": "Открытие корзины", "cart_item": "Товар в корзине",
}

type reading struct {
	date    time.Time
	target  float64
	control *float64
}

func plot(signal *monitorObservation, rows []monitorObservation, release string) views.Chart {
	var data []reading
	seen := map[time.Time]bool{}
	add := func(day string, target, control *float64) {
		t, err := time.Parse(time.DateOnly, day)
		if err != nil || target == nil || control == nil || seen[t] {
			return
		}
		seen[t] = true
		data = append(data, reading{t, *target, control})
	}
	for _, row := range rows {
		if row.snapshot == signal.snapshot && row.Product == signal.Product && row.Metric == watchdogMetric && row.Status == "observed" &&
			row.Segment == signal.Segment && row.details.Control == signal.details.Control {
			add(row.Period.To, row.details.TargetCurrent, row.details.ControlCurrent)
		}
	}
	add(signal.details.Baseline.To, signal.details.TargetBase, signal.details.ControlBase)
	return drawTrend(data, release, "Средние по наблюдаемым окнам")
}

type legacyWindows struct {
	BaselineFrom, BaselineTo, CurrentFrom string
	Baseline, Current                     *float64
}

func legacyReading(row monitorObservation) (legacyWindows, bool) {
	var d struct {
		Variant      string   `json:"variant"`
		BaselineFrom string   `json:"baseline_from"`
		BaselineTo   string   `json:"baseline_to"`
		CurrentFrom  string   `json:"current_from"`
		Baseline     *float64 `json:"baseline_mean"`
		Current      *float64 `json:"current_mean"`
	}
	if row.Source != "sales" || row.Status != "observed" || row.Metric != "family_orders_idx.relative_change_pct" ||
		json.Unmarshal([]byte(row.Details), &d) != nil || d.Variant != "full_weeks" || d.Baseline == nil || d.Current == nil {
		return legacyWindows{}, false
	}
	return legacyWindows{d.BaselineFrom, d.BaselineTo, d.CurrentFrom, d.Baseline, d.Current}, true
}

func legacyTrend(record decision.Record, rows []monitorObservation, release string) (views.Chart, string, *monitorObservation) {
	h := record.Hypothesis
	for _, row := range rows {
		if row.Product != h.ProductKey || row.snapshot != record.EvidenceSnapshotID || !slices.Contains(h.SignalIDs, row.ID) {
			continue
		}
		d, ok := legacyReading(row)
		if !ok {
			continue
		}
		before, e1 := time.Parse(time.DateOnly, d.BaselineTo)
		after, e2 := time.Parse(time.DateOnly, row.Period.To)
		if e1 != nil || e2 != nil || !before.Before(after) {
			continue
		}
		points := []reading{{date: before, target: *d.Baseline}, {date: after, target: *d.Current}}
		control := ""
		for _, candidate := range rows {
			if h.SuccessCriterion.ControlProductKey == "" || candidate.Product != h.SuccessCriterion.ControlProductKey ||
				candidate.snapshot != row.snapshot || candidate.Period != row.Period || candidate.Segment != row.Segment || candidate.Unit != row.Unit ||
				!slices.Contains(h.CounterIDs, candidate.ID) {
				continue
			}
			other, ok := legacyReading(candidate)
			if ok && other.BaselineFrom == d.BaselineFrom && other.BaselineTo == d.BaselineTo && other.CurrentFrom == d.CurrentFrom {
				points[0].control, points[1].control = other.Baseline, other.Current
				control = productName(candidate.Product)
				break
			}
		}
		return drawTrend(points, release, "Средние индексы по полным неделям"), control, &row
	}
	return views.Chart{}, "", nil
}

func drawTrend(data []reading, release, caption string) views.Chart {
	if len(data) < 2 {
		return views.Chart{}
	}
	slices.SortFunc(data, func(a, b reading) int { return a.date.Compare(b.date) })
	first, last := data[0].date, data[len(data)-1].date
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, d := range data {
		lo, hi = min(lo, d.target), max(hi, d.target)
		if d.control != nil {
			lo, hi = min(lo, *d.control), max(hi, *d.control)
		}
	}
	y := func(value float64) string {
		if hi == lo {
			return "104"
		}
		return strconv.FormatFloat(174-(value-lo)/(hi-lo)*140, 'f', 1, 64)
	}
	x := func(t time.Time) string {
		return strconv.FormatFloat(30+t.Sub(first).Seconds()/last.Sub(first).Seconds()*540, 'f', 1, 64)
	}
	c := views.Chart{First: first.Format("02.01"), Last: last.Format("02.01"), Min: num(lo), Max: num(hi), Caption: caption}
	if t, err := time.Parse(time.DateOnly, release); err == nil && !t.Before(first) && !t.After(last) {
		c.ReleaseX = x(t)
	}
	var target, control []string
	for _, d := range data {
		p, t := x(d.date), y(d.target)
		dot := views.ChartDot{X: p, TargetY: t, Label: d.date.Format("02.01.2006") + " | " + num(d.target)}
		target = append(target, p+","+t)
		if d.control != nil {
			dot.ControlY = y(*d.control)
			dot.Label += " / " + num(*d.control)
			control = append(control, p+","+dot.ControlY)
		}
		c.Dots = append(c.Dots, dot)
	}
	c.Target = strings.Join(target, " ")
	if len(control) == len(target) {
		c.Control = strings.Join(control, " ")
		slices.Reverse(control)
		c.Fill = c.Target + " " + strings.Join(control, " ")
	}
	return c
}

func monitorEconomics(v *views.Investigation, orders, margin string) {
	if orders == "" && margin == "" {
		return
	}
	v.Orders, v.Margin = orders, margin
	parse := func(s string) (float64, bool) {
		n, e := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(s), ",", "."), 64)
		return n, e == nil && !math.IsNaN(n) && n >= 0 && n <= 1e8
	}
	n, ordersOK := parse(orders)
	m, marginOK := parse(margin)
	if !ordersOK || !marginOK {
		v.CostError = "Введите два неотрицательных числа не больше 100 млн."
		return
	}
	v.Cost = rub(n*m) + " / неделю"
}

func (h *Handler) browserFrame(w http.ResponseWriter, r *http.Request) error {
	ctx, q := r.Context(), r.URL.Query()
	user, ok := agentsdk.CallerFromContext(ctx).User()
	if !ok || h.files == nil || q.Get("snapshot") == "" {
		http.NotFound(w, r)
		return nil
	}
	data, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: q.Get("snapshot"), IDs: []string{q.Get("frame")}, Source: "browser", Limit: 1})
	if err != nil || len(data.Rows) != 1 {
		http.NotFound(w, r)
		return nil
	}
	var d monitorDetails
	_ = json.Unmarshal([]byte(data.Rows[0].Details), &d)
	path := callerFramePath(d.Screenshot, user.ID)
	if path == "" {
		http.NotFound(w, r)
		return nil
	}
	resolved, err := h.files.ResolveFilePath(ctx, path, agentsdk.FileOperationRead)
	if err != nil || string(resolved) != path {
		http.NotFound(w, r)
		return nil
	}
	ref, err := h.files.StatFileRef(ctx, path)
	if err != nil || ref.Path != d.Screenshot {
		http.NotFound(w, r)
		return nil
	}
	f, err := h.files.OpenFile(ctx, path)
	if err != nil {
		http.NotFound(w, r)
		return nil
	}
	defer f.Close()
	const maxPNG = 8 << 20
	png, err := io.ReadAll(io.LimitReader(f, maxPNG+1))
	if err != nil || len(png) > maxPNG || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		http.NotFound(w, r)
		return nil
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, err = w.Write(png)
	return err
}

func callerFramePath(path, user string) string {
	parts := strings.Split(path, "/")
	if len(parts) != 8 || parts[0] != "" || parts[1] != "apps" || parts[3] != "tmp" ||
		parts[4] != "path-check" || parts[5] != "user-"+user ||
		(parts[7] != "desktop.png" && parts[7] != "mobile.png") {
		return ""
	}
	if _, err := uuid.Parse(parts[2]); err != nil {
		return ""
	}
	if _, err := uuid.Parse(parts[6]); err != nil {
		return ""
	}
	return strings.Join(parts[3:], "/")
}
