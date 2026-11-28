package handlers

import (
	"agent/internal/decision"
	"agent/internal/evidence"
	"agent/views"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	Origin        string   `json:"origin"`
	Synthetic     bool     `json:"synthetic"`
	Detected      string   `json:"detected_at"`
	FirstDetected string   `json:"first_detected_at"`
	Metric        string   `json:"metric_key"`
	Scenario      string   `json:"scenario_id"`
	Control       string   `json:"control_product_key"`
	Stage         string   `json:"stage"`
	Reason        string   `json:"reason"`
	Screenshot    string   `json:"screenshot_path"`
	Vendor        string   `json:"vendor"`
	ControlVendor string   `json:"control_vendor"`
	ReleaseID     string   `json:"release_id"`
	ReleaseDate   string   `json:"release_date"`
	Adjusted      *float64 `json:"control_adjusted_change_pct"`
}

// preparedOrigin labels a prepared synthetic snapshot so the panel does not
// present seeded values as production observations.
const preparedOrigin = "ДЕМО · синтетический срез"

func originText(d monitorDetails) string {
	if d.Synthetic || d.Origin == "synthetic_fixture" {
		return preparedOrigin
	}
	if d.Origin == "imported_historical" {
		return "Исторические данные"
	}
	return ""
}

func observationOrigin(row monitorObservation) string {
	if educationalObservation(row.Row, row.details) {
		return preparedOrigin
	}
	return originText(row.details)
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

func (h *Handler) monitor(ctx context.Context, snapshots []evidence.Summary, records []decision.Record, selected, pinned, alert string, autoOpen, demo bool) (*views.Monitor, error) {
	rows, err := h.monitorRows(ctx, snapshots)
	if err != nil {
		return nil, err
	}
	return monitorView(separateMonitorRows(rows, demo), records, selected, pinned, alert, autoOpen), nil
}

func (h *Handler) monitorRows(ctx context.Context, snapshots []evidence.Summary) ([]monitorObservation, error) {
	ids, err := h.svc.Evidence.MonitorSnapshotIDs(ctx, snapshots)
	if err != nil {
		return nil, err
	}
	var rows []monitorObservation
	for _, id := range ids {
		data, err := h.readMonitorSnapshot(ctx, id)
		if err != nil {
			return nil, err
		}
		rows = append(rows, data...)
	}
	return rows, nil
}

func (h *Handler) readMonitorSnapshot(ctx context.Context, snapshotID string) ([]monitorObservation, error) {
	const pageSize = 100
	var rows []monitorObservation
	for offset := 0; ; {
		data, err := h.svc.Evidence.Read(ctx, evidence.Query{SnapshotID: snapshotID, Offset: offset, Limit: pageSize})
		if err != nil {
			return nil, err
		}
		for _, row := range data.Rows {
			o := monitorObservation{Row: row, snapshot: snapshotID, received: data.ImportedAt}
			_ = json.Unmarshal([]byte(row.Details), &o.details)
			rows = append(rows, o)
		}
		if monitorPageComplete(offset, len(data.Rows), data.Total) {
			return rows, nil
		}
		offset += len(data.Rows)
	}
}

func monitorPageComplete(offset, rows, total int) bool {
	return rows == 0 || offset+rows >= total
}

func monitorView(rows []monitorObservation, records []decision.Record, selected, pinned, alert string, autoOpen bool) *views.Monitor {
	slices.SortStableFunc(rows, func(a, b monitorObservation) int { return strings.Compare(b.Period.To, a.Period.To) })
	fresh, active := watchdogState(rows)
	active, firstDetected := consolidateActiveAlerts(active, rows)
	if autoOpen && selected == "" {
		for _, row := range active {
			if legacyFixtureFamily(row.Product) == "" && !educationalObservation(row.Row, row.details) &&
				firstSignalLabel(row, rows) != "" {
				selected, pinned, alert = row.Product, row.snapshot, row.ID
				break
			}
		}
	}
	m := &views.Monitor{}
	for _, rec := range records {
		for _, row := range rows {
			if row.snapshot == rec.EvidenceSnapshotID && row.Product == rec.Hypothesis.ProductKey &&
				slices.Contains(rec.Hypothesis.SignalIDs, row.ID) {
				m.Records = append(m.Records, views.Link{
					Label: observedProductName(row.Product, row), URL: href("id", rec.Hypothesis.ID),
					Detail: label(statusTitles, rec.Hypothesis.Status),
				})
				switch rec.Hypothesis.Status {
				case "supported", "rejected":
				default:
					m.ReviewCount++
				}
				break
			}
		}
	}
	for _, row := range rows {
		if row.received != nil && !row.received.IsZero() {
			stamp := row.received.UTC().Format(time.RFC3339)
			if stamp > m.Updated {
				m.Updated = stamp
			}
		}
	}
	if t, err := time.Parse(time.RFC3339, m.Updated); err == nil {
		m.Updated = t.Format("02.01.2006 15:04 UTC")
	}
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
			m.Products = append(m.Products, views.MonitorProduct{Name: observedProductName(row.Product, row), URL: link, Origin: observationOrigin(row)})
		}
		p := &m.Products[i]
		if p.Origin == "" && educationalObservation(row.Row, row.details) {
			p.Name, p.Origin = observedProductName(row.Product, row), observationOrigin(row)
		}
		if old := p.Cells[col].Label; old == "" || col == 0 && row.Metric == watchdogMetric &&
			(old == "Есть замер" || old == "Нет замера") {
			p.Cells[col] = cell
		}
	}
	for _, row := range active {
		p := &m.Products[products[row.Product]]
		first := firstDetected[row.Product]
		if first == "" {
			first = firstSignalLabel(row, rows)
		}
		if first == "" {
			m.Diagnostics = append(m.Diagnostics, views.Link{
				Label: p.Name, URL: href("snapshot", row.snapshot, "obs", row.ID),
				Detail: "Нет отметки обнаружения в " + row.Ref.SourceID,
			})
			m.ReviewCount++
			continue
		}
		window := evidence.WatchdogWindow(row.Row)
		magnitude := signalMagnitude(window)
		delta := signalDelta(window)
		change, comparison := "", ""
		if magnitude != nil {
			change, comparison = magnitude.Change, magnitude.Caption
		}
		link := href("monitor", row.Product, "snapshot", row.snapshot, "alert", row.ID)
		if p.AlertWeek == "" {
			p.AlertWeek, p.URL = date(row.Period.To), link
			p.Cells[0] = views.MonitorCell{Label: "Открытая тревога", Tone: "alarm"}
			if latest := fresh[seriesOf(row)]; latest.Status != "observed" || (latest.Value != "true" && latest.Value != "false") {
				p.Freshness = "Неполный замер " + date(latest.Period.To)
			}
		}
		weekly, basis, amount := signalPlanning(records, row)
		weeklyLoss, weeklyLossBasis := demoWeeklyLoss(rows, row, window)
		m.Pulse = append(m.Pulse, views.PulseItem{
			Product: p.Name, ProductKey: row.Product, ControlKey: row.details.Control, ControlName: productName(row.details.Control), Vendor: row.details.Vendor, ControlVendor: row.details.ControlVendor, Delta: delta, URL: link, FirstDetected: first,
			Freshness: p.Freshness, Weekly: weekly, WeeklyBasis: basis, WeeklyLoss: weeklyLoss, WeeklyLossBasis: weeklyLossBasis, Amount: amount,
			Coverage: signalCoverage(rows, row.Product, row.snapshot),
			Week:     span(row.Period.From, row.Period.To), Detected: signalTime(row.details.Detected, row.received),
			Change: change, Comparison: comparison, Origin: observationOrigin(row),
		})
	}
	for _, row := range rows {
		key := row.details.Control
		if row.Metric == watchdogMetric && key != "" && evidence.WatchdogWindow(row.Row) != nil {
			if _, ok := products[key]; ok {
				continue
			}
			products[key] = len(m.Products)
			p := views.MonitorProduct{Name: observedProductName(key, row), URL: href("monitor", row.Product, "snapshot", row.snapshot), Origin: observationOrigin(row)}
			p.Cells[0] = views.MonitorCell{Label: "Контроль", Tone: "quiet"}
			m.Products = append(m.Products, p)
		}
	}
	slices.SortStableFunc(m.Pulse, func(a, b views.PulseItem) int {
		if a.Amount == nil && b.Amount == nil {
			return 0
		}
		if a.Amount == nil {
			return 1
		}
		if b.Amount == nil {
			return -1
		}
		if *a.Amount < *b.Amount {
			return 1
		}
		if *a.Amount > *b.Amount {
			return -1
		}
		return 0
	})
	if selected != "" {
		if alert != "" {
			m.Selected = investigation(selected, focusRows, records, alert)
		} else {
			m.Selected = investigation(selected, focusRows, records)
		}
		m.Selected.ProductKey, m.Selected.SnapshotID, m.Selected.AlertID = selected, pinned, alert
		m.Selected.Prices = currentPrices(selected, pinned, focusRows, records)
	}
	return m
}

func signalDelta(window *evidence.WindowContrast) *float64 {
	if window == nil {
		return nil
	}
	if window.BaselineMean > 0 && window.ControlCurrentMean > 0 {
		value := (window.CurrentMean/window.BaselineMean*window.ControlBaselineMean/window.ControlCurrentMean - 1) * 100
		if !math.IsNaN(value) && !math.IsInf(value, 0) {
			return &value
		}
	}
	if !math.IsNaN(window.RelativeChangePct) && !math.IsInf(window.RelativeChangePct, 0) {
		value := window.RelativeChangePct
		return &value
	}
	return nil
}

type watchdogSeries struct{ product, segment, metric, control, origin, scenario, source string }

func seriesOf(o monitorObservation) watchdogSeries {
	source, _, _ := strings.Cut(o.Ref.Locator, "#")
	return watchdogSeries{o.Product, o.Segment, o.details.Metric, o.details.Control, observationOrigin(o), o.details.Scenario, source}
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

func consolidateActiveAlerts(active []monitorObservation, rows []monitorObservation) ([]monitorObservation, map[string]string) {
	selected := map[string]monitorObservation{}
	firstAt := map[string]time.Time{}
	firstLabel := map[string]string{}
	for _, row := range active {
		key := row.Product
		if current, ok := selected[key]; !ok || alertStrength(row) > alertStrength(current) {
			selected[key] = row
		}
		label := firstSignalLabel(row, rows)
		if label == "" {
			continue
		}
		firstLabel[key] = label
		const prefix = "Впервые обнаружено "
		if value, ok := strings.CutPrefix(label, prefix); ok {
			if parsed, err := time.Parse("02.01.2006, 15:04 UTC", value); err == nil && (firstAt[key].IsZero() || parsed.Before(firstAt[key])) {
				firstAt[key] = parsed
			}
		}
	}
	out := make([]monitorObservation, 0, len(selected))
	seen := map[string]bool{}
	for _, row := range active {
		if seen[row.Product] || selected[row.Product].ID != row.ID {
			continue
		}
		seen[row.Product] = true
		out = append(out, row)
	}
	labels := map[string]string{}
	for product, label := range firstLabel {
		if at := firstAt[product]; !at.IsZero() {
			labels[product] = "Впервые обнаружено " + at.UTC().Format("02.01.2006, 15:04 UTC")
		} else {
			labels[product] = label
		}
	}
	return out, labels
}

func alertStrength(row monitorObservation) float64 {
	window := evidence.WatchdogWindow(row.Row)
	if window == nil || math.IsNaN(window.RelativeChangePct) || math.IsInf(window.RelativeChangePct, 0) {
		return -1
	}
	return math.Abs(window.RelativeChangePct)
}

const demoKESSKU = "KL4863RAKFS"

func demoWeeklyLoss(rows []monitorObservation, signal monitorObservation, window *evidence.WindowContrast) (string, string) {
	if window == nil || window.RelativeChangePct >= 0 || !isKESSignal(signal) {
		return "", ""
	}
	price := demoKESPrice(rows, signal.snapshot)
	orders := demoWeeklyOrders(rows, signal)
	missing := orders * math.Abs(window.RelativeChangePct) / 100
	if price <= 0 || orders <= 0 || missing <= 0 {
		return "", ""
	}
	return "Потеря ≈ " + rub(price*missing) + " в неделю", "ДЕМО · " + rub(price) + " × " + num(missing) + " недобор заказов"
}

func isKESSignal(signal monitorObservation) bool {
	name := strings.ToLower(observedProductName(signal.Product, signal))
	return strings.Contains(name, "kaspersky endpoint security") || strings.Contains(strings.ToUpper(signal.Product), "KL4863")
}

func demoKESPrice(rows []monitorObservation, snapshot string) float64 {
	const seededPrice = 3500000.0
	best, bestPeriod := 0.0, ""
	for _, row := range rows {
		if row.Source != "market" || row.Kind != "offer" || row.Metric != "displayed_price" || row.Status != "observed" || row.Unit != "RUB" {
			continue
		}
		var details struct {
			SKU string `json:"sku"`
		}
		_ = json.Unmarshal([]byte(row.Details), &details)
		if !strings.EqualFold(row.Product, demoKESSKU) && !strings.EqualFold(details.SKU, demoKESSKU) {
			continue
		}
		if snapshot != "" && row.snapshot != snapshot {
			continue
		}
		value, err := strconv.ParseFloat(row.Value, 64)
		if err != nil || value <= 0 || (bestPeriod != "" && row.Period.To <= bestPeriod) {
			continue
		}
		best, bestPeriod = value, row.Period.To
	}
	if best > 0 {
		return best
	}
	return seededPrice
}

func demoWeeklyOrders(rows []monitorObservation, signal monitorObservation) float64 {
	for _, row := range rows {
		if row.Source != "sales" || row.Status != "observed" || (row.Product != signal.details.Control && row.Product != signal.Product) {
			continue
		}
		var details map[string]float64
		if json.Unmarshal([]byte(row.Details), &details) != nil {
			continue
		}
		for _, key := range []string{"weekly_orders", "orders_per_week", "order_count"} {
			if value := details[key]; value > 0 && !math.IsNaN(value) && !math.IsInf(value, 0) {
				return value
			}
		}
	}
	return 1
}

func (h *Handler) cardInvestigation(ctx context.Context, record decision.Record, orders, margin string, related ...evidence.Summary) (*views.Investigation, error) {
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
	if len(related) > 0 && v.ReleaseHistory == nil {
		next, e := h.releaseRows(ctx, rows, record, related)
		if e != nil {
			return nil, e
		}
		if len(next) > 0 {
			v.ReleaseHistory = nativeReleaseHistory(record.Hypothesis.ProductKey, next, next[0].snapshot)
		}
	}
	monitorEconomics(v, orders, margin)
	return v, nil
}

func savedCard(record decision.Record, rows []monitorObservation) *views.Investigation {
	key := record.Hypothesis.ProductKey
	v := investigation(key, rows, []decision.Record{record}, record.Hypothesis.SignalIDs...)
	releaseMeasurement(v, record)
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
		if records[i].Hypothesis.ProductKey == key && linkedRecord(records[i], rows, signal) {
			record = &records[i]
			break
		}
	}
	if signal != nil {
		v.Product = observedProductName(key, *signal)
		v.FirstDetected = firstSignalLabel(*signal, rows)
		v.Coverage = signalCoverage(rows, key, signal.snapshot)
		v.Weekly, v.WeeklyBasis, _ = signalPlanning(records, *signal)
		v.Origin, v.Detected, v.Week = observationOrigin(*signal), signalTime(signal.details.Detected, signal.received), span(signal.Period.From, signal.Period.To)
		if signal.details.Control != "" {
			v.Control = observedProductName(signal.details.Control, *signal)
		}
		v.Demand = demandComparison(key, signal.details.Control, rows)
		v.SnapshotURL = href("snapshot", signal.snapshot, "obs", signal.ID)
		v.Magnitude = signalMagnitude(evidence.WatchdogWindow(signal.Row))
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
			tone := "incomplete"
			if row.Status == "observed" {
				switch row.Value {
				case "path_broken", "metrika_add_not_observed":
					tone = "failed"
				case "ok":
					tone = "verified"
				}
			}
			v.Frames = append(v.Frames, views.BrowserFrame{Device: device, Step: step, Verdict: verdict, Image: img, Tone: tone, SourceURL: href("snapshot", row.snapshot, "obs", row.ID)})
		}
	}
	if record != nil {
		v.Status = record.Hypothesis.Status
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
	snapshot := ""
	if signal != nil {
		snapshot = signal.snapshot
	} else if record != nil {
		snapshot = record.EvidenceSnapshotID
	}
	v.ReleaseHistory = nativeReleaseHistory(key, rows, snapshot)
	if record != nil {
		releaseMeasurement(v, *record)
	}
	if signal != nil {
		v.Chart = plot(signal, rows, v.ReleaseDate)
	} else if record != nil {
		var source *monitorObservation
		v.Chart, v.Control, source = legacyTrend(*record, rows, v.ReleaseDate)
		if source != nil {
			v.Product = observedProductName(key, *source)
			v.Origin = observationOrigin(*source)
			v.Detected = signalTime(source.details.Detected, source.received)
		}
	}
	attachEvidencePins(v)
	return v
}

type demandPoint struct {
	from, to string
	number   float64
}

func demandComparison(product, control string, rows []monitorObservation) *views.DemandComparison {
	if control == "" {
		control = "astra_linux_special_edition"
	}
	result := &views.DemandComparison{
		Product: observedProductName(product, monitorObservation{Row: evidence.Row{Product: product}}),
		Control: productName(control),
		Origin:  preparedOrigin,
	}
	configs := []struct {
		name, unit string
		match      func(monitorObservation) bool
	}{
		{name: "Wordstat", unit: "запросов", match: func(row monitorObservation) bool {
			return row.Metric == "search_queries" && strings.Contains(strings.ToLower(row.Ref.SourceID), "wordstat")
		}},
		{name: "Google Trends", unit: "индекс 0–100", match: func(row monitorObservation) bool {
			return row.Metric == "search_interest" && strings.Contains(strings.ToLower(row.Ref.SourceID), "google")
		}},
	}
	for _, cfg := range configs {
		target := demandPoints(product, cfg.match, rows)
		controlPoints := demandPoints(control, cfg.match, rows)
		source := views.DemandSource{Name: cfg.name, Unit: cfg.unit, Status: "Нет наблюдения", Hint: "Источник не прислал две сопоставимые недели."}
		if len(target) >= 2 && len(controlPoints) >= 2 {
			t0, t1 := target[0], target[len(target)-1]
			c0, c1 := controlPoints[0], controlPoints[len(controlPoints)-1]
			source.TargetBaseline, source.TargetCurrent = demandNumber(t0.number), demandNumber(t1.number)
			source.ControlBaseline, source.ControlCurrent = demandNumber(c0.number), demandNumber(c1.number)
			source.TargetChange, source.ControlChange = demandChange(t0.number, t1.number), demandChange(c0.number, c1.number)
			maxValue := math.Max(t1.number, c1.number)
			if maxValue > 0 {
				source.TargetWidth = num(t1.number / maxValue * 100)
				source.ControlWidth = num(c1.number / maxValue * 100)
			}
			source.Period = span(t0.from, t1.to)
			source.Status, source.Hint = "Сопоставимые недели", "ДЕМО · синтетический срез из сохранённого снимка"
		}
		result.Sources = append(result.Sources, source)
	}
	return result
}

func demandPoints(product string, match func(monitorObservation) bool, rows []monitorObservation) []demandPoint {
	points := make([]demandPoint, 0, 8)
	for _, row := range rows {
		if row.Product != product || !match(row) || row.Status != "observed" {
			continue
		}
		value, err := strconv.ParseFloat(row.Value, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
			continue
		}
		points = append(points, demandPoint{from: row.Period.From, to: row.Period.To, number: value})
	}
	slices.SortStableFunc(points, func(a, b demandPoint) int { return strings.Compare(a.from, b.from) })
	return points
}

func demandNumber(value float64) string {
	return strings.TrimSuffix(strings.TrimSuffix(num(value), "0"), ",")
}

func demandChange(before, after float64) string {
	if before <= 0 {
		return "н/д"
	}
	return signalPercent((after/before - 1) * 100)
}

func attachEvidencePins(v *views.Investigation) {
	if v == nil || len(v.Chart.Dots) == 0 || len(v.Evidence) == 0 {
		return
	}
	seen := make(map[string]struct{}, len(v.Evidence))
	for i, row := range v.Evidence {
		if i >= 4 {
			break
		}
		key := strings.ToLower(strings.TrimSpace(row.Metric)) + "|" + strings.ToLower(strings.TrimSpace(row.Source))
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		dot := v.Chart.Dots[min(i, len(v.Chart.Dots)-1)]
		kind := "М"
		metric := strings.ToLower(row.Metric)
		source := strings.ToLower(row.Source)
		switch {
		case strings.Contains(metric, "цен"):
			kind = "П"
		case strings.Contains(source, "конкур") || strings.Contains(metric, "магаз"):
			kind = "К"
		case strings.Contains(source, "исслед") || strings.Contains(source, "wordstat") || strings.Contains(source, "trends"):
			kind = "И"
		}
		v.Chart.Pins = append(v.Chart.Pins, views.ChartPin{Kind: kind, Title: row.Metric, Detail: row.Value, X: dot.X, Y: dot.TargetY, URL: row.OpenURL})
	}
}

var browserStages = map[string]string{
	"": "Шаг неизвестен", "entry": "Карточка продукта", "browser": "Запуск браузера",
	"configuration": "Выбор лицензии", "add_to_cart": "Добавление в корзину",
	"open_cart": "Открытие корзины", "cart_item": "Товар в корзине",
}

func signalPercent(v float64) string {
	return strings.Replace(num(v), "-", "−", 1) + " %"
}

func signalMagnitude(d *evidence.WindowContrast) *views.SignalMagnitude {
	if d == nil {
		return nil
	}
	mean := func(n float64) string { return strings.TrimSuffix(num(n), ",0") }
	return &views.SignalMagnitude{
		Change: signalPercent(d.RelativeChangePct), Caption: fmt.Sprintf("К предыдущему окну из %d нед.", d.WindowWeeks),
		Baseline: span(d.Baseline.From, d.Baseline.To), Current: span(d.Current.From, d.Current.To),
		Before: mean(d.BaselineMean), After: mean(d.CurrentMean),
	}
}

type reading struct {
	date    time.Time
	target  float64
	control *float64
	period  string
}

func plot(signal *monitorObservation, rows []monitorObservation, release string) views.Chart {
	contrast := evidence.WatchdogWindow(signal.Row)
	if contrast == nil {
		return views.Chart{}
	}
	var data []reading
	seen := map[time.Time]bool{}
	add := func(period evidence.Period, target, control float64) {
		t, err := time.Parse(time.DateOnly, period.To)
		if err != nil || seen[t] {
			return
		}
		seen[t] = true
		data = append(data, reading{date: t, target: target, control: &control, period: span(period.From, period.To)})
	}
	add(contrast.Baseline, contrast.BaselineMean, contrast.ControlBaselineMean)
	add(contrast.Current, contrast.CurrentMean, contrast.ControlCurrentMean)
	for _, row := range rows {
		if row.snapshot != signal.snapshot || row.Metric != watchdogMetric || seriesOf(row) != seriesOf(*signal) {
			continue
		}
		if d := evidence.WatchdogWindow(row.Row); d != nil {
			add(d.Current, d.CurrentMean, d.ControlCurrentMean)
		}
	}
	return drawTrend(data, release, "Каждая точка — среднее своего окна")
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
				control = observedProductName(candidate.Product, candidate)
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
	reference := 0.0
	if data[0].control != nil && *data[0].control > 0 {
		reference = data[0].target / *data[0].control
	}
	for _, d := range data {
		lo, hi = min(lo, d.target), max(hi, d.target)
		if d.control != nil {
			lo, hi = min(lo, *d.control), max(hi, *d.control)
			if reference > 0 {
				lo, hi = min(lo, *d.control*reference), max(hi, *d.control*reference)
			}
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
	var target, control, equivalent []string
	for _, d := range data {
		p, t := x(d.date), y(d.target)
		period := d.period
		if period == "" {
			period = d.date.Format("02.01.2006")
		}
		dot := views.ChartDot{X: p, TargetY: t, Label: period + " | " + num(d.target)}
		target = append(target, p+","+t)
		if d.control != nil {
			dot.ControlY = y(*d.control)
			dot.Label += " / " + num(*d.control)
			control = append(control, p+","+dot.ControlY)
			if reference > 0 {
				equivalent = append(equivalent, p+","+y(*d.control*reference))
			}
		}
		c.Dots = append(c.Dots, dot)
	}
	c.Target = strings.Join(target, " ")
	if len(control) == len(target) {
		c.Control = strings.Join(control, " ")
		if len(equivalent) == len(target) {
			c.Counterfactual = strings.Join(equivalent, " ")
			slices.Reverse(equivalent)
			c.Fill = c.Target + " " + strings.Join(equivalent, " ")
		}
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
